package local_test

import (
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// TestEndToEnd drives one inbox task through the forge port on one bare
// remote: ingest, publish, checks, review, merge and revert.
func TestEndToEnd(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	var forge ports.Forge = f

	e.drop(t, "add-a.task.md", taskText("Add a", "Please add a."))
	if rej := e.ingest(t, f); len(rej) != 0 {
		t.Fatalf("rejections = %v", rej)
	}
	tasks, err := forge.ListCandidateTasks(ctx)
	if err != nil {
		t.Fatalf("list candidate tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "Add a" {
		t.Fatalf("candidate tasks = %+v, want the ingested task", tasks)
	}
	src := tasks[0].Source
	key, err := model.NewTaskKey(src)
	if err != nil {
		t.Fatal(err)
	}

	branch := "feat/issue-" + src.SourceID + "-add-a"
	head := e.commitOn(t, branch, "a.txt", "a\n")
	change, err := forge.PublishChange(ctx, ports.WriteModeLive, ports.PublishRequest{
		Key: key, Branch: branch, BaseBranch: integrationBranch, HeadSHA: head, Title: "Add a", Body: "Adds a.",
	})
	if err != nil {
		t.Fatalf("publish change: %v", err)
	}

	e.host.ScriptRun(key, fake.RunStep{Outcome: ports.CellRunOutcome{ExitCode: 0}})
	runChecks(t, f)
	checks, err := forge.ReadChecks(ctx, head)
	if err != nil {
		t.Fatalf("read checks: %v", err)
	}
	wantCheck(t, checks, head, true, true)

	if err := forge.PostReview(ctx, ports.WriteModeLive, ports.ReviewPost{
		ChangeID: change.ID, HeadSHA: head, Reviewer: ports.Identity{ID: "rev-a", Name: "Reviewer A"},
		Verdict: model.ReviewVerdictApprove, Body: "Looks good",
	}); err != nil {
		t.Fatalf("post review: %v", err)
	}

	merge, err := forge.Merge(ctx, ports.WriteModeLive, change.ID, head)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := e.remoteTip(t, integrationBranch); got != merge {
		t.Fatalf("integration tip = %s, want merge %s", got, merge)
	}

	found, ok, err := forge.FindChangeByTaskKey(ctx, key)
	if err != nil || !ok {
		t.Fatalf("find change: %v found=%v", err, ok)
	}
	if found.ID != change.ID || found.Open {
		t.Fatalf("found change = %+v, want merged change %s", found, change.ID)
	}

	revert, err := forge.Revert(ctx, ports.WriteModeLive, key, merge)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if !revert.Open || revert.ID == change.ID {
		t.Fatalf("revert = %+v, want a new open change", revert)
	}
	if want := "revert/issue-" + src.SourceID + "-revert-" + merge[:12]; revert.Branch != want {
		t.Errorf("revert branch = %q, want %q", revert.Branch, want)
	}
	if !strings.Contains(revert.Branch, "-"+src.SourceID+"-") {
		t.Errorf("revert branch %q lacks task number %s", revert.Branch, src.SourceID)
	}
	if got := e.remoteTip(t, revert.Branch); got != revert.HeadSHA {
		t.Errorf("remote %s = %s, want %s", revert.Branch, got, revert.HeadSHA)
	}
}
