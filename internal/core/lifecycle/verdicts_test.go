package lifecycle_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

func TestRoutePlanner(t *testing.T) {
	p := st(model.TaskStatePlanning)
	owner := func(reason string) lifecycle.Outcome {
		return lifecycle.Outcome{State: model.TaskStatePlanning, Flag: model.TaskFlagNeedsOwner, Reason: reason}
	}
	t.Run("READY to ready", func(t *testing.T) {
		got, err := lifecycle.RoutePlanner(p, model.PlannerVerdictReady, 0, 3, "")
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReady})
	})
	t.Run("DECOMPOSE within max creates children", func(t *testing.T) {
		got, err := lifecycle.RoutePlanner(p, model.PlannerVerdictDecompose, 3, 3, "")
		check(t, got, err, lifecycle.Outcome{State: model.TaskStatePlanning, Action: lifecycle.ActionCreateChildren})
	})
	t.Run("DECOMPOSE above max needs owner", func(t *testing.T) {
		got, err := lifecycle.RoutePlanner(p, model.PlannerVerdictDecompose, 4, 3, "")
		check(t, got, err, owner("decompose into 4 children exceeds maxAutoChildren 3"))
	})
	t.Run("ASK needs owner", func(t *testing.T) {
		got, err := lifecycle.RoutePlanner(p, model.PlannerVerdictAsk, 0, 3, "which db?")
		check(t, got, err, owner("which db?"))
	})
	t.Run("REJECT needs owner with reason", func(t *testing.T) {
		got, err := lifecycle.RoutePlanner(p, model.PlannerVerdictReject, 0, 3, "out of scope")
		check(t, got, err, owner("out of scope"))
	})
	t.Run("REJECT without reason", func(t *testing.T) {
		_, err := lifecycle.RoutePlanner(p, model.PlannerVerdictReject, 0, 3, "")
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("DECOMPOSE without children", func(t *testing.T) {
		_, err := lifecycle.RoutePlanner(p, model.PlannerVerdictDecompose, 0, 3, "")
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("unknown verdict", func(t *testing.T) {
		_, err := lifecycle.RoutePlanner(p, "MAYBE", 0, 3, "")
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("not in planning", func(t *testing.T) {
		_, err := lifecycle.RoutePlanner(st(model.TaskStateInbox), model.PlannerVerdictReady, 0, 3, "")
		checkErr(t, err, lifecycle.ErrInvalidTransition)
	})
}

func TestRouteExecutor(t *testing.T) {
	for _, s := range []model.TaskState{model.TaskStateRunning, model.TaskStateFixing} {
		t.Run(string(s)+" COMPLETE to publishing", func(t *testing.T) {
			got, err := lifecycle.RouteExecutor(st(s), model.ExecutorVerdictComplete)
			check(t, got, err, lifecycle.Outcome{State: model.TaskStatePublishing})
		})
		t.Run(string(s)+" BLOCKED needs owner", func(t *testing.T) {
			got, err := lifecycle.RouteExecutor(st(s), model.ExecutorVerdictBlocked)
			check(t, got, err, lifecycle.Outcome{State: s, Flag: model.TaskFlagNeedsOwner, Reason: "executor blocked"})
		})
	}
	t.Run("not running", func(t *testing.T) {
		_, err := lifecycle.RouteExecutor(st(model.TaskStateReview), model.ExecutorVerdictComplete)
		checkErr(t, err, lifecycle.ErrInvalidTransition)
	})
	t.Run("unknown verdict", func(t *testing.T) {
		_, err := lifecycle.RouteExecutor(st(model.TaskStateRunning), "DONE")
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
}

func approve(role model.Role, head string) lifecycle.ReviewResult {
	return lifecycle.ReviewResult{Role: role, Head: head, Verdict: model.ReviewVerdictApprove}
}

func finding(sev model.Severity, cat model.FindingCategory) model.Finding {
	return model.Finding{ID: "f1", Severity: sev, Category: cat, Evidence: "e"}
}

func requestChanges(role model.Role, head string, fs ...model.Finding) lifecycle.ReviewResult {
	return lifecycle.ReviewResult{Role: role, Head: head, Verdict: model.ReviewVerdictRequestChanges, Findings: fs}
}

func withChecklist(r lifecycle.ReviewResult, s lifecycle.ChecklistStatus) lifecycle.ReviewResult {
	r.Checklist = []lifecycle.ChecklistItem{{Item: "secrets", Status: lifecycle.ChecklistClear}, {Item: "auth", Status: s}}
	return r
}

func clearChecklist(r lifecycle.ReviewResult) lifecycle.ReviewResult {
	return withChecklist(r, lifecycle.ChecklistClear)
}

func reviewers(t *testing.T, c model.RiskClass) []policy.Reviewer {
	t.Helper()
	rs, err := policy.Reviewers(c)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestRouteReview(t *testing.T) {
	rv := st(model.TaskStateReview)
	both := reviewers(t, model.RiskClassStandard) // R2 runs the checklist
	r1only := reviewers(t, model.RiskClassLow)    // R1 runs the checklist
	p1 := finding(model.SeverityP1, model.FindingCategoryCorrectness)
	hs := finding(model.SeverityP1, model.FindingCategoryHardStop)
	ok := clearChecklist

	t.Run("missing required result", func(t *testing.T) {
		_, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{approve(model.RoleR1, "h1")})
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("result for another head", func(t *testing.T) {
		_, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{ok(approve(model.RoleR1, "h0"))})
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("REQUEST_CHANGES without blocking", func(t *testing.T) {
		_, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{
			ok(requestChanges(model.RoleR1, "h1", finding(model.SeverityP2, model.FindingCategoryDocs))),
		})
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("r1-only approve without checklist is invalid", func(t *testing.T) {
		got, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{approve(model.RoleR1, "h1")})
		checkErr(t, err, lifecycle.ErrInvalidInput)
		if got.State == model.TaskStateMerge || got.Action == lifecycle.ActionMerge {
			t.Fatal("merged without a checklist")
		}
	})
	t.Run("r2 approve with empty checklist is invalid", func(t *testing.T) {
		r2 := approve(model.RoleR2, "h1")
		r2.Checklist = []lifecycle.ChecklistItem{}
		got, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{approve(model.RoleR1, "h1"), r2})
		checkErr(t, err, lifecycle.ErrInvalidInput)
		if got.State == model.TaskStateMerge || got.Action == lifecycle.ActionMerge {
			t.Fatal("merged with an empty checklist")
		}
	})
	t.Run("hard-stop finding without checklist is invalid", func(t *testing.T) {
		_, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{requestChanges(model.RoleR1, "h1", hs)})
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("blocking finding without checklist is invalid", func(t *testing.T) {
		_, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{approve(model.RoleR1, "h1"), requestChanges(model.RoleR2, "h1", p1)})
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("unassessed reruns reviewer before findings", func(t *testing.T) {
		got, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{
			requestChanges(model.RoleR1, "h1", hs),
			withChecklist(requestChanges(model.RoleR2, "h1", p1), lifecycle.ChecklistUnassessed),
		})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview, Action: lifecycle.ActionRerunReviewer,
			Roles: []model.Role{model.RoleR2}, Reason: "hard-stop checklist item unassessed"})
	})
	t.Run("optional checklist unassessed reruns that reviewer", func(t *testing.T) {
		got, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{
			withChecklist(approve(model.RoleR1, "h1"), lifecycle.ChecklistUnassessed),
			ok(approve(model.RoleR2, "h1")),
		})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview, Action: lifecycle.ActionRerunReviewer,
			Roles: []model.Role{model.RoleR1}, Reason: "hard-stop checklist item unassessed"})
	})
	t.Run("optional checklist hit needs owner", func(t *testing.T) {
		got, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{
			withChecklist(approve(model.RoleR1, "h1"), lifecycle.ChecklistHit),
			ok(approve(model.RoleR2, "h1")),
		})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview, Flag: model.TaskFlagNeedsOwner, SetHitStop: true, Reason: "hard stop found in review"})
	})
	t.Run("hard-stop finding needs owner with hit_stop", func(t *testing.T) {
		got, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{ok(requestChanges(model.RoleR1, "h1", hs))})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview, Flag: model.TaskFlagNeedsOwner, SetHitStop: true, Reason: "hard stop found in review"})
	})
	t.Run("checklist hit needs owner with hit_stop", func(t *testing.T) {
		got, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{
			approve(model.RoleR1, "h1"),
			withChecklist(requestChanges(model.RoleR2, "h1", hs), lifecycle.ChecklistHit),
		})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview, Flag: model.TaskFlagNeedsOwner, SetHitStop: true, Reason: "hard stop found in review"})
	})
	t.Run("blocking finding to fixing", func(t *testing.T) {
		got, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{
			approve(model.RoleR1, "h1"), ok(requestChanges(model.RoleR2, "h1", p1)),
		})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateFixing})
	})
	t.Run("security medium blocks to fixing", func(t *testing.T) {
		f := finding(model.SeverityP2, model.FindingCategorySecurity)
		f.SecurityLevel = model.SecurityLevelMedium
		got, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{ok(requestChanges(model.RoleR1, "h1", f))})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateFixing})
	})
	t.Run("r1 raiseRisk adds r2", func(t *testing.T) {
		r := ok(approve(model.RoleR1, "h1"))
		r.RaiseRisk = model.RiskClassStandard
		got, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{r})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview, Action: lifecycle.ActionAddReviewer,
			Roles: []model.Role{model.RoleR2}, Risk: model.RiskClassStandard, Reason: "r1 raised risk to standard"})
	})
	t.Run("r1 raiseRisk with r2 present merges", func(t *testing.T) {
		r := approve(model.RoleR1, "h1")
		r.RaiseRisk = model.RiskClassSensitive
		got, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{r, ok(approve(model.RoleR2, "h1"))})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateMerge})
	})
	t.Run("all approve to merge", func(t *testing.T) {
		r := approve(model.RoleR1, "h1")
		r.Findings = []model.Finding{finding(model.SeverityP3, model.FindingCategoryDocs)}
		got, err := lifecycle.RouteReview(rv, "h1", both, []lifecycle.ReviewResult{r, ok(approve(model.RoleR2, "h1"))})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateMerge})
	})
	t.Run("r1 only approve with checklist merges", func(t *testing.T) {
		got, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{ok(approve(model.RoleR1, "h1"))})
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateMerge})
	})
	t.Run("result outside the set", func(t *testing.T) {
		_, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{ok(approve(model.RoleR1, "h1")), ok(approve(model.RoleR2, "h1"))})
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("invalid finding", func(t *testing.T) {
		_, err := lifecycle.RouteReview(rv, "h1", r1only, []lifecycle.ReviewResult{ok(requestChanges(model.RoleR1, "h1", finding("P9", model.FindingCategoryDocs)))})
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("not in review", func(t *testing.T) {
		_, err := lifecycle.RouteReview(st(model.TaskStateCI), "h1", r1only, []lifecycle.ReviewResult{ok(approve(model.RoleR1, "h1"))})
		checkErr(t, err, lifecycle.ErrInvalidTransition)
	})
	t.Run("input not mutated", func(t *testing.T) {
		req := reviewers(t, model.RiskClassLow)
		in := []lifecycle.ReviewResult{withChecklist(approve(model.RoleR1, "h1"), lifecycle.ChecklistUnassessed)}
		before := []lifecycle.ReviewResult{withChecklist(approve(model.RoleR1, "h1"), lifecycle.ChecklistUnassessed)}
		if _, err := lifecycle.RouteReview(rv, "h1", req, in); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in, before) || !reflect.DeepEqual(req, reviewers(t, model.RiskClassLow)) {
			t.Fatal("input mutated")
		}
	})
}

func TestParseChecklist(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got, err := lifecycle.ParseChecklist(json.RawMessage(`[{"item":"secrets","status":"clear"},{"item":"auth","status":"hit"}]`))
		if err != nil {
			t.Fatal(err)
		}
		want := []lifecycle.ChecklistItem{{Item: "secrets", Status: lifecycle.ChecklistClear}, {Item: "auth", Status: lifecycle.ChecklistHit}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		got, err := lifecycle.ParseChecklist(nil)
		if err != nil || got != nil {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("unknown status", func(t *testing.T) {
		_, err := lifecycle.ParseChecklist(json.RawMessage(`[{"item":"x","status":"maybe"}]`))
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("malformed", func(t *testing.T) {
		_, err := lifecycle.ParseChecklist(json.RawMessage(`{`))
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
}
