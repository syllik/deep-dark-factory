package ports

import (
	"context"
	"io/fs"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// AgentFailureClass is the failure class of an agent run (spec §7.3).
type AgentFailureClass string

// AgentFailureClass values.
const (
	AgentFailureCredential  AgentFailureClass = "credential"
	AgentFailureRateLimited AgentFailureClass = "rate-limited"
	AgentFailureRetryable   AgentFailureClass = "retryable"
	AgentFailureFatal       AgentFailureClass = "fatal"
)

// ParseAgentFailureClass returns the AgentFailureClass spelled s; an unknown or empty value is an error.
func ParseAgentFailureClass(s string) (AgentFailureClass, error) {
	return parseEnum("agent failure class", s, []AgentFailureClass{AgentFailureCredential, AgentFailureRateLimited, AgentFailureRetryable, AgentFailureFatal})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseAgentFailureClass.
func (c *AgentFailureClass) UnmarshalText(b []byte) error {
	return unmarshalEnum(c, b, ParseAgentFailureClass)
}

// AgentFailure is the only error Agent.Run returns. ResetAt is set only for
// AgentFailureRateLimited.
type AgentFailure struct {
	Class   AgentFailureClass
	ResetAt time.Time
	Err     error
}

// Error implements error.
func (f *AgentFailure) Error() string {
	if f.Err == nil {
		return "agent run failed: " + string(f.Class)
	}
	return "agent run failed: " + string(f.Class) + ": " + f.Err.Error()
}

// Unwrap returns the cause.
func (f *AgentFailure) Unwrap() error { return f.Err }

// CredentialSource yields the routed entry's credential to an agent adapter
// (spec §7.3). A missing or empty credential is AgentFailureCredential,
// checked before any provider call.
type CredentialSource interface {
	Credential(ctx context.Context) (Secret, error)
}

// RoutingEntry is one routing entry (spec §7.2).
type RoutingEntry struct {
	Provider, Model, Effort string
}

// AgentLimits bounds one agent run. MaxSubagentsPerRun is advice to the
// agent; MaxCostUSD is nil when no cost cap applies.
type AgentLimits struct {
	Timeout            time.Duration
	MaxSubagentsPerRun int
	MaxCostUSD         *float64
}

// AgentRunRequest is the input of one agent run. Workspace is the run's
// workspace content, never a host path.
type AgentRunRequest struct {
	Role        model.Role
	Entry       RoutingEntry
	Brief       []byte
	Workspace   fs.FS
	Limits      AgentLimits
	Credentials CredentialSource
	Key         model.TaskKey
	RunID       string
}

// AgentUsage is the model usage of one run.
type AgentUsage struct {
	ModelID                   string
	InputTokens, OutputTokens int64
	CostUSD                   float64
}

// AgentRunResult is the output of one successful run. Result is validated
// against the role schema before Run returns; Patch is set for the Executor
// only.
type AgentRunResult struct {
	Result, Patch []byte
	Usage         AgentUsage
}

// Agent is the agent port (spec §7.3). Every non-nil error from Run is an
// *AgentFailure; callers treat any other error as AgentFailureFatal.
type Agent interface {
	Run(ctx context.Context, r AgentRunRequest) (AgentRunResult, error)
}
