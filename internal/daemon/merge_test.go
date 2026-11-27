package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/daemon"
)

// mergeIntegration lets the engine merge: the profile requests
// merge-integration under the default operator maximum merge-integration.
func mergeIntegration(s *daemon.Settings, _ *daemon.Deps) {
	s.Profile.Autonomy = policy.AutonomyMergeIntegration
}

// faultForge fails chosen forge writes before they reach the inner forge.
type faultForge struct {
	ports.Forge
	mu         sync.Mutex
	mergeErr   error // returned by every Merge when set
	closeFails int   // CloseTask calls still to fail
}

func (f *faultForge) Merge(ctx context.Context, mode ports.WriteMode, changeID, headSHA string) (string, error) {
	if f.mergeErr != nil {
		return "", f.mergeErr
	}
	return f.Forge.Merge(ctx, mode, changeID, headSHA)
}

func (f *faultForge) CloseTask(ctx context.Context, mode ports.WriteMode, src model.TaskSource, comment string) error {
	f.mu.Lock()
	fail := f.closeFails > 0
	if fail {
		f.closeFails--
	}
	f.mu.Unlock()
	if fail {
		return errors.New("test forge: close task unavailable")
	}
	return f.Forge.CloseTask(ctx, mode, src, comment)
}

// countCreates counts the intents of op the engine created.
func countCreates(e *engEnv, op model.IntentOperation) int {
	n := 0
	for _, s := range e.rec.eventStrings() {
		if s == fmt.Sprintf("create %s dryRun=false", op) {
			n++
		}
	}
	return n
}

func mainTip(t *testing.T, e *engEnv) string {
	t.Helper()
	return engGit(t, e.git, e.bare, "rev-parse", "--verify", "refs/heads/"+engBranch+"^{commit}")
}

func TestTick_HappyPathToDone(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, mergeIntegration)
	e.addInbox(t)
	e.scriptToMerge()
	seed := mainTip(t, e)
	task := e.tickUntil(t, 15, model.TaskStateDone)

	wantStates := []model.TaskState{model.TaskStateInbox, model.TaskStatePlanning, model.TaskStateReady,
		model.TaskStateRunning, model.TaskStatePublishing, model.TaskStateCI, model.TaskStateReview,
		model.TaskStateMerge, model.TaskStateDone}
	if !slices.Equal(e.rec.states, wantStates) {
		t.Errorf("stored states = %v, want %v", e.rec.states, wantStates)
	}
	var wantEvents []string
	for _, op := range []string{"push", "open-change", "post-review", "merge", "close-task"} {
		wantEvents = append(wantEvents, "create "+op+" dryRun=false", "write "+op+" live", "resolve "+op+" succeeded")
	}
	if got := e.rec.eventStrings(); !slices.Equal(got, wantEvents) {
		t.Errorf("intent and write calls = %v, want %v", got, wantEvents)
	}
	if len(e.rec.outside) != 0 {
		t.Errorf("writes outside an intent: %v", e.rec.outside)
	}
	if pending, err := e.store.PendingIntents(t.Context()); err != nil || len(pending) != 0 {
		t.Errorf("pending intents = %v, %v; want none", pending, err)
	}

	c := storedChange(t, e, task.Key)
	tip := mainTip(t, e)
	if c.MergeSHA == "" || tip != c.MergeSHA {
		t.Errorf("bare main tip = %s, stored merge sha = %q", tip, c.MergeSHA)
	}
	if parent := engGit(t, e.git, e.bare, "rev-parse", tip+"^"); parent != seed {
		t.Errorf("merge parent = %s, want seed %s", parent, seed)
	}
	if got := engGit(t, e.git, e.bare, "show", tip+":hello.txt"); got != "hello" {
		t.Errorf("merged hello.txt = %q", got)
	}
	if c.Open || c.MergedAt == nil {
		t.Errorf("stored change open %v mergedAt %v, want closed with mergedAt", c.Open, c.MergedAt)
	}
	if cands, err := e.forge.ListCandidateTasks(t.Context()); err != nil || len(cands) != 0 {
		t.Errorf("candidates = %v, %v; want none (task closed)", cands, err)
	}
	rs := storedReviews(t, e, task, c)
	if len(rs) != 1 || rs[0].Role != model.RoleR1 || rs[0].Round != 1 || rs[0].KeyID != e.signer.KeyID() {
		t.Fatalf("stored reviews = %+v, want r1 round 1 signed by the engine key", rs)
	}
	verifyReview(t, e, task, c, rs[0])
}

func TestTick_ProposeStopsAtMerge(t *testing.T) {
	cases := []struct {
		name              string
		profile, operator policy.Autonomy
	}{
		{"profile propose under operator merge-integration", policy.AutonomyPropose, policy.AutonomyMergeIntegration},
		{"profile merge-integration under operator propose", policy.AutonomyMergeIntegration, policy.AutonomyPropose},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, _ *daemon.Deps) {
				s.Profile.Autonomy, s.OperatorMax = tc.profile, tc.operator
			})
			e.addInbox(t)
			e.scriptToMerge()
			before := mainTip(t, e)
			task := e.tickUntil(t, 15, model.TaskStateMerge)
			for range 2 {
				if err := e.engine.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			task, err := e.store.GetTask(t.Context(), task.Key)
			if err != nil {
				t.Fatal(err)
			}
			if task.State != model.TaskStateMerge || len(task.Flags) != 0 || task.HitStop {
				t.Errorf("task = %s %v hitStop %v, want merge without flags", task.State, task.Flags, task.HitStop)
			}
			if n := countCreates(e, model.IntentOperationMerge); n != 0 {
				t.Errorf("merge intents = %d, want 0", n)
			}
			info, found, err := e.forge.FindChangeByTaskKey(t.Context(), task.Key)
			if err != nil || !found || !info.Open {
				t.Errorf("forge change = %+v found %v err %v, want open", info, found, err)
			}
			if after := mainTip(t, e); after != before {
				t.Errorf("bare main = %s, want unchanged %s", after, before)
			}
		})
	}
}

func TestTick_MergeGateRefusesForgedReview(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, mergeIntegration)
	e.addInbox(t)
	e.scriptToMerge()
	before := mainTip(t, e)
	task := e.tickUntil(t, 15, model.TaskStateMerge)

	c := storedChange(t, e, task.Key)
	runs, err := e.store.ListRuns(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(runs, func(r model.Run) bool { return r.Role == model.RoleR1 })
	if i < 0 {
		t.Fatalf("stored runs = %+v, want an r1 run", runs)
	}
	_, forger := engSigner(t, 2)
	digest, err := signature.FindingsDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := forger.Sign(signature.Payload{
		Version: signature.Version, KeyID: forger.KeyID(), TaskKey: task.Key, Forge: task.Forge,
		Repository: task.Repository, ChangeID: c.ID, Head: c.HeadSHA, Role: model.RoleR1, Round: 2,
		Verdict: model.ReviewVerdictApprove, FindingsDigest: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.CreateReview(t.Context(), model.Review{
		Key: task.Key, ID: "forged-review", ChangeID: c.ID, Role: model.RoleR1, HeadSHA: c.HeadSHA, Round: 2,
		Verdict: model.ReviewVerdictApprove, FindingsDigest: digest, SigVersion: 1, KeyID: forger.KeyID(),
		Signature: sig, RunID: runs[i].ID, CreatedAt: e.clock.Now(),
	}, nil); err != nil {
		t.Fatalf("store forged review: %v", err)
	}

	if err := e.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	task, err = e.store.GetTask(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateReview || len(task.Flags) != 0 {
		t.Errorf("task = %s %v, want review", task.State, task.Flags)
	}
	if n := countCreates(e, model.IntentOperationMerge); n != 0 {
		t.Errorf("merge intents = %d, want 0", n)
	}
	if after := mainTip(t, e); after != before {
		t.Errorf("bare main = %s, want unchanged %s", after, before)
	}
}

func TestTick_MergeHeadMovedToCI(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, d *daemon.Deps) {
		mergeIntegration(s, d)
		d.Forge = &faultForge{Forge: d.Forge, mergeErr: fmt.Errorf("test forge: %w", ports.ErrHeadMoved)}
	})
	e.addInbox(t)
	e.scriptToMerge()
	task := e.tickUntil(t, 15, model.TaskStateMerge)
	if err := e.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	task, err := e.store.GetTask(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateCI || len(task.Flags) != 0 {
		t.Errorf("task = %s %v, want ci", task.State, task.Flags)
	}
	if got := e.rec.eventStrings(); !slices.Contains(got, "resolve merge failed") {
		t.Errorf("intent and write calls = %v, want resolve merge failed", got)
	}
	if c := storedChange(t, e, task.Key); c.MergeSHA != "" || !c.Open {
		t.Errorf("stored change = %+v, want open and unmerged", c)
	}
}

func TestTick_MergeResumesCloseAfterError(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, d *daemon.Deps) {
		mergeIntegration(s, d)
		d.Forge = &faultForge{Forge: d.Forge, closeFails: 1}
	})
	e.addInbox(t)
	e.scriptToMerge()
	task := e.tickUntil(t, 15, model.TaskStateMerge)

	err := e.engine.Tick(t.Context())
	if err == nil || !strings.Contains(err.Error(), "close task unavailable") {
		t.Fatalf("merge tick err = %v, want the close-task error", err)
	}
	if task, err = e.store.GetTask(t.Context(), task.Key); err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateMerge {
		t.Errorf("task = %s, want merge after the failed close", task.State)
	}
	c := storedChange(t, e, task.Key)
	if c.MergeSHA == "" || c.MergedAt == nil || c.Open {
		t.Fatalf("stored change = %+v, want merged", c)
	}

	if err := e.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if task, err = e.store.GetTask(t.Context(), task.Key); err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateDone {
		t.Errorf("task = %s, want done", task.State)
	}
	if n := countCreates(e, model.IntentOperationMerge); n != 1 {
		t.Errorf("merge intents = %d, want 1", n)
	}
	if n := countCreates(e, model.IntentOperationCloseTask); n != 2 {
		t.Errorf("close-task intents = %d, want 2", n)
	}
	if tip := mainTip(t, e); tip != c.MergeSHA {
		t.Errorf("bare main = %s, want merge sha %s", tip, c.MergeSHA)
	}
}
