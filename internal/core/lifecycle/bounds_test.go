package lifecycle

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

var boundsT0 = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

func boundsRev(n int) *int { return &n }

func boundsRun(purpose model.RunPurpose, attempt int, status model.RunStatus) model.Run {
	return model.Run{ID: "r", Role: model.RoleExecutor, Purpose: purpose, Attempt: attempt, Status: status, StartedAt: boundsT0}
}

func boundsRuns(n int, purpose model.RunPurpose) []model.Run {
	out := make([]model.Run, n)
	for i := range out {
		out[i] = boundsRun(purpose, 1, model.RunStatusSucceeded)
		if purpose == model.RunPurposeCIFix {
			out[i].Revision = boundsRev(1)
		}
	}
	return out
}

func isNeedsOwner(o Outcome) bool { return o.Flag == model.TaskFlagNeedsOwner }

func TestDefaultLimits(t *testing.T) {
	want := Limits{
		PlannerTimeout:       30 * time.Minute,
		ExecutorTimeout:      3 * time.Hour,
		ReviewerTimeout:      1 * time.Hour,
		InvalidResultRetries: 1,
		CIWait:               2 * time.Hour,
		CIFixPerRevision:     2,
		CorrectionBatches:    3,
		SyncBatches:          2,
		MaxRunsPerTask:       20,
	}
	if got := DefaultLimits(); got != want {
		t.Errorf("DefaultLimits() = %+v, want %+v", got, want)
	}
}

func TestCountRuns(t *testing.T) {
	in := []model.Run{
		boundsRun(model.RunPurposePlanning, 1, model.RunStatusSucceeded),
		boundsRun(model.RunPurposeInitial, 1, model.RunStatusSucceeded),
		boundsRun(model.RunPurposeReview, 1, model.RunStatusFailed),
		boundsRun(model.RunPurposeCorrection, 1, model.RunStatusFailed),
		boundsRun(model.RunPurposeCorrection, 2, model.RunStatusSucceeded), // retry: same batch
		boundsRun(model.RunPurposeCorrection, 1, model.RunStatusDiscarded), // discarded: not a batch
		boundsRun(model.RunPurposeCorrection, 1, model.RunStatusRunning),   // rerun of the discarded run
		boundsRun(model.RunPurposeSync, 1, model.RunStatusSucceeded),
		boundsRun(model.RunPurposeSync, 2, model.RunStatusSucceeded),
		boundsRun(model.RunPurposeSync, 1, model.RunStatusDiscarded),
		{ID: "c1", Role: model.RoleExecutor, Purpose: model.RunPurposeCIFix, Revision: boundsRev(1), Attempt: 1, Status: model.RunStatusSucceeded},
		{ID: "c2", Role: model.RoleExecutor, Purpose: model.RunPurposeCIFix, Revision: boundsRev(1), Attempt: 2, Status: model.RunStatusSucceeded},
		{ID: "c3", Role: model.RoleExecutor, Purpose: model.RunPurposeCIFix, Revision: boundsRev(1), Attempt: 1, Status: model.RunStatusDiscarded},
		{ID: "c4", Role: model.RoleExecutor, Purpose: model.RunPurposeCIFix, Revision: boundsRev(2), Attempt: 1, Status: model.RunStatusFailed},
	}
	before := append([]model.Run(nil), in...)
	c, err := CountRuns(in)
	if err != nil {
		t.Fatal(err)
	}
	if c.Runs != len(in) {
		t.Errorf("Runs = %d, want %d (every run counts)", c.Runs, len(in))
	}
	if c.CorrectionBatches != 2 {
		t.Errorf("CorrectionBatches = %d, want 2", c.CorrectionBatches)
	}
	if c.SyncBatches != 1 {
		t.Errorf("SyncBatches = %d, want 1", c.SyncBatches)
	}
	if got := c.CIFixAttempts(1); got != 1 {
		t.Errorf("CIFixAttempts(1) = %d, want 1", got)
	}
	if got := c.CIFixAttempts(2); got != 1 {
		t.Errorf("CIFixAttempts(2) = %d, want 1", got)
	}
	if got := c.CIFixAttempts(3); got != 0 {
		t.Errorf("CIFixAttempts(3) = %d, want 0", got)
	}
	if !reflect.DeepEqual(in, before) {
		t.Error("input runs mutated")
	}
}

func TestCountRunsInvalid(t *testing.T) {
	cases := map[string]model.Run{
		"attempt 0":           boundsRun(model.RunPurposeInitial, 0, model.RunStatusSucceeded),
		"unknown purpose":     boundsRun("bogus", 1, model.RunStatusSucceeded),
		"unknown status":      boundsRun(model.RunPurposeSync, 1, "bogus"),
		"ci-fix, no revision": boundsRun(model.RunPurposeCIFix, 1, model.RunStatusSucceeded),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := CountRuns([]model.Run{r}); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestCheckStartBounds covers the count bounds: at the limit is allowed,
// one more is needs-owner.
func TestCheckStartBounds(t *testing.T) {
	l := DefaultLimits()
	s := Status{State: model.TaskStateFixing}
	cases := []struct {
		name  string
		prior []model.Run
		next  model.Run
		owner bool
	}{
		{"runs per task: 20th run", boundsRuns(19, model.RunPurposeReview), boundsRun(model.RunPurposeReview, 1, model.RunStatusRunning), false},
		{"runs per task: 21st run", boundsRuns(20, model.RunPurposeReview), boundsRun(model.RunPurposeReview, 1, model.RunStatusRunning), true},
		{"runs per task: 21st run is a retry", boundsRuns(20, model.RunPurposeReview), boundsRun(model.RunPurposeReview, 2, model.RunStatusRunning), true},
		{"correction batches: 3rd", boundsRuns(2, model.RunPurposeCorrection), boundsRun(model.RunPurposeCorrection, 1, model.RunStatusRunning), false},
		{"correction batches: 4th", boundsRuns(3, model.RunPurposeCorrection), boundsRun(model.RunPurposeCorrection, 1, model.RunStatusRunning), true},
		{"correction batches: retry of 3rd", boundsRuns(3, model.RunPurposeCorrection), boundsRun(model.RunPurposeCorrection, 2, model.RunStatusRunning), false},
		{"sync batches: 2nd", boundsRuns(1, model.RunPurposeSync), boundsRun(model.RunPurposeSync, 1, model.RunStatusRunning), false},
		{"sync batches: 3rd", boundsRuns(2, model.RunPurposeSync), boundsRun(model.RunPurposeSync, 1, model.RunStatusRunning), true},
		{"ci-fix: 2nd on revision 1", boundsRuns(1, model.RunPurposeCIFix), model.Run{Purpose: model.RunPurposeCIFix, Revision: boundsRev(1), Attempt: 1}, false},
		{"ci-fix: 3rd on revision 1", boundsRuns(2, model.RunPurposeCIFix), model.Run{Purpose: model.RunPurposeCIFix, Revision: boundsRev(1), Attempt: 1}, true},
		{"ci-fix: 1st on revision 2", boundsRuns(2, model.RunPurposeCIFix), model.Run{Purpose: model.RunPurposeCIFix, Revision: boundsRev(2), Attempt: 1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := CheckStart(s, c.prior, c.next, l)
			if err != nil {
				t.Fatal(err)
			}
			if isNeedsOwner(o) != c.owner {
				t.Errorf("needs-owner = %v, want %v (%+v)", isNeedsOwner(o), c.owner, o)
			}
			if !c.owner && !reflect.DeepEqual(o, stay(s)) {
				t.Errorf("outcome = %+v, want stay", o)
			}
			if c.owner && o.State != s.State {
				t.Errorf("state = %s, want %s", o.State, s.State)
			}
		})
	}
}

func TestCheckStartInvalid(t *testing.T) {
	l := DefaultLimits()
	s := Status{State: model.TaskStateFixing}
	for name, next := range map[string]model.Run{
		"attempt 0":           {Purpose: model.RunPurposeSync},
		"ci-fix, no revision": {Purpose: model.RunPurposeCIFix, Attempt: 1},
		"unknown purpose":     {Purpose: "bogus", Attempt: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CheckStart(s, nil, next, l); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
	flagged := Status{State: model.TaskStateFixing, Flags: []model.TaskFlag{model.TaskFlagNeedsOwner}}
	if _, err := CheckStart(flagged, nil, boundsRun(model.RunPurposeSync, 1, model.RunStatusRunning), l); !errors.Is(err, ErrFlagged) {
		t.Errorf("flagged err = %v, want ErrFlagged", err)
	}
}

// TestRoleBounds covers the run timeout and invalid-result retry of the
// planner, executor and reviewer rows.
func TestRoleBounds(t *testing.T) {
	l := DefaultLimits()
	s := Status{State: model.TaskStateRunning}
	for _, c := range []struct {
		role  model.Role
		limit time.Duration
	}{
		{model.RolePlanner, 30 * time.Minute},
		{model.RoleExecutor, 3 * time.Hour},
		{model.RoleR1, time.Hour},
		{model.RoleR2, time.Hour},
	} {
		t.Run(string(c.role), func(t *testing.T) {
			r := model.Run{ID: "x", Role: c.role, Purpose: model.RunPurposeInitial, Attempt: 1, Status: model.RunStatusRunning, StartedAt: boundsT0}

			// Invalid result: Attempt 1 retries (at the limit), Attempt 2 is over it.
			o, err := CheckRetry(s, r, l)
			if err != nil {
				t.Fatal(err)
			}
			if isNeedsOwner(o) || o.Action != ActionRetry || o.State != s.State {
				t.Errorf("attempt 1 retry = %+v, want ActionRetry", o)
			}
			r2 := r
			r2.Attempt = 2
			if o, err = CheckRetry(s, r2, l); err != nil || !isNeedsOwner(o) {
				t.Errorf("attempt 2 retry = %+v, %v, want needs-owner", o, err)
			}

			// Timeout: elapsed == limit is within it; limit+1ns exceeds it.
			if o, err = CheckRunTimeout(s, r2, l, boundsT0.Add(c.limit)); err != nil || !reflect.DeepEqual(o, stay(s)) {
				t.Errorf("at limit = %+v, %v, want stay", o, err)
			}
			if o, err = CheckRunTimeout(s, r2, l, boundsT0.Add(c.limit+time.Nanosecond)); err != nil || !isNeedsOwner(o) {
				t.Errorf("over limit, attempt 2 = %+v, %v, want needs-owner", o, err)
			}
			if o, err = CheckRunTimeout(s, r, l, boundsT0.Add(c.limit+time.Nanosecond)); err != nil || isNeedsOwner(o) || o.Action != ActionRetry {
				t.Errorf("over limit, attempt 1 = %+v, %v, want ActionRetry", o, err)
			}
		})
	}
}

func TestCheckRunTimeoutFinishedRun(t *testing.T) {
	s := Status{State: model.TaskStateRunning}
	r := model.Run{Role: model.RoleExecutor, Attempt: 2, Status: model.RunStatusSucceeded, StartedAt: boundsT0}
	o, err := CheckRunTimeout(s, r, DefaultLimits(), boundsT0.Add(24*time.Hour))
	if err != nil || !reflect.DeepEqual(o, stay(s)) {
		t.Errorf("finished run = %+v, %v, want stay", o, err)
	}
	r.Role = "bogus"
	if _, err := CheckRunTimeout(s, r, DefaultLimits(), boundsT0); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown role err = %v, want ErrInvalidInput", err)
	}
}

func TestCheckCIWait(t *testing.T) {
	l := DefaultLimits()
	s := Status{State: model.TaskStateCI}
	since := boundsT0
	c := model.Change{ID: "c", CISince: &since}
	if o, err := CheckCIWait(s, c, l, boundsT0.Add(2*time.Hour)); err != nil || !reflect.DeepEqual(o, stay(s)) {
		t.Errorf("at limit = %+v, %v, want stay", o, err)
	}
	if o, err := CheckCIWait(s, c, l, boundsT0.Add(2*time.Hour+time.Nanosecond)); err != nil || !isNeedsOwner(o) {
		t.Errorf("over limit = %+v, %v, want needs-owner", o, err)
	}
	if _, err := CheckCIWait(s, model.Change{ID: "c"}, l, boundsT0); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("no CISince err = %v, want ErrInvalidInput", err)
	}
	if _, err := CheckCIWait(Status{State: model.TaskStateReview}, c, l, boundsT0); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("review state err = %v, want ErrInvalidTransition", err)
	}
}
