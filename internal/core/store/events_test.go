package store_test

import (
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

func TestEvents_AppendGlobal(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	for _, typ := range []string{"paused", "resumed"} {
		if err := s.AppendEvent(t.Context(), model.Event{Type: typ, Identity: "owner", At: t0}); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
	evs := mustEvents(t, s, "")
	if len(evs) != 2 || evs[0].Type != "paused" || evs[1].Type != "resumed" || evs[0].Seq >= evs[1].Seq || evs[0].Key != "" {
		t.Fatalf("global events = %+v", evs)
	}
	if !evs[0].At.Equal(t0) || evs[0].At.Location() != t0.UTC().Location() || evs[0].Identity != "owner" {
		t.Fatalf("global event = %+v", evs[0])
	}
	for _, ev := range []model.Event{taskEvent(task, "x"), {Seq: 3, Type: "x", At: t0}, {Role: "boss", Type: "x", At: t0}} {
		if err := s.AppendEvent(t.Context(), ev); err == nil {
			t.Errorf("AppendEvent(%+v) succeeded", ev)
		}
	}
	if n := len(mustEvents(t, s, task.Key)) + len(mustEvents(t, s, "")); n != 3 {
		t.Fatalf("events after refused appends = %d, want 3", n)
	}
}

// r2-1: events.seq is assigned by SQLite; an existing seq cannot be replaced.
func TestSchema_EventsSeqAuto(t *testing.T) {
	s, path := newTestStore(t)
	db := rawDB(t, path)
	ins := func(q string) error {
		_, err := db.ExecContext(t.Context(), q)
		return err
	}
	if err := ins(`INSERT INTO events (task_key, type, at, payload) VALUES (NULL, 'first', 1, '{"schema":"ddf.event/v1"}')`); err != nil {
		t.Fatalf("insert without seq: %v", err)
	}
	if err := ins(`INSERT OR REPLACE INTO events (seq, task_key, type, at, payload) VALUES (1, NULL, 'forged', 2, '{"schema":"ddf.event/v1"}')`); err == nil {
		t.Fatal("INSERT OR REPLACE over an existing seq succeeded")
	}
	if evs := mustEvents(t, s, ""); len(evs) != 1 || evs[0].Seq != 1 || evs[0].Type != "first" {
		t.Fatalf("events after replace attempt = %+v", evs)
	}
	if err := s.AppendEvent(t.Context(), model.Event{Type: "next", At: t0}); err != nil {
		t.Fatalf("AppendEvent after aborted replace: %v", err)
	}
	if evs := mustEvents(t, s, ""); len(evs) != 2 || evs[1].Seq != 2 || evs[1].Type != "next" {
		t.Fatalf("events after append = %+v, want seq 2 = max+1", evs)
	}
}
