package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// mustBranchIntent stores update-branch intent id of key, resolved with
// status and newSHA unless status is pending.
func mustBranchIntent(t *testing.T, s *store.Store, key model.TaskKey, id string, op model.IntentOperation,
	status model.IntentStatus, newSHA string) {
	t.Helper()
	in := newIntent(key, id, t0)
	in.Operation = op
	if err := s.CreateIntent(t.Context(), in); err != nil {
		t.Fatalf("CreateIntent %s: %v", id, err)
	}
	if status == model.IntentStatusPending {
		return
	}
	if err := s.ResolveIntent(t.Context(), id, status, store.IntentOutcome{NewSHA: newSHA}, t0); err != nil {
		t.Fatalf("ResolveIntent %s: %v", id, err)
	}
}

func newBaseMerge(key model.TaskKey, mergeSHA, intentID string) store.BaseMerge {
	return store.BaseMerge{
		Key: key, ChangeID: "c1", MergeSHA: mergeSHA, BaseSHA: "base1", ParentHeadSHA: "head1",
		IntentID: intentID, CreatedAt: t0,
	}
}

func countBaseMerges(t *testing.T, path string) int {
	t.Helper()
	var n int
	if err := rawDB(t, path).QueryRowContext(t.Context(), "SELECT COUNT(*) FROM base_merges").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBaseMerge_CreateList(t *testing.T) {
	s, _ := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	mustCreateChange(t, s, newChange(task.Key, "c1"))
	mustCreateChange(t, s, newChange(task.Key, "c2"))
	merges := []store.BaseMerge{
		newBaseMerge(task.Key, "m-b", "i1"),
		newBaseMerge(task.Key, "m-a", "i2"),
		newBaseMerge(task.Key, "m-c", "i3"),
	}
	merges[2].CreatedAt = t0.Add(-time.Second)
	for _, bm := range merges {
		mustBranchIntent(t, s, task.Key, bm.IntentID, model.IntentOperationUpdateBranch, model.IntentStatusSucceeded, bm.MergeSHA)
		if err := s.CreateBaseMerge(t.Context(), bm); err != nil {
			t.Fatalf("CreateBaseMerge %s: %v", bm.MergeSHA, err)
		}
	}
	other := newBaseMerge(task.Key, "m-x", "i4")
	other.ChangeID = "c2"
	mustBranchIntent(t, s, task.Key, "i4", model.IntentOperationUpdateBranch, model.IntentStatusSucceeded, "m-x")
	if err := s.CreateBaseMerge(t.Context(), other); err != nil {
		t.Fatalf("CreateBaseMerge other change: %v", err)
	}

	got, err := s.ListBaseMerges(t.Context(), task.Key, "c1")
	if err != nil {
		t.Fatalf("ListBaseMerges: %v", err)
	}
	want := []store.BaseMerge{merges[2], merges[1], merges[0]}
	for i := range want {
		want[i].CreatedAt = want[i].CreatedAt.UTC()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListBaseMerges =\n%+v\nwant\n%+v", got, want)
	}

	if err := s.CreateBaseMerge(t.Context(), merges[0]); err == nil {
		t.Error("duplicate CreateBaseMerge succeeded")
	}
}

func TestBaseMerge_RequiresIntent(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s *store.Store, key, otherKey model.TaskKey)
	}{
		{"missing intent", func(*testing.T, *store.Store, model.TaskKey, model.TaskKey) {}},
		{"intent of another task", func(t *testing.T, s *store.Store, _, otherKey model.TaskKey) {
			mustBranchIntent(t, s, otherKey, "i1", model.IntentOperationUpdateBranch, model.IntentStatusSucceeded, "m1")
		}},
		{"operation push", func(t *testing.T, s *store.Store, key, _ model.TaskKey) {
			mustBranchIntent(t, s, key, "i1", model.IntentOperationPush, model.IntentStatusSucceeded, "m1")
		}},
		{"status pending", func(t *testing.T, s *store.Store, key, _ model.TaskKey) {
			mustBranchIntent(t, s, key, "i1", model.IntentOperationUpdateBranch, model.IntentStatusPending, "")
		}},
		{"status failed", func(t *testing.T, s *store.Store, key, _ model.TaskKey) {
			mustBranchIntent(t, s, key, "i1", model.IntentOperationUpdateBranch, model.IntentStatusFailed, "m1")
		}},
		{"other new SHA", func(t *testing.T, s *store.Store, key, _ model.TaskKey) {
			mustBranchIntent(t, s, key, "i1", model.IntentOperationUpdateBranch, model.IntentStatusSucceeded, "m2")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path := newTestStore(t)
			task, other := newTask(t, "1"), newTask(t, "2")
			mustCreate(t, s, task)
			mustCreate(t, s, other)
			mustCreateChange(t, s, newChange(task.Key, "c1"))
			tc.setup(t, s, task.Key, other.Key)
			err := s.CreateBaseMerge(t.Context(), newBaseMerge(task.Key, "m1", "i1"))
			if !errors.Is(err, store.ErrBaseMergeIntent) {
				t.Fatalf("CreateBaseMerge = %v, want ErrBaseMergeIntent", err)
			}
			if n := countBaseMerges(t, path); n != 0 {
				t.Errorf("base_merges rows = %d, want 0", n)
			}
		})
	}
}

func TestBaseMerge_Invalid(t *testing.T) {
	s, path := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	mustBranchIntent(t, s, task.Key, "i1", model.IntentOperationUpdateBranch, model.IntentStatusSucceeded, "m1")

	if err := s.CreateBaseMerge(t.Context(), newBaseMerge(task.Key, "m1", "i1")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CreateBaseMerge without change = %v, want ErrNotFound", err)
	}
	mustCreateChange(t, s, newChange(task.Key, "c1"))
	cases := []struct {
		name string
		edit func(*store.BaseMerge)
	}{
		{"bad key", func(bm *store.BaseMerge) { bm.Key = "nope" }},
		{"empty change_id", func(bm *store.BaseMerge) { bm.ChangeID = "" }},
		{"empty merge_sha", func(bm *store.BaseMerge) { bm.MergeSHA = "" }},
		{"empty base_sha", func(bm *store.BaseMerge) { bm.BaseSHA = "" }},
		{"empty parent_head_sha", func(bm *store.BaseMerge) { bm.ParentHeadSHA = "" }},
		{"empty intent_id", func(bm *store.BaseMerge) { bm.IntentID = "" }},
		{"merge equals parent head", func(bm *store.BaseMerge) { bm.ParentHeadSHA = bm.MergeSHA }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bm := newBaseMerge(task.Key, "m1", "i1")
			tc.edit(&bm)
			if err := s.CreateBaseMerge(t.Context(), bm); err == nil {
				t.Fatal("CreateBaseMerge succeeded")
			}
		})
	}
	if n := countBaseMerges(t, path); n != 0 {
		t.Errorf("base_merges rows = %d, want 0", n)
	}
}

// #73: every bad stored base_merges value fails the read with ErrDecode.
func TestBaseMerge_DecodeFailsClosed(t *testing.T) {
	cases := []struct{ name, stmt string }{
		{"base_merges.merge_sha empty", "UPDATE base_merges SET merge_sha = ''"},
		{"base_merges.base_sha empty", "UPDATE base_merges SET base_sha = ''"},
		{"base_merges.parent_head_sha empty", "UPDATE base_merges SET parent_head_sha = ''"},
		{"base_merges.intent_id empty", "UPDATE base_merges SET intent_id = ''"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path := newTestStore(t)
			task := newTask(t, "1")
			mustCreate(t, s, task)
			mustCreateChange(t, s, newChange(task.Key, "c1"))
			mustBranchIntent(t, s, task.Key, "i1", model.IntentOperationUpdateBranch, model.IntentStatusSucceeded, "m1")
			if err := s.CreateBaseMerge(t.Context(), newBaseMerge(task.Key, "m1", "i1")); err != nil {
				t.Fatal(err)
			}
			if _, err := rawDB(t, path).ExecContext(t.Context(), tc.stmt); err != nil {
				t.Fatalf("raw exec: %v", err)
			}
			got, err := s.ListBaseMerges(t.Context(), task.Key, "c1")
			if got != nil {
				t.Errorf("partial list %+v", got)
			}
			if !errors.Is(err, store.ErrDecode) {
				t.Fatalf("ListBaseMerges error = %v, want ErrDecode", err)
			}
		})
	}
}
