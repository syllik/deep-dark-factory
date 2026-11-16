package store_test

import (
	"errors"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// #73: every bad stored value fails the read with ErrDecode, never a default.
func TestDecode_FailsClosed(t *testing.T) {
	const ev = "INSERT INTO events (task_key, type, at, payload) SELECT task_key, 'x', 0, "
	cases := []struct {
		name, stmt, read string
	}{
		{"tasks.kind", "UPDATE tasks SET kind = 'epic'", "task"},
		{"tasks.state", "UPDATE tasks SET state = 'limbo'", "task"},
		{"tasks.risk unknown", "UPDATE tasks SET risk = 'extreme'", "task"},
		{"tasks.risk empty", "UPDATE tasks SET risk = ''", "task"},
		{"tasks.priority", "UPDATE tasks SET priority = 'urgent'", "task"},
		{"tasks.flag_needs_owner", "UPDATE tasks SET flag_needs_owner = 2", "task"},
		{"tasks.flag_failed", "UPDATE tasks SET flag_failed = 2", "task"},
		{"tasks payload schema", `UPDATE tasks SET payload = '{"schema":"ddf.task/v2"}'`, "task"},
		{"tasks payload without schema", `UPDATE tasks SET payload = '{"title":"t"}'`, "task"},
		{"tasks payload not an object", `UPDATE tasks SET payload = '[]'`, "task"},
		{"intents.operation", "UPDATE intents SET operation = 'fork'", "pending"},
		{"intents.status", "UPDATE intents SET status = 'stuck'", "resolve"},
		{"intents.dry_run", "UPDATE intents SET dry_run = 2", "pending"},
		{"intents payload schema", `UPDATE intents SET payload = '{"schema":"ddf.task/v1"}'`, "pending"},
		{"events payload schema", ev + `'{"schema":"ddf.event/v2"}' FROM tasks`, "events"},
		{"events unknown field", ev + `'{"schema":"ddf.event/v1","token":"secret"}' FROM tasks`, "events"},
		{"events unknown role", ev + `'{"schema":"ddf.event/v1","role":"boss"}' FROM tasks`, "events"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path := newTestStore(t)
			task := newTask(t, "1")
			mustCreate(t, s, task)
			if err := s.CreateIntent(t.Context(), newIntent(task.Key, "i1", t0)); err != nil {
				t.Fatal(err)
			}
			if _, err := rawDB(t, path).ExecContext(t.Context(), tc.stmt); err != nil {
				t.Fatalf("raw exec: %v", err)
			}
			var err error
			switch tc.read {
			case "task":
				_, err = s.GetTask(t.Context(), task.Key)
			case "pending":
				var got []model.Intent
				if got, err = s.PendingIntents(t.Context()); got != nil {
					t.Errorf("partial list %+v", got)
				}
			case "resolve":
				err = s.ResolveIntent(t.Context(), "i1", model.IntentStatusFailed, store.IntentOutcome{}, t0)
			case "events":
				var got []model.Event
				if got, err = s.ListEvents(t.Context(), task.Key); got != nil {
					t.Errorf("partial list %+v", got)
				}
			}
			if !errors.Is(err, store.ErrDecode) {
				t.Fatalf("read error = %v, want ErrDecode", err)
			}
		})
	}
}
