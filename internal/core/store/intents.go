package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

const (
	tableIntents = "intents"

	intentColumns   = "intent_id, task_key, operation, status, dry_run, created_at, resolved_at, payload"
	insertIntentSQL = "INSERT INTO intents (" + intentColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?)"
	getIntentSQL    = "SELECT " + intentColumns + " FROM intents WHERE intent_id = ?"
	resolveSQL      = "UPDATE intents SET status = ?, resolved_at = ?, payload = ? WHERE intent_id = ? AND status = 'pending'"
	pendingSQL      = "SELECT " + intentColumns + " FROM intents WHERE status = 'pending' ORDER BY created_at, intent_id"
)

// ErrIntentResolved reports a resolve of an intent that is no longer
// pending.
var ErrIntentResolved = errors.New("store: intent already resolved")

// IntentOutcome is the outcome of a forge write: the forge object ID, the
// new SHA and the error class (spec §4.5 Tables).
type IntentOutcome struct {
	ObjectID   string
	NewSHA     string
	ErrorClass string
}

// intentPayload is the ddf.intent/v1 payload: the call's arguments, the
// reconciliation evidence and the outcome.
type intentPayload struct {
	Schema         model.SchemaID  `json:"schema"`
	Args           json.RawMessage `json:"args,omitempty"`
	Marker         string          `json:"marker,omitempty"`
	TargetRef      string          `json:"targetRef,omitempty"`
	ExpectedOldSHA string          `json:"expectedOldSha,omitempty"`
	HeadSHA        string          `json:"headSha,omitempty"`
	ObjectID       string          `json:"objectId,omitempty"`
	NewSHA         string          `json:"newSha,omitempty"`
	ErrorClass     string          `json:"errorClass,omitempty"`
}

// CreateIntent commits a pending intent in its own transaction, before the
// forge call it records.
//
// spec §4.5 SQL rules: an intent row is committed before the forge call
func (s *Store) CreateIntent(ctx context.Context, in model.Intent) error {
	if err := in.Validate(); err != nil {
		return fmt.Errorf("create intent %s: %w", in.ID, err)
	}
	if in.Status != model.IntentStatusPending {
		return fmt.Errorf("create intent %s: status %q, want pending", in.ID, in.Status)
	}
	if in.ResolvedAt != nil || in.ObjectID != "" || in.NewSHA != "" || in.ErrorClass != "" {
		return fmt.Errorf("create intent %s: outcome set on a pending intent", in.ID)
	}
	payload, err := encodeIntentPayload(in)
	if err != nil {
		return fmt.Errorf("create intent %s: %w", in.ID, err)
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insertIntentSQL, in.ID, string(in.Key), string(in.Operation), string(in.Status),
			boolInt(in.DryRun), ms(in.CreatedAt), nil, payload)
		return err
	})
	if err != nil {
		return fmt.Errorf("create intent %s: %w", in.ID, err)
	}
	return nil
}

// ResolveIntent records the outcome of intent id in its own transaction.
// status must be succeeded or failed. A missing intent is ErrNotFound; an
// intent that is not pending is ErrIntentResolved.
func (s *Store) ResolveIntent(ctx context.Context, id string, status model.IntentStatus, out IntentOutcome, at time.Time) error {
	if status != model.IntentStatusSucceeded && status != model.IntentStatusFailed {
		return fmt.Errorf("resolve intent %s: status %q, want succeeded or failed", id, status)
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		in, err := scanIntent(tx.QueryRowContext(ctx, getIntentSQL, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if in.Status != model.IntentStatusPending {
			return fmt.Errorf("%w: status %s", ErrIntentResolved, in.Status)
		}
		in.ObjectID, in.NewSHA, in.ErrorClass = out.ObjectID, out.NewSHA, out.ErrorClass
		payload, err := encodeIntentPayload(in)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, resolveSQL, string(status), ms(at), payload, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("%d rows resolved, want 1", n)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("resolve intent %s: %w", id, err)
	}
	return nil
}

// PendingIntents returns the pending intents ordered by created_at, then
// intent_id. Any undecodable row fails the whole read.
func (s *Store) PendingIntents(ctx context.Context) (intents []model.Intent, err error) {
	rows, err := s.db.QueryContext(ctx, pendingSQL)
	if err != nil {
		return nil, fmt.Errorf("pending intents: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			intents, err = nil, fmt.Errorf("close intents rows: %w", cerr)
		}
	}()
	for rows.Next() {
		in, err := scanIntent(rows)
		if err != nil {
			return nil, fmt.Errorf("pending intents: %w", err)
		}
		intents = append(intents, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pending intents: %w", err)
	}
	return intents, nil
}

func encodeIntentPayload(in model.Intent) (string, error) {
	return encodePayload(tableIntents, intentPayload{
		Schema:         model.SchemaIDIntent,
		Args:           in.Args,
		Marker:         in.Marker,
		TargetRef:      in.TargetRef,
		ExpectedOldSHA: in.ExpectedOldSHA,
		HeadSHA:        in.HeadSHA,
		ObjectID:       in.ObjectID,
		NewSHA:         in.NewSHA,
		ErrorClass:     in.ErrorClass,
	})
}

// scanIntent decodes one intents row, failing closed on any unknown value.
func scanIntent(row scanner) (model.Intent, error) {
	var (
		in                              model.Intent
		key, operation, status, payload string
		dryRun, created                 int64
		resolved                        sql.NullInt64
		err                             error
	)
	if err := row.Scan(&in.ID, &key, &operation, &status, &dryRun, &created, &resolved, &payload); err != nil {
		return model.Intent{}, err
	}
	if in.Key, err = parseCol(tableIntents, "task_key", key, model.ParseTaskKey); err != nil {
		return model.Intent{}, err
	}
	if in.Operation, err = parseCol(tableIntents, "operation", operation, model.ParseIntentOperation); err != nil {
		return model.Intent{}, err
	}
	if in.Status, err = parseCol(tableIntents, "status", status, model.ParseIntentStatus); err != nil {
		return model.Intent{}, err
	}
	if in.DryRun, err = parseBool(tableIntents, "dry_run", dryRun); err != nil {
		return model.Intent{}, err
	}
	in.CreatedAt, in.ResolvedAt = fromMS(created), fromNullMS(resolved)
	var p intentPayload
	if err := decodePayload(tableIntents, payload, &p, &p.Schema, model.SchemaIDIntent); err != nil {
		return model.Intent{}, err
	}
	in.Args, in.Marker, in.TargetRef, in.ExpectedOldSHA, in.HeadSHA = p.Args, p.Marker, p.TargetRef, p.ExpectedOldSHA, p.HeadSHA
	in.ObjectID, in.NewSHA, in.ErrorClass = p.ObjectID, p.NewSHA, p.ErrorClass
	if err := validRecord(tableIntents, in.Validate()); err != nil {
		return model.Intent{}, err
	}
	return in, nil
}
