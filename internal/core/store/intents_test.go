package store_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

func newIntent(key model.TaskKey, id string, at time.Time) model.Intent {
	return model.Intent{
		Key: key, ID: id, Operation: model.IntentOperationPush, Status: model.IntentStatusPending, DryRun: true,
		Args: json.RawMessage(`{"ref":"main"}`), Marker: "ddf:task-key=" + string(key), TargetRef: "refs/heads/x",
		ExpectedOldSHA: "old", HeadSHA: "head", CreatedAt: at,
	}
}

func mustPending(t *testing.T, s *store.Store) []model.Intent {
	t.Helper()
	ins, err := s.PendingIntents(t.Context())
	if err != nil {
		t.Fatalf("PendingIntents: %v", err)
	}
	return ins
}

func utcIntent(in model.Intent) model.Intent {
	in.CreatedAt = in.CreatedAt.UTC()
	return in
}

func TestIntents_Lifecycle(t *testing.T) {
	s, path := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	b, a, c := newIntent(task.Key, "b", t0), newIntent(task.Key, "a", t0), newIntent(task.Key, "c", t0.Add(-time.Second))
	for _, in := range []model.Intent{b, a, c} {
		if err := s.CreateIntent(t.Context(), in); err != nil {
			t.Fatalf("CreateIntent %s: %v", in.ID, err)
		}
	}
	want := []model.Intent{utcIntent(c), utcIntent(a), utcIntent(b)}
	if got := mustPending(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingIntents =\n%+v\nwant\n%+v", got, want)
	}

	out := store.IntentOutcome{ObjectID: "obj", NewSHA: "new", ErrorClass: "none"}
	if err := s.ResolveIntent(t.Context(), "a", model.IntentStatusSucceeded, out, t0.Add(time.Minute)); err != nil {
		t.Fatalf("ResolveIntent: %v", err)
	}
	if got := mustPending(t, s); !reflect.DeepEqual(got, []model.Intent{utcIntent(c), utcIntent(b)}) {
		t.Fatalf("PendingIntents after resolve = %+v", got)
	}
	var status, payload string
	var resolvedAt int64
	if err := rawDB(t, path).QueryRowContext(t.Context(), "SELECT status, resolved_at, payload FROM intents WHERE intent_id = 'a'").
		Scan(&status, &resolvedAt, &payload); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || resolvedAt != t0.Add(time.Minute).UnixMilli() ||
		p["objectId"] != "obj" || p["newSha"] != "new" || p["errorClass"] != "none" || p["marker"] != a.Marker {
		t.Fatalf("resolved row = %s %d %s", status, resolvedAt, payload)
	}
	if got, err := s.GetTask(t.Context(), task.Key); err != nil || !reflect.DeepEqual(got, utcTask(task)) {
		t.Fatalf("GetTask after resolve = %+v, %v", got, err)
	}

	if err := s.ResolveIntent(t.Context(), "a", model.IntentStatusFailed, out, t0); !errors.Is(err, store.ErrIntentResolved) {
		t.Errorf("double resolve = %v, want ErrIntentResolved", err)
	}
	if err := s.ResolveIntent(t.Context(), "zz", model.IntentStatusFailed, out, t0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("resolve missing = %v, want ErrNotFound", err)
	}
	if err := s.ResolveIntent(t.Context(), "b", model.IntentStatusPending, out, t0); err == nil {
		t.Error("resolve with status pending succeeded")
	}
	done := newIntent(task.Key, "d", t0)
	done.Status = model.IntentStatusSucceeded
	withOutcome := newIntent(task.Key, "e", t0)
	withOutcome.NewSHA = "x"
	for _, in := range []model.Intent{done, withOutcome} {
		if err := s.CreateIntent(t.Context(), in); err == nil {
			t.Errorf("CreateIntent %s succeeded", in.ID)
		}
	}
	if got := len(mustPending(t, s)); got != 2 {
		t.Fatalf("pending after refused creates = %d, want 2", got)
	}
}

func TestStore_Reopen(t *testing.T) {
	s, path := newTestStore(t)
	parent := newTask(t, "parent")
	mustCreate(t, s, parent)
	task := fullTask(t, parent.Key)
	mustCreate(t, s, task)
	if err := s.AppendEvent(t.Context(), model.Event{Type: "paused", At: t0}); err != nil {
		t.Fatal(err)
	}
	in := newIntent(task.Key, "i1", t0)
	if err := s.CreateIntent(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	evs, global := mustEvents(t, s, task.Key), mustEvents(t, s, "")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if got, err := s2.GetTask(t.Context(), task.Key); err != nil || !reflect.DeepEqual(got, utcTask(task)) {
		t.Fatalf("GetTask after reopen = %+v, %v", got, err)
	}
	if got := mustEvents(t, s2, task.Key); !reflect.DeepEqual(got, evs) {
		t.Fatalf("task events after reopen = %+v, want %+v", got, evs)
	}
	if got := mustEvents(t, s2, ""); !reflect.DeepEqual(got, global) || len(global) != 1 {
		t.Fatalf("global events after reopen = %+v, want %+v", got, global)
	}
	if got := mustPending(t, s2); !reflect.DeepEqual(got, []model.Intent{utcIntent(in)}) {
		t.Fatalf("pending after reopen = %+v", got)
	}
}
