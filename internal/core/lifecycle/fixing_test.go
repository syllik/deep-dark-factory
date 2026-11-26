package lifecycle_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
)

func fid(id, prior string, sev model.Severity, cat model.FindingCategory) model.Finding {
	f := finding(sev, cat)
	f.ID, f.PriorID = id, prior
	return f
}

func checkInvalidResult(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, lifecycle.ErrInvalidInput) || !errors.Is(err, model.ErrInvalidResult) {
		t.Fatalf("error = %v, want ErrInvalidInput and model.ErrInvalidResult", err)
	}
}

func TestRouteReviewFindingIdentity(t *testing.T) {
	rv := st(model.TaskStateReview)
	r1only := reviewers(t, model.RiskClassLow)
	p1 := func(id, prior string) model.Finding {
		return fid(id, prior, model.SeverityP1, model.FindingCategoryCorrectness)
	}
	p3 := func(id, prior string) model.Finding {
		return fid(id, prior, model.SeverityP3, model.FindingCategoryDocs)
	}
	route := func(r lifecycle.ReviewResult) (lifecycle.Outcome, error) {
		return lifecycle.RouteReview(rv, "h2", r1only, []lifecycle.ReviewResult{clearChecklist(r)})
	}
	withPrior := func(r lifecycle.ReviewResult, prior ...model.Finding) lifecycle.ReviewResult {
		r.Prior, r.HasPrior = prior, true
		return r
	}

	t.Run("duplicate finding id", func(t *testing.T) {
		_, err := route(requestChanges(model.RoleR1, "h2", p1("a", ""), p3("a", "")))
		checkInvalidResult(t, err)
	})
	t.Run("duplicate priorId", func(t *testing.T) {
		_, err := route(withPrior(requestChanges(model.RoleR1, "h2", p1("a", "x"), p3("b", "x")), p3("x", "")))
		checkInvalidResult(t, err)
	})
	t.Run("priorId without prior review", func(t *testing.T) {
		_, err := route(requestChanges(model.RoleR1, "h2", p1("a", "x")))
		checkInvalidResult(t, err)
	})
	t.Run("priorId naming no prior finding", func(t *testing.T) {
		_, err := route(withPrior(requestChanges(model.RoleR1, "h2", p1("a", "y")), p3("x", "")))
		checkInvalidResult(t, err)
	})
	t.Run("empty priorIds are not duplicates", func(t *testing.T) {
		got, err := route(withPrior(requestChanges(model.RoleR1, "h2", p1("a", ""), p3("b", "")), p1("x", "")))
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateFixing})
	})
	t.Run("loop guard: blocking continues blocking prior", func(t *testing.T) {
		got, err := route(withPrior(requestChanges(model.RoleR1, "h2", p1("a", "x")), p1("x", "")))
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview, Flag: model.TaskFlagNeedsOwner,
			Reason: "fix loop: r1 finding a continues blocking prior finding x"})
	})
	t.Run("blocking continues non-blocking prior goes to fixing", func(t *testing.T) {
		got, err := route(withPrior(requestChanges(model.RoleR1, "h2", p1("a", "x")), p3("x", "")))
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateFixing})
	})
	t.Run("non-blocking continues blocking prior merges", func(t *testing.T) {
		r := approve(model.RoleR1, "h2")
		r.Findings = []model.Finding{p3("a", "x")}
		got, err := route(withPrior(r, p1("x", "")))
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateMerge})
	})
	t.Run("hard stop wins over loop guard", func(t *testing.T) {
		hs := fid("b", "", model.SeverityP1, model.FindingCategoryHardStop)
		got, err := route(withPrior(requestChanges(model.RoleR1, "h2", p1("a", "x"), hs), p1("x", "")))
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview, Flag: model.TaskFlagNeedsOwner,
			SetHitStop: true, Reason: "hard stop found in review"})
	})
	t.Run("input not mutated", func(t *testing.T) {
		mk := func() lifecycle.ReviewResult {
			return clearChecklist(withPrior(requestChanges(model.RoleR1, "h2", p1("a", "x")), p1("x", "")))
		}
		in := []lifecycle.ReviewResult{mk()}
		if _, err := lifecycle.RouteReview(rv, "h2", r1only, in); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(in, []lifecycle.ReviewResult{mk()}) {
			t.Fatal("input mutated")
		}
	})
}

func TestRouteFindings(t *testing.T) {
	p1 := fid("b1", "", model.SeverityP1, model.FindingCategoryCorrectness)
	p2 := fid("m2", "", model.SeverityP2, model.FindingCategoryCorrectness)
	p3 := fid("m3", "", model.SeverityP3, model.FindingCategoryDocs)
	one := func(f model.Finding) []lifecycle.ReviewResult {
		r := approve(model.RoleR1, "h1")
		r.Findings = []model.Finding{f}
		return []lifecycle.ReviewResult{r}
	}
	cases := []struct {
		name string
		kind model.TaskKind
		f    model.Finding
		want model.FindingRoute
	}{
		{"blocking to fixing", model.TaskKindTask, p1, model.FindingRouteFixing},
		{"P2 to followup", model.TaskKindTask, p2, model.FindingRouteFollowup},
		{"P2 on a followup task to minors", model.TaskKindFollowup, p2, model.FindingRouteMinors},
		{"P3 to minors", model.TaskKindRequest, p3, model.FindingRouteMinors},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := lifecycle.RouteFindings(c.kind, one(c.f))
			if err != nil {
				t.Fatal(err)
			}
			want := []lifecycle.RoutedFinding{{Role: model.RoleR1, ID: c.f.ID, Route: c.want}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
	t.Run("invalid task kind", func(t *testing.T) {
		_, err := lifecycle.RouteFindings("epic", one(p2))
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("invalid finding", func(t *testing.T) {
		_, err := lifecycle.RouteFindings(model.TaskKindTask, one(fid("x", "", "P9", model.FindingCategoryDocs)))
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
	t.Run("ordered by role then finding order", func(t *testing.T) {
		r2 := approve(model.RoleR2, "h1")
		r2.Findings = []model.Finding{p3, p2}
		r1 := approve(model.RoleR1, "h1")
		r1.Findings = []model.Finding{p1}
		in := []lifecycle.ReviewResult{r2, r1}
		got, err := lifecycle.RouteFindings(model.TaskKindTask, in)
		if err != nil {
			t.Fatal(err)
		}
		want := []lifecycle.RoutedFinding{
			{Role: model.RoleR1, ID: "b1", Route: model.FindingRouteFixing},
			{Role: model.RoleR2, ID: "m3", Route: model.FindingRouteMinors},
			{Role: model.RoleR2, ID: "m2", Route: model.FindingRouteFollowup},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		if in[0].Role != model.RoleR2 {
			t.Fatal("input mutated")
		}
	})
}
