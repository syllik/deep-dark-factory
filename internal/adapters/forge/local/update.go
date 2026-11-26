package local

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// ErrWorkflowChanged is returned, wrapped, by UpdateBranchFromBase when the
// integration branch changed the project profile since the merge base: the
// profile's verify is forge-local's CI, so the base is not merged in (Task
// 10c agentDefault 2).
var ErrWorkflowChanged = errors.New("base changed the workflow configuration")

// SyncConflictError is returned by UpdateBranchFromBase when merging the
// integration branch into the change head conflicts. Nothing is pushed.
type SyncConflictError struct {
	// Paths are the conflicting paths, sorted and unique.
	Paths []string
}

func (e *SyncConflictError) Error() string {
	return "merging the base conflicts on " + strings.Join(e.Paths, ", ")
}

// UpdateBranchFromBase merges the integration tip into the head of the open
// change changeID (spec §5.2 "Sync with base",
// docs/adapters/forge-local.md#other-writes). The merge commit has the old
// head and the integration tip as parents, in that order, and
// Config.Identity at the Config.Clock time. It is pushed with a
// compare-and-swap lease on the old head; then the record head is updated,
// a check attempt is started for the new head and the new head is
// returned. A head that already contains the base is returned with no
// write. A conflict returns *SyncConflictError and a changed project
// profile wraps ErrWorkflowChanged, both with no write. The check order is
// Task 10c agentDefault 13.
func (f *Forge) UpdateBranchFromBase(ctx context.Context, mode ports.WriteMode, changeID string) (string, error) {
	live, err := f.guard(mode, "UpdateBranchFromBase", "changeId", changeID)
	if err != nil || !live {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !safeID(changeID) {
		return "", fmt.Errorf("update branch from base: change id %q is not path-safe", changeID)
	}
	// f.mu is held from the record read to the attempt start (Task 10c
	// agentDefault 13).
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.loadChange(changeID)
	if err != nil {
		return "", fmt.Errorf("update branch from base: %w", err)
	}
	switch r.State {
	case changeOpen:
	case changeMerged:
		return "", fmt.Errorf("update branch from base: change %s is merged", r.ID)
	case changeClosed:
		return "", fmt.Errorf("update branch from base: change %s is closed", r.ID)
	default:
		return "", fmt.Errorf("update branch from base: change %s has unknown state %q", r.ID, r.State)
	}
	if r.BaseBranch != f.cfg.IntegrationBranch {
		return "", fmt.Errorf("update branch from base: change %s targets %q, not the integration branch %q",
			r.ID, r.BaseBranch, f.cfg.IntegrationBranch)
	}
	if err := f.cfg.Git.Fetch(ctx, f.cfg.CloneDir); err != nil {
		return "", fmt.Errorf("update branch from base: %w", err)
	}
	oldHead := r.HeadSHA
	if err := f.checkChangeTipLocked(ctx, r.Branch, oldHead); err != nil {
		return "", fmt.Errorf("update branch from base: %w", err)
	}
	base, err := f.branchTipLocked(ctx, f.cfg.IntegrationBranch)
	if err != nil {
		return "", fmt.Errorf("update branch from base: %w", err)
	}
	// The project profile is the only workflow path (Task 10c agentDefault
	// 2).
	outcome, err := f.cfg.Git.SyncWithBase(ctx, f.cfg.CloneDir, git.SyncInput{
		Head: oldHead, Base: base, WorkflowPaths: []string{".ddf/project.yaml"}, Identity: f.cfg.Identity,
		When:    f.cfg.Clock.Now(),
		Message: "Merge branch '" + f.cfg.IntegrationBranch + "' into " + r.Branch,
	})
	if err != nil {
		return "", fmt.Errorf("update branch from base: %w", err)
	}
	var merge string
	switch o := outcome.(type) {
	case git.SyncUpToDate:
		return oldHead, nil
	case git.SyncConflict:
		return "", &SyncConflictError{Paths: o.Paths}
	case git.SyncWorkflowChanged:
		return "", fmt.Errorf("update branch from base: %s changed %s: %w",
			f.cfg.IntegrationBranch, strings.Join(o.Paths, ", "), ErrWorkflowChanged)
	case git.SyncMerged:
		merge = o.Merge.MergeSHA
	default:
		return "", fmt.Errorf("update branch from base: unexpected sync outcome %T", outcome)
	}
	if err := f.pushLease(ctx, merge, r.Branch, oldHead); err != nil {
		return "", fmt.Errorf("update branch from base: %w", f.classifySyncPushLocked(ctx, err, r.Branch, oldHead))
	}
	r.HeadSHA = merge
	if err := f.storeChange(r); err != nil {
		return "", fmt.Errorf("update branch from base: %w", err)
	}
	attempts, err := f.listAttempts(ctx, merge)
	if err != nil {
		return "", fmt.Errorf("update branch from base: %w", err)
	}
	if len(attempts) == 0 {
		a, err := f.startAttemptLocked(ctx, merge, r.Marker)
		if err != nil {
			return "", fmt.Errorf("update branch from base: %w", err)
		}
		f.cfg.Logger.Info("forge-local check attempt started",
			"changeId", r.ID, "head", a.HeadSHA, "attempt", a.Attempt, "runId", a.RunID)
	}
	f.cfg.Logger.Info("forge-local change synced with base",
		"changeId", r.ID, "oldHead", oldHead, "baseSha", base, "headSha", merge)
	return merge, nil
}

// classifySyncPushLocked attributes a failed base-sync push (Task 10c
// agentDefault 8). An error that is not a git exit is returned as is. A
// refused push is classified by a fetch and a re-read of the change tip: a
// tip other than the old head wraps ports.ErrHeadMoved, else the push
// error is returned. The caller holds f.mu.
func (f *Forge) classifySyncPushLocked(ctx context.Context, pushErr error, branch, oldHead string) error {
	if !isCommandError(pushErr) {
		return pushErr
	}
	if err := f.cfg.Git.Fetch(ctx, f.cfg.CloneDir); err != nil {
		return errors.Join(pushErr, err)
	}
	if err := f.checkChangeTipLocked(ctx, branch, oldHead); err != nil {
		if errors.Is(err, ports.ErrHeadMoved) {
			return err
		}
		return errors.Join(pushErr, err)
	}
	return pushErr
}
