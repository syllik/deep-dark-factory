package local

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// resourceContainer is the sandbox resource kind Open recovery kills;
// volumes are left to DestroyByTaskKey (Task 10b agentDefault 2).
const resourceContainer = "container"

// pendingAttempt is a running check attempt this instance started and has
// not finished yet (Task 10b agentDefault 1). The list is guarded by f.mu.
type pendingAttempt struct {
	head    string
	attempt uint64
	runID   string
}

// validAttemptName reports whether s is a canonical decimal >= 1.
func validAttemptName(s string) bool { return decimalPattern.MatchString(s) }

// loadAttempt reads checks/<head>/<n>.json; a missing attempt gives an
// error wrapping os.ErrNotExist.
func (f *Forge) loadAttempt(head string, n uint64) (attemptRecord, error) {
	if !validSHA(head) {
		return attemptRecord{}, fmt.Errorf("head sha %q is not a full lowercase sha", head)
	}
	b, err := readRegular(f.path(checksDir, head, formatUint(n)+".json"), maxRecordBytes)
	if err != nil {
		return attemptRecord{}, fmt.Errorf("read check attempt %s/%d: %w", head, n, err)
	}
	return decodeAttempt(b, head, n)
}

// storeAttempt validates r and writes checks/<head>/<attempt>.json
// atomically, creating checks/<head>/ (0700) when missing and replacing an
// existing attempt file.
func (f *Forge) storeAttempt(r attemptRecord) error {
	r.Schema = checkSchema
	r.Name = checkName
	if err := r.validate(r.HeadSHA, r.Attempt); err != nil {
		return fmt.Errorf("store check attempt: %w", err)
	}
	b, err := encodeRecord(r)
	if err != nil {
		return fmt.Errorf("encode check attempt: %w", err)
	}
	dir := f.path(checksDir, r.HeadSHA)
	if err := mkdirNew(dir); err != nil {
		return fmt.Errorf("store check attempt: %w", err)
	}
	if err := writeAtomic(dir, formatUint(r.Attempt)+".json", b); err != nil {
		return fmt.Errorf("write check attempt %s/%d: %w", r.HeadSHA, r.Attempt, err)
	}
	return nil
}

// listAttempts decodes the attempts of head ordered by attempt number;
// none gives an empty, non-nil list.
func (f *Forge) listAttempts(ctx context.Context, head string) ([]attemptRecord, error) {
	if !validSHA(head) {
		return nil, fmt.Errorf("head sha %q is not a full lowercase sha", head)
	}
	names, err := jsonNames(f.path(checksDir, head), validAttemptName)
	if errors.Is(err, os.ErrNotExist) {
		return []attemptRecord{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list check attempts: %w", err)
	}
	out := make([]attemptRecord, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("list check attempts: %w", err)
		}
		r, err := f.loadAttempt(head, n)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b attemptRecord) int { return cmp.Compare(a.Attempt, b.Attempt) })
	return out, nil
}

// latestAttempt returns the highest attempt of head; ok is false when the
// head has none.
func (f *Forge) latestAttempt(ctx context.Context, head string) (r attemptRecord, ok bool, err error) {
	all, err := f.listAttempts(ctx, head)
	if err != nil || len(all) == 0 {
		return attemptRecord{}, false, err
	}
	return all[len(all)-1], true, nil
}

// attemptHeads returns the sorted head SHAs that have a checks/ directory.
func (f *Forge) attemptHeads() ([]string, error) {
	heads, err := subdirNames(f.path(checksDir), validSHA)
	if err != nil {
		return nil, fmt.Errorf("list check heads: %w", err)
	}
	return heads, nil
}

// startAttemptLocked writes a new running attempt of head (the highest
// existing attempt + 1) with a new run id, Config.Clock start time and
// marker, and appends it to the pending list (Task 10b agentDefault 1). The
// caller holds f.mu and has validated head and marker.
func (f *Forge) startAttemptLocked(ctx context.Context, head, marker string) (attemptRecord, error) {
	all, err := f.listAttempts(ctx, head)
	if err != nil {
		return attemptRecord{}, err
	}
	n := uint64(1)
	if len(all) > 0 {
		n = all[len(all)-1].Attempt + 1
	}
	runID := f.cfg.IDs.NewID()
	if !safeID(runID) {
		return attemptRecord{}, fmt.Errorf("start check attempt: run id %q is not path-safe", runID)
	}
	r := attemptRecord{
		Schema: checkSchema, Name: checkName, HeadSHA: head, Attempt: n,
		Status: statusRunning, RunID: runID, Marker: marker, StartedAt: f.cfg.Clock.Now(),
	}
	if err := r.validate(head, n); err != nil {
		return attemptRecord{}, fmt.Errorf("start check attempt: %w", err)
	}
	b, err := encodeRecord(r)
	if err != nil {
		return attemptRecord{}, fmt.Errorf("encode check attempt: %w", err)
	}
	dir := f.path(checksDir, head)
	if err := mkdirNew(dir); err != nil {
		return attemptRecord{}, fmt.Errorf("start check attempt: %w", err)
	}
	if err := writeNew(dir, formatUint(n)+".json", b); err != nil {
		return attemptRecord{}, fmt.Errorf("write check attempt %s/%d: %w", head, n, err)
	}
	f.pending = append(f.pending, pendingAttempt{head: head, attempt: n, runID: runID})
	return r, nil
}

// recoverAttempts runs on Open once storage and clone are ready. Runs are
// in-process, so every running attempt on disk was left by a previous
// instance (Task 10b agentDefault 2). For each one it kills the run's
// containers, removes RunDir/<run id> and marks the attempt lost. Every
// head whose latest attempt is then lost gets a new running, pending
// attempt with the same marker, which also covers a crash between the two
// writes. Any error fails Open. It takes runMu, then mu, in RunChecks'
// order (Task 10b agentDefault 9).
func (f *Forge) recoverAttempts(ctx context.Context) error {
	f.runMu.Lock()
	defer f.runMu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	heads, err := f.attemptHeads()
	if err != nil {
		return err
	}
	var restart []attemptRecord
	for _, head := range heads {
		all, err := f.listAttempts(ctx, head)
		if err != nil {
			return err
		}
		for i, a := range all {
			if a.Status != statusRunning {
				continue
			}
			if err := f.reclaimRun(ctx, a); err != nil {
				return err
			}
			a.Status = statusLost
			a.FinishedAt = f.cfg.Clock.Now()
			if err := f.storeAttempt(a); err != nil {
				return err
			}
			all[i] = a
		}
		if len(all) > 0 && all[len(all)-1].Status == statusLost {
			restart = append(restart, all[len(all)-1])
		}
	}
	// Restart in the order the lost attempts were started.
	slices.SortStableFunc(restart, func(a, b attemptRecord) int { return a.StartedAt.Compare(b.StartedAt) })
	for _, a := range restart {
		r, err := f.startAttemptLocked(ctx, a.HeadSHA, a.Marker)
		if err != nil {
			return err
		}
		f.cfg.Logger.Info("forge-local check attempt lost and restarted",
			"head", a.HeadSHA, "lostRunId", a.RunID, "attempt", r.Attempt, "runId", r.RunID)
	}
	return nil
}

// reclaimRun kills every container of the attempt's run and removes its
// run copy.
func (f *Forge) reclaimRun(ctx context.Context, a attemptRecord) error {
	key, err := parseMarker(a.Marker)
	if err != nil {
		return err
	}
	res, err := f.cfg.Sandbox.ListResources(ctx, ports.ResourceFilter{Key: key, RunID: a.RunID})
	if err != nil {
		return fmt.Errorf("list resources of run %s: %w", a.RunID, err)
	}
	for _, r := range res {
		if r.Kind != resourceContainer {
			continue
		}
		if err := f.cfg.Sandbox.Kill(ctx, r.ID); err != nil {
			return fmt.Errorf("kill %s of run %s: %w", r.ID, a.RunID, err)
		}
	}
	return f.removeRunCopy(a.RunID)
}

// removeRunCopy removes RunDir/<runID> if present; runID must be path-safe.
func (f *Forge) removeRunCopy(runID string) error {
	if !safeID(runID) {
		return fmt.Errorf("run id %q is not path-safe", runID)
	}
	if err := os.RemoveAll(runCopyPath(f.cfg.RunDir, runID)); err != nil {
		return fmt.Errorf("remove run copy of %s: %w", runID, err)
	}
	return nil
}

// runCopyPath is the per-run copy directory of a check attempt.
func runCopyPath(runDir, runID string) string { return filepath.Join(runDir, runID) }
