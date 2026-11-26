package local_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// revertFixture is a merge fixture whose change was merged; task 1 carries
// the change's key, so its revert branch is revert/issue-1-....
type revertFixture struct {
	mergeFixture
	key   model.TaskKey
	merge string
}

func newRevertFixture(t *testing.T) revertFixture {
	t.Helper()
	fx := newMergeFixture(t)
	key := testKey(t, "1")
	if src := createTask(t, fx.f, key, "Task one"); src.SourceID != "1" {
		t.Fatalf("task number = %s, want 1", src.SourceID)
	}
	merge, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	return revertFixture{mergeFixture: fx, key: key, merge: merge}
}

// commitOnMain commits name with content on top of the remote integration
// tip and pushes it; it returns the new tip.
func (e *env) commitOnMain(t *testing.T, name, content string) string {
	t.Helper()
	e.fetchWork(t)
	gitOut(t, e.git, e.work, "checkout", "-q", "-B", integrationBranch, "refs/remotes/origin/"+integrationBranch)
	return e.commitOn(t, integrationBranch, name, content)
}

func revertBranch(merge string) string { return "revert/issue-1-revert-" + merge[:12] }

func TestRevert_Clean(t *testing.T) {
	fx := newRevertFixture(t)
	e := fx.e
	tip := e.commitOnMain(t, "later.txt", "later\n")
	info, err := fx.f.Revert(ctx, ports.WriteModeLive, fx.key, fx.merge)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	branch := revertBranch(fx.merge)
	if info.Branch != branch || !info.Open {
		t.Fatalf("revert info = %+v, want open on %s", info, branch)
	}
	head := e.remoteTip(t, branch)
	if head != info.HeadSHA {
		t.Fatalf("remote %s = %s, want head %s", branch, head, info.HeadSHA)
	}
	if got := e.parents(t, head); !slices.Equal(got, []string{tip}) {
		t.Errorf("revert parents = %v, want [%s]", got, tip)
	}
	files := strings.Fields(gitOut(t, e.git, e.remote, "ls-tree", "-r", "--name-only", head))
	if !slices.Equal(files, []string{"README", "later.txt"}) {
		t.Errorf("revert tree files = %v, want [README later.txt]", files)
	}
	if got := gitOut(t, e.git, e.remote, "show", head+":later.txt"); got != "later" {
		t.Errorf("later.txt = %q, want the later commit's content", got)
	}
	_, msg := e.rawCommit(t, head)
	if want := "Revert: Add a\n\nReverts " + fx.merge + " (change " + fx.change.ID + ").\n"; msg != want {
		t.Errorf("revert message = %q, want %q", msg, want)
	}
	rec := e.readChange(t, info.ID)
	orig := e.readChange(t, fx.change.ID)
	if rec["kind"] != "revert" || rec["state"] != "open" || rec["marker"] != orig["marker"] ||
		rec["revertOf"] != fx.merge || rec["headSha"] != head || rec["branch"] != branch {
		t.Errorf("revert record = %v", rec)
	}
	if n := e.attemptCount(t, head); n != 1 {
		t.Fatalf("attempts of revert head = %d, want 1", n)
	}
	if a := e.readAttempt(t, head, 1); a["status"] != "running" || a["marker"] != orig["marker"] {
		t.Errorf("revert attempt = %v", a)
	}

	snap, refs := snapshot(t, e.root()), e.remoteRefs(t)
	again, err := fx.f.Revert(ctx, ports.WriteModeLive, fx.key, fx.merge)
	if err != nil {
		t.Fatalf("second revert: %v", err)
	}
	if again != info {
		t.Errorf("second revert = %+v, want %+v", again, info)
	}
	sameSnapshot(t, snap, snapshot(t, e.root()))
	if got := e.remoteRefs(t); got != refs {
		t.Errorf("second revert changed remote refs")
	}
}

func TestRevert_Conflict(t *testing.T) {
	fx := newRevertFixture(t)
	e := fx.e
	e.commitOnMain(t, "a.txt", "a changed later\n")
	snap, refs := snapshot(t, e.root()), e.remoteRefs(t)
	_, err := fx.f.Revert(ctx, ports.WriteModeLive, fx.key, fx.merge)
	if !errors.Is(err, local.ErrRevertConflict) {
		t.Fatalf("revert error = %v, want ErrRevertConflict", err)
	}
	sameSnapshot(t, snap, snapshot(t, e.root()))
	if got := e.remoteRefs(t); got != refs {
		t.Errorf("conflicting revert changed remote refs:\n%s\nwant\n%s", got, refs)
	}
}

func TestRevert_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, fx revertFixture) (model.TaskKey, string)
	}{
		{"not a merge sha", func(t *testing.T, fx revertFixture) (model.TaskKey, string) {
			return fx.key, fx.e.mainSHA
		}},
		{"change head instead of merge sha", func(t *testing.T, fx revertFixture) (model.TaskKey, string) {
			return fx.key, fx.change.HeadSHA
		}},
		{"merge sha of another key", func(t *testing.T, fx revertFixture) (model.TaskKey, string) {
			other := testKey(t, "2")
			createTask(t, fx.f, other, "Task two")
			return other, fx.merge
		}},
		{"revert branch exists without a record", func(t *testing.T, fx revertFixture) (model.TaskKey, string) {
			fx.e.pushTo(t, fx.e.mainSHA, revertBranch(fx.merge))
			return fx.key, fx.merge
		}},
		{"invalid merge sha", func(t *testing.T, fx revertFixture) (model.TaskKey, string) {
			return fx.key, "abc"
		}},
		{"invalid key", func(t *testing.T, fx revertFixture) (model.TaskKey, string) {
			return "not-a-key", fx.merge
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newRevertFixture(t)
			key, sha := tc.setup(t, fx)
			snap, refs := snapshot(t, fx.e.root()), fx.e.remoteRefs(t)
			if _, err := fx.f.Revert(ctx, ports.WriteModeLive, key, sha); err == nil {
				t.Fatal("revert accepted")
			} else if errors.Is(err, local.ErrRevertConflict) {
				t.Fatalf("revert error %v matches ErrRevertConflict", err)
			}
			sameSnapshot(t, snap, snapshot(t, fx.e.root()))
			if got := fx.e.remoteRefs(t); got != refs {
				t.Errorf("refused revert changed remote refs:\n%s\nwant\n%s", got, refs)
			}
		})
	}
}

func TestRevert_NoTaskRecord(t *testing.T) {
	fx := newMergeFixture(t)
	merge, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	snap, refs := snapshot(t, fx.e.root()), fx.e.remoteRefs(t)
	if _, err := fx.f.Revert(ctx, ports.WriteModeLive, testKey(t, "1"), merge); err == nil {
		t.Fatal("revert without a task record accepted")
	}
	sameSnapshot(t, snap, snapshot(t, fx.e.root()))
	if got := fx.e.remoteRefs(t); got != refs {
		t.Errorf("refused revert changed remote refs")
	}
}
