package daemon_test

import (
	"context"
	"slices"
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
		})
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
