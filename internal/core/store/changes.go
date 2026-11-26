package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

const (
	tableChanges = "changes"

	changeColumns = "task_key, change_id, kind, branch, base_branch, head_sha, revision, open, draft, patch_id, " +
		"merge_sha, merged_at, ci_since, created_at, updated_at, payload"
	insertChangeSQL = "INSERT INTO changes (" + changeColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	updateChangeSQL = "UPDATE changes SET head_sha = ?, revision = ?, open = ?, draft = ?, patch_id = ?, merge_sha = ?, " +
		"merged_at = ?, ci_since = ?, updated_at = ?, payload = ? WHERE task_key = ? AND change_id = ?"
	getChangeSQL   = "SELECT " + changeColumns + " FROM changes WHERE task_key = ? AND change_id = ?"
	listChangesSQL = "SELECT " + changeColumns + " FROM changes WHERE task_key = ? ORDER BY created_at, change_id"
)

// changePayload is the ddf.change/v1 payload: every Change field is a
// column, so it carries only the schema ID.
type changePayload struct {
	Schema model.SchemaID `json:"schema"`
}

// CreateChange inserts c. PatchID, HeadSHA, Branch and BaseBranch must be
// set (spec §6.6 rule 4 needs the patch-id), and MergeSHA and MergedAt are
// set together or not at all. No event is appended: the change is its own
// evidence record.
func (s *Store) CreateChange(ctx context.Context, c model.Change) error {
	if err := checkChange(c); err != nil {
		return fmt.Errorf("create change %s: %w", c.ID, err)
	}
	payload, err := encodeChangePayload()
	if err != nil {
		return fmt.Errorf("create change %s: %w", c.ID, err)
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insertChangeSQL,
			string(c.Key), c.ID, string(c.Kind), c.Branch, c.BaseBranch, c.HeadSHA, c.Revision,
			boolInt(c.Open), boolInt(c.Draft), c.PatchID, nullText(c.MergeSHA), nullMS(c.MergedAt), nullMS(c.CISince),
			ms(c.CreatedAt), ms(c.UpdatedAt), payload)
		return err
	})
	if err != nil {
		return fmt.Errorf("create change %s: %w", c.ID, err)
	}
	return nil
}

// UpdateChange writes the mutable columns of the change (c.Key, c.ID) in one
// transaction. A missing change is ErrNotFound. kind, branch, base_branch
// and created_at are immutable, and so are merge_sha and merged_at once
// merge_sha is stored; a change to an immutable field fails and writes
// nothing.
func (s *Store) UpdateChange(ctx context.Context, c model.Change) error {
	if err := checkChange(c); err != nil {
		return fmt.Errorf("update change %s: %w", c.ID, err)
	}
	payload, err := encodeChangePayload()
	if err != nil {
		return fmt.Errorf("update change %s: %w", c.ID, err)
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		old, err := scanChange(tx.QueryRowContext(ctx, getChangeSQL, string(c.Key), c.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := checkChangeImmutable(old, c); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, updateChangeSQL,
			c.HeadSHA, c.Revision, boolInt(c.Open), boolInt(c.Draft), c.PatchID, nullText(c.MergeSHA),
			nullMS(c.MergedAt), nullMS(c.CISince), ms(c.UpdatedAt), payload, string(c.Key), c.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%d rows updated, want 1", n)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("update change %s: %w", c.ID, err)
	}
	return nil
}

// GetChange reads the change changeID of task key; a missing change is
// ErrNotFound.
func (s *Store) GetChange(ctx context.Context, key model.TaskKey, changeID string) (model.Change, error) {
	c, err := scanChange(s.db.QueryRowContext(ctx, getChangeSQL, string(key), changeID))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return model.Change{}, fmt.Errorf("get change %s: %w", changeID, err)
	}
	return c, nil
}

// ListChanges returns the changes of task key ordered by created_at, then
// change_id. Any undecodable row fails the whole read.
func (s *Store) ListChanges(ctx context.Context, key model.TaskKey) (changes []model.Change, err error) {
	rows, err := s.db.QueryContext(ctx, listChangesSQL, string(key))
	if err != nil {
		return nil, fmt.Errorf("list changes %s: %w", key, err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			changes, err = nil, fmt.Errorf("close changes rows: %w", cerr)
		}
	}()
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, fmt.Errorf("list changes %s: %w", key, err)
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list changes %s: %w", key, err)
	}
	return changes, nil
}

// checkChange validates a change before a write.
func checkChange(c model.Change) error {
	if err := c.Validate(); err != nil {
		return err
	}
	for _, f := range []struct{ name, v string }{
		{"id", c.ID}, {"headSha", c.HeadSHA}, {"branch", c.Branch}, {"baseBranch", c.BaseBranch}, {"patchId", c.PatchID},
	} {
		if f.v == "" {
			return fmt.Errorf("empty %s", f.name)
		}
	}
	if (c.MergeSHA == "") != (c.MergedAt == nil) {
		return errors.New("mergeSha and mergedAt must be set together")
	}
	return nil
}

// checkChangeImmutable compares the immutable fields of the stored change
// old with the new value c.
func checkChangeImmutable(old, c model.Change) error {
	if old.Kind != c.Kind {
		return fmt.Errorf("kind is immutable: stored %q, got %q", old.Kind, c.Kind)
	}
	if old.Branch != c.Branch {
		return fmt.Errorf("branch is immutable: stored %q, got %q", old.Branch, c.Branch)
	}
	if old.BaseBranch != c.BaseBranch {
		return fmt.Errorf("base_branch is immutable: stored %q, got %q", old.BaseBranch, c.BaseBranch)
	}
	if ms(old.CreatedAt) != ms(c.CreatedAt) {
		return errors.New("created_at is immutable")
	}
	if old.MergeSHA == "" {
		return nil
	}
	if old.MergeSHA != c.MergeSHA {
		return fmt.Errorf("merge_sha is immutable once set: stored %q, got %q", old.MergeSHA, c.MergeSHA)
	}
	if c.MergedAt == nil || old.MergedAt == nil || ms(*old.MergedAt) != ms(*c.MergedAt) {
		return errors.New("merged_at is immutable once merge_sha is set")
	}
	return nil
}

func encodeChangePayload() (string, error) {
	return encodePayload(tableChanges, changePayload{Schema: model.SchemaIDChange})
}

// scanChange decodes one changes row, failing closed on any unknown value.
func scanChange(row scanner) (model.Change, error) {
	var (
		c                             model.Change
		key, kind, payload            string
		open, draft, created, updated int64
		mergeSHA                      sql.NullString
		mergedAt, ciSince             sql.NullInt64
		err                           error
	)
	if err := row.Scan(&key, &c.ID, &kind, &c.Branch, &c.BaseBranch, &c.HeadSHA, &c.Revision, &open, &draft,
		&c.PatchID, &mergeSHA, &mergedAt, &ciSince, &created, &updated, &payload); err != nil {
		return model.Change{}, err
	}
	if c.Key, err = parseCol(tableChanges, "task_key", key, model.ParseTaskKey); err != nil {
		return model.Change{}, err
	}
	if c.Kind, err = parseCol(tableChanges, "kind", kind, model.ParseChangeKind); err != nil {
		return model.Change{}, err
	}
	if c.Open, err = parseBool(tableChanges, "open", open); err != nil {
		return model.Change{}, err
	}
	if c.Draft, err = parseBool(tableChanges, "draft", draft); err != nil {
		return model.Change{}, err
	}
	c.MergeSHA = mergeSHA.String
	c.MergedAt, c.CISince = fromNullMS(mergedAt), fromNullMS(ciSince)
	c.CreatedAt, c.UpdatedAt = fromMS(created), fromMS(updated)
	var p changePayload
	if err := decodePayload(tableChanges, payload, &p, &p.Schema, model.SchemaIDChange); err != nil {
		return model.Change{}, err
	}
	if err := validRecord(tableChanges, c.Validate()); err != nil {
		return model.Change{}, err
	}
	return c, nil
}
