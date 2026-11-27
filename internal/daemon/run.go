package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// runSpec is what one agent run works on. Planner and executor runs leave
// Revision nil and HeadSHA empty; Deep routes a reviewer to the deep tier
// when its role has no preferred entry.
type runSpec struct {
	Role      model.Role
	Purpose   model.RunPurpose
	Brief     []byte
	Workspace fs.FS
	Revision  *int
	HeadSHA   string
	Deep      bool
}

// Fixed error texts of failed runs. They never carry adapter error text.
const (
	runErrTimeout       = "run timeout"
	runErrInvalidResult = "invalid result"
	runErrValidation    = "result validation error"
	runErrAgentFailure  = "agent failure: "
)

// runAgent runs rs.Role for t with at most one retry. Every attempt is
// checked against the run bounds (lifecycle.CheckStart) before it starts and
// is persisted as a run: created running, finished with its outcome. The run
// timeout is judged from clock readings only: no wall-clock deadline is set
// on ctx, and lifecycle.CheckRunTimeout runs after Agent.Run returns. A
// non-nil owner is the outcome to apply to t instead of using res
// (needs-owner); err means the run could not be decided. On success run is
// the finished, succeeded run of res; with an owner outcome it is the last
// finished run, or the zero run when the bounds refused the start.
func (e *Engine) runAgent(ctx context.Context, t model.Task, status lifecycle.Status, rs runSpec) (res ports.AgentRunResult, run model.Run, owner *lifecycle.Outcome, err error) {
	limit, kind, err := e.roleLimit(rs.Role)
	if err != nil {
		return ports.AgentRunResult{}, model.Run{}, nil, err
	}
	entry, adapter, err := e.routingEntry(rs.Role, rs.Deep)
	if err != nil {
		return ports.AgentRunResult{}, model.Run{}, nil, err
	}
	creds := credentialSource{secrets: e.deps.Secrets, name: "agent/" + entry.Provider}
	l := e.settings.Limits
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		runs, err := e.deps.Store.ListRuns(ctx, t.Key)
		if err != nil {
			return ports.AgentRunResult{}, model.Run{}, nil, fmt.Errorf("run %s: %w", rs.Role, err)
		}
		next := model.Run{Key: t.Key, ID: e.deps.IDs.NewID(), Role: rs.Role, Purpose: rs.Purpose, Revision: rs.Revision,
			Attempt: attempt, HeadSHA: rs.HeadSHA, Status: model.RunStatusRunning, Provider: entry.Provider,
			Routing:   model.RoutingEntry{Adapter: adapter, Provider: entry.Provider, Model: entry.Model, Effort: entry.Effort},
			StartedAt: e.deps.Clock.Now()}
		log := e.deps.Logger.With("key", string(t.Key), "role", string(rs.Role), "runId", next.ID, "attempt", attempt)
		o, err := lifecycle.CheckStart(status, runs, next, l)
		if err != nil {
			return ports.AgentRunResult{}, model.Run{}, nil, fmt.Errorf("run %s %s: %w", rs.Role, next.ID, err)
		}
		if o.Flag != "" {
			log.Warn("agent run refused by the run bounds", "reason", o.Reason)
			return ports.AgentRunResult{}, run, &o, nil
		}
		if err := e.deps.Store.CreateRun(ctx, next); err != nil {
			return ports.AgentRunResult{}, model.Run{}, nil, fmt.Errorf("run %s %s: %w", rs.Role, next.ID, err)
		}
		res, runErr := e.deps.Agent.Run(ctx, ports.AgentRunRequest{
			Role: rs.Role, Entry: entry, Brief: rs.Brief, Workspace: rs.Workspace,
			Limits:      ports.AgentLimits{Timeout: limit, MaxSubagentsPerRun: e.settings.Routing.Limits.MaxSubagentsPerRun},
			Credentials: creds, Key: t.Key, RunID: next.ID,
		})
		d, err := e.decideRun(status, next, kind, res, runErr)
		if err != nil {
			return ports.AgentRunResult{}, model.Run{}, nil, err
		}
		run = finishedRun(next, res, d, e.deps.Clock.Now())
		if err := e.deps.Store.FinishRun(ctx, run); err != nil {
			return ports.AgentRunResult{}, model.Run{}, nil, fmt.Errorf("run %s %s: %w", rs.Role, next.ID, err)
		}
		switch {
		case d.err != nil:
			return ports.AgentRunResult{}, model.Run{}, nil, d.err
		case d.retry:
			log.Warn(d.logMsg+"; retrying", "limit", limit, "err", d.logErr)
			continue
		case d.owner != nil:
			log.Warn(d.logMsg, "limit", limit, "reason", d.owner.Reason, "err", d.logErr)
			return res, run, d.owner, nil
		}
		return res, run, nil, nil
	}
	return ports.AgentRunResult{}, run, ownerOutcome(status, fmt.Sprintf("%s run: no valid result after %d attempts", rs.Role, maxAttempts)), nil
}

// runDecision is how one finished attempt ends: its stored failure (class
// and fixed error text, both empty on success) and its route.
type runDecision struct {
	class  model.FailureClass
	errMsg string
	retry  bool
	owner  *lifecycle.Outcome
	// err is an undecidable result; the run is still finished first.
	err    error
	logMsg string
	logErr error
}

// decideRun judges one returned attempt: the run timeout first, then an
// agent failure, then the result's validity.
func (e *Engine) decideRun(status lifecycle.Status, r model.Run, kind model.ResultKind, res ports.AgentRunResult, runErr error) (runDecision, error) {
	l := e.settings.Limits
	o, err := lifecycle.CheckRunTimeout(status, r, l, e.deps.Clock.Now())
	if err != nil {
		return runDecision{}, fmt.Errorf("run %s %s: %w", r.Role, r.ID, err)
	}
	if o.Action == lifecycle.ActionRetry || o.Flag != "" {
		d := runDecision{class: model.FailureClassFatal, errMsg: runErrTimeout, logMsg: "agent run timed out"}
		if o.Action == lifecycle.ActionRetry {
			d.retry = true
		} else {
			d.owner = &o
		}
		return d, nil
	}
	if runErr != nil {
		class := ports.AgentFailureFatal
		var af *ports.AgentFailure
		if errors.As(runErr, &af) {
			class = af.Class
		}
		fc, perr := model.ParseFailureClass(string(class))
		if perr != nil {
			fc = model.FailureClassFatal
		}
		return runDecision{class: fc, errMsg: runErrAgentFailure + string(fc), logMsg: "agent run failed", logErr: runErr,
			owner: ownerOutcome(status, fmt.Sprintf("%s run failed: %s", r.Role, fc))}, nil
	}
	valErr := e.deps.Validator.Validate(kind, res.Result)
	if valErr == nil {
		return runDecision{}, nil
	}
	if !errors.Is(valErr, model.ErrInvalidResult) {
		return runDecision{class: model.FailureClassFatal, errMsg: runErrValidation,
			err: fmt.Errorf("run %s %s: %w", r.Role, r.ID, valErr)}, nil
	}
	o, err = lifecycle.CheckRetry(status, r, l)
	if err != nil {
		return runDecision{}, fmt.Errorf("run %s %s: %w", r.Role, r.ID, err)
	}
	d := runDecision{class: model.FailureClassFatal, errMsg: runErrInvalidResult, logMsg: "invalid agent result", logErr: valErr}
	if o.Action == lifecycle.ActionRetry {
		d.retry = true
	} else {
		d.owner = &o
	}
	return d, nil
}

// finishedRun is r finished at now with the outcome d and the usage and
// digests of res.
func finishedRun(r model.Run, res ports.AgentRunResult, d runDecision, now time.Time) model.Run {
	r.FinishedAt = &now
	r.Status = model.RunStatusSucceeded
	if d.errMsg != "" {
		r.Status, r.FailureClass, r.Error = model.RunStatusFailed, d.class, d.errMsg
	}
	if u := res.Usage; u.InputTokens != 0 || u.OutputTokens != 0 {
		r.Usage = &model.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens}
	}
	r.ResultDigest, r.PatchDigest = digest(res.Result), digest(res.Patch)
	return r
}

// digest is the lowercase hex SHA-256 of b, or "" for empty b.
func digest(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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
// deep-tier entry when deep, else the first standard-tier entry (spec §7.2),
// with that entry's adapter.
func (e *Engine) routingEntry(role model.Role, deep bool) (ports.RoutingEntry, string, error) {
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
		return ports.RoutingEntry{}, "", fmt.Errorf("routing entry: unknown role %q", role)
	}
	entries, tier := pref.Prefer, "preferred"
	if len(entries) == 0 {
		entries, tier = r.Tiers.Standard, "standard-tier"
		if deep {
			entries, tier = r.Tiers.Deep, "deep-tier"
		}
	}
	if len(entries) == 0 {
		return ports.RoutingEntry{}, "", fmt.Errorf("routing entry: no preferred or %s entry for role %s", tier, role)
	}
	en := entries[0]
	if en.Provider == "" {
		return ports.RoutingEntry{}, "", fmt.Errorf("routing entry: empty provider for role %s", role)
	}
	return ports.RoutingEntry{Provider: en.Provider, Model: en.Model, Effort: string(en.Effort)}, en.Adapter, nil
}
