package fakes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// ErrScriptExhausted is wrapped by the fatal failure of a run whose role has
// no scripted step left.
var ErrScriptExhausted = errors.New("fake agent: script exhausted")

// FileEdit is one whole-file edit an executor step makes to the workspace:
// Content replaces the file (creating it when absent), or Delete removes it.
type FileEdit struct {
	Path    string
	Content []byte
	Delete  bool
}

// AgentStep is one scripted run of a role. A non-nil Failure is returned as
// given; otherwise Result is validated against the role schema, and for the
// executor Patch (or, when Patch is empty, the patch built from Edits
// against the run's workspace) is returned with it.
type AgentStep struct {
	Result  []byte
	Patch   []byte
	Edits   []FileEdit
	Usage   ports.AgentUsage
	Failure *ports.AgentFailure
}

// Agent is a scripted ports.Agent: each role consumes its own FIFO of steps.
type Agent struct {
	v *model.ResultValidator

	mu      sync.Mutex
	scripts map[model.Role][]AgentStep
	calls   []ports.AgentRunRequest
}

var _ ports.Agent = (*Agent)(nil)

// NewAgent returns an Agent with empty scripts that validates results with v.
func NewAgent(v *model.ResultValidator) *Agent {
	return &Agent{v: v, scripts: make(map[model.Role][]AgentStep)}
}

// Script appends steps to the FIFO of role.
func (a *Agent) Script(role model.Role, steps ...AgentStep) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.scripts[role] = append(a.scripts[role], steps...)
}

// Calls returns the recorded run requests in call order.
func (a *Agent) Calls() []ports.AgentRunRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ports.AgentRunRequest(nil), a.calls...)
}

// Run implements ports.Agent. Every non-nil error is a *ports.AgentFailure.
func (a *Agent) Run(ctx context.Context, r ports.AgentRunRequest) (ports.AgentRunResult, error) {
	// spec §7.3: a run that ends by timeout or cancellation is fatal.
	if err := ctx.Err(); err != nil {
		return ports.AgentRunResult{}, fatal(err)
	}
	a.record(r)
	// spec §7.3: the credential is checked before any provider call, so no
	// script step is used.
	if err := checkCredential(ctx, r.Credentials); err != nil {
		return ports.AgentRunResult{}, &ports.AgentFailure{Class: ports.AgentFailureCredential, Err: err}
	}
	step, ok := a.pop(r.Role)
	if !ok {
		return ports.AgentRunResult{}, fatal(fmt.Errorf("role %q: %w", r.Role, ErrScriptExhausted))
	}
	if step.Failure != nil {
		return ports.AgentRunResult{}, step.Failure
	}
	kind, err := resultKind(r.Role)
	if err != nil {
		return ports.AgentRunResult{}, fatal(err)
	}
	if a.v == nil {
		return ports.AgentRunResult{}, fatal(errors.New("fake agent: no result validator"))
	}
	if err := a.v.Validate(kind, step.Result); err != nil {
		return ports.AgentRunResult{}, fatal(err)
	}
	patch, err := stepPatch(r, step)
	if err != nil {
		return ports.AgentRunResult{}, fatal(err)
	}
	return ports.AgentRunResult{Result: bytes.Clone(step.Result), Patch: patch, Usage: step.Usage}, nil
}

func (a *Agent) record(r ports.AgentRunRequest) {
	r.Brief = bytes.Clone(r.Brief)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, r)
}

func (a *Agent) pop(role model.Role) (AgentStep, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.scripts[role]
	if len(q) == 0 {
		return AgentStep{}, false
	}
	a.scripts[role] = q[1:]
	return q[0], true
}

func fatal(err error) *ports.AgentFailure {
	return &ports.AgentFailure{Class: ports.AgentFailureFatal, Err: err}
}

func checkCredential(ctx context.Context, src ports.CredentialSource) error {
	if src == nil {
		return errors.New("no credential source")
	}
	s, err := src.Credential(ctx)
	if err != nil {
		return fmt.Errorf("read credential: %w", err)
	}
	if s.IsEmpty() {
		return errors.New("empty credential")
	}
	return nil
}

func resultKind(role model.Role) (model.ResultKind, error) {
	switch role {
	case model.RolePlanner:
		return model.ResultKindPlanner, nil
	case model.RoleExecutor:
		return model.ResultKindExecutor, nil
	case model.RoleR1, model.RoleR2:
		return model.ResultKindReviewer, nil
	}
	return "", fmt.Errorf("fake agent: unknown role %q", role)
}

// stepPatch returns the patch of step; only the executor returns one
// (ports.AgentRunResult).
func stepPatch(r ports.AgentRunRequest, step AgentStep) ([]byte, error) {
	if r.Role != model.RoleExecutor {
		if len(step.Patch) > 0 || len(step.Edits) > 0 {
			return nil, fmt.Errorf("fake agent: role %q returns no patch", r.Role)
		}
		return nil, nil
	}
	if len(step.Patch) > 0 {
		return bytes.Clone(step.Patch), nil
	}
	if len(step.Edits) == 0 {
		return nil, nil
	}
	p, err := BuildPatch(r.Workspace, step.Edits)
	if err != nil {
		return nil, fmt.Errorf("fake agent: build patch: %w", err)
	}
	return p, nil
}

type staticCredential struct {
	s ports.Secret
}

// NewCredential returns a ports.CredentialSource that always yields value.
func NewCredential(value string) ports.CredentialSource {
	return staticCredential{s: ports.NewSecret([]byte(value))}
}

// Credential implements ports.CredentialSource.
func (c staticCredential) Credential(context.Context) (ports.Secret, error) {
	return c.s, nil
}
