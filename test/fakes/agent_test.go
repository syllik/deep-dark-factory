package fakes_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

const (
	plannerResult  = `{"schema":"ddf.planner.result/v1","verdict":"ASK","question":"Which file?"}`
	executorResult = `{"schema":"ddf.executor.result/v1","verdict":"COMPLETE","summary":"done","verify":{"commands":["go test ./..."],"passed":true}}`
	r1Result       = `{"schema":"ddf.reviewer.result/v1","role":"r1","verdict":"APPROVE","findings":[]}`
	r2Result       = `{"schema":"ddf.reviewer.result/v1","role":"r2","verdict":"APPROVE","findings":[],"hardStopChecklist":[{"item":"secrets and credentials","status":"clear"}]}`
	testCredential = "test-secret-credential"
)

func newValidator(t *testing.T) *model.ResultValidator {
	t.Helper()
	v, err := model.NewResultValidator()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type errCredential struct{}

func (errCredential) Credential(context.Context) (ports.Secret, error) {
	return ports.Secret{}, errors.New("credential store unavailable")
}

func request(role model.Role) ports.AgentRunRequest {
	return ports.AgentRunRequest{
		Role:        role,
		Brief:       []byte("brief"),
		Workspace:   fstest.MapFS{"a.txt": {Data: []byte("old\n")}},
		Credentials: fakes.NewCredential(testCredential),
		Key:         "T-1",
		RunID:       "run-1",
	}
}

func failureOf(t *testing.T, err error) *ports.AgentFailure {
	t.Helper()
	var f *ports.AgentFailure
	if !errors.As(err, &f) {
		t.Fatalf("error %v is not *ports.AgentFailure", err)
	}
	return f
}

func TestAgent_ValidResultsPassThrough(t *testing.T) {
	usage := ports.AgentUsage{ModelID: "fake", InputTokens: 3, OutputTokens: 4, CostUSD: 0.5}
	tests := []struct {
		name   string
		role   model.Role
		result string
	}{
		{"planner result validated by planner schema", model.RolePlanner, plannerResult},
		{"executor result validated by executor schema", model.RoleExecutor, executorResult},
		{"r1 result validated by reviewer schema", model.RoleR1, r1Result},
		{"r2 result validated by reviewer schema", model.RoleR2, r2Result},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := fakes.NewAgent(newValidator(t))
			a.Script(tt.role, fakes.AgentStep{Result: []byte(tt.result), Usage: usage})
			got, err := a.Run(context.Background(), request(tt.role))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if string(got.Result) != tt.result || got.Patch != nil || got.Usage != usage {
				t.Errorf("Run = %+v", got)
			}
		})
	}
}

func TestAgent_Failures(t *testing.T) {
	tests := []struct {
		name      string
		role      model.Role
		steps     []fakes.AgentStep
		req       func(ports.AgentRunRequest) ports.AgentRunRequest
		wantClass ports.AgentFailureClass
		wantIs    error
		unused    bool // the scripted step stays in the queue
	}{
		{
			name:      "invalid result is fatal (result validated before Run returns)",
			role:      model.RolePlanner,
			steps:     []fakes.AgentStep{{Result: []byte(`{"schema":"ddf.planner.result/v1","verdict":"READY"}`)}},
			wantClass: ports.AgentFailureFatal,
			wantIs:    model.ErrInvalidResult,
		},
		{
			name:      "reviewer result for the executor is fatal",
			role:      model.RoleExecutor,
			steps:     []fakes.AgentStep{{Result: []byte(r1Result)}},
			wantClass: ports.AgentFailureFatal,
			wantIs:    model.ErrInvalidResult,
		},
		{
			name:      "empty result is fatal",
			role:      model.RoleR1,
			steps:     []fakes.AgentStep{{}},
			wantClass: ports.AgentFailureFatal,
			wantIs:    model.ErrInvalidResult,
		},
		{
			name:      "script exhausted is fatal",
			role:      model.RolePlanner,
			wantClass: ports.AgentFailureFatal,
			wantIs:    fakes.ErrScriptExhausted,
		},
		{
			name:      "unknown role is fatal",
			role:      model.Role("r3"),
			steps:     []fakes.AgentStep{{Result: []byte(r1Result)}},
			wantClass: ports.AgentFailureFatal,
		},
		{
			name:      "nil credential source is credential (spec §7.3)",
			role:      model.RolePlanner,
			steps:     []fakes.AgentStep{{Result: []byte(plannerResult)}},
			req:       func(r ports.AgentRunRequest) ports.AgentRunRequest { r.Credentials = nil; return r },
			wantClass: ports.AgentFailureCredential,
			unused:    true,
		},
		{
			name:  "empty credential is credential (spec §7.3)",
			role:  model.RolePlanner,
			steps: []fakes.AgentStep{{Result: []byte(plannerResult)}},
			req: func(r ports.AgentRunRequest) ports.AgentRunRequest {
				r.Credentials = fakes.NewCredential("")
				return r
			},
			wantClass: ports.AgentFailureCredential,
			unused:    true,
		},
		{
			name:      "credential read error is credential (spec §7.3)",
			role:      model.RolePlanner,
			steps:     []fakes.AgentStep{{Result: []byte(plannerResult)}},
			req:       func(r ports.AgentRunRequest) ports.AgentRunRequest { r.Credentials = errCredential{}; return r },
			wantClass: ports.AgentFailureCredential,
			unused:    true,
		},
		{
			name:      "reviewer with a patch is fatal (only the executor returns a patch)",
			role:      model.RoleR2,
			steps:     []fakes.AgentStep{{Result: []byte(r2Result), Patch: []byte("diff --git a/x b/x\n")}},
			wantClass: ports.AgentFailureFatal,
		},
		{
			name:      "planner with edits is fatal (only the executor returns a patch)",
			role:      model.RolePlanner,
			steps:     []fakes.AgentStep{{Result: []byte(plannerResult), Edits: []fakes.FileEdit{{Path: "x", Content: []byte("x\n")}}}},
			wantClass: ports.AgentFailureFatal,
		},
		{
			name:      "executor edits that cannot be built are fatal",
			role:      model.RoleExecutor,
			steps:     []fakes.AgentStep{{Result: []byte(executorResult), Edits: []fakes.FileEdit{{Path: "../x", Content: []byte("x\n")}}}},
			wantClass: ports.AgentFailureFatal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := fakes.NewAgent(newValidator(t))
			a.Script(tt.role, tt.steps...)
			req := request(tt.role)
			if tt.req != nil {
				req = tt.req(req)
			}
			got, err := a.Run(context.Background(), req)
			if err == nil {
				t.Fatalf("Run = %+v, nil; want a failure", got)
			}
			f := failureOf(t, err)
			if f.Class != tt.wantClass {
				t.Errorf("class = %q, want %q (%v)", f.Class, tt.wantClass, err)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(%v, %v) = false", err, tt.wantIs)
			}
			if bytes.Contains([]byte(err.Error()), []byte(testCredential)) {
				t.Errorf("error leaks the credential: %v", err)
			}
			if tt.unused {
				req.Credentials = fakes.NewCredential(testCredential)
				if _, err := a.Run(context.Background(), req); err != nil {
					t.Errorf("step was consumed by the refused run: %v", err)
				}
			}
		})
	}
}

func TestAgent_ScriptedFailureReturnedAsGiven(t *testing.T) {
	resetAt := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	want := &ports.AgentFailure{Class: ports.AgentFailureRateLimited, ResetAt: resetAt}
	a := fakes.NewAgent(newValidator(t))
	a.Script(model.RoleR1, fakes.AgentStep{Failure: want}, fakes.AgentStep{Result: []byte(r1Result)})
	_, err := a.Run(context.Background(), request(model.RoleR1))
	if f := failureOf(t, err); f != want || !f.ResetAt.Equal(resetAt) {
		t.Errorf("failure = %+v, want %+v", f, want)
	}
	if _, err := a.Run(context.Background(), request(model.RoleR1)); err != nil {
		t.Errorf("second step: %v", err)
	}
}

func TestAgent_CancelledContextIsFatal(t *testing.T) {
	a := fakes.NewAgent(newValidator(t))
	a.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(plannerResult)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.Run(ctx, request(model.RolePlanner))
	if f := failureOf(t, err); f.Class != ports.AgentFailureFatal || !errors.Is(err, context.Canceled) {
		t.Errorf("failure = %v, want fatal wrapping context.Canceled (spec §7.3)", err)
	}
	if n := len(a.Calls()); n != 0 {
		t.Errorf("cancelled run recorded %d calls, want 0", n)
	}
}

func TestAgent_ExecutorPatch(t *testing.T) {
	edits := []fakes.FileEdit{{Path: "a.txt", Content: []byte("new\n")}, {Path: "b.txt", Content: []byte("b\n")}}
	built, err := fakes.BuildPatch(request(model.RoleExecutor).Workspace, edits)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("diff --git a/x b/x\n")
	tests := []struct {
		name string
		step fakes.AgentStep
		want []byte
	}{
		{"raw patch returned", fakes.AgentStep{Result: []byte(executorResult), Patch: raw}, raw},
		{"patch built from edits against the workspace", fakes.AgentStep{Result: []byte(executorResult), Edits: edits}, built},
		{"raw patch wins over edits", fakes.AgentStep{Result: []byte(executorResult), Patch: raw, Edits: edits}, raw},
		{"no patch and no edits", fakes.AgentStep{Result: []byte(executorResult)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := fakes.NewAgent(newValidator(t))
			a.Script(model.RoleExecutor, tt.step)
			got, err := a.Run(context.Background(), request(model.RoleExecutor))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !bytes.Equal(got.Patch, tt.want) {
				t.Errorf("patch = %q, want %q", got.Patch, tt.want)
			}
			if len(got.Patch) > 0 {
				got.Patch[0] = 'X'
				if tt.step.Patch != nil && tt.step.Patch[0] == 'X' {
					t.Error("returned patch aliases the scripted patch")
				}
			}
		})
	}
}

func TestAgent_RolesHaveSeparateQueues(t *testing.T) {
	a := fakes.NewAgent(newValidator(t))
	a.Script(model.RoleR1, fakes.AgentStep{Result: []byte(r1Result)})
	if _, err := a.Run(context.Background(), request(model.RoleR2)); !errors.Is(err, fakes.ErrScriptExhausted) {
		t.Errorf("r2 run = %v, want ErrScriptExhausted", err)
	}
	if _, err := a.Run(context.Background(), request(model.RoleR1)); err != nil {
		t.Errorf("r1 run: %v", err)
	}
}

func TestAgent_CallsRecorded(t *testing.T) {
	a := fakes.NewAgent(newValidator(t))
	a.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(plannerResult)})
	a.Script(model.RoleExecutor, fakes.AgentStep{Result: []byte(executorResult)})
	noCred := request(model.RoleR1)
	noCred.Credentials = nil
	reqs := []ports.AgentRunRequest{request(model.RolePlanner), request(model.RoleExecutor), noCred}
	for _, r := range reqs {
		_, _ = a.Run(context.Background(), r) // outcomes are covered by other tests
	}
	calls := a.Calls()
	if len(calls) != len(reqs) {
		t.Fatalf("Calls = %d, want %d", len(calls), len(reqs))
	}
	for i, c := range calls {
		if c.Role != reqs[i].Role || c.RunID != reqs[i].RunID || !bytes.Equal(c.Brief, reqs[i].Brief) {
			t.Errorf("call %d = %+v, want %+v", i, c, reqs[i])
		}
	}
	calls[0].Role = model.RoleR2
	if a.Calls()[0].Role != model.RolePlanner {
		t.Error("Calls returned the internal slice")
	}
}
