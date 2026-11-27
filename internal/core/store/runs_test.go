package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// newRun is a started executor run of task key.
func newRun(key model.TaskKey, id string) model.Run {
	return model.Run{
		Key: key, ID: id, Role: model.RoleExecutor, Purpose: model.RunPurposeInitial, Attempt: 1,
		Status: model.RunStatusRunning, Provider: "p",
		Routing:   model.RoutingEntry{Adapter: "a", Provider: "p", Model: "m"},
		StartedAt: t0,
	}
}

// utcRun is r as the store returns it: UTC times.
func utcRun(r model.Run) model.Run {
	r.StartedAt = r.StartedAt.UTC()
	if r.FinishedAt != nil {
		u := r.FinishedAt.UTC()
		r.FinishedAt = &u
	}
	return r
}

func mustCreateRun(t *testing.T, s *store.Store, r model.Run) {
	t.Helper()
	if err := s.CreateRun(t.Context(), r); err != nil {
		t.Fatalf("CreateRun %s: %v", r.ID, err)
	}
}

func mustGetRun(t *testing.T, s *store.Store, id string) model.Run {
	t.Helper()
	r, err := s.GetRun(t.Context(), id)
	if err != nil {
		t.Fatalf("GetRun %s: %v", id, err)
	}
	return r
}

func finished(r model.Run) model.Run {
	end := t0.Add(time.Minute)
	r.Status, r.FinishedAt = model.RunStatusSucceeded, &end
	return r
}

func TestRun_FullRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	rev := 3
	r := newRun(task.Key, "r1")
	r.Purpose, r.Revision, r.Attempt, r.HeadSHA = model.RunPurposeCorrection, &rev, 2, "head"
	r.Routing.Effort = "high"
	mustCreateRun(t, s, r)
	if got := mustGetRun(t, s, "r1"); !reflect.DeepEqual(got, utcRun(r)) {
		t.Fatalf("GetRun after create =\n%+v\nwant\n%+v", got, utcRun(r))
	}

	end := t0.Add(time.Hour)
	r.Status, r.FailureClass, r.FinishedAt = model.RunStatusFailed, model.FailureClassRetryable, &end
	r.Usage = &model.Usage{InputTokens: 10, OutputTokens: 20}
	r.ResultDigest, r.PatchDigest, r.Error = "sha256:res", "sha256:patch", "boom"
	if err := s.FinishRun(t.Context(), r); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	got := mustGetRun(t, s, "r1")
	if !reflect.DeepEqual(got, utcRun(r)) {
		t.Fatalf("GetRun after finish =\n%+v\nwant\n%+v", got, utcRun(r))
	}
	if got.StartedAt.Location() != time.UTC || got.FinishedAt.Location() != time.UTC {
		t.Errorf("times not UTC: %v %v", got.StartedAt, got.FinishedAt)
	}
}

func TestRun_ListOrdered(t *testing.T) {
	s, _ := newTestStore(t)
	task, other := newTask(t, "1"), newTask(t, "2")
	mustCreate(t, s, task)
	mustCreate(t, s, other)
	b, a, c := newRun(task.Key, "b"), newRun(task.Key, "a"), newRun(task.Key, "c")
	c.StartedAt = t0.Add(-time.Second)
	for _, r := range []model.Run{b, a, c, newRun(other.Key, "o")} {
		mustCreateRun(t, s, r)
	}
	runs, err := s.ListRuns(t.Context(), task.Key)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if want := []model.Run{utcRun(c), utcRun(a), utcRun(b)}; !reflect.DeepEqual(runs, want) {
		t.Fatalf("ListRuns =\n%+v\nwant\n%+v", runs, want)
	}
	if runs, err := s.ListRuns(t.Context(), newTask(t, "3").Key); err != nil || runs != nil {
		t.Fatalf("ListRuns of a task without runs = %+v, %v", runs, err)
	}
}

func TestRun_CreateRefusedWritesNothing(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	end := t0
	cases := map[string]func(*model.Run){
		"status succeeded":  func(r *model.Run) { r.Status = model.RunStatusSucceeded },
		"attempt 3":         func(r *model.Run) { r.Attempt = 3 },
		"attempt 0":         func(r *model.Run) { r.Attempt = 0 },
		"provider mismatch": func(r *model.Run) { r.Provider = "q" },
		"empty provider":    func(r *model.Run) { r.Provider, r.Routing.Provider = "", "" },
		"empty id":          func(r *model.Run) { r.ID = "" },
		"finished at":       func(r *model.Run) { r.FinishedAt = &end },
		"failure class":     func(r *model.Run) { r.FailureClass = model.FailureClassRetryable },
		"unknown role":      func(r *model.Run) { r.Role = "boss" },
		"unknown task":      func(r *model.Run) { r.Key = newTask(t, "9").Key },
	}
	for name, mutate := range cases {
		r := newRun(task.Key, "r1")
		mutate(&r)
		if err := s.CreateRun(t.Context(), r); err == nil {
			t.Errorf("CreateRun %s succeeded", name)
		}
	}
	if runs, err := s.ListRuns(t.Context(), task.Key); err != nil || runs != nil {
		t.Fatalf("ListRuns after refused creates = %+v, %v", runs, err)
	}
	if _, err := s.GetRun(t.Context(), "r1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetRun after refused creates = %v, want ErrNotFound", err)
	}
}

func TestRun_FinishRefusedLeavesRow(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	running := newRun(task.Key, "r1")
	mustCreateRun(t, s, running)

	if err := s.FinishRun(t.Context(), finished(newRun(task.Key, "zz"))); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FinishRun missing = %v, want ErrNotFound", err)
	}
	rev := 1
	changed := map[string]func(*model.Run){
		"purpose":    func(r *model.Run) { r.Purpose = model.RunPurposeSync },
		"attempt":    func(r *model.Run) { r.Attempt = 2 },
		"role":       func(r *model.Run) { r.Role = model.RoleR1 },
		"revision":   func(r *model.Run) { r.Revision = &rev },
		"head sha":   func(r *model.Run) { r.HeadSHA = "other" },
		"provider":   func(r *model.Run) { r.Provider, r.Routing.Provider = "q", "q" },
		"started at": func(r *model.Run) { r.StartedAt = t0.Add(time.Second) },
		"routing":    func(r *model.Run) { r.Routing.Model = "m2" },
		"task key":   func(r *model.Run) { r.Key = newTask(t, "2").Key },
		"status running": func(r *model.Run) {
			r.Status = model.RunStatusRunning
		},
		"no finished at": func(r *model.Run) { r.FinishedAt = nil },
		"failure class on success": func(r *model.Run) {
			r.FailureClass = model.FailureClassRetryable
		},
	}
	for name, mutate := range changed {
		r := finished(running)
		mutate(&r)
		if err := s.FinishRun(t.Context(), r); err == nil {
			t.Errorf("FinishRun with changed %s succeeded", name)
		}
	}
	if got := mustGetRun(t, s, "r1"); !reflect.DeepEqual(got, utcRun(running)) {
		t.Fatalf("run after refused finishes =\n%+v\nwant\n%+v", got, utcRun(running))
	}

	done := finished(running)
	if err := s.FinishRun(t.Context(), done); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	again := done
	again.Status, again.Error = model.RunStatusDiscarded, "late"
	if err := s.FinishRun(t.Context(), again); !errors.Is(err, store.ErrRunFinished) {
		t.Errorf("FinishRun on a finished run = %v, want ErrRunFinished", err)
	}
	if got := mustGetRun(t, s, "r1"); !reflect.DeepEqual(got, utcRun(done)) {
		t.Fatalf("run after second finish =\n%+v\nwant\n%+v", got, utcRun(done))
	}
}

// #73: every bad stored runs value fails the read with ErrDecode.
func TestRun_DecodeFailsClosed(t *testing.T) {
	cases := []struct {
		name, stmt string
		get        bool
	}{
		{"role unknown", "UPDATE runs SET role = 'boss'", false},
		{"role empty", "UPDATE runs SET role = ''", false},
		{"purpose unknown", "UPDATE runs SET purpose = 'fun'", false},
		{"purpose empty", "UPDATE runs SET purpose = ''", false},
		{"status unknown", "UPDATE runs SET status = 'stuck'", false},
		{"status empty", "UPDATE runs SET status = ''", false},
		{"failure_class unknown", "UPDATE runs SET failure_class = 'cosmic'", false},
		{"failure_class empty", "UPDATE runs SET failure_class = ''", false},
		{"attempt 3", "UPDATE runs SET attempt = 3", false},
		{"task_key not a key", "UPDATE runs SET task_key = 'not a key'", true},
		{"payload schema", `UPDATE runs SET payload = '{"schema":"ddf.run/v2","routing":{"adapter":"a","provider":"p","model":"m"}}'`, false},
		{"payload unknown field", `UPDATE runs SET payload = '{"schema":"ddf.run/v1","routing":{"adapter":"a","provider":"p","model":"m"},"token":"x"}'`, false},
		{"provider differs from routing", "UPDATE runs SET provider = 'q'", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path := newTestStore(t)
			task := newTask(t, "1")
			mustCreate(t, s, task)
			mustCreateRun(t, s, newRun(task.Key, "r1"))
			if _, err := rawDB(t, path).ExecContext(t.Context(), tc.stmt); err != nil {
				t.Fatalf("raw exec: %v", err)
			}
			var err error
			if tc.get {
				_, err = s.GetRun(t.Context(), "r1")
			} else {
				var got []model.Run
				if got, err = s.ListRuns(t.Context(), task.Key); got != nil {
					t.Errorf("partial list %+v", got)
				}
			}
			if !errors.Is(err, store.ErrDecode) {
				t.Fatalf("read error = %v, want ErrDecode", err)
			}
			if _, err := s.GetRun(t.Context(), "r1"); !errors.Is(err, store.ErrDecode) {
				t.Fatalf("GetRun error = %v, want ErrDecode", err)
			}
		})
	}
}
