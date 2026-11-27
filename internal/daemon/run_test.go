package daemon_test

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/daemon"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

// clockAgent advances the fake clock by advance during every planner run,
// then delegates to the scripted agent.
type clockAgent struct {
	inner   ports.Agent
	clock   *fakes.Clock
	advance time.Duration

	mu       sync.Mutex
	planners int
}

func (a *clockAgent) Run(ctx context.Context, r ports.AgentRunRequest) (ports.AgentRunResult, error) {
	if r.Role == model.RolePlanner {
		a.mu.Lock()
		a.planners++
		a.mu.Unlock()
		if err := a.clock.Advance(a.advance); err != nil {
			return ports.AgentRunResult{}, &ports.AgentFailure{Class: ports.AgentFailureFatal, Err: err}
		}
	}
	if _, ok := ctx.Deadline(); ok {
		return ports.AgentRunResult{}, &ports.AgentFailure{Class: ports.AgentFailureFatal, Err: context.DeadlineExceeded}
	}
	return a.inner.Run(ctx, r)
}

func TestTick_RunTimeoutFromClock(t *testing.T) {
	cases := []struct {
		name      string
		extra     time.Duration
		wantCalls int
		wantState model.TaskState
		wantFlags []model.TaskFlag
	}{
		// lifecycle.md#bounds: elapsed equal to the timeout is within it.
		{"exactly the timeout stays within", 0, 1, model.TaskStateReady, nil},
		// A timed-out run is retried once, then goes to the owner.
		{"over the timeout on both attempts", time.Minute, 2, model.TaskStatePlanning, []model.TaskFlag{model.TaskFlagNeedsOwner}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ca *clockAgent
			e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, d *daemon.Deps) {
				ca = &clockAgent{inner: d.Agent, clock: d.Clock.(*fakes.Clock), advance: s.Limits.PlannerTimeout + c.extra}
				d.Agent = ca
			})
			e.addInbox(t)
			e.agent.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(plannerReady)}, fakes.AgentStep{Result: []byte(plannerReady)})
			// Intake, inbox to planning, then the planning step.
			for range 3 {
				if err := e.engine.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			task, err := e.store.GetTask(t.Context(), e.candidateKey(t))
			if err != nil {
				t.Fatal(err)
			}
			if ca.planners != c.wantCalls {
				t.Errorf("planner calls = %d, want %d", ca.planners, c.wantCalls)
			}
			if task.State != c.wantState || !slices.Equal(task.Flags, c.wantFlags) {
				t.Errorf("task = %s %v, want %s %v", task.State, task.Flags, c.wantState, c.wantFlags)
			}
			for _, call := range e.agent.Calls() {
				if call.Limits.Timeout != e.settings.Limits.PlannerTimeout {
					t.Errorf("agent limits timeout = %s, want %s", call.Limits.Timeout, e.settings.Limits.PlannerTimeout)
				}
			}
			runs, err := e.store.ListRuns(t.Context(), task.Key)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != c.wantCalls {
				t.Fatalf("stored runs = %d, want %d", len(runs), c.wantCalls)
			}
			for i, r := range runs {
				if r.Role != model.RolePlanner || r.Attempt != i+1 || r.FinishedAt == nil {
					t.Errorf("run %d = %s attempt %d finished %v, want planner attempt %d finished", i, r.Role, r.Attempt, r.FinishedAt, i+1)
				}
				if c.wantFlags == nil {
					if r.Status != model.RunStatusSucceeded {
						t.Errorf("run %d status = %s, want succeeded", i, r.Status)
					}
					continue
				}
				if r.Status != model.RunStatusFailed || r.FailureClass != model.FailureClassFatal || r.Error != "run timeout" {
					t.Errorf("run %d = %s %s %q, want failed fatal \"run timeout\"", i, r.Status, r.FailureClass, r.Error)
				}
			}
		})
	}
}

func TestTick_RunsPersisted(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	e.addInbox(t)
	e.scriptToMerge()
	task := e.tickUntil(t, 15, model.TaskStateMerge)

	runs, err := e.store.ListRuns(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	// The fake clock does not move, so the store orders these runs by ID;
	// they are matched by role.
	byRole := map[model.Role]model.Run{}
	for _, r := range runs {
		byRole[r.Role] = r
		if r.Attempt != 1 || r.Status != model.RunStatusSucceeded || r.Provider != "fakeprov" || r.FinishedAt == nil {
			t.Errorf("run %s = attempt %d %s provider %q finished %v, want attempt 1 succeeded fakeprov finished",
				r.Role, r.Attempt, r.Status, r.Provider, r.FinishedAt)
		}
		if r.Routing.Provider != "fakeprov" || r.Routing.Adapter != "fake" || r.Routing.Model != "fake-model" {
			t.Errorf("run %s routing = %+v, want fake fakeprov fake-model", r.Role, r.Routing)
		}
		if r.ResultDigest == "" || r.FailureClass != "" || r.Error != "" {
			t.Errorf("run %s = digest %q class %q error %q, want a digest and no failure", r.Role, r.ResultDigest, r.FailureClass, r.Error)
		}
	}
	planner, executor, r1 := byRole[model.RolePlanner], byRole[model.RoleExecutor], byRole[model.RoleR1]
	if len(runs) != 3 || planner.ID == "" || executor.ID == "" || r1.ID == "" {
		t.Fatalf("stored runs = %+v, want exactly planner, executor and r1", runs)
	}
	if u := planner.Usage; u == nil || u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Errorf("planner usage = %+v, want 10 in 5 out", u)
	}
	if planner.Revision != nil || planner.HeadSHA != "" || executor.Revision != nil || executor.HeadSHA != "" {
		t.Errorf("planner/executor runs carry a revision or head: %+v %+v", planner, executor)
	}
	changes, err := e.store.ListChanges(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("no stored change")
	}
	head := changes[len(changes)-1].HeadSHA
	if head == "" || r1.HeadSHA != head || r1.Purpose != model.RunPurposeReview {
		t.Errorf("r1 run head %q purpose %s, want head %q purpose review", r1.HeadSHA, r1.Purpose, head)
	}
}

// syncBuffer is a bytes.Buffer safe for concurrent writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestTick_RunBudgetExceeded(t *testing.T) {
	logs := &syncBuffer{}
	e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, d *daemon.Deps) {
		s.Limits.MaxRunsPerTask = 2
		d.Logger = slog.New(slog.NewTextHandler(logs, nil))
	})
	e.addInbox(t)
	e.scriptToMerge()
	task := e.tickUntil(t, 15, model.TaskStateReview)
	for range 3 {
		if slices.Contains(task.Flags, model.TaskFlagNeedsOwner) {
			break
		}
		if err := e.engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		var err error
		if task, err = e.store.GetTask(t.Context(), task.Key); err != nil {
			t.Fatal(err)
		}
	}
	if task.State != model.TaskStateReview || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
		t.Fatalf("task = %s %v, want review with needs-owner", task.State, task.Flags)
	}
	if !strings.Contains(logs.String(), "agent runs per task") {
		t.Errorf("logs do not name the agent runs per task bound:\n%s", logs.String())
	}
	if n := len(e.agent.Calls()); n != 2 {
		t.Errorf("agent calls = %d, want 2", n)
	}
	runs, err := e.store.ListRuns(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("stored runs = %d, want 2", len(runs))
	}
}

func TestTick_AgentFailureSetsNeedsOwner(t *testing.T) {
	cases := []struct {
		name  string
		steps []fakes.AgentStep
	}{
		{"fatal failure", []fakes.AgentStep{{Failure: &ports.AgentFailure{Class: ports.AgentFailureFatal}}}},
		{"rate limited", []fakes.AgentStep{{Failure: &ports.AgentFailure{Class: ports.AgentFailureRateLimited}}}},
		{"script exhausted", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEngEnv(t, ports.WriteModeLive, nil)
			e.addInbox(t)
			e.agent.Script(model.RolePlanner, c.steps...)
			for range 3 {
				if err := e.engine.Tick(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			task, err := e.store.GetTask(t.Context(), e.candidateKey(t))
			if err != nil {
				t.Fatal(err)
			}
			if task.State != model.TaskStatePlanning || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
				t.Errorf("task = %s %v, want planning with needs-owner", task.State, task.Flags)
			}
			if n := len(e.agent.Calls()); n != 1 {
				t.Errorf("agent calls = %d, want 1 (a failure is not retried)", n)
			}
		})
	}
}

func TestTick_MissingCredentialSetsNeedsOwner(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, func(_ *daemon.Settings, d *daemon.Deps) {
		d.Secrets = fakes.NewSecrets(nil)
	})
	e.addInbox(t)
	e.agent.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(plannerReady)})
	for range 3 {
		if err := e.engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	task, err := e.store.GetTask(t.Context(), e.candidateKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
		t.Errorf("flags = %v, want needs-owner", task.Flags)
	}
}
