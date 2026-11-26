package lifecycle

import (
	"fmt"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// Bound actions.
const (
	ActionRetry     Action = "retry"      // rerun the run as Attempt 2 (invalid-result retry)
	ActionSyncBatch Action = "sync-batch" // start an Executor sync batch
)

// Limits holds every bound of lifecycle.md#bounds (spec §5.3). The daemon
// overrides MaxRunsPerTask from the project config.
type Limits struct {
	PlannerTimeout       time.Duration
	ExecutorTimeout      time.Duration
	ReviewerTimeout      time.Duration
	InvalidResultRetries int // retries after an invalid or timed-out result, per run
	CIWait               time.Duration
	CIFixPerRevision     int
	CorrectionBatches    int // per change
	SyncBatches          int // per change
	MaxRunsPerTask       int // all roles
}

// DefaultLimits returns the default bounds of spec §5.3.
func DefaultLimits() Limits {
	return Limits{
		PlannerTimeout:       30 * time.Minute,
		ExecutorTimeout:      3 * time.Hour,
		ReviewerTimeout:      time.Hour,
		InvalidResultRetries: 1,
		CIWait:               2 * time.Hour,
		CIFixPerRevision:     2,
		CorrectionBatches:    3,
		SyncBatches:          2,
		MaxRunsPerTask:       20,
	}
}

// Counters are the §5.3 counters of one task, derived from its runs.
type Counters struct {
	Runs              int // every run, any role, status or attempt
	CorrectionBatches int
	SyncBatches       int
	ciFix             map[int]int // CI-fix attempts by Run.Revision
}

// CIFixAttempts returns the CI-fix attempts counted on revision.
func (c Counters) CIFixAttempts(revision int) int { return c.ciFix[revision] }

// CountRuns derives the counters from the runs of one task. Every run counts
// toward Runs. A correction batch, sync batch or CI-fix attempt is a run of
// that purpose with Attempt 1 whose status is not discarded: an Attempt 2
// retry and the rerun of a discarded run belong to the same batch. V1 has one
// change of kind change per task, so per-change counters span the task's runs.
func CountRuns(runs []model.Run) (Counters, error) {
	c := Counters{ciFix: map[int]int{}}
	for _, r := range runs {
		if r.Attempt < 1 {
			return Counters{}, fmt.Errorf("%w: run %q attempt %d", ErrInvalidInput, r.ID, r.Attempt)
		}
		c.Runs++
		counted, err := countsAsBatch(r)
		if err != nil {
			return Counters{}, err
		}
		if !counted {
			continue
		}
		switch r.Purpose {
		case model.RunPurposeCorrection:
			c.CorrectionBatches++
		case model.RunPurposeSync:
			c.SyncBatches++
		case model.RunPurposeCIFix:
			if r.Revision == nil {
				return Counters{}, fmt.Errorf("%w: ci-fix run %q has no revision", ErrInvalidInput, r.ID)
			}
			c.ciFix[*r.Revision]++
		case model.RunPurposePlanning, model.RunPurposeInitial, model.RunPurposeReview:
		}
	}
	return c, nil
}

// countsAsBatch reports whether r opens a batch: a first attempt that was not
// discarded.
func countsAsBatch(r model.Run) (bool, error) {
	switch r.Purpose {
	case model.RunPurposeCorrection, model.RunPurposeSync, model.RunPurposeCIFix:
	case model.RunPurposePlanning, model.RunPurposeInitial, model.RunPurposeReview:
		return false, nil
	default:
		return false, fmt.Errorf("%w: run %q purpose %q", ErrInvalidInput, r.ID, r.Purpose)
	}
	switch r.Status {
	case model.RunStatusDiscarded:
		return false, nil
	case model.RunStatusRunning, model.RunStatusSucceeded, model.RunStatusFailed:
	default:
		return false, fmt.Errorf("%w: run %q status %q", ErrInvalidInput, r.ID, r.Status)
	}
	return r.Attempt == 1, nil
}

func boundOutcome(s Status, bound string, limit any) Outcome {
	return ownerOutcome(s, fmt.Sprintf("bound exceeded: %s (%v)", bound, limit))
}

// CheckStart checks the bounds before starting next, a run not yet in runs
// (the caller fills Purpose, Attempt and, for a CI-fix, Revision). It returns
// needs-owner naming the bound when the run would exceed MaxRunsPerTask or,
// for Attempt 1 of a correction, sync or CI-fix run, the batch bound of its
// purpose; otherwise stay.
func CheckStart(s Status, runs []model.Run, next model.Run, l Limits) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	c, err := CountRuns(runs)
	if err != nil {
		return Outcome{}, err
	}
	if next.Attempt < 1 {
		return Outcome{}, fmt.Errorf("%w: next run attempt %d", ErrInvalidInput, next.Attempt)
	}
	if c.Runs+1 > l.MaxRunsPerTask {
		return boundOutcome(s, "agent runs per task", l.MaxRunsPerTask), nil
	}
	if next.Attempt != 1 {
		return stay(s), nil
	}
	switch next.Purpose {
	case model.RunPurposeCorrection:
		if c.CorrectionBatches+1 > l.CorrectionBatches {
			return boundOutcome(s, "correction batches per change", l.CorrectionBatches), nil
		}
	case model.RunPurposeSync:
		if c.SyncBatches+1 > l.SyncBatches {
			return boundOutcome(s, "sync batches per change", l.SyncBatches), nil
		}
	case model.RunPurposeCIFix:
		if next.Revision == nil {
			return Outcome{}, fmt.Errorf("%w: next ci-fix run has no revision", ErrInvalidInput)
		}
		if c.CIFixAttempts(*next.Revision)+1 > l.CIFixPerRevision {
			return boundOutcome(s, "ci-fix attempts per revision", l.CIFixPerRevision), nil
		}
	case model.RunPurposePlanning, model.RunPurposeInitial, model.RunPurposeReview:
	default:
		return Outcome{}, fmt.Errorf("%w: next run purpose %q", ErrInvalidInput, next.Purpose)
	}
	return stay(s), nil
}

// CheckRetry routes an invalid or timed-out result of failed: while the
// retries used (Attempt - 1) are below InvalidResultRetries it is ActionRetry,
// otherwise needs-owner. Starting the retry still goes through CheckStart.
func CheckRetry(s Status, failed model.Run, l Limits) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	if failed.Attempt < 1 {
		return Outcome{}, fmt.Errorf("%w: run %q attempt %d", ErrInvalidInput, failed.ID, failed.Attempt)
	}
	if _, err := timeout(failed.Role, l); err != nil {
		return Outcome{}, err
	}
	if failed.Attempt-1 < l.InvalidResultRetries {
		return Outcome{State: s.State, Action: ActionRetry, Reason: fmt.Sprintf("%s run %q: invalid or timed-out result", failed.Role, failed.ID)}, nil
	}
	return boundOutcome(s, string(failed.Role)+" invalid-result retries", l.InvalidResultRetries), nil
}

// timeout returns the run timeout of role.
func timeout(role model.Role, l Limits) (time.Duration, error) {
	switch role {
	case model.RolePlanner:
		return l.PlannerTimeout, nil
	case model.RoleExecutor:
		return l.ExecutorTimeout, nil
	case model.RoleR1, model.RoleR2:
		return l.ReviewerTimeout, nil
	}
	return 0, fmt.Errorf("%w: role %q", ErrInvalidInput, role)
}

// CheckRunTimeout checks the run timeout of a running run from its StartedAt
// and now. Elapsed equal to the timeout is within it; a timed-out run is
// routed like an invalid result (CheckRetry). A run that is not running stays.
func CheckRunTimeout(s Status, r model.Run, l Limits, now time.Time) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	limit, err := timeout(r.Role, l)
	if err != nil {
		return Outcome{}, err
	}
	switch r.Status {
	case model.RunStatusRunning:
	case model.RunStatusSucceeded, model.RunStatusFailed, model.RunStatusDiscarded:
		return stay(s), nil
	default:
		return Outcome{}, fmt.Errorf("%w: run %q status %q", ErrInvalidInput, r.ID, r.Status)
	}
	if now.Sub(r.StartedAt) <= limit {
		return stay(s), nil
	}
	return CheckRetry(s, r, l)
}

// CheckCIWait checks the CI wait for the current head of c, a change of a
// task in ci, from c.CISince and now. Elapsed equal to CIWait is within it.
func CheckCIWait(s Status, c model.Change, l Limits, now time.Time) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	if s.State != model.TaskStateCI {
		return Outcome{}, fmt.Errorf("%w: ci wait in state %s", ErrInvalidTransition, s.State)
	}
	if c.CISince == nil {
		return Outcome{}, fmt.Errorf("%w: change %q has no ci wait start", ErrInvalidInput, c.ID)
	}
	if now.Sub(*c.CISince) <= l.CIWait {
		return stay(s), nil
	}
	return boundOutcome(s, "ci wait for one head", l.CIWait), nil
}
