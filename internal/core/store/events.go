package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

const (
	tableEvents = "events"

	insertEventSQL     = "INSERT INTO events (task_key, type, at, payload) VALUES (?, ?, ?, ?)"
	eventColumns       = "seq, task_key, type, at, payload"
	listTaskEventsSQL  = "SELECT " + eventColumns + " FROM events WHERE task_key = ? ORDER BY seq"
	listGlobalEventSQL = "SELECT " + eventColumns + " FROM events WHERE task_key IS NULL ORDER BY seq"
)

// eventPayload is the ddf.event/v1 payload with the spec §8.6 audit fields.
type eventPayload struct {
	Schema         model.SchemaID `json:"schema"`
	Role           model.Role     `json:"role,omitempty"`
	Identity       string         `json:"identity,omitempty"`
	PermissionSet  []string       `json:"permissionSet,omitempty"`
	TokenExpiresAt *int64         `json:"tokenExpiresAt,omitempty"`
	Commits        []string       `json:"commits,omitempty"`
	Model          string         `json:"model,omitempty"`
	Usage          *model.Usage   `json:"usage,omitempty"`
}

// AppendEvent appends a global event (empty Key) in its own transaction. A
// task event is appended by CreateTask or UpdateTask with its state change.
func (s *Store) AppendEvent(ctx context.Context, ev model.Event) error {
	if ev.Key != "" {
		return fmt.Errorf("append event: key %s set; task events go with their state change", ev.Key)
	}
	if ev.Seq != 0 {
		return fmt.Errorf("append event: seq %d, want 0 (assigned by the store)", ev.Seq)
	}
	if err := ev.Validate(); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	if err := s.inTx(ctx, func(tx *sql.Tx) error { return insertEvent(ctx, tx, ev) }); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// ListEvents returns the events of key in seq order; an empty key lists the
// global events. Any undecodable row fails the whole read.
func (s *Store) ListEvents(ctx context.Context, key model.TaskKey) (events []model.Event, err error) {
	q, args := listGlobalEventSQL, []any{}
	if key != "" {
		q, args = listTaskEventsSQL, []any{string(key)}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list events %s: %w", key, err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			events, err = nil, fmt.Errorf("close events rows: %w", cerr)
		}
	}()
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("list events %s: %w", key, err)
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events %s: %w", key, err)
	}
	return events, nil
}

// insertEvent appends ev inside tx; SQLite assigns seq.
func insertEvent(ctx context.Context, tx *sql.Tx, ev model.Event) error {
	payload, err := encodePayload(tableEvents, eventPayload{
		Schema:         model.SchemaIDEvent,
		Role:           ev.Role,
		Identity:       ev.Identity,
		PermissionSet:  ev.PermissionSet,
		TokenExpiresAt: ptrMS(ev.TokenExpiresAt),
		Commits:        ev.Commits,
		Model:          ev.Model,
		Usage:          ev.Usage,
	})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, insertEventSQL, nullText(ev.Key), ev.Type, ms(ev.At), payload); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// scanEvent decodes one events row, failing closed on any unknown value.
func scanEvent(row scanner) (model.Event, error) {
	var (
		ev      model.Event
		key     sql.NullString
		at      int64
		payload string
		err     error
	)
	if err := row.Scan(&ev.Seq, &key, &ev.Type, &at, &payload); err != nil {
		return model.Event{}, err
	}
	if ev.Key, err = parseNullCol(tableEvents, "task_key", key, model.ParseTaskKey); err != nil {
		return model.Event{}, err
	}
	if ev.Seq <= 0 {
		return model.Event{}, decodeErr(tableEvents, "seq", errors.New("not positive"))
	}
	ev.At = fromMS(at)
	var p eventPayload
	if err := decodePayload(tableEvents, payload, &p, &p.Schema, model.SchemaIDEvent); err != nil {
		return model.Event{}, err
	}
	ev.Role, ev.Identity, ev.PermissionSet, ev.Commits, ev.Model, ev.Usage = p.Role, p.Identity, p.PermissionSet, p.Commits, p.Model, p.Usage
	ev.TokenExpiresAt = fromPtrMS(p.TokenExpiresAt)
	if err := validRecord(tableEvents, ev.Validate()); err != nil {
		return model.Event{}, err
	}
	return ev, nil
}
