package model

import "fmt"

// parseEnum returns the member of values spelled s, or an error naming what.
func parseEnum[T ~string](what, s string, values []T) (T, error) {
	for _, v := range values {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("parse %s: unknown value %q", what, s)
}

// unmarshalEnum parses b with parse and stores the result in dst.
func unmarshalEnum[T ~string](dst *T, b []byte, parse func(string) (T, error)) error {
	v, err := parse(string(b))
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// TaskState is a task's conveyor state (spec §5).
type TaskState string

// TaskState values.
const (
	TaskStateInbox      TaskState = "inbox"
	TaskStatePlanning   TaskState = "planning"
	TaskStateReady      TaskState = "ready"
	TaskStateWaiting    TaskState = "waiting"
	TaskStateRunning    TaskState = "running"
	TaskStatePublishing TaskState = "publishing"
	TaskStateCI         TaskState = "ci"
	TaskStateReview     TaskState = "review"
	TaskStateFixing     TaskState = "fixing"
	TaskStateMerge      TaskState = "merge"
	TaskStateDone       TaskState = "done"
)

// ParseTaskState returns the TaskState spelled s; an unknown or empty value is an error.
func ParseTaskState(s string) (TaskState, error) {
	return parseEnum("task state", s, []TaskState{TaskStateInbox, TaskStatePlanning, TaskStateReady, TaskStateWaiting, TaskStateRunning, TaskStatePublishing, TaskStateCI, TaskStateReview, TaskStateFixing, TaskStateMerge, TaskStateDone})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseTaskState.
func (ts *TaskState) UnmarshalText(b []byte) error {
	return unmarshalEnum(ts, b, ParseTaskState)
}

// TaskFlag is a flag that takes a task off the conveyor (spec §5).
type TaskFlag string

// TaskFlag values.
const (
	TaskFlagNeedsOwner TaskFlag = "needs-owner"
	TaskFlagHold       TaskFlag = "hold"
	TaskFlagFailed     TaskFlag = "failed"
)

// ParseTaskFlag returns the TaskFlag spelled s; an unknown or empty value is an error.
func ParseTaskFlag(s string) (TaskFlag, error) {
	return parseEnum("task flag", s, []TaskFlag{TaskFlagNeedsOwner, TaskFlagHold, TaskFlagFailed})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseTaskFlag.
func (tf *TaskFlag) UnmarshalText(b []byte) error {
	return unmarshalEnum(tf, b, ParseTaskFlag)
}

// Role is an agent role (spec §7.1).
type Role string

// Role values.
const (
	RolePlanner  Role = "planner"
	RoleExecutor Role = "executor"
	RoleR1       Role = "r1"
	RoleR2       Role = "r2"
)

// ParseRole returns the Role spelled s; an unknown or empty value is an error.
func ParseRole(s string) (Role, error) {
	return parseEnum("role", s, []Role{RolePlanner, RoleExecutor, RoleR1, RoleR2})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseRole.
func (r *Role) UnmarshalText(b []byte) error {
	return unmarshalEnum(r, b, ParseRole)
}

// RiskClass is a change's risk class (spec §6.2).
type RiskClass string

// RiskClass values.
const (
	RiskClassDocs      RiskClass = "docs"
	RiskClassLow       RiskClass = "low"
	RiskClassStandard  RiskClass = "standard"
	RiskClassSensitive RiskClass = "sensitive"
	RiskClassRevert    RiskClass = "revert"
)

// ParseRiskClass returns the RiskClass spelled s; an unknown or empty value is an error.
func ParseRiskClass(s string) (RiskClass, error) {
	return parseEnum("risk class", s, []RiskClass{RiskClassDocs, RiskClassLow, RiskClassStandard, RiskClassSensitive, RiskClassRevert})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseRiskClass.
func (rc *RiskClass) UnmarshalText(b []byte) error {
	return unmarshalEnum(rc, b, ParseRiskClass)
}

// PlannerVerdict is the Planner's verdict (spec §5.2).
type PlannerVerdict string

// PlannerVerdict values.
const (
	PlannerVerdictReady     PlannerVerdict = "READY"
	PlannerVerdictDecompose PlannerVerdict = "DECOMPOSE"
	PlannerVerdictAsk       PlannerVerdict = "ASK"
	PlannerVerdictReject    PlannerVerdict = "REJECT"
)

// ParsePlannerVerdict returns the PlannerVerdict spelled s; an unknown or empty value is an error.
func ParsePlannerVerdict(s string) (PlannerVerdict, error) {
	return parseEnum("planner verdict", s, []PlannerVerdict{PlannerVerdictReady, PlannerVerdictDecompose, PlannerVerdictAsk, PlannerVerdictReject})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParsePlannerVerdict.
func (pv *PlannerVerdict) UnmarshalText(b []byte) error {
	return unmarshalEnum(pv, b, ParsePlannerVerdict)
}

// ExecutorVerdict is the Executor's verdict (spec §5.2).
type ExecutorVerdict string

// ExecutorVerdict values.
const (
	ExecutorVerdictComplete ExecutorVerdict = "COMPLETE"
	ExecutorVerdictBlocked  ExecutorVerdict = "BLOCKED"
)

// ParseExecutorVerdict returns the ExecutorVerdict spelled s; an unknown or empty value is an error.
func ParseExecutorVerdict(s string) (ExecutorVerdict, error) {
	return parseEnum("executor verdict", s, []ExecutorVerdict{ExecutorVerdictComplete, ExecutorVerdictBlocked})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseExecutorVerdict.
func (ev *ExecutorVerdict) UnmarshalText(b []byte) error {
	return unmarshalEnum(ev, b, ParseExecutorVerdict)
}

// ReviewVerdict is a reviewer's verdict.
type ReviewVerdict string

// ReviewVerdict values.
const (
	ReviewVerdictApprove        ReviewVerdict = "APPROVE"
	ReviewVerdictRequestChanges ReviewVerdict = "REQUEST_CHANGES"
)

// ParseReviewVerdict returns the ReviewVerdict spelled s; an unknown or empty value is an error.
func ParseReviewVerdict(s string) (ReviewVerdict, error) {
	return parseEnum("review verdict", s, []ReviewVerdict{ReviewVerdictApprove, ReviewVerdictRequestChanges})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseReviewVerdict.
func (rv *ReviewVerdict) UnmarshalText(b []byte) error {
	return unmarshalEnum(rv, b, ParseReviewVerdict)
}

// Severity is a finding's severity (spec §6.3).
type Severity string

// Severity values.
const (
	SeverityP0 Severity = "P0"
	SeverityP1 Severity = "P1"
	SeverityP2 Severity = "P2"
	SeverityP3 Severity = "P3"
)

// ParseSeverity returns the Severity spelled s; an unknown or empty value is an error.
func ParseSeverity(s string) (Severity, error) {
	return parseEnum("severity", s, []Severity{SeverityP0, SeverityP1, SeverityP2, SeverityP3})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseSeverity.
func (s *Severity) UnmarshalText(b []byte) error {
	return unmarshalEnum(s, b, ParseSeverity)
}

// FindingCategory is a finding's category (spec §6.3).
type FindingCategory string

// FindingCategory values.
const (
	FindingCategoryCorrectness FindingCategory = "correctness"
	FindingCategoryAcceptance  FindingCategory = "acceptance"
	FindingCategorySecurity    FindingCategory = "security"
	FindingCategoryData        FindingCategory = "data"
	FindingCategoryHardStop    FindingCategory = "hard-stop"
	FindingCategoryDocs        FindingCategory = "docs"
)

// ParseFindingCategory returns the FindingCategory spelled s; an unknown or empty value is an error.
func ParseFindingCategory(s string) (FindingCategory, error) {
	return parseEnum("finding category", s, []FindingCategory{FindingCategoryCorrectness, FindingCategoryAcceptance, FindingCategorySecurity, FindingCategoryData, FindingCategoryHardStop, FindingCategoryDocs})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseFindingCategory.
func (fc *FindingCategory) UnmarshalText(b []byte) error {
	return unmarshalEnum(fc, b, ParseFindingCategory)
}

// SecurityLevel is a security finding's level (spec §6.3).
type SecurityLevel string

// SecurityLevel values.
const (
	SecurityLevelHigh   SecurityLevel = "High"
	SecurityLevelMedium SecurityLevel = "Medium"
	SecurityLevelLow    SecurityLevel = "Low"
)

// ParseSecurityLevel returns the SecurityLevel spelled s; an unknown or empty value is an error.
func ParseSecurityLevel(s string) (SecurityLevel, error) {
	return parseEnum("security level", s, []SecurityLevel{SecurityLevelHigh, SecurityLevelMedium, SecurityLevelLow})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseSecurityLevel.
func (sl *SecurityLevel) UnmarshalText(b []byte) error {
	return unmarshalEnum(sl, b, ParseSecurityLevel)
}

// IntentStatus is the outcome state of a forge-write intent (spec §4.5).
type IntentStatus string

// IntentStatus values.
const (
	IntentStatusPending   IntentStatus = "pending"
	IntentStatusSucceeded IntentStatus = "succeeded"
	IntentStatusFailed    IntentStatus = "failed"
)

// ParseIntentStatus returns the IntentStatus spelled s; an unknown or empty value is an error.
func ParseIntentStatus(s string) (IntentStatus, error) {
	return parseEnum("intent status", s, []IntentStatus{IntentStatusPending, IntentStatusSucceeded, IntentStatusFailed})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseIntentStatus.
func (is *IntentStatus) UnmarshalText(b []byte) error {
	return unmarshalEnum(is, b, ParseIntentStatus)
}

// RunStatus is the state of an agent run (spec §4.5).
type RunStatus string

// RunStatus values.
const (
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusDiscarded RunStatus = "discarded"
)

// ParseRunStatus returns the RunStatus spelled s; an unknown or empty value is an error.
func ParseRunStatus(s string) (RunStatus, error) {
	return parseEnum("run status", s, []RunStatus{RunStatusRunning, RunStatusSucceeded, RunStatusFailed, RunStatusDiscarded})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseRunStatus.
func (rs *RunStatus) UnmarshalText(b []byte) error {
	return unmarshalEnum(rs, b, ParseRunStatus)
}

// ResultKind is the kind of role result a schema validates.
type ResultKind string

// ResultKind values.
const (
	ResultKindPlanner  ResultKind = "planner"
	ResultKindExecutor ResultKind = "executor"
	ResultKindReviewer ResultKind = "reviewer"
)

// ParseResultKind returns the ResultKind spelled s; an unknown or empty value is an error.
func ParseResultKind(s string) (ResultKind, error) {
	return parseEnum("result kind", s, []ResultKind{ResultKindPlanner, ResultKindExecutor, ResultKindReviewer})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseResultKind.
func (rk *ResultKind) UnmarshalText(b []byte) error {
	return unmarshalEnum(rk, b, ParseResultKind)
}

// TaskKind is what a task row stands for (spec §4.5).
type TaskKind string

// TaskKind values.
const (
	TaskKindTask     TaskKind = "task"
	TaskKindRequest  TaskKind = "request"
	TaskKindFollowup TaskKind = "followup"
)

// ParseTaskKind returns the TaskKind spelled s; an unknown or empty value is an error.
func ParseTaskKind(s string) (TaskKind, error) {
	return parseEnum("task kind", s, []TaskKind{TaskKindTask, TaskKindRequest, TaskKindFollowup})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseTaskKind.
func (tk *TaskKind) UnmarshalText(b []byte) error {
	return unmarshalEnum(tk, b, ParseTaskKind)
}

// Priority is a task's scheduling priority (spec §4.5).
type Priority string

// Priority values.
const (
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// ParsePriority returns the Priority spelled s; an unknown or empty value is an error.
func ParsePriority(s string) (Priority, error) {
	return parseEnum("priority", s, []Priority{PriorityHigh, PriorityNormal, PriorityLow})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParsePriority.
func (p *Priority) UnmarshalText(b []byte) error {
	return unmarshalEnum(p, b, ParsePriority)
}

// RunPurpose is why an agent run was started (spec §4.5).
type RunPurpose string

// RunPurpose values.
const (
	RunPurposePlanning   RunPurpose = "planning"
	RunPurposeInitial    RunPurpose = "initial"
	RunPurposeCorrection RunPurpose = "correction"
	RunPurposeSync       RunPurpose = "sync"
	RunPurposeCIFix      RunPurpose = "ci-fix"
	RunPurposeReview     RunPurpose = "review"
)

// ParseRunPurpose returns the RunPurpose spelled s; an unknown or empty value is an error.
func ParseRunPurpose(s string) (RunPurpose, error) {
	return parseEnum("run purpose", s, []RunPurpose{RunPurposePlanning, RunPurposeInitial, RunPurposeCorrection, RunPurposeSync, RunPurposeCIFix, RunPurposeReview})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseRunPurpose.
func (rp *RunPurpose) UnmarshalText(b []byte) error {
	return unmarshalEnum(rp, b, ParseRunPurpose)
}

// FailureClass is the failure class of a failed run (spec §4.5, §7.3). Its
// spellings equal those of ports.AgentFailureClass.
type FailureClass string

// FailureClass values.
const (
	FailureClassCredential  FailureClass = "credential"
	FailureClassRateLimited FailureClass = "rate-limited"
	FailureClassRetryable   FailureClass = "retryable"
	FailureClassFatal       FailureClass = "fatal"
)

// ParseFailureClass returns the FailureClass spelled s; an unknown or empty value is an error.
func ParseFailureClass(s string) (FailureClass, error) {
	return parseEnum("failure class", s, []FailureClass{FailureClassCredential, FailureClassRateLimited, FailureClassRetryable, FailureClassFatal})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseFailureClass.
func (fc *FailureClass) UnmarshalText(b []byte) error {
	return unmarshalEnum(fc, b, ParseFailureClass)
}

// ChangeKind is what a change does (spec §4.5).
type ChangeKind string

// ChangeKind values.
const (
	ChangeKindChange ChangeKind = "change"
	ChangeKindRevert ChangeKind = "revert"
)

// ParseChangeKind returns the ChangeKind spelled s; an unknown or empty value is an error.
func ParseChangeKind(s string) (ChangeKind, error) {
	return parseEnum("change kind", s, []ChangeKind{ChangeKindChange, ChangeKindRevert})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseChangeKind.
func (ck *ChangeKind) UnmarshalText(b []byte) error {
	return unmarshalEnum(ck, b, ParseChangeKind)
}

// FindingRoute is where a finding was routed (spec §4.5).
type FindingRoute string

// FindingRoute values.
const (
	FindingRouteFixing   FindingRoute = "fixing"
	FindingRouteFollowup FindingRoute = "followup"
	FindingRouteMinors   FindingRoute = "minors"
)

// ParseFindingRoute returns the FindingRoute spelled s; an unknown or empty value is an error.
func ParseFindingRoute(s string) (FindingRoute, error) {
	return parseEnum("finding route", s, []FindingRoute{FindingRouteFixing, FindingRouteFollowup, FindingRouteMinors})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseFindingRoute.
func (fr *FindingRoute) UnmarshalText(b []byte) error {
	return unmarshalEnum(fr, b, ParseFindingRoute)
}

// IntentOperation is the forge write an intent records (spec §4.5).
type IntentOperation string

// IntentOperation values.
const (
	IntentOperationCreateTask   IntentOperation = "create-task"
	IntentOperationPush         IntentOperation = "push"
	IntentOperationOpenChange   IntentOperation = "open-change"
	IntentOperationUpdateChange IntentOperation = "update-change"
	IntentOperationPostReview   IntentOperation = "post-review"
	IntentOperationUpdateBranch IntentOperation = "update-branch"
	IntentOperationMerge        IntentOperation = "merge"
	IntentOperationRevert       IntentOperation = "revert"
	IntentOperationCloseTask    IntentOperation = "close-task"
	IntentOperationReopenTask   IntentOperation = "reopen-task"
)

// ParseIntentOperation returns the IntentOperation spelled s; an unknown or empty value is an error.
func ParseIntentOperation(s string) (IntentOperation, error) {
	return parseEnum("intent operation", s, []IntentOperation{IntentOperationCreateTask, IntentOperationPush, IntentOperationOpenChange, IntentOperationUpdateChange, IntentOperationPostReview, IntentOperationUpdateBranch, IntentOperationMerge, IntentOperationRevert, IntentOperationCloseTask, IntentOperationReopenTask})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseIntentOperation.
func (op *IntentOperation) UnmarshalText(b []byte) error {
	return unmarshalEnum(op, b, ParseIntentOperation)
}
