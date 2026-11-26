package local_test

import (
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

const (
	secretTitle   = "dry-title-7f3a"
	secretBody    = "dry-body-91c2"
	secretComment = "dry-comment-55de"
	secretLabel   = "dry-label-0b8e"
)

// dryRunFixture is what dryRunEnv set up: task 1 with its source, a new
// key, an open change with its head, a pushed but unpublished branch, and
// task 2 whose change is merged.
type dryRunFixture struct {
	src       model.TaskSource
	key       model.TaskKey
	change    ports.ChangeInfo
	newBranch string
	newHead   string
	mergedKey model.TaskKey
	mergeSHA  string
}

// dryRunCall runs one write with mode and reports whether its result is
// the zero value.
type dryRunCall struct {
	name string
	call func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (zero bool, err error)
}

func dryRunCalls() []dryRunCall {
	return []dryRunCall{
		{"Ingest", func(f *local.Forge, _ dryRunFixture, mode ports.WriteMode) (bool, error) {
			rej, err := f.Ingest(ctx, mode)
			return rej == nil, err
		}},
		{"CreateTask", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			src, err := f.CreateTask(ctx, mode, ports.NewTask{Key: fx.key, Title: secretTitle, Body: secretBody, Labels: []string{secretLabel}})
			return src == (model.TaskSource{}), err
		}},
		{"CloseTask", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			return true, f.CloseTask(ctx, mode, fx.src, secretComment)
		}},
		{"ReopenTask", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			return true, f.ReopenTask(ctx, mode, fx.src, secretComment)
		}},
		{"SetProjectionLabels", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			return true, f.SetProjectionLabels(ctx, mode, fx.src, []string{secretLabel})
		}},
		{"PublishChange", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			info, err := f.PublishChange(ctx, mode, ports.PublishRequest{
				Key: fx.key, Branch: fx.newBranch, BaseBranch: integrationBranch, HeadSHA: fx.newHead,
				Title: secretTitle, Body: secretBody,
			})
			return info == (ports.ChangeInfo{}), err
		}},
		{"UpdateChangeBody", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			return true, f.UpdateChangeBody(ctx, mode, fx.change.ID, secretBody)
		}},
		{"PostReview", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			return true, f.PostReview(ctx, mode, ports.ReviewPost{
				ChangeID: fx.change.ID, HeadSHA: fx.change.HeadSHA,
				Reviewer: ports.Identity{ID: "rev-a", Name: "Reviewer A"},
				Verdict:  model.ReviewVerdictApprove, Body: secretComment,
			})
		}},
		{"RerunChecks", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			return true, f.RerunChecks(ctx, mode, fx.change.HeadSHA)
		}},
		{"Merge", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			sha, err := f.Merge(ctx, mode, fx.change.ID, fx.change.HeadSHA)
			return sha == "", err
		}},
		{"UpdateBranchFromBase", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			sha, err := f.UpdateBranchFromBase(ctx, mode, fx.change.ID)
			return sha == "", err
		}},
		{"Revert", func(f *local.Forge, fx dryRunFixture, mode ports.WriteMode) (bool, error) {
			info, err := f.Revert(ctx, mode, fx.mergedKey, fx.mergeSHA)
			return info == (ports.ChangeInfo{}), err
		}},
	}
}

// dryRunEnv opens a project holding task 1, an un-ingested inbox file
// with the secret title and body, an open change on feat/a, a pushed,
// unpublished branch feat/new, and task 2 with a merged change on feat/m.
func dryRunEnv(t *testing.T) (*env, *local.Forge, dryRunFixture) {
	t.Helper()
	e := newEnv(t)
	f := e.open(t)
	fx := dryRunFixture{src: createTask(t, f, testKey(t, "existing"), "Existing"), key: testKey(t, "new")}
	head := e.commitOn(t, "feat/a", "a.txt", "a\n")
	fx.change = publish(t, f, publishReq(testKey(t, "existing"), "feat/a", head))
	fx.newBranch = "feat/new"
	fx.newHead = e.commitOn(t, fx.newBranch, "new.txt", "new\n")
	fx.mergedKey = testKey(t, "merged")
	createTask(t, f, fx.mergedKey, "Merged")
	merged := publish(t, f, publishReq(fx.mergedKey, "feat/m", e.commitOn(t, "feat/m", "m.txt", "m\n")))
	sha, err := f.Merge(ctx, ports.WriteModeLive, merged.ID, merged.HeadSHA)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	fx.mergeSHA = sha
	e.drop(t, "dry.task.md", taskText(secretTitle, secretBody))
	e.logs.Reset()
	return e, f, fx
}

func TestDryRun_WritesNothing(t *testing.T) {
	for _, c := range dryRunCalls() {
		t.Run(c.name, func(t *testing.T) {
			e, f, fx := dryRunEnv(t)
			before, refs := snapshot(t, e.root()), e.remoteRefs(t)
			zero, err := c.call(f, fx, ports.WriteModeDryRun)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if !zero {
				t.Error("dry run returned a non-zero result")
			}
			sameSnapshot(t, before, snapshot(t, e.root()))
			if got := e.remoteRefs(t); got != refs {
				t.Errorf("remote refs changed: before %q, after %q", refs, got)
			}
			logs := e.logs.String()
			if !strings.Contains(logs, c.name) {
				t.Errorf("log %q lacks the method name", logs)
			}
			for _, s := range []string{secretTitle, secretBody, secretComment, secretLabel} {
				if strings.Contains(logs, s) {
					t.Errorf("log contains free text %q: %s", s, logs)
				}
			}
		})
	}
}

func TestDryRun_InvalidModeWritesNothing(t *testing.T) {
	for _, mode := range []ports.WriteMode{"", "bogus"} {
		for _, c := range dryRunCalls() {
			t.Run(c.name+"/"+string(mode), func(t *testing.T) {
				e, f, fx := dryRunEnv(t)
				before, refs := snapshot(t, e.root()), e.remoteRefs(t)
				if _, err := c.call(f, fx, mode); err == nil {
					t.Fatal("invalid write mode accepted")
				}
				sameSnapshot(t, before, snapshot(t, e.root()))
				if got := e.remoteRefs(t); got != refs {
					t.Errorf("remote refs changed: before %q, after %q", refs, got)
				}
			})
		}
	}
}
