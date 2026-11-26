package lifecycle_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

func st(s model.TaskState, flags ...model.TaskFlag) lifecycle.Status {
	return lifecycle.Status{State: s, Flags: flags}
}

func check(t *testing.T, got lifecycle.Outcome, err error, want lifecycle.Outcome) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outcome = %+v, want %+v", got, want)
	}
}

func checkErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestAdvanceHappyPath(t *testing.T) {
	cases := []struct {
		name     string
		from, to model.TaskState
	}{
		{"inbox to planning", model.TaskStateInbox, model.TaskStatePlanning},
		{"ready to running", model.TaskStateReady, model.TaskStateRunning},
		{"publishing to ci", model.TaskStatePublishing, model.TaskStateCI},
		{"ci to review", model.TaskStateCI, model.TaskStateReview},
		{"merge to done", model.TaskStateMerge, model.TaskStateDone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := lifecycle.Advance(st(c.from))
			check(t, got, err, lifecycle.Outcome{State: c.to})
		})
	}
}

func TestAdvanceOtherStates(t *testing.T) {
	for _, s := range []model.TaskState{
		model.TaskStatePlanning, model.TaskStateWaiting, model.TaskStateRunning,
		model.TaskStateReview, model.TaskStateFixing, model.TaskStateDone,
	} {
		t.Run(string(s), func(t *testing.T) {
			_, err := lifecycle.Advance(st(s))
			checkErr(t, err, lifecycle.ErrInvalidTransition)
		})
	}
	t.Run("unknown state", func(t *testing.T) {
		_, err := lifecycle.Advance(st("bogus"))
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
}

func TestFlags(t *testing.T) {
	for _, f := range []model.TaskFlag{model.TaskFlagNeedsOwner, model.TaskFlagHold, model.TaskFlagFailed} {
		t.Run(string(f), func(t *testing.T) {
			in := st(model.TaskStateCI, model.TaskFlagHold)
			s, err := lifecycle.SetFlag(in, f)
			if err != nil {
				t.Fatal(err)
			}
			again, err := lifecycle.SetFlag(s, f)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(s, again) {
				t.Fatalf("not idempotent: %v then %v", s, again)
			}
			for i := 1; i < len(s.Flags); i++ {
				if s.Flags[i-1] >= s.Flags[i] {
					t.Fatalf("flags not sorted and unique: %v", s.Flags)
				}
			}
			if !reflect.DeepEqual(in.Flags, []model.TaskFlag{model.TaskFlagHold}) {
				t.Fatalf("input mutated: %v", in.Flags)
			}

			flagged, _ := lifecycle.SetFlag(st(model.TaskStateCI), f)
			calls := map[string]func() error{
				"advance": func() error { _, err := lifecycle.Advance(flagged); return err },
				"planner": func() error {
					p := flagged
					p.State = model.TaskStatePlanning
					_, err := lifecycle.RoutePlanner(p, model.PlannerVerdictReady, 0, 3, "")
					return err
				},
				"executor": func() error {
					p := flagged
					p.State = model.TaskStateRunning
					_, err := lifecycle.RouteExecutor(p, model.ExecutorVerdictComplete)
					return err
				},
				"review": func() error {
					p := flagged
					p.State = model.TaskStateReview
					_, err := lifecycle.RouteReview(p, "h1", []policy.Reviewer{{Role: model.RoleR1, HardStopChecklist: true}}, []lifecycle.ReviewResult{approve(model.RoleR1, "h1")})
					return err
				},
				"merge": func() error {
					p := flagged
					p.State = model.TaskStateMerge
					_, err := lifecycle.RouteMerge(p, lifecycle.MergeResultMerged)
					return err
				},
			}
			for name, call := range calls {
				if err := call(); !errors.Is(err, lifecycle.ErrFlagged) {
					t.Errorf("%s: error = %v, want ErrFlagged", name, err)
				}
			}

			cleared := lifecycle.ClearFlag(flagged, f)
			if !reflect.DeepEqual(cleared, st(model.TaskStateCI)) {
				t.Fatalf("cleared = %+v, want unchanged state ci without flags", cleared)
			}
			got, err := lifecycle.Advance(cleared)
			check(t, got, err, lifecycle.Outcome{State: model.TaskStateReview})
		})
	}
	t.Run("unknown flag", func(t *testing.T) {
		_, err := lifecycle.SetFlag(st(model.TaskStateCI), "bogus")
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
}

func TestRouteMerge(t *testing.T) {
	m := st(model.TaskStateMerge)
	t.Run("merged to done", func(t *testing.T) {
		got, err := lifecycle.RouteMerge(m, lifecycle.MergeResultMerged)
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateDone})
	})
	t.Run("head moved to ci", func(t *testing.T) {
		got, err := lifecycle.RouteMerge(m, lifecycle.MergeResultHeadMoved)
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateCI, Reason: "head-moved"})
	})
	t.Run("task closed leaves", func(t *testing.T) {
		got, err := lifecycle.RouteMerge(m, lifecycle.MergeResultTaskClosed)
		check(t, got, err, lifecycle.Outcome{State: model.TaskStateMerge, Action: lifecycle.ActionLeave, Reason: "task-closed"})
	})
	t.Run("not in merge", func(t *testing.T) {
		_, err := lifecycle.RouteMerge(st(model.TaskStateCI), lifecycle.MergeResultMerged)
		checkErr(t, err, lifecycle.ErrInvalidTransition)
	})
	t.Run("unknown result", func(t *testing.T) {
		_, err := lifecycle.RouteMerge(m, "bogus")
		checkErr(t, err, lifecycle.ErrInvalidInput)
	})
}
