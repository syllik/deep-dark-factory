package local

import (
	"context"
	"errors"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// ErrBehindBase is returned, wrapped, by Merge when the integration branch
// is not an ancestor of the pinned head, or moved inside the lease window
// (Task 10c agentDefault 1). An error never matches both ErrBehindBase and
// ports.ErrHeadMoved.
var ErrBehindBase = errors.New("change is behind its base")

// Merge squash-merges changeID onto the integration branch only if its
// head equals headSHA (spec §6.6, docs/adapters/forge-local.md#merge). The
// squash commit has the head's tree, the integration tip as its only
// parent, Config.Identity at the Config.Clock time and the stored title and
// body as message. It is pushed with a compare-and-swap lease on the old
// tip. On success the change is recorded merged, a check attempt is started
// for the merge commit and its SHA is returned. A merged change named with
// its head returns the stored merge SHA with no write. The check order is
// Task 10c agentDefault 7.
func (f *Forge) Merge(ctx context.Context, mode ports.WriteMode, changeID, headSHA string) (string, error) {
	live, err := f.guard(mode, "Merge", "changeId", changeID, "headSha", headSHA)
	if err != nil || !live {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !safeID(changeID) {
		return "", fmt.Errorf("merge: change id %q is not path-safe", changeID)
	}
	if !validSHA(headSHA) {
		return "", fmt.Errorf("merge: head sha %q is not a full lowercase sha", headSHA)
	}
	// f.mu is held from the record read to the attempt start (Task 10c
	// agentDefault 7).
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.loadChange(changeID)
	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	switch r.State {
	case changeOpen:
	case changeMerged:
		if r.HeadSHA == headSHA {
			return r.MergeSHA, nil
		}
		return "", fmt.Errorf("merge: change %s is merged with head %s, not %s", r.ID, r.HeadSHA, headSHA)
	case changeClosed:
		return "", fmt.Errorf("merge: change %s is closed", r.ID)
	default:
		return "", fmt.Errorf("merge: change %s has unknown state %q", r.ID, r.State)
	}
	if r.BaseBranch != f.cfg.IntegrationBranch {
		return "", fmt.Errorf("merge: change %s targets %q, not the integration branch %q", r.ID, r.BaseBranch, f.cfg.IntegrationBranch)
	}
	if r.HeadSHA != headSHA {
		return "", fmt.Errorf("merge: change %s head is %s, not %s: %w", r.ID, r.HeadSHA, headSHA, ports.ErrHeadMoved)
	}
	if err := f.cfg.Git.Fetch(ctx, f.cfg.CloneDir); err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	if err := f.checkChangeTipLocked(ctx, r.Branch, headSHA); err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	base, err := f.branchTipLocked(ctx, f.cfg.IntegrationBranch)
	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	if base == headSHA {
		return "", fmt.Errorf("merge: head %s is the integration tip, nothing to merge", headSHA)
	}
	if err := f.checkMergeableLocked(ctx, base, headSHA); err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	tree, err := f.treeOf(ctx, headSHA)
	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	merge, err := f.cfg.Git.CommitTree(ctx, f.cfg.CloneDir, git.CommitInput{
		Tree: tree, Parents: []string{base}, Identity: f.cfg.Identity,
		When: f.cfg.Clock.Now(), Message: commitMessage(r.Title, r.Body),
	})
	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	if f.cfg.beforeMergePush != nil {
		f.cfg.beforeMergePush()
	}
	// Only the change tip is re-checked: a moved integration branch is
	// refused by the lease itself (Task 10c agentDefault 9).
	if err := f.cfg.Git.Fetch(ctx, f.cfg.CloneDir); err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	if err := f.checkChangeTipLocked(ctx, r.Branch, headSHA); err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	if err := f.pushLease(ctx, merge, f.cfg.IntegrationBranch, base); err != nil {
		return "", fmt.Errorf("merge: %w", f.classifyMergePushLocked(ctx, err, r.Branch, headSHA, base))
	}
	r.State, r.MergeSHA = changeMerged, merge
	if err := f.storeChange(r); err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	attempts, err := f.listAttempts(ctx, merge)
	if err != nil {
		return "", fmt.Errorf("merge: %w", err)
	}
	if len(attempts) == 0 {
		a, err := f.startAttemptLocked(ctx, merge, r.Marker)
		if err != nil {
			return "", fmt.Errorf("merge: %w", err)
		}
		f.cfg.Logger.Info("forge-local check attempt started",
			"changeId", r.ID, "head", a.HeadSHA, "attempt", a.Attempt, "runId", a.RunID)
	}
	f.cfg.Logger.Info("forge-local change merged", "changeId", r.ID, "headSha", headSHA, "mergeSha", merge)
	return merge, nil
}

// checkChangeTipLocked requires the fetched tip of branch to be head: a
// missing branch is a plain error, any other tip wraps ports.ErrHeadMoved.
// The caller holds f.mu.
func (f *Forge) checkChangeTipLocked(ctx context.Context, branch, head string) error {
	tip, err := f.branchTipLocked(ctx, branch)
	if err != nil {
		return err
	}
	if tip != head {
		return fmt.Errorf("branch %q tip is %s, not head %s: %w", branch, tip, head, ports.ErrHeadMoved)
	}
	return nil
}

// checkMergeableLocked requires base to be an ancestor of head. When it is
// not, a base with head's tree is a plain error, since it looks like a
// merge pushed without its record (Task 10c agentDefault 12); otherwise
// the error wraps ErrBehindBase. The caller holds f.mu.
func (f *Forge) checkMergeableLocked(ctx context.Context, base, head string) error {
	_, err := f.cfg.Git.Run(ctx, f.cfg.CloneDir, nil, "merge-base", "--is-ancestor", base, head)
	if err == nil {
		return nil
	}
	var ce *git.CommandError
	if !errors.As(err, &ce) || ce.ExitCode != 1 {
		return fmt.Errorf("ancestry of %s and %s: %w", base, head, err)
	}
	baseTree, err := f.treeOf(ctx, base)
	if err != nil {
		return err
	}
	headTree, err := f.treeOf(ctx, head)
	if err != nil {
		return err
	}
	if baseTree == headTree {
		return fmt.Errorf("integration tip %s already has the tree of head %s but no merge record: refusing to adopt it", base, head)
	}
	return fmt.Errorf("integration tip %s is not an ancestor of head %s: %w", base, head, ErrBehindBase)
}

// classifyMergePushLocked attributes a failed merge push (Task 10c
// agentDefault 8). An error that is not a git exit is returned as is. A
// refused push is classified by a fetch and a re-read of the tips: a moved
// integration branch wraps ErrBehindBase, else a moved change head wraps
// ports.ErrHeadMoved, else the push error is returned. The caller holds
// f.mu.
func (f *Forge) classifyMergePushLocked(ctx context.Context, pushErr error, branch, head, base string) error {
	if !isCommandError(pushErr) {
		return pushErr
	}
	if err := f.cfg.Git.Fetch(ctx, f.cfg.CloneDir); err != nil {
		return errors.Join(pushErr, err)
	}
	tip, err := f.branchTipLocked(ctx, f.cfg.IntegrationBranch)
	if err != nil {
		return errors.Join(pushErr, err)
	}
	if tip != base {
		return fmt.Errorf("integration branch moved from %s to %s: %w", base, tip, ErrBehindBase)
	}
	if err := f.checkChangeTipLocked(ctx, branch, head); err != nil {
		if errors.Is(err, ports.ErrHeadMoved) {
			return err
		}
		return errors.Join(pushErr, err)
	}
	return pushErr
}
