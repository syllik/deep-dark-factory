package local

import (
	"context"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
)

// RunChecks runs this instance's pending check attempts synchronously, one
// by one in start order, through publisher.LocalGate on the head's tree in
// the clean clone (docs/adapters/forge-local.md#checks, Task 10b
// agentDefault 1). Each attempt is recorded passed or failed with a
// Config.Clock finish time and leaves the pending list. A LocalGate error
// leaves the attempt running and pending, stops the loop and is returned.
// RunChecks takes runMu first and holds f.mu only to pick an attempt and to
// write its result, never during a gate run (Task 10b agentDefault 9). It
// starts no goroutine and stores no verify output.
func (f *Forge) RunChecks(ctx context.Context) error {
	f.runMu.Lock()
	defer f.runMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p, key, ok, err := f.nextPending(ctx)
		if err != nil {
			return fmt.Errorf("run checks: %w", err)
		}
		if !ok {
			return nil
		}
		// Only LocalGate creates RunDir/<run id>; a leftover one is from a
		// gate run of this attempt that could not clean up.
		if err := f.removeRunCopy(p.runID); err != nil {
			return fmt.Errorf("run checks: %w", err)
		}
		res, err := publisher.LocalGate(ctx, f.cfg.Git, f.cfg.Sandbox, f.cfg.CloneDir, publisher.GateInput{
			Key: key, RunID: p.runID, Commit: p.head, CopyDir: runCopyPath(f.cfg.RunDir, p.runID),
			Verify: f.cfg.Verify, Limits: f.cfg.Limits, Egress: f.cfg.Egress,
		})
		if err != nil {
			return fmt.Errorf("run checks: attempt %s/%d: %w", p.head, p.attempt, err)
		}
		if err := f.finishPending(ctx, p, res); err != nil {
			return fmt.Errorf("run checks: %w", err)
		}
	}
}

// nextPending returns the first pending attempt and its task key, read from
// the attempt's marker (Task 10b agentDefault 4); ok is false when none is
// pending. It holds f.mu.
func (f *Forge) nextPending(ctx context.Context) (p pendingAttempt, key model.TaskKey, ok bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pending) == 0 {
		return pendingAttempt{}, "", false, nil
	}
	p = f.pending[0]
	a, err := f.pendingRecordLocked(ctx, p)
	if err != nil {
		return pendingAttempt{}, "", false, err
	}
	key, err = parseMarker(a.Marker)
	if err != nil {
		return pendingAttempt{}, "", false, err
	}
	return p, key, true, nil
}

// finishPending re-reads the attempt of p, records the gate result on it
// and removes p from the pending list. It holds f.mu.
func (f *Forge) finishPending(ctx context.Context, p pendingAttempt, res publisher.GateResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, err := f.pendingRecordLocked(ctx, p)
	if err != nil {
		return err
	}
	a.Status = statusFailed
	if res.Passed {
		a.Status = statusPassed
	}
	a.FinishedAt = f.cfg.Clock.Now()
	if err := f.storeAttempt(a); err != nil {
		return err
	}
	f.dropPendingLocked(p)
	f.cfg.Logger.Info("forge-local check attempt finished",
		"runId", a.RunID, "head", a.HeadSHA, "status", string(a.Status),
		"failedCommand", res.Failed, "exitCode", res.Outcome.ExitCode, "timedOut", res.Outcome.TimedOut)
	return nil
}

// pendingRecordLocked loads the attempt of p, which must still be running
// with p's run id. An attempt changed by someone else leaves the pending
// list and is an error (fail closed). The caller holds f.mu.
func (f *Forge) pendingRecordLocked(ctx context.Context, p pendingAttempt) (attemptRecord, error) {
	if err := ctx.Err(); err != nil {
		return attemptRecord{}, err
	}
	a, err := f.loadAttempt(p.head, p.attempt)
	if err != nil {
		return attemptRecord{}, err
	}
	if a.Status != statusRunning || a.RunID != p.runID {
		f.dropPendingLocked(p)
		return attemptRecord{}, fmt.Errorf("check attempt %s/%d is %s with run id %s, not running with run id %s",
			p.head, p.attempt, a.Status, a.RunID, p.runID)
	}
	return a, nil
}

// dropPendingLocked removes p from the pending list. The caller holds f.mu.
func (f *Forge) dropPendingLocked(p pendingAttempt) {
	for i, q := range f.pending {
		if q == p {
			f.pending = append(f.pending[:i], f.pending[i+1:]...)
			return
		}
	}
}

// ReadChecks returns the verify check of headSHA from its latest attempt
// only (docs/adapters/forge-local.md#checks): none gives an empty, non-nil
// list; running and lost are not completed.
func (f *Forge) ReadChecks(ctx context.Context, headSHA string) ([]ports.Check, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validSHA(headSHA) {
		return nil, fmt.Errorf("read checks: head sha %q is not a full lowercase sha", headSHA)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok, err := f.latestAttempt(ctx, headSHA)
	if err != nil {
		return nil, fmt.Errorf("read checks: %w", err)
	}
	if !ok {
		return []ports.Check{}, nil
	}
	c := ports.Check{Name: checkName, HeadSHA: headSHA}
	switch a.Status {
	case statusPassed:
		c.Completed, c.Passed = true, true
	case statusFailed:
		c.Completed = true
	case statusRunning, statusLost:
	}
	return []ports.Check{c}, nil
}

// RerunChecks starts a new check attempt of headSHA and keeps the older
// ones (docs/adapters/forge-local.md#checks). headSHA must be the headSha
// or mergeSha of a change record, whose marker the attempt carries (Task
// 10b agentDefault 4); records with different keys for one SHA are an
// error.
func (f *Forge) RerunChecks(ctx context.Context, mode ports.WriteMode, headSHA string) error {
	live, err := f.guard(mode, "RerunChecks", "headSha", headSHA)
	if err != nil || !live {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validSHA(headSHA) {
		return fmt.Errorf("rerun checks: head sha %q is not a full lowercase sha", headSHA)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	all, err := f.listChanges(ctx)
	if err != nil {
		return fmt.Errorf("rerun checks: %w", err)
	}
	marker := ""
	for _, c := range all {
		if c.HeadSHA != headSHA && c.MergeSHA != headSHA {
			continue
		}
		if marker != "" && c.Marker != marker {
			return fmt.Errorf("rerun checks: changes of different task keys name sha %s", headSHA)
		}
		marker = c.Marker
	}
	if marker == "" {
		return fmt.Errorf("rerun checks: sha %s is not the head or merge commit of a change", headSHA)
	}
	a, err := f.startAttemptLocked(ctx, headSHA, marker)
	if err != nil {
		return fmt.Errorf("rerun checks: %w", err)
	}
	f.cfg.Logger.Info("forge-local check attempt started",
		"head", a.HeadSHA, "attempt", a.Attempt, "runId", a.RunID)
	return nil
}
