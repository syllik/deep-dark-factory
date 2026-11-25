package local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

var ctx = context.Background()

func createTask(t *testing.T, f *local.Forge, key model.TaskKey, title string, labels ...string) model.TaskSource {
	t.Helper()
	src, err := f.CreateTask(ctx, ports.WriteModeLive, ports.NewTask{Key: key, Title: title, Body: "body of " + title, Labels: labels})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return src
}

func TestCounter_AllocatesInOrder(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.task.md", taskText("A", ""))
	e.drop(t, "b.request.md", taskText("B", ""))
	e.ingest(t, f)
	src := createTask(t, f, testKey(t, "1"), "C")
	if src.SourceID != "3" {
		t.Fatalf("third task = %s, want 3", src.SourceID)
	}
	if got := e.taskFiles(t); !slices.Equal(got, []string{"1.json", "2.json", "3.json"}) {
		t.Fatalf("records = %v", got)
	}
	if b, _ := os.ReadFile(e.path("counter")); string(b) != "3\n" {
		t.Fatalf("counter = %q", b)
	}
}

func TestCounter_GapAfterCrashIsNeverReused(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	createTask(t, f, testKey(t, "1"), "One")
	// Simulated crash: the counter advanced to 2, record 2 was never written.
	e.writeFile(t, "counter", []byte("2\n"))
	f = e.open(t)
	src := createTask(t, f, testKey(t, "2"), "Three")
	if src.SourceID != "3" {
		t.Fatalf("next task = %s, want 3", src.SourceID)
	}
	if got := e.taskFiles(t); !slices.Equal(got, []string{"1.json", "3.json"}) {
		t.Fatalf("records = %v", got)
	}
}

func TestCounter_LowerThanRecordsFailsOpen(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	createTask(t, f, testKey(t, "1"), "One")
	createTask(t, f, testKey(t, "2"), "Two")
	e.writeFile(t, "counter", []byte("1\n"))
	if _, err := local.Open(ctx, e.cfg); err == nil {
		t.Fatal("open succeeded with a counter below the highest record")
	}
}

func TestCreateTask_IdempotentPerKey(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	key := testKey(t, "7")
	first := createTask(t, f, key, "Child", "b", "a", "b")
	second := createTask(t, f, key, "Child again")
	want := model.TaskSource{Forge: "local", Repository: projectID, SourceKind: "task", SourceID: "1"}
	if first != want || second != want {
		t.Fatalf("sources = %v, %v; want %v", first, second, want)
	}
	if got := e.taskFiles(t); len(got) != 1 {
		t.Fatalf("records = %v, want one", got)
	}
	m := e.record(t, 1)
	if got := rawString(t, m, "marker"); got != "ddf:task-key="+string(key) {
		t.Errorf("marker = %q", got)
	}
	if got := rawString(t, m, "createdBy"); got != "ddf-daemon" {
		t.Errorf("createdBy = %q", got)
	}
	var labels []string
	if err := json.Unmarshal(m["labels"], &labels); err != nil || !slices.Equal(labels, []string{"a", "b"}) {
		t.Errorf("labels = %v, %v; want sorted and de-duplicated", labels, err)
	}
	if info, err := os.Lstat(e.path("answers", "1")); err != nil || !info.IsDir() {
		t.Errorf("answers/1: %v", err)
	}
}

func TestCreateTask_RefusesInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		task ports.NewTask
	}{
		{"invalid key", ports.NewTask{Key: "x", Title: "T"}},
		{"upper-case key", ports.NewTask{Key: model.TaskKey(bytes.ToUpper([]byte(testKey(t, "1")))), Title: "T"}},
		{"empty title", ports.NewTask{Key: testKey(t, "1"), Title: "  "}},
		{"empty label", ports.NewTask{Key: testKey(t, "1"), Title: "T", Labels: []string{""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			f := e.open(t)
			before := snapshot(t, e.root())
			if _, err := f.CreateTask(ctx, ports.WriteModeLive, tc.task); err == nil {
				t.Fatal("create task succeeded")
			}
			sameSnapshot(t, before, snapshot(t, e.root()))
		})
	}
}

func TestReadIntakeActor(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.task.md", taskText("Inbox", ""))
	e.ingest(t, f)
	created := createTask(t, f, testKey(t, "1"), "Created")
	for _, tc := range []struct {
		name string
		src  model.TaskSource
		want ports.Identity
	}{
		{"inbox record", source("task", 1), local.LocalOperator},
		{"created record", created, local.Daemon},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.ReadIntakeActor(ctx, tc.src)
			if err != nil || got != tc.want {
				t.Fatalf("actor = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	if local.LocalOperator.ID != "local-operator" || local.Daemon.ID != "ddf-daemon" {
		t.Errorf("identities = %v, %v", local.LocalOperator, local.Daemon)
	}
}

func TestReadAnswers(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.task.md", taskText("A", ""))
	e.drop(t, "b.task.md", taskText("B", ""))
	e.ingest(t, f)
	write := func(name, body, at string) {
		a := answer()
		a["body"], a["time"] = body, at
		e.writeFile(t, filepath.Join("answers", "1", name), mustJSON(t, a))
	}
	write("z.json", "second", "2026-11-25T10:00:00Z")
	write("a.json", "third", "2026-11-25T11:00:00Z")
	write("m.json", "first", "2026-11-25T09:00:00Z")
	write("b.json", "second-b", "2026-11-25T10:00:00Z")
	e.writeFile(t, "answers/1/.in-flight.json", []byte("partial"))
	got, err := f.ReadAnswers(ctx, source("task", 1))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range got {
		ids = append(ids, a.ID)
		if a.Author != local.LocalOperator {
			t.Errorf("answer %s author = %v", a.ID, a.Author)
		}
	}
	if !slices.Equal(ids, []string{"m", "b", "z", "a"}) {
		t.Fatalf("order = %v", ids)
	}
	if got[0].Body != "first" || !got[0].At.Equal(time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("first answer = %+v", got[0])
	}
	empty, err := f.ReadAnswers(ctx, source("task", 2))
	if err != nil || len(empty) != 0 {
		t.Fatalf("answers of task 2 = %v, %v", empty, err)
	}
	if err := os.RemoveAll(e.path("answers", "2")); err != nil {
		t.Fatal(err)
	}
	missing, err := f.ReadAnswers(ctx, source("task", 2))
	if err != nil || len(missing) != 0 {
		t.Fatalf("answers without a directory = %v, %v", missing, err)
	}
	e.writeFile(t, "answers/1/bad name.json", mustJSON(t, answer()))
	if _, err := f.ReadAnswers(ctx, source("task", 1)); err == nil {
		t.Error("read answers accepted an invalid answer id")
	}
}

func TestMalformedSource(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.task.md", taskText("A", ""))
	e.ingest(t, f)
	ok := source("task", 1)
	mod := func(fn func(s *model.TaskSource)) model.TaskSource { s := ok; fn(&s); return s }
	sources := []struct {
		name string
		src  model.TaskSource
	}{
		{"forge github", mod(func(s *model.TaskSource) { s.Forge = "github" })},
		{"another repository", mod(func(s *model.TaskSource) { s.Repository = "other" })},
		{"unknown kind", mod(func(s *model.TaskSource) { s.SourceKind = "issue" })},
		{"kind not matching the record", mod(func(s *model.TaskSource) { s.SourceKind = "request" })},
		{"leading zero", mod(func(s *model.TaskSource) { s.SourceID = "01" })},
		{"negative", mod(func(s *model.TaskSource) { s.SourceID = "-1" })},
		{"not a number", mod(func(s *model.TaskSource) { s.SourceID = "x" })},
		{"unknown number", mod(func(s *model.TaskSource) { s.SourceID = "99" })},
	}
	calls := []struct {
		name string
		call func(src model.TaskSource) error
	}{
		{"ReadIntakeActor", func(src model.TaskSource) error { _, err := f.ReadIntakeActor(ctx, src); return err }},
		{"ReadAnswers", func(src model.TaskSource) error { _, err := f.ReadAnswers(ctx, src); return err }},
		{"CloseTask", func(src model.TaskSource) error { return f.CloseTask(ctx, ports.WriteModeLive, src, "c") }},
		{"ReopenTask", func(src model.TaskSource) error { return f.ReopenTask(ctx, ports.WriteModeLive, src, "c") }},
		{"SetProjectionLabels", func(src model.TaskSource) error {
			return f.SetProjectionLabels(ctx, ports.WriteModeLive, src, []string{"l"})
		}},
	}
	for _, c := range calls {
		for _, s := range sources {
			t.Run(c.name+"/"+s.name, func(t *testing.T) {
				before := snapshot(t, e.root())
				if err := c.call(s.src); err == nil {
					t.Fatal("call succeeded")
				}
				sameSnapshot(t, before, snapshot(t, e.root()))
			})
		}
	}
}

// otherFields returns the record's fields without the given keys.
func otherFields(m map[string]json.RawMessage, drop ...string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if !slices.Contains(drop, k) {
			out[k] = string(v)
		}
	}
	return out
}

func sameFields(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Errorf("field set changed: %v vs %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("field %s changed: %s -> %s", k, v, after[k])
		}
	}
}

func TestCloseAndReopenTask(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	src := createTask(t, f, testKey(t, "1"), "T", "x")
	steps := []struct {
		name    string
		call    func() error
		state   string
		comment string
	}{
		{"close", func() error { return f.CloseTask(ctx, ports.WriteModeLive, src, "done") }, "closed", "done"},
		{"close again", func() error { return f.CloseTask(ctx, ports.WriteModeLive, src, "still done") }, "closed", "still done"},
		{"reopen", func() error { return f.ReopenTask(ctx, ports.WriteModeLive, src, "reverted") }, "open", "reverted"},
		{"reopen without comment", func() error { return f.ReopenTask(ctx, ports.WriteModeLive, src, "") }, "open", ""},
	}
	for i, s := range steps {
		before := e.record(t, 1)
		var beforeComments []json.RawMessage
		if err := json.Unmarshal(before["comments"], &beforeComments); err != nil {
			t.Fatal(err)
		}
		if err := e.clock.Advance(time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := s.call(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		after := e.record(t, 1)
		if got := rawString(t, after, "state"); got != s.state {
			t.Errorf("%s: state = %q, want %q", s.name, got, s.state)
		}
		var comments []struct {
			Body, Author string
			Time         time.Time
		}
		if err := json.Unmarshal(after["comments"], &comments); err != nil {
			t.Fatal(err)
		}
		wantLen := len(beforeComments)
		if s.comment != "" {
			wantLen++
		}
		if len(comments) != wantLen {
			t.Fatalf("%s: %d comments, want %d", s.name, len(comments), wantLen)
		}
		if s.comment != "" {
			last := comments[len(comments)-1]
			if last.Body != s.comment || last.Author != "ddf-daemon" || !last.Time.Equal(e.clock.Now()) {
				t.Errorf("%s: comment = %+v", s.name, last)
			}
		}
		var afterComments []json.RawMessage
		if err := json.Unmarshal(after["comments"], &afterComments); err != nil {
			t.Fatal(err)
		}
		for j := range beforeComments {
			if !bytes.Equal(beforeComments[j], afterComments[j]) {
				t.Errorf("step %d: earlier comment %d changed", i, j)
			}
		}
		sameFields(t, otherFields(before, "state", "comments"), otherFields(after, "state", "comments"))
	}
}

func TestSetProjectionLabels(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	src := createTask(t, f, testKey(t, "1"), "T", "old")
	before := e.record(t, 1)
	if err := f.SetProjectionLabels(ctx, ports.WriteModeLive, src, []string{"ddf:b", "ddf:a", "ddf:b"}); err != nil {
		t.Fatal(err)
	}
	after := e.record(t, 1)
	var labels []string
	if err := json.Unmarshal(after["labels"], &labels); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(labels, []string{"ddf:a", "ddf:b"}) {
		t.Fatalf("labels = %v", labels)
	}
	sameFields(t, otherFields(before, "labels"), otherFields(after, "labels"))

	snap := snapshot(t, e.root())
	if err := f.SetProjectionLabels(ctx, ports.WriteModeLive, src, []string{"ok", ""}); err == nil {
		t.Fatal("empty label accepted")
	}
	sameSnapshot(t, snap, snapshot(t, e.root()))

	if err := f.SetProjectionLabels(ctx, ports.WriteModeLive, src, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(e.record(t, 1)["labels"]); got != "[]" {
		t.Errorf("cleared labels = %s", got)
	}
}

func TestListCandidateTasks(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.task.md", taskText("Inbox task", "inbox body"))
	e.drop(t, "b.request.md", taskText("Request", "question"))
	e.ingest(t, f)
	created := createTask(t, f, testKey(t, "1"), "Created", "l2", "l1")
	if err := f.CloseTask(ctx, ports.WriteModeLive, source("request", 2), "no"); err != nil {
		t.Fatal(err)
	}
	got, err := f.ListCandidateTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.CandidateTask{
		{Source: source("task", 1), Title: "Inbox task", Body: "inbox body", Labels: []string{}},
		{Source: created, Title: "Created", Body: "body of Created", Labels: []string{"l1", "l2"}},
	}
	if len(got) != len(want) {
		t.Fatalf("tasks = %+v", got)
	}
	for i := range want {
		if got[i].Source != want[i].Source || got[i].Title != want[i].Title || got[i].Body != want[i].Body ||
			!slices.Equal(got[i].Labels, want[i].Labels) {
			t.Errorf("task %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPermissions(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	e.drop(t, "a.task.md", taskText("A", ""))
	e.drop(t, "b.md", []byte("rejected"))
	e.ingest(t, f)
	src := createTask(t, f, testKey(t, "1"), "C", "l")
	steps := []error{
		f.CloseTask(ctx, ports.WriteModeLive, src, "closed"),
		f.ReopenTask(ctx, ports.WriteModeLive, src, "reopened"),
		f.SetProjectionLabels(ctx, ports.WriteModeLive, src, []string{"m"}),
	}
	for i, err := range steps {
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	seen := 0
	err := filepath.WalkDir(e.root(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		want := fs.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", p, info.Mode().Perm(), want)
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"format", "counter", "tasks/1.json", "tasks/2.json", "answers/1", "answers/2", "inbox/rejected/b.md.reason"} {
		if _, err := os.Lstat(e.path(p)); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
	if seen < 12 {
		t.Errorf("walked only %d entries", seen)
	}
}
