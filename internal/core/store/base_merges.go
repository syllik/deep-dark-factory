package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

const (
	tableBaseMerges = "base_merges"

	baseMergeColumns   = "task_key, change_id, merge_sha, base_sha, parent_head_sha, intent_id, created_at"
	insertBaseMergeSQL = "INSERT INTO base_merges (" + baseMergeColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?)"
	listBaseMergesSQL  = "SELECT " + baseMergeColumns + " FROM base_merges WHERE task_key = ? AND change_id = ? " +
		"ORDER BY created_at, merge_sha"
	changeBranchSQL        = "SELECT branch FROM changes WHERE task_key = ? AND change_id = ?"
	baseMergeIntentUsedSQL = "SELECT 1 FROM base_merges WHERE intent_id = ? LIMIT 1"
)

// ErrBaseMergeIntent reports a base merge without its daemon intent: the
// intent is missing, of another task, not an update-branch, not succeeded,
// its new SHA is not the merge SHA, its expected old SHA is not the parent
// head SHA, its target ref is not refs/heads/<change branch>, or another
// base merge already cites it.
var ErrBaseMergeIntent = errors.New("store: base merge without its daemon intent")

// BaseMerge is a daemon-created base merge of spec §6.6 rule 4: the merge
// commit MergeSHA of base BaseSHA into the change head ParentHeadSHA, made
// by the update-branch intent IntentID.
type BaseMerge struct {
	Key           model.TaskKey
	ChangeID      string
	MergeSHA      string
	BaseSHA       string
	ParentHeadSHA string
	IntentID      string
	CreatedAt     time.Time
}

// CreateBaseMerge inserts bm in one transaction. The change (bm.Key,
// bm.ChangeID) must exist, else ErrNotFound. The intent bm.IntentID must be
// a succeeded update-branch intent of bm.Key whose new SHA is bm.MergeSHA,
// whose expected old SHA is bm.ParentHeadSHA and whose target ref is
// refs/heads/<change branch>, and no base merge (of any task or change) may
// already cite it; else ErrBaseMergeIntent and nothing is written. Base
// merges are insert-only, and no event is appended.
//
// spec §6.6 rule 4: its SHA recorded in the store from the daemon's own intent
func (s *Store) CreateBaseMerge(ctx context.Context, bm BaseMerge) error {
	if err := checkBaseMerge(bm); err != nil {
		return fmt.Errorf("create base merge %s: %w", bm.MergeSHA, err)
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var branch string
		err := tx.QueryRowContext(ctx, changeBranchSQL, string(bm.Key), bm.ChangeID).Scan(&branch)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("change %s: %w", bm.ChangeID, ErrNotFound)
		}
		if err != nil {
			return err
		}
		in, err := scanIntent(tx.QueryRowContext(ctx, getIntentSQL, bm.IntentID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: intent %s not found", ErrBaseMergeIntent, bm.IntentID)
		}
		if err != nil {
			return err
		}
		switch {
		case in.Key != bm.Key:
			return fmt.Errorf("%w: intent %s of task %s", ErrBaseMergeIntent, in.ID, in.Key)
		case in.Operation != model.IntentOperationUpdateBranch:
			return fmt.Errorf("%w: intent %s operation %s", ErrBaseMergeIntent, in.ID, in.Operation)
		case in.Status != model.IntentStatusSucceeded:
			return fmt.Errorf("%w: intent %s status %s", ErrBaseMergeIntent, in.ID, in.Status)
		case in.NewSHA != bm.MergeSHA:
			return fmt.Errorf("%w: intent %s new SHA %q", ErrBaseMergeIntent, in.ID, in.NewSHA)
		case in.ExpectedOldSHA != bm.ParentHeadSHA:
			return fmt.Errorf("%w: intent %s expected old SHA %q, parent head %q",
				ErrBaseMergeIntent, in.ID, in.ExpectedOldSHA, bm.ParentHeadSHA)
		case in.TargetRef != "refs/heads/"+branch:
			return fmt.Errorf("%w: intent %s target ref %q, change branch %q", ErrBaseMergeIntent, in.ID, in.TargetRef, branch)
		}
		var one int
		err = tx.QueryRowContext(ctx, baseMergeIntentUsedSQL, bm.IntentID).Scan(&one)
		switch {
		case err == nil:
			return fmt.Errorf("%w: intent %s already used by a base merge", ErrBaseMergeIntent, in.ID)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err = tx.ExecContext(ctx, insertBaseMergeSQL, string(bm.Key), bm.ChangeID, bm.MergeSHA, bm.BaseSHA,
			bm.ParentHeadSHA, bm.IntentID, ms(bm.CreatedAt))
		return err
	})
	if err != nil {
		return fmt.Errorf("create base merge %s: %w", bm.MergeSHA, err)
	}
	return nil
}

// ListBaseMerges returns the base merges of change changeID of task key
// ordered by created_at, then merge_sha. Any undecodable row fails the
// whole read.
func (s *Store) ListBaseMerges(ctx context.Context, key model.TaskKey, changeID string) (merges []BaseMerge, err error) {
	rows, err := s.db.QueryContext(ctx, listBaseMergesSQL, string(key), changeID)
	if err != nil {
		return nil, fmt.Errorf("list base merges %s: %w", changeID, err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			merges, err = nil, fmt.Errorf("close base merges rows: %w", cerr)
		}
	}()
	for rows.Next() {
		bm, err := scanBaseMerge(rows)
		if err != nil {
			return nil, fmt.Errorf("list base merges %s: %w", changeID, err)
		}
		merges = append(merges, bm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list base merges %s: %w", changeID, err)
	}
	return merges, nil
}

// checkBaseMerge validates a base merge before a write.
func checkBaseMerge(bm BaseMerge) error {
	if _, err := model.ParseTaskKey(string(bm.Key)); err != nil {
		return err
	}
	for _, f := range baseMergeFields(bm) {
		if f.v == "" {
			return fmt.Errorf("empty %s", f.name)
		}
	}
	if bm.MergeSHA == bm.ParentHeadSHA {
		return errors.New("merge SHA equals the parent head SHA")
	}
	return nil
}

// baseMergeFields lists the required text fields of bm by column name.
func baseMergeFields(bm BaseMerge) []struct{ name, v string } {
	return []struct{ name, v string }{
		{"change_id", bm.ChangeID}, {"merge_sha", bm.MergeSHA}, {"base_sha", bm.BaseSHA},
		{"parent_head_sha", bm.ParentHeadSHA}, {"intent_id", bm.IntentID},
	}
}

// scanBaseMerge decodes one base_merges row, failing closed on a bad key or
// an empty text column.
func scanBaseMerge(row scanner) (BaseMerge, error) {
	var (
		bm      BaseMerge
		key     string
		created int64
		err     error
	)
	if err := row.Scan(&key, &bm.ChangeID, &bm.MergeSHA, &bm.BaseSHA, &bm.ParentHeadSHA, &bm.IntentID, &created); err != nil {
		return BaseMerge{}, err
	}
	if bm.Key, err = parseCol(tableBaseMerges, "task_key", key, model.ParseTaskKey); err != nil {
		return BaseMerge{}, err
	}
	for _, f := range baseMergeFields(bm) {
		if f.v == "" {
			return BaseMerge{}, decodeErr(tableBaseMerges, f.name, errors.New("empty value"))
		}
	}
	bm.CreatedAt = fromMS(created)
	return bm, nil
}
