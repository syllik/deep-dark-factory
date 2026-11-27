package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/config"
	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// maxAttempts is the first run plus the invalid-result retry (spec §5.3).
const maxAttempts = 2

// credentialSource reads the routed entry's credential from the secrets
// port on demand.
type credentialSource struct {
	secrets ports.Secrets
	name    string
}

// Credential implements ports.CredentialSource.
func (c credentialSource) Credential(ctx context.Context) (ports.Secret, error) {
	s, err := c.secrets.Read(ctx, c.name)
	if err != nil {
		return ports.Secret{}, fmt.Errorf("read credential %s: %w", c.name, err)
	}
	return s, nil
}

// runAgent runs role for t with at most one retry. The run timeout is
// judged from clock readings only: no wall-clock deadline is set on ctx, and
// lifecycle.CheckRunTimeout runs after Agent.Run returns. A non-nil owner is
// the outcome to apply to t instead of using res (needs-owner); err means
// the run could not be decided.
func (e *Engine) runAgent(ctx context.Context, t model.Task, status lifecycle.Status, role model.Role, purpose model.RunPurpose,
	brief []byte, ws fs.FS) (res ports.AgentRunResult, owner *lifecycle.Outcome, err error) {
	limit, kind, err := e.roleLimit(role)
	if err != nil {
		return ports.AgentRunResult{}, nil, err
	}
	entry, err := e.routingEntry(role)
	if err != nil {
		return ports.AgentRunResult{}, nil, err
	}
	creds := credentialSource{secrets: e.deps.Secrets, name: "agent/" + entry.Provider}
	l := e.settings.Limits
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		run := model.Run{Key: t.Key, ID: e.deps.IDs.NewID(), Role: role, Purpose: purpose, Attempt: attempt,
			Status: model.RunStatusRunning, StartedAt: e.deps.Clock.Now()}
		log := e.deps.Logger.With("key", string(t.Key), "role", string(role), "runId", run.ID, "attempt", attempt)
		res, runErr := e.deps.Agent.Run(ctx, ports.AgentRunRequest{
			Role: role, Entry: entry, Brief: brief, Workspace: ws,
			Limits:      ports.AgentLimits{Timeout: limit, MaxSubagentsPerRun: e.settings.Routing.Limits.MaxSubagentsPerRun},
			Credentials: creds, Key: t.Key, RunID: run.ID,
		})
		o, err := lifecycle.CheckRunTimeout(status, run, l, e.deps.Clock.Now())
		if err != nil {
			return ports.AgentRunResult{}, nil, fmt.Errorf("run %s %s: %w", role, run.ID, err)
		}
		if o.Action == lifecycle.ActionRetry {
			log.Warn("agent run timed out; retrying", "limit", limit)
			continue
		}
		if o.Flag != "" {
			log.Warn("agent run timed out", "limit", limit, "reason", o.Reason)
			return res, &o, nil
		}
		if runErr != nil {
			var af *ports.AgentFailure
			class := ports.AgentFailureFatal
			if errors.As(runErr, &af) {
				class = af.Class
			}
			log.Warn("agent run failed", "class", string(class), "err", runErr)
			return res, ownerOutcome(status, fmt.Sprintf("%s run failed: %s", role, class)), nil
		}
		valErr := e.deps.Validator.Validate(kind, res.Result)
		if valErr == nil {
			return res, nil, nil
		}
		if !errors.Is(valErr, model.ErrInvalidResult) {
			return ports.AgentRunResult{}, nil, fmt.Errorf("run %s %s: %w", role, run.ID, valErr)
		}
		o, err = lifecycle.CheckRetry(status, run, l)
		if err != nil {
			return ports.AgentRunResult{}, nil, fmt.Errorf("run %s %s: %w", role, run.ID, err)
		}
		if o.Action == lifecycle.ActionRetry {
			log.Warn("invalid agent result; retrying", "err", valErr)
			continue
		}
		log.Warn("invalid agent result", "reason", o.Reason, "err", valErr)
		return res, &o, nil
	}
	return ports.AgentRunResult{}, ownerOutcome(status, fmt.Sprintf("%s run: no valid result after %d attempts", role, maxAttempts)), nil
}

// ownerOutcome is the needs-owner outcome for a task staying in place.
func ownerOutcome(s lifecycle.Status, reason string) *lifecycle.Outcome {
	return &lifecycle.Outcome{State: s.State, Flag: model.TaskFlagNeedsOwner, Reason: reason}
}

// roleLimit returns the run timeout and the result kind of role.
func (e *Engine) roleLimit(role model.Role) (time.Duration, model.ResultKind, error) {
	l := e.settings.Limits
	switch role {
	case model.RolePlanner:
		return l.PlannerTimeout, model.ResultKindPlanner, nil
	case model.RoleExecutor:
		return l.ExecutorTimeout, model.ResultKindExecutor, nil
	case model.RoleR1, model.RoleR2:
		return l.ReviewerTimeout, model.ResultKindReviewer, nil
	}
	return 0, "", fmt.Errorf("run agent: unknown role %q", role)
}

// routingEntry returns the role's first preferred entry, else the first
// standard-tier entry (spec §7.2).
func (e *Engine) routingEntry(role model.Role) (ports.RoutingEntry, error) {
	r := e.settings.Routing
	var pref config.RolePreference
	switch role {
	case model.RolePlanner:
		pref = r.Roles.Planner
	case model.RoleExecutor:
		pref = r.Roles.Executor
	case model.RoleR1:
		pref = r.Roles.R1
	case model.RoleR2:
		pref = r.Roles.R2
	default:
		return ports.RoutingEntry{}, fmt.Errorf("routing entry: unknown role %q", role)
	}
	entries := pref.Prefer
	if len(entries) == 0 {
		entries = r.Tiers.Standard
	}
	if len(entries) == 0 {
		return ports.RoutingEntry{}, fmt.Errorf("routing entry: no preferred or standard-tier entry for role %s", role)
	}
	en := entries[0]
	if en.Provider == "" {
		return ports.RoutingEntry{}, fmt.Errorf("routing entry: empty provider for role %s", role)
	}
	return ports.RoutingEntry{Provider: en.Provider, Model: en.Model, Effort: string(en.Effort)}, nil
}
