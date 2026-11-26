package local

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// ErrRevertConflict is returned, wrapped, by Revert when undoing the merge
// on the current integration tip conflicts. Nothing is pushed or recorded.
// The conflicting paths are not named: git.Runner.Run returns no stdout
// with a non-zero exit (Task 10c agentDefault 10).
var ErrRevertConflict = errors.New("revert conflicts with the integration branch")

// Revert opens a change of kind revert that undoes mergeSHA, the merge
// commit of a merged change of key, on the current integration tip (spec
// §5.2, docs/adapters/forge-local.md#revert). The revert commit has the
// integration tip as its only parent and Config.Identity at the
// Config.Clock time; it is pushed create-only to the branch BranchPattern
// names with {type} = revert, {n} = the task number and {slug} =
// revert-<first 12 hex of mergeSHA>. Then the change is recorded open with
// the key's marker and revertOf, a check attempt is started for its head
// and its ChangeInfo is returned. An open revert of mergeSHA is returned
// with no write. The check order is Task 10c agentDefault 11; the revert
// is computed with object writes only (Task 10c agentDefault 10).
func (f *Forge) Revert(ctx context.Context, mode ports.WriteMode, key model.TaskKey, mergeSHA string) (ports.ChangeInfo, error) {
	live, err := f.guard(mode, "Revert", "key", string(key), "mergeSha", mergeSHA)
	if err != nil || !live {
		return ports.ChangeInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.ChangeInfo{}, err
	}
	k, err := model.ParseTaskKey(string(key))
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	if !validSHA(mergeSHA) {
		return ports.ChangeInfo{}, fmt.Errorf("revert: merge sha %q is not a full lowercase sha", mergeSHA)
	}
	// f.mu is held throughout (Task 10c agentDefault 11).
	f.mu.Lock()
	defer f.mu.Unlock()
	marker := markerPrefix + string(k)
	orig, existing, err := f.revertTargetLocked(ctx, marker, mergeSHA)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	if existing != nil {
		return existing.info(), nil
	}
	n, err := f.taskNumberLocked(ctx, k)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	branch := expandBranchPattern(f.cfg.BranchPattern, string(kindRevert), formatUint(n), "revert-"+mergeSHA[:12])
	if err := refFormatOK(ctx, f.cfg.Git, f.cfg.CloneDir, branch); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	if branch == f.cfg.IntegrationBranch {
		return ports.ChangeInfo{}, fmt.Errorf("revert: branch %q is the integration branch", branch)
	}
	if err := f.cfg.Git.Fetch(ctx, f.cfg.CloneDir); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	// An existing branch without a record fails closed (Task 10c
	// agentDefault 12).
	if err := f.requireNoBranchLocked(ctx, branch); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	tip, err := f.branchTipLocked(ctx, f.cfg.IntegrationBranch)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	title := "Revert: " + orig.Title
	body := "Reverts " + mergeSHA + " (change " + orig.ID + ")."
	head, err := f.revertCommitLocked(ctx, mergeSHA, tip, commitMessage(title, body))
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	id, err := f.newChangeIDLocked(ctx)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	if err := f.pushLease(ctx, head, branch, ""); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	rec := changeRecord{
		Schema: changeSchema, ID: id, Kind: kindRevert, Branch: branch, BaseBranch: f.cfg.IntegrationBranch,
		HeadSHA: head, Title: title, Body: body, Marker: marker, State: changeOpen,
		RevertOf: mergeSHA, CreatedAt: f.cfg.Clock.Now(),
	}
	if err := f.storeChange(rec); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	attempts, err := f.listAttempts(ctx, head)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
	}
	if len(attempts) == 0 {
		a, err := f.startAttemptLocked(ctx, head, marker)
		if err != nil {
			return ports.ChangeInfo{}, fmt.Errorf("revert: %w", err)
		}
		f.cfg.Logger.Info("forge-local check attempt started",
			"changeId", rec.ID, "head", a.HeadSHA, "attempt", a.Attempt, "runId", a.RunID)
	}
	f.cfg.Logger.Info("forge-local revert opened", "changeId", rec.ID, "mergeSha", mergeSHA, "headSha", head)
	return rec.info(), nil
}

// revertTargetLocked reads the change records of marker (Task 10c
// agentDefault 11). An open revert of mergeSHA is returned as existing.
// Any other open change of the key, or a merged or closed revert of
// mergeSHA, is an error. Otherwise orig is the merged change whose merge
// SHA is mergeSHA; none is an error. The caller holds f.mu.
func (f *Forge) revertTargetLocked(ctx context.Context, marker, mergeSHA string) (orig changeRecord, existing *changeRecord, err error) {
	all, err := f.listChanges(ctx)
	if err != nil {
		return changeRecord{}, nil, err
	}
	var open, found *changeRecord
	for i := range all {
		r := &all[i]
		if r.Marker != marker {
			continue
		}
		isRevert := r.Kind == kindRevert && r.RevertOf == mergeSHA
		switch r.State {
		case changeOpen:
			if isRevert {
				return changeRecord{}, r, nil
			}
			open = r
		case changeMerged:
			if isRevert {
				return changeRecord{}, nil, fmt.Errorf("revert %s of %s is already merged", r.ID, mergeSHA)
			}
			if r.MergeSHA == mergeSHA {
				found = r
			}
		case changeClosed:
			if isRevert {
				return changeRecord{}, nil, fmt.Errorf("revert %s of %s is closed", r.ID, mergeSHA)
			}
		default:
			return changeRecord{}, nil, fmt.Errorf("change %s has unknown state %q", r.ID, r.State)
		}
	}
	if open != nil {
		return changeRecord{}, nil, fmt.Errorf("change %s of the key is open", open.ID)
	}
	if found == nil {
		return changeRecord{}, nil, fmt.Errorf("%s is not the merge sha of a merged change of the key", mergeSHA)
	}
	return *found, nil, nil
}

// taskNumberLocked returns the number of the task record of key (Task 10c
// agentDefault 6): the record whose marker carries key, for tasks DDF
// created, else the record whose source has key, for inbox tasks. No match
// or more than one is an error. The caller holds f.mu.
func (f *Forge) taskNumberLocked(ctx context.Context, key model.TaskKey) (uint64, error) {
	records, err := f.loadTasks(ctx)
	if err != nil {
		return 0, err
	}
	marker := markerPrefix + string(key)
	var byMarker, bySource []uint64
	for _, r := range records {
		if r.Marker == marker {
			byMarker = append(byMarker, r.Number)
			continue
		}
		k, err := model.NewTaskKey(f.source(r))
		if err != nil {
			return 0, fmt.Errorf("task %d: %w", r.Number, err)
		}
		if k == key {
			bySource = append(bySource, r.Number)
		}
	}
	for _, nums := range [][]uint64{byMarker, bySource} {
		switch len(nums) {
		case 0:
			continue
		case 1:
			return nums[0], nil
		default:
			return 0, fmt.Errorf("tasks %v all carry key %s", nums, key)
		}
	}
	return 0, fmt.Errorf("no task record carries key %s", key)
}

// requireNoBranchLocked refuses a branch that exists in the fetched clean
// clone. The caller holds f.mu.
func (f *Forge) requireNoBranchLocked(ctx context.Context, branch string) error {
	_, err := f.cfg.Git.Run(ctx, f.cfg.CloneDir, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return fmt.Errorf("branch %q already exists on the remote without a revert record", branch)
	}
	if isCommandError(err) {
		return nil
	}
	return fmt.Errorf("read branch %q: %w", branch, err)
}

// revertCommitLocked computes the revert of merge on tip in the clean
// clone with object writes only (Task 10c agentDefault 10). merge must have
// exactly one parent P and be an ancestor of tip. A dangling helper commit
// X with P's tree and merge as its only parent makes merge the only merge
// base of tip and X, so `merge-tree tip X` is git revert's three-way merge
// (base merge, ours tip, theirs P). A conflict wraps ErrRevertConflict and
// a tree equal to tip's is an error. The returned commit has the merged
// tree, tip as its only parent and msg. The caller holds f.mu.
func (f *Forge) revertCommitLocked(ctx context.Context, merge, tip, msg string) (string, error) {
	out, err := f.cfg.Git.Run(ctx, f.cfg.CloneDir, nil, "rev-list", "--parents", "-n", "1", merge)
	if err != nil {
		return "", fmt.Errorf("parents of %s: %w", merge, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || fields[0] != merge || !validSHA(fields[1]) {
		return "", fmt.Errorf("merge %s does not have exactly one parent: %q", merge, out)
	}
	parent := fields[1]
	if _, err := f.cfg.Git.Run(ctx, f.cfg.CloneDir, nil, "merge-base", "--is-ancestor", merge, tip); err != nil {
		var ce *git.CommandError
		if errors.As(err, &ce) && ce.ExitCode == 1 {
			return "", fmt.Errorf("merge %s is not on the integration branch", merge)
		}
		return "", fmt.Errorf("ancestry of %s and %s: %w", merge, tip, err)
	}
	parentTree, err := f.treeOf(ctx, parent)
	if err != nil {
		return "", err
	}
	when := f.cfg.Clock.Now()
	helper, err := f.cfg.Git.CommitTree(ctx, f.cfg.CloneDir, git.CommitInput{
		Tree: parentTree, Parents: []string{merge}, Identity: f.cfg.Identity,
		When: when, Message: "ddf revert base " + merge + "\n",
	})
	if err != nil {
		return "", err
	}
	res, err := f.mergeTree(ctx, tip, helper)
	if err != nil {
		return "", err
	}
	if res.Conflict {
		if len(res.Paths) > 0 {
			return "", fmt.Errorf("reverting %s on %s conflicts on %s: %w", merge, tip, strings.Join(res.Paths, ", "), ErrRevertConflict)
		}
		return "", fmt.Errorf("reverting %s on %s: %w", merge, tip, ErrRevertConflict)
	}
	tipTree, err := f.treeOf(ctx, tip)
	if err != nil {
		return "", err
	}
	if res.Tree == tipTree {
		return "", fmt.Errorf("reverting %s on %s changes nothing", merge, tip)
	}
	return f.cfg.Git.CommitTree(ctx, f.cfg.CloneDir, git.CommitInput{
		Tree: res.Tree, Parents: []string{tip}, Identity: f.cfg.Identity, When: when, Message: msg,
	})
}

// newChangeIDLocked returns a new change id from Config.IDs that is
// path-safe and not used by any change record. The caller holds f.mu.
func (f *Forge) newChangeIDLocked(ctx context.Context) (string, error) {
	id := f.cfg.IDs.NewID()
	if !safeID(id) {
		return "", fmt.Errorf("change id %q is not path-safe", id)
	}
	all, err := f.listChanges(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range all {
		if c.ID == id {
			return "", fmt.Errorf("change id %q is already used", id)
		}
	}
	return id, nil
}
