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

// newSyncFixture opens a project with an open change of testKey "1" on
// feat/a whose head writes name with content.
func newSyncFixture(t *testing.T, name, content string) mergeFixture {
	t.Helper()
	e := newEnv(t)
	f := e.open(t)
	head := e.commitOn(t, "feat/a", name, content)
	return mergeFixture{e: e, f: f, change: publish(t, f, publishReq(testKey(t, "1"), "feat/a", head))}
}

// refusedSync asserts that a refused UpdateBranchFromBase left the remote
// refs and the whole storage, the change record included, unchanged.
func refusedSync(t *testing.T, fx mergeFixture, refs string, snap map[string]string) {
	t.Helper()
	if got := fx.e.remoteRefs(t); got != refs {
		t.Errorf("remote refs changed:\n%s\nwant\n%s", got, refs)
	}
	sameSnapshot(t, snap, snapshot(t, fx.e.root()))
	if got := fx.e.readChange(t, fx.change.ID)["headSha"]; got != fx.change.HeadSHA {
		t.Errorf("record head = %v, want %s", got, fx.change.HeadSHA)
	}
}

func TestUpdateBranchFromBase_Clean(t *testing.T) {
	fx := newSyncFixture(t, "a.txt", "a\n")
	e, oldHead := fx.e, fx.change.HeadSHA
	base := e.commitOn(t, integrationBranch, "b.txt", "b\n")
	head, err := fx.f.UpdateBranchFromBase(ctx, ports.WriteModeLive, fx.change.ID)
	if err != nil {
		t.Fatalf("update branch from base: %v", err)
	}
	if got := e.parents(t, head); !slices.Equal(got, []string{oldHead, base}) {
		t.Errorf("new head parents = %v, want [%s %s]", got, oldHead, base)
	}
	header, msg := e.rawCommit(t, head)
	if want := "Merge branch 'main' into feat/a\n"; msg != want {
		t.Errorf("new head message = %q, want %q", msg, want)
	}
	sig := fmt.Sprintf("%s <%s> %d +0000", testIdentity.Name, testIdentity.Email, testStart.Unix())
	for _, line := range []string{"author " + sig, "committer " + sig} {
		if !slices.Contains(strings.Split(header, "\n"), line) {
			t.Errorf("new head header lacks %q:\n%s", line, header)
		}
	}
	if got := e.remoteTip(t, "feat/a"); got != head {
		t.Errorf("remote feat/a = %s, want new head %s", got, head)
	}
	if got := e.remoteTip(t, integrationBranch); got != base {
		t.Errorf("integration tip = %s, want unchanged %s", got, base)
	}
	rec := e.readChange(t, fx.change.ID)
	if rec["headSha"] != head || rec["state"] != "open" {
		t.Errorf("change record = head %v state %v, want %s open", rec["headSha"], rec["state"], head)
	}
	entries, err := os.ReadDir(e.path("checks", head))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "1.json" {
		t.Fatalf("attempts of the new head = %v, want only 1.json", entries)
	}
	a := e.readAttempt(t, head, 1)
	if a["status"] != "running" || a["marker"] != rec["marker"] {
		t.Errorf("new head attempt = status %v marker %v, want running %v", a["status"], a["marker"], rec["marker"])
	}
}

func TestUpdateBranchFromBase_Conflict(t *testing.T) {
	fx := newSyncFixture(t, "README", "feature\n")
	fx.e.commitOn(t, integrationBranch, "README", "main\n")
	refs, snap := fx.e.remoteRefs(t), snapshot(t, fx.e.root())
	_, err := fx.f.UpdateBranchFromBase(ctx, ports.WriteModeLive, fx.change.ID)
	var ce *local.SyncConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("update branch from base = %v, want *SyncConflictError", err)
	}
	if !slices.Equal(ce.Paths, []string{"README"}) {
		t.Errorf("conflict paths = %v, want [README]", ce.Paths)
	}
	refusedSync(t, fx, refs, snap)
}

func TestUpdateBranchFromBase_WorkflowChanged(t *testing.T) {
	fx := newSyncFixture(t, "a.txt", "a\n")
	fx.e.commitOn(t, integrationBranch, ".ddf/project.yaml", "verify: []\n")
	refs, snap := fx.e.remoteRefs(t), snapshot(t, fx.e.root())
	_, err := fx.f.UpdateBranchFromBase(ctx, ports.WriteModeLive, fx.change.ID)
	if !errors.Is(err, local.ErrWorkflowChanged) {
		t.Fatalf("update branch from base = %v, want ErrWorkflowChanged", err)
	}
	refusedSync(t, fx, refs, snap)
}

func TestUpdateBranchFromBase_UpToDate(t *testing.T) {
	fx := newSyncFixture(t, "a.txt", "a\n")
	refs, snap, heads := fx.e.remoteRefs(t), snapshot(t, fx.e.root()), fx.e.checkHeads(t)
	head, err := fx.f.UpdateBranchFromBase(ctx, ports.WriteModeLive, fx.change.ID)
	if err != nil {
		t.Fatalf("update branch from base: %v", err)
	}
	if head != fx.change.HeadSHA {
		t.Errorf("head = %s, want the old head %s", head, fx.change.HeadSHA)
	}
	if got := fx.e.checkHeads(t); !slices.Equal(got, heads) {
		t.Errorf("checks heads = %v, want %v", got, heads)
	}
	refusedSync(t, fx, refs, snap)
}

func TestUpdateBranchFromBase_HeadMoved(t *testing.T) {
	fx := newSyncFixture(t, "a.txt", "a\n")
	fx.e.commitOn(t, integrationBranch, "b.txt", "b\n")
	fx.e.commitOn(t, "feat/a", "a2.txt", "a2\n")
	refs, snap := fx.e.remoteRefs(t), snapshot(t, fx.e.root())
	_, err := fx.f.UpdateBranchFromBase(ctx, ports.WriteModeLive, fx.change.ID)
	if !errors.Is(err, ports.ErrHeadMoved) {
		t.Fatalf("update branch from base = %v, want ErrHeadMoved", err)
	}
	refusedSync(t, fx, refs, snap)
}

func TestUpdateBranchFromBase_Refusals(t *testing.T) {
	t.Run("closed change", func(t *testing.T) {
		fx := newSyncFixture(t, "a.txt", "a\n")
		fx.e.writeFile(t, "changes/c-1.json", mustJSON(t, changeJSON("c-1", testKey(t, "2"), "closed", "2026-11-25T09:00:00Z")))
		fx.e.commitOn(t, integrationBranch, "b.txt", "b\n")
		refs, snap := fx.e.remoteRefs(t), snapshot(t, fx.e.root())
		if _, err := fx.f.UpdateBranchFromBase(ctx, ports.WriteModeLive, "c-1"); err == nil {
			t.Fatal("update of a closed change succeeded")
		}
		refusedSync(t, fx, refs, snap)
	})
	t.Run("merged change", func(t *testing.T) {
		fx := newSyncFixture(t, "a.txt", "a\n")
		if _, err := fx.f.Merge(ctx, ports.WriteModeLive, fx.change.ID, fx.change.HeadSHA); err != nil {
			t.Fatal(err)
		}
		refs, snap := fx.e.remoteRefs(t), snapshot(t, fx.e.root())
		if _, err := fx.f.UpdateBranchFromBase(ctx, ports.WriteModeLive, fx.change.ID); err == nil {
			t.Fatal("update of a merged change succeeded")
		}
		if got := fx.e.remoteRefs(t); got != refs {
			t.Error("remote refs changed")
		}
		sameSnapshot(t, snap, snapshot(t, fx.e.root()))
	})
	for _, id := range []string{"nope", "../x", "a/b", "", "."} {
		t.Run("change id "+id, func(t *testing.T) {
			fx := newSyncFixture(t, "a.txt", "a\n")
			fx.e.commitOn(t, integrationBranch, "b.txt", "b\n")
			refs, snap := fx.e.remoteRefs(t), snapshot(t, fx.e.root())
			if _, err := fx.f.UpdateBranchFromBase(ctx, ports.WriteModeLive, id); err == nil {
				t.Fatal("update succeeded")
			}
			refusedSync(t, fx, refs, snap)
		})
	}
}
