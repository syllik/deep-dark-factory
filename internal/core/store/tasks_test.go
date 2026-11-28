package store_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// t0 is a ms-precision time in a non-UTC location.
var t0 = time.Date(2026, 11, 16, 10, 30, 0, 123_000_000, time.FixedZone("UTC+3", 3*3600))

// newTestStore opens a store on a file in t.TempDir and closes it at cleanup.
func newTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ddf.db")
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

// rawDB opens a plain database/sql handle (no store settings) on path.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func taskKey(t *testing.T, id string) model.TaskKey {
	t.Helper()
	k, err := model.NewTaskKey(model.TaskSource{Forge: "github", Repository: "o/r", SourceKind: "issue", SourceID: id})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newTask(t *testing.T, id string) model.Task {
	t.Helper()
	return model.Task{
		Key: taskKey(t, id), Forge: "github", Repository: "o/r", SourceKind: "issue", SourceID: id,
		Kind: model.TaskKindTask, State: model.TaskStateInbox, Priority: model.PriorityNormal,
		CreatedAt: t0, UpdatedAt: t0,
	}
}

func taskEvent(t model.Task, typ string) model.Event {
	return model.Event{Key: t.Key, Type: typ, At: t0}
}

func mustCreate(t *testing.T, s *store.Store, task model.Task) {
	t.Helper()
	if err := s.CreateTask(t.Context(), task, taskEvent(task, "created")); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

func mustEvents(t *testing.T, s *store.Store, key model.TaskKey) []model.Event {
	t.Helper()
	evs, err := s.ListEvents(t.Context(), key)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	return evs
}

// fullTask sets every field; parent must already exist.
func fullTask(t *testing.T, parent model.TaskKey) model.Task {
	task := newTask(t, "full")
	teardown := t0.Add(time.Hour)
	task.Kind, task.ParentKey, task.State = model.TaskKindFollowup, parent, model.TaskStateReview
	task.Flags = []model.TaskFlag{model.TaskFlagFailed, model.TaskFlagNeedsOwner, model.TaskFlagHold}
	task.Risk, task.Priority, task.HitStop = model.RiskClassSensitive, model.PriorityHigh, true
	task.Title, task.Spec = "Title", "Spec"
	task.Acceptance, task.Zones, task.DependsOn = []string{"a1"}, []string{"core"}, []string{"#1"}
	task.AgentDefaults = []model.Decision{{Key: task.Key, Question: "q?", Choice: "c", CreatedAt: t0}}
	task.PlannerRisk, task.UpdatedAt, task.TeardownAt = model.RiskClassLow, t0.Add(time.Minute), &teardown
	return task
}

// utcTask is task as the store returns it: UTC times, flags in fixed order.
func utcTask(task model.Task) model.Task {
	task.CreatedAt, task.UpdatedAt = task.CreatedAt.UTC(), task.UpdatedAt.UTC()
	if task.TeardownAt != nil {
		u := task.TeardownAt.UTC()
		task.TeardownAt = &u
	}
	for i := range task.AgentDefaults {
		task.AgentDefaults[i].CreatedAt = task.AgentDefaults[i].CreatedAt.UTC()
	}
	var flags []model.TaskFlag
	for _, f := range []model.TaskFlag{model.TaskFlagNeedsOwner, model.TaskFlagHold, model.TaskFlagFailed} {
		for _, g := range task.Flags {
			if f == g {
				flags = append(flags, f)
			}
		}
	}
	task.Flags = flags
	return task
}

func TestTask_CreateGetRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	parent := newTask(t, "parent")
	mustCreate(t, s, parent)
	task := fullTask(t, parent.Key)
	expiry := t0.Add(2 * time.Hour)
	ev := model.Event{Key: task.Key, Type: "created", Role: model.RolePlanner, Identity: "app", PermissionSet: []string{"issues:write"},
		TokenExpiresAt: &expiry, Commits: []string{"abc"}, Model: "m", Usage: &model.Usage{InputTokens: 1, OutputTokens: 2}, At: t0}
	if err := s.CreateTask(t.Context(), task, ev); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, err := s.GetTask(t.Context(), task.Key)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if want := utcTask(task); !reflect.DeepEqual(got, want) {
		t.Fatalf("GetTask =\n%+v\nwant\n%+v", got, want)
	}
	evs := mustEvents(t, s, task.Key)
	if len(evs) != 1 || evs[0].Seq <= 0 {
		t.Fatalf("events = %+v, want one with seq > 0", evs)
	}
	ev.Seq, ev.At = evs[0].Seq, t0.UTC()
	u := expiry.UTC()
	ev.TokenExpiresAt = &u
	if !reflect.DeepEqual(evs[0], ev) {
		t.Fatalf("event =\n%+v\nwant\n%+v", evs[0], ev)
	}
}

func TestTask_Update(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	task.State, task.HitStop, task.Flags, task.UpdatedAt = model.TaskStatePlanning, true, []model.TaskFlag{model.TaskFlagHold}, t0.Add(time.Second)
	if err := s.UpdateTask(t.Context(), task, taskEvent(task, "planning")); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	got, err := s.GetTask(t.Context(), task.Key)
	if err != nil || !reflect.DeepEqual(got, utcTask(task)) {
		t.Fatalf("GetTask = %+v, %v; want %+v", got, err, utcTask(task))
	}
	if evs := mustEvents(t, s, task.Key); len(evs) != 2 || evs[1].Type != "planning" || evs[1].Seq <= evs[0].Seq {
		t.Fatalf("events = %+v, want created then planning", evs)
	}

	// Clearing hit_stop fails and leaves the row and the events unchanged.
	cleared := task
	cleared.HitStop, cleared.State = false, model.TaskStateReady
	if err := s.UpdateTask(t.Context(), cleared, taskEvent(cleared, "ready")); err == nil {
		t.Fatal("UpdateTask clearing hit_stop succeeded")
	}
	if got, err := s.GetTask(t.Context(), task.Key); err != nil || !reflect.DeepEqual(got, utcTask(task)) {
		t.Fatalf("GetTask after failed update = %+v, %v", got, err)
	}
	if evs := mustEvents(t, s, task.Key); len(evs) != 2 {
		t.Fatalf("events after failed update = %d, want 2", len(evs))
	}

	missing := newTask(t, "missing")
	if err := s.UpdateTask(t.Context(), missing, taskEvent(missing, "x")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateTask missing = %v, want ErrNotFound", err)
	}
	if evs := mustEvents(t, s, missing.Key); len(evs) != 0 {
		t.Fatalf("events of missing task = %+v", evs)
	}
}

func TestTask_InvalidWritesNothing(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.GetTask(t.Context(), taskKey(t, "none")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTask missing = %v, want ErrNotFound", err)
	}
	task := newTask(t, "1")
	bad := task
	bad.State = "limbo"
	otherKey := taskEvent(task, "created")
	otherKey.Key = taskKey(t, "2")
	withSeq := taskEvent(task, "created")
	withSeq.Seq = 7
	cases := []struct {
		name string
		task model.Task
		ev   model.Event
	}{
		{"invalid task", bad, taskEvent(bad, "created")},
		{"event key mismatch", task, otherKey},
		{"event without key", task, model.Event{Type: "created", At: t0}},
		{"event with seq", task, withSeq},
	}
	for _, tc := range cases {
		if err := s.CreateTask(t.Context(), tc.task, tc.ev); err == nil {
			t.Errorf("%s: CreateTask succeeded", tc.name)
		}
	}
	if _, err := s.GetTask(t.Context(), task.Key); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTask after invalid creates = %v, want ErrNotFound", err)
	}
	if evs := mustEvents(t, s, task.Key); len(evs) != 0 {
		t.Fatalf("events after invalid creates = %+v", evs)
	}
}

// r2-2: hit_stop is 0 or 1.
func TestSchema_HitStopCheck(t *testing.T) {
	s, path := newTestStore(t)
	task := newTask(t, "1")
	task.HitStop = true
	mustCreate(t, s, task)
	if _, err := rawDB(t, path).ExecContext(t.Context(), "UPDATE tasks SET hit_stop = 2 WHERE task_key = ?", string(task.Key)); err == nil {
		t.Error("UPDATE hit_stop = 2 succeeded")
	}
	if got, err := s.GetTask(t.Context(), task.Key); err != nil || !got.HitStop {
		t.Fatalf("GetTask after refused update = HitStop %v, %v; want true", got.HitStop, err)
	}
}

// r2-2: no non-test store source uses a replacing write.
func TestSource_NoReplace(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToUpper(string(b)), "REPLACE") {
			t.Errorf("%s contains REPLACE", name)
		}
	}
}

// r2-3: questions.head_sha exists right after category and is nullable.
func TestSchema_QuestionsHeadSHA(t *testing.T) {
	_, path := newTestStore(t)
	rows, err := rawDB(t, path).QueryContext(t.Context(), "PRAGMA table_info(questions)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var (
			cid, notNull, pk int
			name, typ        string
			def              sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "head_sha" && (typ != "TEXT" || notNull != 0) {
			t.Errorf("head_sha type %s notnull %d, want TEXT nullable", typ, notNull)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cols, ","); !strings.Contains(got, "category,head_sha,") {
		t.Fatalf("questions columns %s, want head_sha after category", got)
	}
}

// #158: ListTasks returns every task, done included, by created_at then key.
func TestTask_ListAll(t *testing.T) {
	s, _ := newTestStore(t)
	a, b, c := newTask(t, "a"), newTask(t, "b"), newTask(t, "c")
	c.CreatedAt, c.UpdatedAt = t0.Add(-time.Second), t0.Add(-time.Second)
	for _, task := range []model.Task{b, a, c} {
		mustCreate(t, s, task)
	}
	b.State, b.UpdatedAt = model.TaskStateDone, t0.Add(time.Hour)
	if err := s.UpdateTask(t.Context(), b, taskEvent(b, "done")); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	got, err := s.ListTasks(t.Context())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	want := []model.Task{utcTask(c)}
	for _, task := range []model.Task{a, b} {
		want = append(want, utcTask(task))
	}
	if want[1].Key > want[2].Key {
		want[1], want[2] = want[2], want[1]
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListTasks =\n%+v\nwant\n%+v", got, want)
	}
	found := false
	for _, task := range got {
		found = found || (task.Key == b.Key && task.State == model.TaskStateDone)
	}
	if !found {
		t.Error("done task missing from ListTasks")
	}
}

// #158: an undecodable tasks row fails the whole ListTasks read.
func TestTask_ListAllFailsClosed(t *testing.T) {
	s, path := newTestStore(t)
	mustCreate(t, s, newTask(t, "a"))
	mustCreate(t, s, newTask(t, "b"))
	if _, err := rawDB(t, path).ExecContext(t.Context(), "UPDATE tasks SET state = 'lost' WHERE source_id = 'b'"); err != nil {
		t.Fatalf("raw exec: %v", err)
	}
	got, err := s.ListTasks(t.Context())
	if got != nil {
		t.Errorf("partial list %+v", got)
	}
	if !errors.Is(err, store.ErrDecode) {
		t.Fatalf("ListTasks error = %v, want ErrDecode", err)
	}
}
