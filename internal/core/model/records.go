package model

import "time"

// Task is one unit of work on the conveyor, keyed by its source (spec §4.5,
// §5).
type Task struct {
	Key        TaskKey    `json:"key"`
	Forge      string     `json:"forge"`
	Repository string     `json:"repository"`
	SourceKind string     `json:"sourceKind"`
	SourceID   string     `json:"sourceId"`
	State      TaskState  `json:"state"`
	Flags      []TaskFlag `json:"flags,omitempty"`
	Risk       RiskClass  `json:"risk,omitempty"`
	Spec       string     `json:"spec,omitempty"`
	Acceptance []string   `json:"acceptance,omitempty"`
	Zones      []string   `json:"zones,omitempty"`
	DependsOn  []string   `json:"dependsOn,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	// TeardownAt records when the task's run cell was destroyed and
	// verified (spec §4.5 teardown record).
	TeardownAt *time.Time `json:"teardownAt,omitempty"`
}

// Run is one agent run for a task. Runs are not intents; an interrupted run
// is discarded (spec §4.5).
type Run struct {
	Key        TaskKey    `json:"key"`
	ID         string     `json:"id"`
	Role       Role       `json:"role"`
	Status     RunStatus  `json:"status"`
	HeadSHA    string     `json:"headSha,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Change is the forge change (pull request) DDF opened for a task.
type Change struct {
	Key       TaskKey   `json:"key"`
	ID        string    `json:"id"`
	Branch    string    `json:"branch"`
	HeadSHA   string    `json:"headSha"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
}

// Review is one reviewer's verdict on one head of a change (spec §5.2).
type Review struct {
	Key       TaskKey       `json:"key"`
	Role      Role          `json:"role"`
	HeadSHA   string        `json:"headSha"`
	Verdict   ReviewVerdict `json:"verdict"`
	RaiseRisk RiskClass     `json:"raiseRisk,omitempty"`
	CreatedAt time.Time     `json:"createdAt"`
}

// Finding is one reviewer finding (spec §6.3), as in the reviewer result
// schema, scoped by task key, reviewer role and reviewed head.
type Finding struct {
	Key           TaskKey         `json:"key"`
	Role          Role            `json:"role"`
	HeadSHA       string          `json:"headSha"`
	ID            string          `json:"id"`
	Severity      Severity        `json:"severity"`
	Category      FindingCategory `json:"category"`
	SecurityLevel SecurityLevel   `json:"securityLevel,omitempty"`
	Evidence      string          `json:"evidence"`
	Location      string          `json:"location,omitempty"`
	FixHint       string          `json:"fixHint,omitempty"`
}

// Question is a question to the owner, such as a Planner ASK, and its
// answer once given (spec §5.2 owner answers).
type Question struct {
	Key        TaskKey    `json:"key"`
	ID         string     `json:"id"`
	Text       string     `json:"text"`
	Answer     string     `json:"answer,omitempty"`
	AskedAt    time.Time  `json:"askedAt"`
	AnsweredAt *time.Time `json:"answeredAt,omitempty"`
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
	Key        TaskKey      `json:"key"`
	ID         string       `json:"id"`
	Operation  string       `json:"operation"`
	Status     IntentStatus `json:"status"`
	Outcome    string       `json:"outcome,omitempty"`
	CreatedAt  time.Time    `json:"createdAt"`
	ResolvedAt *time.Time   `json:"resolvedAt,omitempty"`
}

// Event is one entry of the append-only event journal (spec §4.5). Key is
// empty only for global events, such as a pause.
type Event struct {
	Key  TaskKey   `json:"key,omitempty"`
	Seq  int64     `json:"seq"`
	Type string    `json:"type"`
	At   time.Time `json:"at"`
}
