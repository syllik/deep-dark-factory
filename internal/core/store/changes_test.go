package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// newChange is an open change of kind change on branch "b" into "master".
func newChange(key model.TaskKey, id string) model.Change {
	return model.Change{
		Key: key, ID: id, Kind: model.ChangeKindChange, Branch: "b", BaseBranch: "master",
		HeadSHA: "head1", Revision: 1, Open: true, PatchID: "patch1", CreatedAt: t0, UpdatedAt: t0,
	}
}

// utcChange is c as the store returns it: UTC times.
func utcChange(c model.Change) model.Change {
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	for _, p := range []**time.Time{&c.MergedAt, &c.CISince} {
		if *p != nil {
			u := (**p).UTC()
			*p = &u
		}
	}
	return c
}

func mustChange(t *testing.T, s *store.Store, key model.TaskKey, id string) model.Change {
	t.Helper()
	c, err := s.GetChange(t.Context(), key, id)
	if err != nil {
		t.Fatalf("GetChange: %v", err)
	}
	return c
}

func mustCreateChange(t *testing.T, s *store.Store, c model.Change) {
	t.Helper()
	if err := s.CreateChange(t.Context(), c); err != nil {
		t.Fatalf("CreateChange %s: %v", c.ID, err)
	}
}

func TestChange_CreateGetRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	c := newChange(task.Key, "c1")
	merged, ci := t0.Add(time.Hour), t0.Add(time.Minute)
	c.Kind, c.Draft, c.Open, c.Revision = model.ChangeKindRevert, true, false, 3
	c.MergeSHA, c.MergedAt, c.CISince, c.UpdatedAt = "merge1", &merged, &ci, t0.Add(2*time.Hour)
	mustCreateChange(t, s, c)
	got := mustChange(t, s, task.Key, "c1")
	if want := utcChange(c); !reflect.DeepEqual(got, want) {
		t.Fatalf("GetChange =\n%+v\nwant\n%+v", got, want)
	}
	if got.CreatedAt.Location() != time.UTC || got.MergedAt.Location() != time.UTC {
		t.Errorf("times not UTC: %v %v", got.CreatedAt, got.MergedAt)
	}
	if _, err := s.GetChange(t.Context(), task.Key, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetChange missing = %v, want ErrNotFound", err)
	}
}

func TestChange_Update(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	c := newChange(task.Key, "c1")
	mustCreateChange(t, s, c)

	ci := t0.Add(time.Minute)
	c.HeadSHA, c.Revision, c.Open, c.Draft, c.PatchID = "head2", 2, false, true, "patch2"
	c.CISince, c.UpdatedAt = &ci, t0.Add(time.Hour)
	if err := s.UpdateChange(t.Context(), c); err != nil {
		t.Fatalf("UpdateChange: %v", err)
	}
	if got := mustChange(t, s, task.Key, "c1"); !reflect.DeepEqual(got, utcChange(c)) {
		t.Fatalf("after update =\n%+v\nwant\n%+v", got, utcChange(c))
	}

	merged := t0.Add(2 * time.Hour)
	c.MergeSHA, c.MergedAt, c.CISince = "merge1", &merged, nil
	if err := s.UpdateChange(t.Context(), c); err != nil {
		t.Fatalf("UpdateChange merge: %v", err)
	}
	if got := mustChange(t, s, task.Key, "c1"); !reflect.DeepEqual(got, utcChange(c)) {
		t.Fatalf("after merge =\n%+v\nwant\n%+v", got, utcChange(c))
	}

	missing := newChange(task.Key, "missing")
	if err := s.UpdateChange(t.Context(), missing); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UpdateChange missing = %v, want ErrNotFound", err)
	}
}

func TestChange_ImmutableFields(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	merged := t0.Add(time.Hour)
	c := newChange(task.Key, "c1")
	c.MergeSHA, c.MergedAt = "merge1", &merged
	mustCreateChange(t, s, c)
	before := mustChange(t, s, task.Key, "c1")

	other := t0.Add(2 * time.Hour)
	cases := []struct {
		name string
		edit func(*model.Change)
	}{
		{"kind", func(c *model.Change) { c.Kind = model.ChangeKindRevert }},
		{"branch", func(c *model.Change) { c.Branch = "other" }},
		{"base_branch", func(c *model.Change) { c.BaseBranch = "main" }},
		{"created_at", func(c *model.Change) { c.CreatedAt = t0.Add(time.Second) }},
		{"merge_sha", func(c *model.Change) { c.MergeSHA = "merge2" }},
		{"merged_at", func(c *model.Change) { c.MergedAt = &other }},
		{"merge cleared", func(c *model.Change) { c.MergeSHA, c.MergedAt = "", nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := c
			u.HeadSHA, u.UpdatedAt = "head2", t0.Add(3*time.Hour)
			tc.edit(&u)
			if err := s.UpdateChange(t.Context(), u); err == nil {
				t.Fatal("UpdateChange succeeded")
			}
			if got := mustChange(t, s, task.Key, "c1"); !reflect.DeepEqual(got, before) {
				t.Fatalf("row changed:\n%+v\nwant\n%+v", got, before)
			}
		})
	}
}

func TestChange_RequiredFields(t *testing.T) {
	s, path := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	mustCreateChange(t, s, newChange(task.Key, "c1"))
	before := mustChange(t, s, task.Key, "c1")
	merged := t0
	cases := []struct {
		name string
		edit func(*model.Change)
	}{
		{"patch_id", func(c *model.Change) { c.PatchID = "" }},
		{"head_sha", func(c *model.Change) { c.HeadSHA = "" }},
		{"branch", func(c *model.Change) { c.Branch = "" }},
		{"base_branch", func(c *model.Change) { c.BaseBranch = "" }},
		{"kind", func(c *model.Change) { c.Kind = "" }},
		{"merge_sha without merged_at", func(c *model.Change) { c.MergeSHA = "m" }},
		{"merged_at without merge_sha", func(c *model.Change) { c.MergedAt = &merged }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created := newChange(task.Key, "c2")
			tc.edit(&created)
			if err := s.CreateChange(t.Context(), created); err == nil {
				t.Error("CreateChange succeeded")
			}
			updated := newChange(task.Key, "c1")
			tc.edit(&updated)
			if err := s.UpdateChange(t.Context(), updated); err == nil {
				t.Error("UpdateChange succeeded")
			}
			var n int
			if err := rawDB(t, path).QueryRowContext(t.Context(), "SELECT COUNT(*) FROM changes").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Errorf("changes rows = %d, want 1", n)
			}
			if got := mustChange(t, s, task.Key, "c1"); !reflect.DeepEqual(got, before) {
				t.Errorf("row changed:\n%+v\nwant\n%+v", got, before)
			}
		})
	}
}

func TestChange_ListOrder(t *testing.T) {
	s, _ := newTestStore(t)
	task, other := newTask(t, "1"), newTask(t, "2")
	mustCreate(t, s, task)
	mustCreate(t, s, other)
	b, a, c := newChange(task.Key, "b"), newChange(task.Key, "a"), newChange(task.Key, "c")
	c.CreatedAt = t0.Add(-time.Second)
	for _, ch := range []model.Change{b, a, c, newChange(other.Key, "x")} {
		mustCreateChange(t, s, ch)
	}
	got, err := s.ListChanges(t.Context(), task.Key)
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if want := []model.Change{utcChange(c), utcChange(a), utcChange(b)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListChanges =\n%+v\nwant\n%+v", got, want)
	}
}

// #73: every bad stored changes value fails the read with ErrDecode.
func TestChange_DecodeFailsClosed(t *testing.T) {
	cases := []struct{ name, stmt string }{
		{"changes.kind", "UPDATE changes SET kind = 'fork'"},
		{"changes.kind empty", "UPDATE changes SET kind = ''"},
		{"changes.open", "UPDATE changes SET open = 2"},
		{"changes.draft", "UPDATE changes SET draft = 2"},
		{"changes payload schema", `UPDATE changes SET payload = '{"schema":"ddf.change/v2"}'`},
		{"changes payload unknown field", `UPDATE changes SET payload = '{"schema":"ddf.change/v1","x":1}'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path := newTestStore(t)
			task := newTask(t, "1")
			mustCreate(t, s, task)
			mustCreateChange(t, s, newChange(task.Key, "c1"))
			if _, err := rawDB(t, path).ExecContext(t.Context(), tc.stmt); err != nil {
				t.Fatalf("raw exec: %v", err)
			}
			if _, err := s.GetChange(t.Context(), task.Key, "c1"); !errors.Is(err, store.ErrDecode) {
				t.Errorf("GetChange error = %v, want ErrDecode", err)
			}
			got, err := s.ListChanges(t.Context(), task.Key)
			if got != nil {
				t.Errorf("partial list %+v", got)
			}
			if !errors.Is(err, store.ErrDecode) {
				t.Errorf("ListChanges error = %v, want ErrDecode", err)
			}
		})
	}
}

// #158: FindChangeByMergeSHA maps an integration commit to its one change.
func TestChange_FindByMergeSHA(t *testing.T) {
	s, _ := newTestStore(t)
	done, other := newTask(t, "1"), newTask(t, "2")
	mustCreate(t, s, done)
	mustCreate(t, s, other)
	merged := t0.Add(time.Hour)
	c := newChange(done.Key, "c1")
	c.Open, c.MergeSHA, c.MergedAt = false, "merge1", &merged
	mustCreateChange(t, s, c)
	mustCreateChange(t, s, newChange(other.Key, "c1"))
	done.State, done.UpdatedAt = model.TaskStateDone, merged
	if err := s.UpdateTask(t.Context(), done, taskEvent(done, "done")); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	got, err := s.FindChangeByMergeSHA(t.Context(), "merge1")
	if err != nil {
		t.Fatalf("FindChangeByMergeSHA: %v", err)
	}
	if want := utcChange(c); !reflect.DeepEqual(got, want) {
		t.Fatalf("FindChangeByMergeSHA =\n%+v\nwant\n%+v", got, want)
	}
	if _, err := s.FindChangeByMergeSHA(t.Context(), "unknown"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown SHA = %v, want ErrNotFound", err)
	}
	if _, err := s.FindChangeByMergeSHA(t.Context(), ""); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("empty SHA = %v, want a non-ErrNotFound error", err)
	}

	dup := newChange(other.Key, "c2")
	dup.Open, dup.MergeSHA, dup.MergedAt = false, "merge1", &merged
	mustCreateChange(t, s, dup)
	_, err = s.FindChangeByMergeSHA(t.Context(), "merge1")
	if err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("shared merge SHA = %v, want a non-ErrNotFound error", err)
	}
}

// #158: ListChangesSince returns changes of one kind across tasks from since.
func TestChange_ListSince(t *testing.T) {
	s, _ := newTestStore(t)
	t1, t2 := newTask(t, "1"), newTask(t, "2")
	mustCreate(t, s, t1)
	mustCreate(t, s, t2)
	revert := func(key model.TaskKey, id string, at time.Time) model.Change {
		c := newChange(key, id)
		c.Kind, c.CreatedAt, c.UpdatedAt = model.ChangeKindRevert, at, at
		return c
	}
	since := t0
	atSince2 := revert(t2.Key, "r1", since)
	atSince1 := revert(t1.Key, "r1", since)
	later := revert(t1.Key, "r2", since.Add(time.Minute))
	older := revert(t2.Key, "r0", since.Add(-time.Millisecond))
	plain := newChange(t1.Key, "c1")
	plain.CreatedAt = since.Add(time.Second)
	for _, c := range []model.Change{later, atSince2, older, plain, atSince1} {
		mustCreateChange(t, s, c)
	}

	got, err := s.ListChangesSince(t.Context(), model.ChangeKindRevert, since)
	if err != nil {
		t.Fatalf("ListChangesSince: %v", err)
	}
	first, second := atSince1, atSince2
	if first.Key > second.Key {
		first, second = second, first
	}
	want := []model.Change{utcChange(first), utcChange(second), utcChange(later)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListChangesSince =\n%+v\nwant\n%+v", got, want)
	}
	if _, err := s.ListChangesSince(t.Context(), "fork", since); err == nil {
		t.Error("ListChangesSince with invalid kind succeeded")
	}
	if _, err := s.ListChangesSince(t.Context(), "", since); err == nil {
		t.Error("ListChangesSince with empty kind succeeded")
	}
}

// #158: an undecodable changes row fails the whole ListChangesSince read.
func TestChange_ListSinceFailsClosed(t *testing.T) {
	s, path := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	c := newChange(task.Key, "r1")
	c.Kind = model.ChangeKindRevert
	mustCreateChange(t, s, c)
	if _, err := rawDB(t, path).ExecContext(t.Context(), "UPDATE changes SET open = 2"); err != nil {
		t.Fatalf("raw exec: %v", err)
	}
	got, err := s.ListChangesSince(t.Context(), model.ChangeKindRevert, t0)
	if got != nil {
		t.Errorf("partial list %+v", got)
	}
	if !errors.Is(err, store.ErrDecode) {
		t.Fatalf("ListChangesSince error = %v, want ErrDecode", err)
	}
}
