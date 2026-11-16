package model

import (
	"encoding/json"
	"time"
)

// Task is one unit of work on the conveyor, keyed by its source (spec §4.5,
// §5).
type Task struct {
	Key        TaskKey  `json:"key"`
	Forge      string   `json:"forge"`
	Repository string   `json:"repository"`
	SourceKind string   `json:"sourceKind"`
	SourceID   string   `json:"sourceId"`
	Kind       TaskKind `json:"kind"`
	// ParentKey links a request to its child or a parent to its follow-up;
	// empty means none.
	ParentKey TaskKey    `json:"parentKey,omitempty"`
	State     TaskState  `json:"state"`
	Flags     []TaskFlag `json:"flags,omitempty"`
	Risk      RiskClass  `json:"risk,omitempty"`
	Priority  Priority   `json:"priority"`
	// HitStop is sticky: set once the task hit a hard stop or path policy.
	HitStop       bool       `json:"hitStop"`
	Title         string     `json:"title,omitempty"`
	Spec          string     `json:"spec,omitempty"`
	Acceptance    []string   `json:"acceptance,omitempty"`
	Zones         []string   `json:"zones,omitempty"`
	DependsOn     []string   `json:"dependsOn,omitempty"`
	AgentDefaults []Decision `json:"agentDefaults,omitempty"`
	PlannerRisk   RiskClass  `json:"plannerRisk,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	// TeardownAt records when the task's run cell was destroyed and
	// verified (spec §4.5 teardown record).
	TeardownAt *time.Time `json:"teardownAt,omitempty"`
}

// RoutingEntry is the routing entry a run used (spec §7.2).
type RoutingEntry struct {
	Adapter  string `json:"adapter"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
}

// Usage is the model usage of a run or journaled action.
type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

// Run is one agent run for a task. Runs are not intents; an interrupted run
// is discarded (spec §4.5).
type Run struct {
	Key     TaskKey    `json:"key"`
	ID      string     `json:"id"`
	Role    Role       `json:"role"`
	Purpose RunPurpose `json:"purpose"`
	// Revision is the change revision the run works on; nil when none.
	Revision *int `json:"revision,omitempty"`
	// Attempt is 1, or 2 for the invalid-result retry.
	Attempt      int          `json:"attempt"`
	HeadSHA      string       `json:"headSha,omitempty"`
	Status       RunStatus    `json:"status"`
	Provider     string       `json:"provider"`
	FailureClass FailureClass `json:"failureClass,omitempty"`
	Routing      RoutingEntry `json:"routing"`
	Usage        *Usage       `json:"usage,omitempty"`
	ResultDigest string       `json:"resultDigest,omitempty"`
	PatchDigest  string       `json:"patchDigest,omitempty"`
	// Error is the redacted error text of a failed run.
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Change is the forge change (pull request) DDF opened for a task.
type Change struct {
	Key        TaskKey    `json:"key"`
	ID         string     `json:"id"`
	Kind       ChangeKind `json:"kind"`
	Branch     string     `json:"branch"`
	BaseBranch string     `json:"baseBranch"`
	HeadSHA    string     `json:"headSha"`
	Revision   int        `json:"revision"`
	Open       bool       `json:"open"`
	Draft      bool       `json:"draft"`
	PatchID    string     `json:"patchId,omitempty"`
	MergeSHA   string     `json:"mergeSha,omitempty"`
	MergedAt   *time.Time `json:"mergedAt,omitempty"`
	// CISince is when the CI wait for the current head started.
	CISince   *time.Time `json:"ciSince,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// Review is one reviewer's verdict on one head of a change (spec §5.2).
type Review struct {
	Key            TaskKey       `json:"key"`
	ID             string        `json:"id"`
	ChangeID       string        `json:"changeId"`
	Role           Role          `json:"role"`
	HeadSHA        string        `json:"headSha"`
	Round          int           `json:"round"`
	Verdict        ReviewVerdict `json:"verdict"`
	BlockingCount  int           `json:"blockingCount"`
	FindingsDigest string        `json:"findingsDigest"`
	RaiseRisk      RiskClass     `json:"raiseRisk,omitempty"`
	SigVersion     int           `json:"sigVersion"`
	KeyID          string        `json:"keyId"`
	Signature      string        `json:"signature"`
	RunID          string        `json:"runId"`
	// HardStopChecklist is the reviewer result's checklist, kept verbatim.
	HardStopChecklist json.RawMessage `json:"hardStopChecklist,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	CreatedAt         time.Time       `json:"createdAt"`
}

// Finding is one reviewer finding (spec §6.3), as in the reviewer result
// schema, scoped by task key, review, reviewer role and reviewed head.
type Finding struct {
	Key           TaskKey         `json:"key"`
	ReviewID      string          `json:"reviewId"`
	Role          Role            `json:"role"`
	HeadSHA       string          `json:"headSha"`
	ID            string          `json:"id"`
	Severity      Severity        `json:"severity"`
	Category      FindingCategory `json:"category"`
	SecurityLevel SecurityLevel   `json:"securityLevel,omitempty"`
	Blocking      bool            `json:"blocking"`
	PriorID       string          `json:"priorId,omitempty"`
	Routed        FindingRoute    `json:"routed,omitempty"`
	Evidence      string          `json:"evidence"`
	Location      string          `json:"location,omitempty"`
	FixHint       string          `json:"fixHint,omitempty"`
	WontFixReason string          `json:"wontFixReason,omitempty"`
}

// Question is a question to the owner, such as a Planner ASK, and its
// answer once given (spec §5.2 owner answers).
type Question struct {
	Key         TaskKey    `json:"key"`
	ID          string     `json:"id"`
	Category    string     `json:"category"`
	Text        string     `json:"text"`
	Paths       []string   `json:"paths,omitempty"`
	Answer      string     `json:"answer,omitempty"`
	AnswerActor string     `json:"answerActor,omitempty"`
	AskedAt     time.Time  `json:"askedAt"`
	AnsweredAt  *time.Time `json:"answeredAt,omitempty"`
}

// Decision is an AGENT-DEFAULT decision proposed by the Planner for a task
// (spec §5.2).
type Decision struct {
	Key       TaskKey   `json:"key"`
	Question  string    `json:"question"`
	Choice    string    `json:"choice"`
	CreatedAt time.Time `json:"createdAt"`
}

// Intent is a forge write recorded before the call, with its outcome after
// (spec §4.5).
type Intent struct {
	Key       TaskKey         `json:"key"`
	ID        string          `json:"id"`
	Operation IntentOperation `json:"operation"`
	Status    IntentStatus    `json:"status"`
	DryRun    bool            `json:"dryRun"`
	// Args are the forge call's arguments.
	Args json.RawMessage `json:"args,omitempty"`
	// Marker, TargetRef, ExpectedOldSHA and HeadSHA are the evidence
	// reconciliation needs.
	Marker         string `json:"marker,omitempty"`
	TargetRef      string `json:"targetRef,omitempty"`
	ExpectedOldSHA string `json:"expectedOldSha,omitempty"`
	HeadSHA        string `json:"headSha,omitempty"`
	// ObjectID, NewSHA and ErrorClass are the outcome.
	ObjectID   string     `json:"objectId,omitempty"`
	NewSHA     string     `json:"newSha,omitempty"`
	ErrorClass string     `json:"errorClass,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// Event is one entry of the append-only event journal (spec §4.5) with the
// audit fields of spec §8.6. Key is empty only for global events, such as a
// pause.
type Event struct {
	Key           TaskKey  `json:"key,omitempty"`
	Seq           int64    `json:"seq"`
	Type          string   `json:"type"`
	Role          Role     `json:"role,omitempty"`
	Identity      string   `json:"identity,omitempty"`
	PermissionSet []string `json:"permissionSet,omitempty"`
	// TokenExpiresAt is the credential's expiry; the token itself is never
	// recorded.
	TokenExpiresAt *time.Time `json:"tokenExpiresAt,omitempty"`
	Commits        []string   `json:"commits,omitempty"`
	Model          string     `json:"model,omitempty"`
	Usage          *Usage     `json:"usage,omitempty"`
	At             time.Time  `json:"at"`
}
