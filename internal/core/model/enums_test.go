package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// checkEnum pins the spelling of every value and checks that each one
// round-trips through parse and JSON, and that unknown and empty values are
// refused by parse and by JSON decoding into a field of type T.
func checkEnum[T ~string](t *testing.T, spellings string, values []T, parse func(string) (T, error)) {
	t.Helper()
	want := strings.Fields(spellings)
	if len(want) != len(values) {
		t.Fatalf("got %d values, want %d", len(values), len(want))
	}
	type holder struct {
		V T `json:"v"`
	}
	for i, v := range values {
		if string(v) != want[i] {
			t.Errorf("value %d = %q, want %q", i, v, want[i])
		}
		got, err := parse(want[i])
		if err != nil || got != v {
			t.Errorf("parse %q = %q, %v; want %q", want[i], got, err, v)
		}
		raw, err := json.Marshal(holder{V: v})
		if err != nil {
			t.Fatalf("marshal %q: %v", v, err)
		}
		var back holder
		if err := json.Unmarshal(raw, &back); err != nil || back.V != v {
			t.Errorf("json round trip %q = %q, %v", v, back.V, err)
		}
	}
	for _, bad := range []string{"unknown", ""} {
		if _, err := parse(bad); err == nil {
			t.Errorf("parse %q: want error", bad)
		}
		var h holder
		if err := json.Unmarshal([]byte(`{"v":"`+bad+`"}`), &h); err == nil {
			t.Errorf("json unmarshal %q: want error", bad)
		}
	}
}

func TestEnums_RoundTripAndRejectUnknown(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{"TaskState spec §5", func(t *testing.T) {
			checkEnum(t, "inbox planning ready waiting running publishing ci review fixing merge done",
				[]model.TaskState{model.TaskStateInbox, model.TaskStatePlanning, model.TaskStateReady, model.TaskStateWaiting, model.TaskStateRunning, model.TaskStatePublishing, model.TaskStateCI, model.TaskStateReview, model.TaskStateFixing, model.TaskStateMerge, model.TaskStateDone}, model.ParseTaskState)
		}},
		{"TaskFlag spec §5", func(t *testing.T) {
			checkEnum(t, "needs-owner hold failed", []model.TaskFlag{model.TaskFlagNeedsOwner, model.TaskFlagHold, model.TaskFlagFailed}, model.ParseTaskFlag)
		}},
		{"Role spec §7.1", func(t *testing.T) {
			checkEnum(t, "planner executor r1 r2", []model.Role{model.RolePlanner, model.RoleExecutor, model.RoleR1, model.RoleR2}, model.ParseRole)
		}},
		{"RiskClass spec §6.2", func(t *testing.T) {
			checkEnum(t, "docs low standard sensitive revert", []model.RiskClass{model.RiskClassDocs, model.RiskClassLow, model.RiskClassStandard, model.RiskClassSensitive, model.RiskClassRevert}, model.ParseRiskClass)
		}},
		{"PlannerVerdict planner schema", func(t *testing.T) {
			checkEnum(t, "READY DECOMPOSE ASK REJECT", []model.PlannerVerdict{model.PlannerVerdictReady, model.PlannerVerdictDecompose, model.PlannerVerdictAsk, model.PlannerVerdictReject}, model.ParsePlannerVerdict)
		}},
		{"ExecutorVerdict executor schema", func(t *testing.T) {
			checkEnum(t, "COMPLETE BLOCKED", []model.ExecutorVerdict{model.ExecutorVerdictComplete, model.ExecutorVerdictBlocked}, model.ParseExecutorVerdict)
		}},
		{"ReviewVerdict reviewer schema", func(t *testing.T) {
			checkEnum(t, "APPROVE REQUEST_CHANGES", []model.ReviewVerdict{model.ReviewVerdictApprove, model.ReviewVerdictRequestChanges}, model.ParseReviewVerdict)
		}},
		{"Severity spec §6.3", func(t *testing.T) {
			checkEnum(t, "P0 P1 P2 P3", []model.Severity{model.SeverityP0, model.SeverityP1, model.SeverityP2, model.SeverityP3}, model.ParseSeverity)
		}},
		{"FindingCategory spec §6.3", func(t *testing.T) {
			checkEnum(t, "correctness acceptance security data hard-stop docs", []model.FindingCategory{model.FindingCategoryCorrectness, model.FindingCategoryAcceptance, model.FindingCategorySecurity, model.FindingCategoryData, model.FindingCategoryHardStop, model.FindingCategoryDocs}, model.ParseFindingCategory)
		}},
		{"SecurityLevel spec §6.3", func(t *testing.T) {
			checkEnum(t, "High Medium Low", []model.SecurityLevel{model.SecurityLevelHigh, model.SecurityLevelMedium, model.SecurityLevelLow}, model.ParseSecurityLevel)
		}},
		{"IntentStatus spec §4.5", func(t *testing.T) {
			checkEnum(t, "pending succeeded failed", []model.IntentStatus{model.IntentStatusPending, model.IntentStatusSucceeded, model.IntentStatusFailed}, model.ParseIntentStatus)
		}},
		{"RunStatus spec §4.5", func(t *testing.T) {
			checkEnum(t, "running succeeded failed discarded", []model.RunStatus{model.RunStatusRunning, model.RunStatusSucceeded, model.RunStatusFailed, model.RunStatusDiscarded}, model.ParseRunStatus)
		}},
		{"ResultKind role results", func(t *testing.T) {
			checkEnum(t, "planner executor reviewer", []model.ResultKind{model.ResultKindPlanner, model.ResultKindExecutor, model.ResultKindReviewer}, model.ParseResultKind)
		}},
		{"TaskKind spec §4.5", func(t *testing.T) {
			checkEnum(t, "task request followup", []model.TaskKind{model.TaskKindTask, model.TaskKindRequest, model.TaskKindFollowup}, model.ParseTaskKind)
		}},
		{"Priority spec §4.5", func(t *testing.T) {
			checkEnum(t, "high normal low", []model.Priority{model.PriorityHigh, model.PriorityNormal, model.PriorityLow}, model.ParsePriority)
		}},
		{"RunPurpose spec §4.5", func(t *testing.T) {
			checkEnum(t, "planning initial correction sync ci-fix review", []model.RunPurpose{model.RunPurposePlanning, model.RunPurposeInitial, model.RunPurposeCorrection, model.RunPurposeSync, model.RunPurposeCIFix, model.RunPurposeReview}, model.ParseRunPurpose)
		}},
		{"FailureClass spec §4.5", func(t *testing.T) {
			checkEnum(t, "credential rate-limited retryable fatal", []model.FailureClass{model.FailureClassCredential, model.FailureClassRateLimited, model.FailureClassRetryable, model.FailureClassFatal}, model.ParseFailureClass)
		}},
		{"ChangeKind spec §4.5", func(t *testing.T) {
			checkEnum(t, "change revert", []model.ChangeKind{model.ChangeKindChange, model.ChangeKindRevert}, model.ParseChangeKind)
		}},
		{"FindingRoute spec §4.5", func(t *testing.T) {
			checkEnum(t, "fixing followup minors", []model.FindingRoute{model.FindingRouteFixing, model.FindingRouteFollowup, model.FindingRouteMinors}, model.ParseFindingRoute)
		}},
		{"IntentOperation spec §4.5", func(t *testing.T) {
			checkEnum(t, "create-task push open-change update-change post-review update-branch merge revert close-task reopen-task",
				[]model.IntentOperation{model.IntentOperationCreateTask, model.IntentOperationPush, model.IntentOperationOpenChange, model.IntentOperationUpdateChange, model.IntentOperationPostReview, model.IntentOperationUpdateBranch, model.IntentOperationMerge, model.IntentOperationRevert, model.IntentOperationCloseTask, model.IntentOperationReopenTask}, model.ParseIntentOperation)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
