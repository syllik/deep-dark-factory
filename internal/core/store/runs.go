package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

const (
	tableRuns = "runs"

	runColumns   = "run_id, task_key, role, purpose, revision, attempt, head_sha, status, provider, failure_class, started_at, finished_at, payload"
	insertRunSQL = "INSERT INTO runs (" + runColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	getRunSQL    = "SELECT " + runColumns + " FROM runs WHERE run_id = ?"
	listRunsSQL  = "SELECT " + runColumns + " FROM runs WHERE task_key = ? ORDER BY started_at, run_id"
	finishRunSQL = "UPDATE runs SET status = ?, failure_class = ?, finished_at = ?, payload = ? WHERE run_id = ? AND status = 'running'"
)

// ErrRunFinished reports a finish of a run that is no longer running.
var ErrRunFinished = errors.New("store: run already finished")

// runPayload is the ddf.run/v1 payload: the routing entry, the usage, the
// result and patch digests and the redacted error.
type runPayload struct {
	Schema       model.SchemaID     `json:"schema"`
	Routing      model.RoutingEntry `json:"routing"`
	Usage        *model.Usage       `json:"usage,omitempty"`
	ResultDigest string             `json:"resultDigest,omitempty"`
	PatchDigest  string             `json:"patchDigest,omitempty"`
	Error        string             `json:"error,omitempty"`
}

// CreateRun inserts a started run in its own transaction. r must be valid,
// running, unfinished, without a failure class, with attempt 1 or 2 (spec
// §4.5), a non-empty ID and Provider, and Provider equal to the routing
// entry's provider. On a violation nothing is written.
func (s *Store) CreateRun(ctx context.Context, r model.Run) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("create run %s: %w", r.ID, err)
	}
	switch {
	case r.ID == "":
		return errors.New("create run: empty id")
	case r.Status != model.RunStatusRunning:
		return fmt.Errorf("create run %s: status %q, want running", r.ID, r.Status)
	case r.FinishedAt != nil:
		return fmt.Errorf("create run %s: finishedAt set on a running run", r.ID)
	case r.FailureClass != "":
		return fmt.Errorf("create run %s: failureClass set on a running run", r.ID)
	case r.Attempt != 1 && r.Attempt != 2:
		return fmt.Errorf("create run %s: attempt %d, want 1 or 2", r.ID, r.Attempt)
	case r.Provider == "":
		return fmt.Errorf("create run %s: empty provider", r.ID)
	case r.Provider != r.Routing.Provider:
		return fmt.Errorf("create run %s: provider %q differs from routing provider %q", r.ID, r.Provider, r.Routing.Provider)
	}
	payload, err := encodeRunPayload(r)
	if err != nil {
		return fmt.Errorf("create run %s: %w", r.ID, err)
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insertRunSQL, r.ID, string(r.Key), string(r.Role), string(r.Purpose),
			nullIntPtr(r.Revision), r.Attempt, nullText(r.HeadSHA), string(r.Status), r.Provider,
			nullText(r.FailureClass), ms(r.StartedAt), nil, payload)
		return err
	})
	if err != nil {
		return fmt.Errorf("create run %s: %w", r.ID, err)
	}
	return nil
}

// FinishRun moves run r.ID from running to succeeded, failed or discarded
// in one transaction, writing status, failure class, finished time and
// payload. A missing run is ErrNotFound; a run that is not running is
// ErrRunFinished. The identity fields (key, role, purpose, revision,
// attempt, head SHA, provider, start time and routing) must equal the
// stored ones; on any difference nothing changes.
func (s *Store) FinishRun(ctx context.Context, r model.Run) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("finish run %s: %w", r.ID, err)
	}
	switch {
	case r.Status != model.RunStatusSucceeded && r.Status != model.RunStatusFailed && r.Status != model.RunStatusDiscarded:
		return fmt.Errorf("finish run %s: status %q, want succeeded, failed or discarded", r.ID, r.Status)
	case r.FinishedAt == nil:
		return fmt.Errorf("finish run %s: finishedAt not set", r.ID)
	case r.FailureClass != "" && r.Status != model.RunStatusFailed:
		return fmt.Errorf("finish run %s: failureClass set on a %s run", r.ID, r.Status)
	}
	payload, err := encodeRunPayload(r)
	if err != nil {
		return fmt.Errorf("finish run %s: %w", r.ID, err)
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		stored, err := scanRun(tx.QueryRowContext(ctx, getRunSQL, r.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if stored.Status != model.RunStatusRunning {
			return fmt.Errorf("%w: status %s", ErrRunFinished, stored.Status)
		}
		if field := runIdentityDiff(stored, r); field != "" {
			return fmt.Errorf("immutable field %s differs from the stored run", field)
		}
		res, err := tx.ExecContext(ctx, finishRunSQL, string(r.Status), nullText(r.FailureClass), nullMS(r.FinishedAt), payload, r.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%d rows finished, want 1", n)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("finish run %s: %w", r.ID, err)
	}
	return nil
}

// GetRun returns run id. A missing run is ErrNotFound.
func (s *Store) GetRun(ctx context.Context, id string) (model.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, getRunSQL, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Run{}, fmt.Errorf("get run %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.Run{}, fmt.Errorf("get run %s: %w", id, err)
	}
	return r, nil
}

// ListRuns returns the runs of task key ordered by started_at, then
// run_id. Any undecodable row fails the whole read.
func (s *Store) ListRuns(ctx context.Context, key model.TaskKey) (runs []model.Run, err error) {
	rows, err := s.db.QueryContext(ctx, listRunsSQL, string(key))
	if err != nil {
		return nil, fmt.Errorf("list runs %s: %w", key, err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			runs, err = nil, fmt.Errorf("close runs rows: %w", cerr)
		}
	}()
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("list runs %s: %w", key, err)
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runs %s: %w", key, err)
	}
	return runs, nil
}

// runIdentityDiff names the first immutable field in which r differs from
// stored, or returns "".
func runIdentityDiff(stored, r model.Run) string {
	switch {
	case stored.Key != r.Key:
		return "task_key"
	case stored.Role != r.Role:
		return "role"
	case stored.Purpose != r.Purpose:
		return "purpose"
	case !equalIntPtr(stored.Revision, r.Revision):
		return "revision"
	case stored.Attempt != r.Attempt:
		return "attempt"
	case stored.HeadSHA != r.HeadSHA:
		return "head_sha"
	case stored.Provider != r.Provider:
		return "provider"
	case ms(stored.StartedAt) != ms(r.StartedAt):
		return "started_at"
	case stored.Routing != r.Routing:
		return "routing"
	}
	return ""
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// nullIntPtr encodes an optional integer; nil is SQL NULL.
func nullIntPtr(v *int) any {
	if v == nil {
		return nil
	}
	return int64(*v)
}

func encodeRunPayload(r model.Run) (string, error) {
	return encodePayload(tableRuns, runPayload{
		Schema:       model.SchemaIDRun,
		Routing:      r.Routing,
		Usage:        r.Usage,
		ResultDigest: r.ResultDigest,
		PatchDigest:  r.PatchDigest,
		Error:        r.Error,
	})
}

// scanRun decodes one runs row, failing closed on any unknown value.
func scanRun(row scanner) (model.Run, error) {
	var (
		r                                   model.Run
		key, role, purpose, status, payload string
		revision                            sql.NullInt64
		headSHA, failureClass               sql.NullString
		attempt, started                    int64
		finished                            sql.NullInt64
		err                                 error
	)
	if err := row.Scan(&r.ID, &key, &role, &purpose, &revision, &attempt, &headSHA, &status, &r.Provider,
		&failureClass, &started, &finished, &payload); err != nil {
		return model.Run{}, err
	}
	if r.Key, err = parseCol(tableRuns, "task_key", key, model.ParseTaskKey); err != nil {
		return model.Run{}, err
	}
	if r.Role, err = parseCol(tableRuns, "role", role, model.ParseRole); err != nil {
		return model.Run{}, err
	}
	if r.Purpose, err = parseCol(tableRuns, "purpose", purpose, model.ParseRunPurpose); err != nil {
		return model.Run{}, err
	}
	if r.Status, err = parseCol(tableRuns, "status", status, model.ParseRunStatus); err != nil {
		return model.Run{}, err
	}
	if r.FailureClass, err = parseNullCol(tableRuns, "failure_class", failureClass, model.ParseFailureClass); err != nil {
		return model.Run{}, err
	}
	if attempt != 1 && attempt != 2 {
		return model.Run{}, decodeErr(tableRuns, "attempt", fmt.Errorf("value %d, want 1 or 2", attempt))
	}
	r.Attempt = int(attempt)
	if revision.Valid {
		v := int(revision.Int64)
		r.Revision = &v
	}
	r.HeadSHA = headSHA.String
	r.StartedAt, r.FinishedAt = fromMS(started), fromNullMS(finished)
	var p runPayload
	if err := decodePayload(tableRuns, payload, &p, &p.Schema, model.SchemaIDRun); err != nil {
		return model.Run{}, err
	}
	if p.Routing.Provider != r.Provider {
		return model.Run{}, decodeErr(tableRuns, "payload",
			fmt.Errorf("routing provider %q differs from provider %q", p.Routing.Provider, r.Provider))
	}
	r.Routing, r.Usage, r.ResultDigest, r.PatchDigest, r.Error = p.Routing, p.Usage, p.ResultDigest, p.PatchDigest, p.Error
	if err := validRecord(tableRuns, r.Validate()); err != nil {
		return model.Run{}, err
	}
	return r, nil
}
