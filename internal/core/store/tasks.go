package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

const (
	tableTasks = "tasks"

	taskColumns = "task_key, forge, repository, source_kind, source_id, kind, parent_key, state, " +
		"flag_needs_owner, flag_hold, flag_failed, risk, priority, hit_stop, created_at, updated_at, teardown_at, payload"
	insertTaskSQL = "INSERT INTO tasks (" + taskColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	updateTaskSQL = "UPDATE tasks SET state = ?, flag_needs_owner = ?, flag_hold = ?, flag_failed = ?, risk = ?, " +
		"priority = ?, hit_stop = ?, updated_at = ?, teardown_at = ?, payload = ? WHERE task_key = ?"
	getTaskSQL = "SELECT " + taskColumns + " FROM tasks WHERE task_key = ?"
)

// taskPayload is the ddf.task/v1 payload: the task fields that are not
// columns (spec §4.5 Tables).
type taskPayload struct {
	Schema        model.SchemaID    `json:"schema"`
	Title         string            `json:"title,omitempty"`
	Spec          string            `json:"spec,omitempty"`
	Acceptance    []string          `json:"acceptance,omitempty"`
	Zones         []string          `json:"zones,omitempty"`
	DependsOn     []string          `json:"dependsOn,omitempty"`
	AgentDefaults []decisionPayload `json:"agentDefaults,omitempty"`
	PlannerRisk   model.RiskClass   `json:"plannerRisk,omitempty"`
}

// decisionPayload is a model.Decision with its time as Unix ms.
type decisionPayload struct {
	Key       model.TaskKey `json:"key"`
	Question  string        `json:"question"`
	Choice    string        `json:"choice"`
	CreatedAt int64         `json:"createdAt"`
}

// CreateTask inserts t and appends ev, its event, in one transaction. ev.Key
// must equal t.Key and ev.Seq must be 0.
//
// spec §4.5 SQL rules: each state change is one transaction with its event
func (s *Store) CreateTask(ctx context.Context, t model.Task, ev model.Event) error {
	if err := checkTaskEvent(t, ev); err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	payload, err := encodeTaskPayload(t)
	if err != nil {
		return fmt.Errorf("create task %s: %w", t.Key, err)
	}
	fl := taskFlags(t.Flags)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insertTaskSQL,
			string(t.Key), t.Forge, t.Repository, t.SourceKind, t.SourceID, string(t.Kind), nullText(t.ParentKey),
			string(t.State), fl[0], fl[1], fl[2], nullText(t.Risk), string(t.Priority), boolInt(t.HitStop),
			ms(t.CreatedAt), ms(t.UpdatedAt), nullMS(t.TeardownAt), payload); err != nil {
			return fmt.Errorf("insert task: %w", err)
		}
		return insertEvent(ctx, tx, ev)
	})
	if err != nil {
		return fmt.Errorf("create task %s: %w", t.Key, err)
	}
	return nil
}

// UpdateTask writes the mutable columns and the payload of t and appends ev
// in one transaction. A missing task is ErrNotFound and appends nothing.
// Clearing HitStop fails on the schema trigger and changes nothing.
func (s *Store) UpdateTask(ctx context.Context, t model.Task, ev model.Event) error {
	if err := checkTaskEvent(t, ev); err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	payload, err := encodeTaskPayload(t)
	if err != nil {
		return fmt.Errorf("update task %s: %w", t.Key, err)
	}
	fl := taskFlags(t.Flags)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, updateTaskSQL,
			string(t.State), fl[0], fl[1], fl[2], nullText(t.Risk), string(t.Priority), boolInt(t.HitStop),
			ms(t.UpdatedAt), nullMS(t.TeardownAt), payload, string(t.Key))
		if err != nil {
			return fmt.Errorf("update task: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("update task: %w", err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return insertEvent(ctx, tx, ev)
	})
	if err != nil {
		return fmt.Errorf("update task %s: %w", t.Key, err)
	}
	return nil
}

// GetTask reads the task with key; a missing task is ErrNotFound.
func (s *Store) GetTask(ctx context.Context, key model.TaskKey) (model.Task, error) {
	t, err := scanTask(s.db.QueryRowContext(ctx, getTaskSQL, string(key)))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return model.Task{}, fmt.Errorf("get task %s: %w", key, err)
	}
	return t, nil
}

// checkTaskEvent validates a task and the event of its state change.
func checkTaskEvent(t model.Task, ev model.Event) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if err := ev.Validate(); err != nil {
		return err
	}
	if ev.Key != t.Key {
		return fmt.Errorf("event key %q, want task key %q", ev.Key, t.Key)
	}
	if ev.Seq != 0 {
		return fmt.Errorf("event seq %d, want 0 (assigned by the store)", ev.Seq)
	}
	return nil
}

// taskFlags maps validated flags to the flag_needs_owner, flag_hold and
// flag_failed column values.
func taskFlags(flags []model.TaskFlag) [3]int64 {
	var out [3]int64
	for _, f := range flags {
		switch f {
		case model.TaskFlagNeedsOwner:
			out[0] = 1
		case model.TaskFlagHold:
			out[1] = 1
		case model.TaskFlagFailed:
			out[2] = 1
		}
	}
	return out
}

func encodeTaskPayload(t model.Task) (string, error) {
	p := taskPayload{
		Schema:      model.SchemaIDTask,
		Title:       t.Title,
		Spec:        t.Spec,
		Acceptance:  t.Acceptance,
		Zones:       t.Zones,
		DependsOn:   t.DependsOn,
		PlannerRisk: t.PlannerRisk,
	}
	for _, d := range t.AgentDefaults {
		p.AgentDefaults = append(p.AgentDefaults, decisionPayload{
			Key: d.Key, Question: d.Question, Choice: d.Choice, CreatedAt: ms(d.CreatedAt),
		})
	}
	return encodePayload(tableTasks, p)
}

// scanTask decodes one tasks row, failing closed on any unknown value.
func scanTask(row scanner) (model.Task, error) {
	var (
		t                                   model.Task
		key, kind, state, priority, payload string
		parent, risk                        sql.NullString
		flags                               [3]int64
		hitStop, created, updated           int64
		teardown                            sql.NullInt64
		err                                 error
	)
	if err := row.Scan(&key, &t.Forge, &t.Repository, &t.SourceKind, &t.SourceID, &kind, &parent, &state,
		&flags[0], &flags[1], &flags[2], &risk, &priority, &hitStop, &created, &updated, &teardown, &payload); err != nil {
		return model.Task{}, err
	}
	if t.Key, err = parseCol(tableTasks, "task_key", key, model.ParseTaskKey); err != nil {
		return model.Task{}, err
	}
	if t.Kind, err = parseCol(tableTasks, "kind", kind, model.ParseTaskKind); err != nil {
		return model.Task{}, err
	}
	if t.ParentKey, err = parseNullCol(tableTasks, "parent_key", parent, model.ParseTaskKey); err != nil {
		return model.Task{}, err
	}
	if t.State, err = parseCol(tableTasks, "state", state, model.ParseTaskState); err != nil {
		return model.Task{}, err
	}
	if t.Risk, err = parseNullCol(tableTasks, "risk", risk, model.ParseRiskClass); err != nil {
		return model.Task{}, err
	}
	if t.Priority, err = parseCol(tableTasks, "priority", priority, model.ParsePriority); err != nil {
		return model.Task{}, err
	}
	flagCols := [3]struct {
		column string
		flag   model.TaskFlag
	}{
		{"flag_needs_owner", model.TaskFlagNeedsOwner},
		{"flag_hold", model.TaskFlagHold},
		{"flag_failed", model.TaskFlagFailed},
	}
	for i, fc := range flagCols {
		set, err := parseBool(tableTasks, fc.column, flags[i])
		if err != nil {
			return model.Task{}, err
		}
		if set {
			t.Flags = append(t.Flags, fc.flag)
		}
	}
	if t.HitStop, err = parseBool(tableTasks, "hit_stop", hitStop); err != nil {
		return model.Task{}, err
	}
	t.CreatedAt, t.UpdatedAt, t.TeardownAt = fromMS(created), fromMS(updated), fromNullMS(teardown)
	var p taskPayload
	if err := decodePayload(tableTasks, payload, &p, &p.Schema, model.SchemaIDTask); err != nil {
		return model.Task{}, err
	}
	t.Title, t.Spec, t.Acceptance, t.Zones, t.DependsOn, t.PlannerRisk = p.Title, p.Spec, p.Acceptance, p.Zones, p.DependsOn, p.PlannerRisk
	for _, d := range p.AgentDefaults {
		t.AgentDefaults = append(t.AgentDefaults, model.Decision{
			Key: d.Key, Question: d.Question, Choice: d.Choice, CreatedAt: fromMS(d.CreatedAt),
		})
	}
	if err := validRecord(tableTasks, t.Validate()); err != nil {
		return model.Task{}, err
	}
	return t, nil
}
