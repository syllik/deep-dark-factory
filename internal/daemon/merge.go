package daemon

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy/gate"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

// Merge gate read names recorded in gate.Input.ReadErrors.
const (
	readChange       = "change"
	readBehindBase   = "behind-base"
	readMergeable    = "mergeable"
	readChangedPaths = "changed-paths"
	readPatchID      = "patch-id"
	readChecks       = "checks"
	readReviews      = "reviews"
	readBaseMerges   = "base-merges"
)

// merge evaluates the merge gate for a task in merge and, when the refusal
// routing allows it, merges the change pinned to the gate's head, records
// the merge and closes the task. A stored merge SHA resumes at the close.
func (e *Engine) merge(ctx context.Context, it tickTask) error {
	t := it.task
	forge := e.deps.Forge
	log := e.deps.Logger.With("key", string(t.Key))
	change, found, err := e.taskChange(ctx, t.Key)
	if err != nil {
		return err
	}
	if !found {
		log.Info("task in merge has no stored change")
		return nil
	}
	w := e.intents.Forge(forge)
	if change.MergeSHA != "" {
		return e.closeMerged(ctx, t, change.MergeSHA, w)
	}

	in, err := e.gateInput(ctx, t, change)
	if err != nil {
		return err
	}
	d := gate.EvaluateMerge(in, e.deps.Verifier)
	status := statusOf(t)
	o, err := lifecycle.RouteRefusals(d, status, nil)
	if err != nil {
		return fmt.Errorf("route merge refusals: %w", err)
	}
	switch o.Action {
	case lifecycle.ActionMerge:
		// Handled below.
	case lifecycle.ActionBackoff:
		log.Info("merge backs off", "reason", o.Reason)
		return nil
	case lifecycle.ActionSync:
		_, err := e.apply(ctx, t, *ownerOutcome(status, "sync with base is not implemented yet (Task 12b): "+o.Reason), nil)
		return err
	case lifecycle.ActionNotify:
		// The hold flag is set; the owner notification is a later task.
		_, err := e.apply(ctx, t, o, nil)
		return err
	case lifecycle.ActionNone:
		if o.Reason != "" {
			log.Info("merge refused", "reason", o.Reason, "next", string(o.State))
		}
		_, err := e.apply(ctx, t, o, nil)
		return err
	case lifecycle.ActionCreateChildren, lifecycle.ActionRerunReviewer, lifecycle.ActionAddReviewer, lifecycle.ActionLeave,
		lifecycle.ActionRetry, lifecycle.ActionSyncBatch, lifecycle.ActionDiscardSyncRun,
		// Pause, revert and CI reruns are Task 12c (#159); they stay
		// fail-closed here until then.
		lifecycle.ActionRerunCI, lifecycle.ActionOpenRevert, lifecycle.ActionPause:
		return fmt.Errorf("route merge refusals: unexpected action %q", o.Action)
	default:
		return fmt.Errorf("route merge refusals: unknown action %q", o.Action)
	}

	sha, err := w.Merge(ctx, t.Key, change.ID, d.Head)
	if errors.Is(err, ports.ErrHeadMoved) {
		log.Warn("change head moved during the merge", "head", d.Head)
		mo, rerr := lifecycle.RouteMerge(status, lifecycle.MergeResultHeadMoved)
		if rerr != nil {
			return rerr
		}
		_, err := e.apply(ctx, t, mo, nil)
		return err
	}
	if err != nil {
		// The next tick evaluates the gate again.
		return err
	}
	if sha == "" {
		log.Info("dry-run merge; task stays in merge", "head", d.Head)
		return nil
	}
	now := e.deps.Clock.Now()
	change.MergeSHA, change.MergedAt, change.Open, change.UpdatedAt = sha, &now, false, now
	if err := e.deps.Store.UpdateChange(ctx, change); err != nil {
		return fmt.Errorf("record merge: %w", err)
	}
	log.Info("change merged", "changeId", change.ID, "head", d.Head, "mergeSha", sha)
	return e.closeMerged(ctx, t, sha, w)
}

// closeMerged moves t, merged as mergeSHA, to done and then closes its
// forge task. Done comes first, so a failed close leaves a done task that
// closeDone closes on a later tick (#154).
func (e *Engine) closeMerged(ctx context.Context, t model.Task, mergeSHA string, w *ForgeWrites) error {
	o, err := lifecycle.RouteMerge(statusOf(t), lifecycle.MergeResultMerged)
	if err != nil {
		return err
	}
	if t, err = e.apply(ctx, t, o, nil); err != nil {
		return err
	}
	return e.closeTask(ctx, t, mergeSHA, w)
}

// closeTask runs the close-task intent of t merged as mergeSHA.
func (e *Engine) closeTask(ctx context.Context, t model.Task, mergeSHA string, w *ForgeWrites) error {
	src := model.TaskSource{Forge: t.Forge, Repository: t.Repository, SourceKind: t.SourceKind, SourceID: t.SourceID}
	return w.CloseTask(ctx, t.Key, src, "Merged by DDF as "+mergeSHA+".")
}

// closeDone runs the close-task intent again, with the same comment, for a
// task in done whose stored change has a merge SHA. Tick steps only tasks
// that Forge.ListCandidateTasks lists, which is the read that proves the
// forge task is still open. Any other done task is left alone.
func (e *Engine) closeDone(ctx context.Context, t model.Task) error {
	change, found, err := e.taskChange(ctx, t.Key)
	if err != nil {
		return err
	}
	if !found || change.MergeSHA == "" {
		return nil
	}
	e.deps.Logger.Info("done task still open on the forge; closing it again", "key", string(t.Key), "mergeSha", change.MergeSHA)
	return e.closeTask(ctx, t, change.MergeSHA, e.intents.Forge(e.deps.Forge))
}

// gateInput builds the merge gate input of t and its stored change from the
// store, the clean clone and forge reads only. Each failed read is recorded
// by name in ReadErrors, which refuses the merge.
func (e *Engine) gateInput(ctx context.Context, t model.Task, change model.Change) (gate.Input, error) {
	forge := e.deps.Forge
	g := e.deps.Git
	clone := e.settings.CloneDir
	if err := g.Fetch(ctx, clone); err != nil {
		return gate.Input{}, err
	}
	base, err := e.integrationTip(ctx)
	if err != nil {
		return gate.Input{}, err
	}
	in := gate.Input{
		OperatorMax: e.settings.OperatorMax,
		Profile:     e.settings.Profile,
		// Pause is Task 12c (#159); until then the merge gate never sees
		// a paused project.
		Paused: false,
		Task: gate.Task{Key: t.Key, Forge: t.Forge, Repository: t.Repository, State: t.State, Flags: t.Flags,
			HitStop: t.HitStop, Risk: t.Risk},
		Approved: nil,
	}
	failed := func(name string, err error) {
		e.deps.Logger.Warn("merge gate read failed", "key", string(t.Key), "read", name, "err", err)
		in.ReadErrors = append(in.ReadErrors, name)
	}

	head, open := change.HeadSHA, change.Open
	switch info, found, err := forge.FindChangeByTaskKey(ctx, t.Key); {
	case err != nil:
		failed(readChange, err)
	case !found:
		failed(readChange, errors.New("change not found on the forge"))
	default:
		head, open = info.HeadSHA, info.Open
	}
	in.Change = gate.Change{ID: change.ID, HeadSHA: head, Open: open, Draft: change.Draft, Mergeable: gate.MergeabilityUnknown}

	if contains, ok := e.gitTest(ctx, failed, readBehindBase, "merge-base", "--is-ancestor", base, head); ok {
		in.Change.Behind = !contains
		if !in.Change.Behind {
			in.Change.Mergeable = gate.MergeabilityMergeable
		} else if clean, ok := e.gitTest(ctx, failed, readMergeable, "merge-tree", "--write-tree", "--no-messages", head, base); ok {
			in.Change.Mergeable = gate.MergeabilityConflicting
			if clean {
				in.Change.Mergeable = gate.MergeabilityMergeable
			}
		}
	}

	if mb, err := e.mergeBase(ctx, base, head); err != nil {
		failed(readChangedPaths, err)
	} else if in.ChangedPaths, err = patch.ChangedPaths(ctx, g, clone, mb, head); err != nil {
		failed(readChangedPaths, err)
	}
	if pid, err := g.PatchID(ctx, clone, base, head); err != nil {
		failed(readPatchID, err)
	} else {
		in.PatchIDs = map[string]string{head: pid}
	}
	if in.Checks, err = forge.ReadChecks(ctx, head); err != nil {
		failed(readChecks, err)
	}
	if reviews, err := e.deps.Store.ListReviews(ctx, t.Key, change.ID); err != nil {
		failed(readReviews, err)
	} else {
		for _, r := range reviews {
			in.Reviews = append(in.Reviews, gate.Review{
				Role: r.Role, Head: r.HeadSHA, Round: r.Round, Verdict: r.Verdict, BlockingCount: r.BlockingCount,
				FindingsDigest: r.FindingsDigest, Version: "ddf.review-signature/v" + strconv.Itoa(r.SigVersion),
				KeyID: r.KeyID, Signature: r.Signature,
			})
		}
	}
	if bms, err := e.deps.Store.ListBaseMerges(ctx, t.Key, change.ID); err != nil {
		failed(readBaseMerges, err)
	} else {
		for _, bm := range bms {
			in.BaseMerges = append(in.BaseMerges, gate.BaseMerge{MergeSHA: bm.MergeSHA, ParentHeadSHA: bm.ParentHeadSHA, BaseSHA: bm.BaseSHA})
		}
	}
	return in, nil
}

// gitTest runs a git command in the clean clone whose exit code is its
// answer: exit 0 is true, exit 1 is false. Any other result records name as
// a failed read and reports ok false.
func (e *Engine) gitTest(ctx context.Context, failed func(string, error), name string, args ...string) (answer, ok bool) {
	_, err := e.deps.Git.Run(ctx, e.settings.CloneDir, nil, args...)
	if err == nil {
		return true, true
	}
	var ce *git.CommandError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return false, true
	}
	failed(name, err)
	return false, false
}
