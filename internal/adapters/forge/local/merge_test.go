package local_test

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// mergeFixture is an opened project with an open change of testKey "1" on
// feat/a, one commit ahead of the integration branch.
type mergeFixture struct {
	e      *env
	f      *local.Forge
	change ports.ChangeInfo
}

func newMergeFixture(t *testing.T) mergeFixture {
	t.Helper()
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	return mergeFixture{e: e, f: f, change: publish(t, f, publishReq(testKey(t, "1"), "feat/a", head))}
}

// unchanged asserts that the remote refs and the checks/ heads are what
// they were before a refused Merge, and that the change is still open with
// no merge SHA.
func (fx mergeFixture) unchanged(t *testing.T, refs string, heads []string) {
	t.Helper()
	if got := fx.e.remoteRefs(t); got != refs {
		t.Errorf("remote refs changed:\n%s\nwant\n%s", got, refs)
	}
	if got := fx.e.checkHeads(t); !slices.Equal(got, heads) {
		t.Errorf("checks heads = %v, want %v", got, heads)
	}
	rec := fx.e.readChange(t, fx.change.ID)
	if rec["state"] != "open" {
		t.Errorf("change state = %v, want open", rec["state"])
	}
	if _, ok := rec["mergeSha"]; ok {
		t.Errorf("change has mergeSha %v", rec["mergeSha"])
	}
}

func TestMerge_Success(t *testing.T) {
	fx := newMergeFixture(t)
	e, head := fx.e, fx.change.HeadSHA
	oldTip := e.remoteTip(t, integrationBranch)
	merge, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, head)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := e.remoteTip(t, integrationBranch); got != merge {
		t.Fatalf("integration tip = %s, want merge %s", got, merge)
	}
	e.fetchWork(t)
	if got := gitOut(t, e.git, e.work, "rev-parse", "refs/remotes/origin/"+integrationBranch); got != merge {
		t.Fatalf("work repository sees integration tip %s, want merge %s", got, merge)
	}
	if got := e.parents(t, merge); !slices.Equal(got, []string{oldTip}) {
		t.Errorf("merge parents = %v, want [%s]", got, oldTip)
	}
	if got, want := e.treeOn(t, merge), e.treeOn(t, head); got != want {
		t.Errorf("merge tree = %s, want head tree %s", got, want)
	}
	header, msg := e.rawCommit(t, merge)
	if want := "Add a\n\nBody of a\n"; msg != want {
		t.Errorf("merge message = %q, want %q", msg, want)
	}
	sig := fmt.Sprintf("%s <%s> %d +0000", testIdentity.Name, testIdentity.Email, testStart.Unix())
	for _, line := range []string{"author " + sig, "committer " + sig} {
		if !slices.Contains(strings.Split(header, "\n"), line) {
			t.Errorf("merge header lacks %q:\n%s", line, header)
		}
	}
	rec := e.readChange(t, fx.change.ID)
	if rec["state"] != "merged" || rec["mergeSha"] != merge {
		t.Errorf("change record = state %v mergeSha %v, want merged %s", rec["state"], rec["mergeSha"], merge)
	}
	entries, err := os.ReadDir(e.path("checks", merge))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "1.json" {
		t.Fatalf("attempts of merge = %v, want only 1.json", entries)
	}
	a := e.readAttempt(t, merge, 1)
	if a["status"] != "running" || a["marker"] != rec["marker"] {
		t.Errorf("merge attempt = status %v marker %v, want running %v", a["status"], a["marker"], rec["marker"])
	}

	snap, refs := snapshot(t, e.root()), e.remoteRefs(t)
	again, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, head)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if again != merge {
		t.Errorf("second merge = %s, want %s", again, merge)
	}
	sameSnapshot(t, snap, snapshot(t, e.root()))
	if got := e.remoteRefs(t); got != refs {
		t.Errorf("second merge changed remote refs")
	}
}

func TestMerge_EmptyBodyMessage(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	req := publishReq(testKey(t, "1"), "feat/a", head)
	req.Body = ""
	info := publish(t, f, req)
	merge, err := f.Merge(ctx, ports.WriteModeLive, info.ID, head)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, msg := e.rawCommit(t, merge); msg != "Add a\n" {
		t.Errorf("merge message = %q, want %q", msg, "Add a\n")
	}
}

// Probe 8, moved integration branch.
func TestMerge_BehindBase(t *testing.T) {
	fx := newMergeFixture(t)
	fx.e.commitOn(t, integrationBranch, "b.txt", "b\n")
	refs, heads := fx.e.remoteRefs(t), fx.e.checkHeads(t)
	_, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA)
	if !errors.Is(err, local.ErrBehindBase) || errors.Is(err, ports.ErrHeadMoved) {
		t.Fatalf("merge = %v, want ErrBehindBase only", err)
	}
	fx.unchanged(t, refs, heads)
}

// Probe 8, moved head.
func TestMerge_HeadMoved(t *testing.T) {
	fx := newMergeFixture(t)
	newHead := fx.e.commitOn(t, "feat/a", "a2.txt", "a2\n")
	refs, heads := fx.e.remoteRefs(t), fx.e.checkHeads(t)
	_, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA)
	if !errors.Is(err, ports.ErrHeadMoved) || errors.Is(err, local.ErrBehindBase) {
		t.Fatalf("merge of the old head = %v, want ErrHeadMoved only", err)
	}
	fx.unchanged(t, refs, heads)
	_, err = fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, newHead)
	if !errors.Is(err, ports.ErrHeadMoved) || errors.Is(err, local.ErrBehindBase) {
		t.Fatalf("merge of a head other than the record head = %v, want ErrHeadMoved only", err)
	}
	fx.unchanged(t, refs, heads)
}

// Lease test (a): the integration branch moves inside the lease window.
func TestMerge_LeaseRefusesMovedBase(t *testing.T) {
	fx := newMergeFixture(t)
	refs, heads := fx.e.remoteRefs(t), fx.e.checkHeads(t)
	var moved string
	local.SetBeforeMergePush(fx.f, func() { moved = fx.e.commitOn(t, integrationBranch, "b.txt", "b\n") })
	_, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA)
	if !errors.Is(err, local.ErrBehindBase) || errors.Is(err, ports.ErrHeadMoved) {
		t.Fatalf("merge = %v, want ErrBehindBase only", err)
	}
	if got := fx.e.remoteTip(t, integrationBranch); got != moved {
		t.Errorf("integration tip = %s, want the hook's commit %s", got, moved)
	}
	if refs == fx.e.remoteRefs(t) {
		t.Fatal("hook did not move the integration branch")
	}
	fx.unchanged(t, fx.e.remoteRefs(t), heads)
}

// Lease test (b): the change head moves inside the lease window.
func TestMerge_LeaseRefusesMovedHead(t *testing.T) {
	fx := newMergeFixture(t)
	oldTip := fx.e.remoteTip(t, integrationBranch)
	heads := fx.e.checkHeads(t)
	local.SetBeforeMergePush(fx.f, func() { fx.e.commitOn(t, "feat/a", "a2.txt", "a2\n") })
	_, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA)
	if !errors.Is(err, ports.ErrHeadMoved) || errors.Is(err, local.ErrBehindBase) {
		t.Fatalf("merge = %v, want ErrHeadMoved only", err)
	}
	if got := fx.e.remoteTip(t, integrationBranch); got != oldTip {
		t.Errorf("integration tip = %s, want unchanged %s", got, oldTip)
	}
	fx.unchanged(t, fx.e.remoteRefs(t), heads)
}

func TestMerge_Refusals(t *testing.T) {
	t.Run("closed change", func(t *testing.T) {
		fx := newMergeFixture(t)
		fx.e.writeFile(t, "changes/c-1.json", mustJSON(t, changeJSON("c-1", testKey(t, "2"), "closed", "2026-11-25T09:00:00Z")))
		refs := fx.e.remoteRefs(t)
		if _, err := fx.f.Merge(ctx, ports.WriteModeLive, "c-1", "0123456789abcdef0123456789abcdef01234567"); err == nil {
			t.Fatal("merge of a closed change succeeded")
		}
		if got := fx.e.remoteRefs(t); got != refs {
			t.Error("remote refs changed")
		}
	})
	t.Run("merged change named with another head", func(t *testing.T) {
		fx := newMergeFixture(t)
		if _, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA); err != nil {
			t.Fatal(err)
		}
		refs, snap := fx.e.remoteRefs(t), snapshot(t, fx.e.root())
		if _, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.e.mainSHA); err == nil {
			t.Fatal("merge of a merged change with another head succeeded")
		}
		if got := fx.e.remoteRefs(t); got != refs {
			t.Error("remote refs changed")
		}
		sameSnapshot(t, snap, snapshot(t, fx.e.root()))
	})
	for _, id := range []string{"nope", "../x", "a/b", "", "."} {
		t.Run("change id "+id, func(t *testing.T) {
			fx := newMergeFixture(t)
			refs, snap := fx.e.remoteRefs(t), snapshot(t, fx.e.root())
			if _, err := fx.f.Merge(ctx, ports.WriteModeLive, id, fx.change.HeadSHA); err == nil {
				t.Fatal("merge succeeded")
			}
			if got := fx.e.remoteRefs(t); got != refs {
				t.Error("remote refs changed")
			}
			sameSnapshot(t, snap, snapshot(t, fx.e.root()))
		})
	}
	t.Run("invalid head sha", func(t *testing.T) {
		fx := newMergeFixture(t)
		refs := fx.e.remoteRefs(t)
		if _, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA[:12]); err == nil {
			t.Fatal("merge succeeded")
		}
		if got := fx.e.remoteRefs(t); got != refs {
			t.Error("remote refs changed")
		}
	})
	t.Run("head equal to the integration tip", func(t *testing.T) {
		e := newEnv(t)
		f := e.open(t)
		e.pushTo(t, e.mainSHA, "feat/same")
		info := publish(t, f, publishReq(testKey(t, "1"), "feat/same", e.mainSHA))
		refs, heads := e.remoteRefs(t), e.checkHeads(t)
		_, err := f.Merge(ctx, ports.WriteModeLive, info.ID, e.mainSHA)
		if err == nil {
			t.Fatal("merge of the integration tip succeeded")
		}
		if errors.Is(err, local.ErrBehindBase) || errors.Is(err, ports.ErrHeadMoved) {
			t.Errorf("merge = %v, want a plain error", err)
		}
		mergeFixture{e: e, f: f, change: info}.unchanged(t, refs, heads)
	})
	t.Run("crash after push", func(t *testing.T) {
		fx := newMergeFixture(t)
		// The integration tip is a squash of the head pushed without its
		// record: not an ancestor of the head, with the head's tree.
		squash := fx.e.commitTreeOn(t, fx.change.HeadSHA, "Add a", fx.e.mainSHA)
		fx.e.pushTo(t, squash, integrationBranch)
		refs, heads := fx.e.remoteRefs(t), fx.e.checkHeads(t)
		_, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA)
		if err == nil {
			t.Fatal("merge succeeded after a simulated crash")
		}
		if errors.Is(err, local.ErrBehindBase) || errors.Is(err, ports.ErrHeadMoved) {
			t.Errorf("merge = %v, want a plain error", err)
		}
		fx.unchanged(t, refs, heads)
	})
	t.Run("change branch missing", func(t *testing.T) {
		fx := newMergeFixture(t)
		gitOut(t, fx.e.git, fx.e.remote, "update-ref", "-d", "refs/heads/feat/a")
		refs, heads := fx.e.remoteRefs(t), fx.e.checkHeads(t)
		_, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA)
		if err == nil || errors.Is(err, local.ErrBehindBase) || errors.Is(err, ports.ErrHeadMoved) {
			t.Fatalf("merge = %v, want a plain error", err)
		}
		fx.unchanged(t, refs, heads)
	})
}
