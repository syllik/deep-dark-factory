package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// Event types recovery journals.
const (
	eventTeardown           = "teardown"
	eventTeardownUnverified = "teardown-unverified"
)

// errorClassNotApplied is the error class of an intent recovery proved was
// never applied on the forge or the remote.
const errorClassNotApplied errorClass = "not-applied"

// resourceKindContainer is the sandbox resource kind of a run cell's
// container; only containers are killed.
const resourceKindContainer = "container"

// Recover runs engine recovery on start (spec §9, plan OD-9): it takes the
// instance lock, tears down or stops the sandbox resources left behind,
// discards orphaned runs and reconciles the pending intents. It returns the
// lock's release function. A nil Lock or Sandbox is an error. Any store,
// sandbox or forge read error ends Recover with an error and releases the
// lock (fail closed). Recovery never repeats a write: it only reads the
// forge and the clean clone, and resolves or holds each pending intent.
func (e *Engine) Recover(ctx context.Context) (release func() error, err error) {
	if e.deps.Lock == nil {
		return nil, errors.New("recover: nil instance lock")
	}
	if e.deps.Sandbox == nil {
		return nil, errors.New("recover: nil sandbox")
	}
	rel, err := e.deps.Lock.TryAcquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover: acquire instance lock: %w", err)
	}
	if rel == nil {
		return nil, errors.New("recover: instance lock returned a nil release")
	}
	if err := e.recover(ctx); err != nil {
		err = fmt.Errorf("recover: %w", err)
		if rerr := rel(); rerr != nil {
			err = errors.Join(err, fmt.Errorf("recover: release instance lock: %w", rerr))
		}
		return nil, err
	}
	e.recovered.Store(true)
	return rel, nil
}

// recover runs the recovery steps in order.
func (e *Engine) recover(ctx context.Context) error {
	if err := e.recoverResources(ctx); err != nil {
		return err
	}
	pending, err := e.deps.Store.PendingIntents(ctx)
	if err != nil {
		return err
	}
	cands, err := e.deps.Forge.ListCandidateTasks(ctx)
	if err != nil {
		return fmt.Errorf("list candidate tasks: %w", err)
	}
	if err := e.discardOrphanRuns(ctx, pending, cands); err != nil {
		return err
	}
	return e.reconcileIntents(ctx, pending, cands)
}

// recoverResources lists every sandbox resource, groups it by task key and
// tears down the resources of unknown and done tasks; for any other task it
// kills the containers and discards their still running runs.
func (e *Engine) recoverResources(ctx context.Context) error {
	res, err := e.deps.Sandbox.ListResources(ctx, ports.ResourceFilter{})
	if err != nil {
		return fmt.Errorf("list sandbox resources: %w", err)
	}
	byKey := map[model.TaskKey][]ports.Resource{}
	for _, r := range res {
		if r.Key == "" {
			return fmt.Errorf("sandbox resource %s has no task key label", r.ID)
		}
		byKey[r.Key] = append(byKey[r.Key], r)
	}
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		t, err := e.deps.Store.GetTask(ctx, key)
		known := true
		switch {
		case errors.Is(err, store.ErrNotFound):
			known = false
		case err != nil:
			return err
		}
		if !known || t.State == model.TaskStateDone {
			if err := e.teardown(ctx, key, t, known); err != nil {
				return err
			}
			continue
		}
		if err := e.stopRuns(ctx, t, byKey[key]); err != nil {
			return err
		}
	}
	return nil
}

// teardown destroys every resource of key. A verified teardown of a known
// task records TeardownAt; an unverified one is logged and, for a known
// task, journaled as a teardown-unverified event, and recovery goes on.
func (e *Engine) teardown(ctx context.Context, key model.TaskKey, t model.Task, known bool) error {
	log := e.deps.Logger.With("key", string(key), "known", known)
	if err := e.deps.Sandbox.DestroyByTaskKey(ctx, key); err != nil {
		log.Warn("teardown unverified", "err", err)
		if !known {
			return nil
		}
		// AppendEvent takes global events only; the key is in the log line.
		if err := e.deps.Store.AppendEvent(ctx, model.Event{Type: eventTeardownUnverified, At: e.deps.Clock.Now()}); err != nil {
			return err
		}
		return nil
	}
	if !known {
		log.Info("resources of an unknown task torn down")
		return nil
	}
	now := e.deps.Clock.Now()
	t.TeardownAt, t.UpdatedAt = &now, now
	if err := e.deps.Store.UpdateTask(ctx, t, model.Event{Key: t.Key, Type: eventTeardown, At: now}); err != nil {
		return err
	}
	log.Info("resources of a done task torn down")
	return nil
}

// stopRuns kills every container of t in res and discards each labelled
// run that is still running. A run ID the store does not know is a
// Publisher gate cell or a forge-local check cell: it is killed only.
func (e *Engine) stopRuns(ctx context.Context, t model.Task, res []ports.Resource) error {
	runIDs := map[string]bool{}
	for _, r := range res {
		if r.Kind == resourceKindContainer {
			if err := e.deps.Sandbox.Kill(ctx, r.ID); err != nil {
				return fmt.Errorf("kill %s of task %s: %w", r.ID, t.Key, err)
			}
		}
		if r.RunID != "" {
			runIDs[r.RunID] = true
		}
	}
	for _, id := range slices.Sorted(maps.Keys(runIDs)) {
		run, err := e.deps.Store.GetRun(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if run.Key != t.Key {
			return fmt.Errorf("run %s labelled for task %s belongs to task %s", id, t.Key, run.Key)
		}
		if run.Status != model.RunStatusRunning {
			continue
		}
		if err := e.discardRun(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

// discardRun finishes a running run as discarded now (spec §4.5: an
// interrupted run is discarded).
func (e *Engine) discardRun(ctx context.Context, run model.Run) error {
	now := e.deps.Clock.Now()
	run.Status, run.FinishedAt = model.RunStatusDiscarded, &now
	if err := e.deps.Store.FinishRun(ctx, run); err != nil {
		return err
	}
	e.deps.Logger.Info("interrupted run discarded", "key", string(run.Key), "runId", run.ID, "role", string(run.Role))
	return nil
}

// discardOrphanRuns discards every still running run of each stored task
// whose key comes from a pending intent or a forge candidate. The normal
// tick retries the work within the run budget.
func (e *Engine) discardOrphanRuns(ctx context.Context, pending []model.Intent, cands []ports.CandidateTask) error {
	keys := map[model.TaskKey]bool{}
	for _, in := range pending {
		keys[in.Key] = true
	}
	for _, c := range cands {
		key, err := model.NewTaskKey(c.Source)
		if err != nil {
			return err
		}
		keys[key] = true
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if _, err := e.deps.Store.GetTask(ctx, key); errors.Is(err, store.ErrNotFound) {
			continue
		} else if err != nil {
			return err
		}
		runs, err := e.deps.Store.ListRuns(ctx, key)
		if err != nil {
			return err
		}
		for _, r := range runs {
			if r.Status != model.RunStatusRunning {
				continue
			}
			if err := e.discardRun(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// verdict is what reconciliation decided for one pending intent: resolve it
// with status and out, or hold it for the owner.
type verdict struct {
	owner  bool
	status model.IntentStatus
	out    store.IntentOutcome
}

var (
	holdForOwner = verdict{owner: true}
	notApplied   = verdict{status: model.IntentStatusFailed, out: store.IntentOutcome{ErrorClass: string(errorClassNotApplied)}}
)

func succeeded(objectID, newSHA string) verdict {
	return verdict{status: model.IntentStatusSucceeded, out: store.IntentOutcome{ObjectID: objectID, NewSHA: newSHA}}
}

// reconcileIntents fetches the clean clone and reconciles each pending
// intent in order from forge and clean-clone reads. An intent whose task is
// not in the store stays pending and makes reconcileIntents return an error
// naming it, after the other intents are reconciled.
func (e *Engine) reconcileIntents(ctx context.Context, pending []model.Intent, cands []ports.CandidateTask) error {
	if err := e.deps.Git.Fetch(ctx, e.settings.CloneDir); err != nil {
		return err
	}
	tasks := map[model.TaskKey]model.Task{}
	var unknown []string
	for _, in := range pending {
		log := e.deps.Logger.With("key", string(in.Key), "intentId", in.ID, "op", string(in.Operation))
		t, ok := tasks[in.Key]
		if !ok {
			var err error
			t, err = e.deps.Store.GetTask(ctx, in.Key)
			if errors.Is(err, store.ErrNotFound) {
				log.Error("pending intent of a task not in the store stays pending")
				unknown = append(unknown, fmt.Sprintf("%s intent %s of task %s", in.Operation, in.ID, in.Key))
				continue
			}
			if err != nil {
				return err
			}
			tasks[in.Key] = t
		}
		v, err := e.reconcileIntent(ctx, t, in, cands)
		if err != nil {
			return fmt.Errorf("reconcile %s intent %s: %w", in.Operation, in.ID, err)
		}
		if v.owner {
			if t, err = e.holdIntent(ctx, t, in); err != nil {
				return err
			}
			tasks[in.Key] = t
			continue
		}
		if err := e.deps.Store.ResolveIntent(ctx, in.ID, v.status, v.out, e.deps.Clock.Now()); err != nil {
			return err
		}
		log.Info("pending intent reconciled", "status", string(v.status), "errorClass", v.out.ErrorClass)
	}
	if len(unknown) > 0 {
		return fmt.Errorf("pending intents of tasks not in the store: %s", strings.Join(unknown, ", "))
	}
	return nil
}

// holdIntent leaves in pending and flags t needs-owner, unless the flag is
// already set. It returns the stored task.
func (e *Engine) holdIntent(ctx context.Context, t model.Task, in model.Intent) (model.Task, error) {
	reason := fmt.Sprintf("unreconcilable %s intent %s", in.Operation, in.ID)
	e.deps.Logger.Warn("pending intent left for the owner", "key", string(t.Key), "intentId", in.ID, "reason", reason)
	if slices.Contains(t.Flags, model.TaskFlagNeedsOwner) {
		return t, nil
	}
	return e.apply(ctx, t, *ownerOutcome(statusOf(t), reason), nil)
}

// reconcileIntent decides one pending intent of stored task t from forge
// and clean-clone reads only; it never writes to the forge or the remote.
// The only store write is the adoption of a change an open-change intent
// proved opened.
func (e *Engine) reconcileIntent(ctx context.Context, t model.Task, in model.Intent, cands []ports.CandidateTask) (verdict, error) {
	if in.DryRun {
		return notApplied, nil
	}
	switch in.Operation {
	case model.IntentOperationPush:
		return e.reconcilePush(ctx, in)
	case model.IntentOperationOpenChange:
		return e.reconcileOpenChange(ctx, t, in)
	case model.IntentOperationUpdateChange:
		if in.HeadSHA != "" {
			return e.reconcileUpdateHead(ctx, t, in)
		}
		return e.reconcileUpdateBody(ctx, t, in)
	case model.IntentOperationUpdateBranch:
		// Recovery never writes a base-merge record.
		info, found, err := e.deps.Forge.FindChangeByTaskKey(ctx, t.Key)
		if err != nil {
			return verdict{}, err
		}
		if found && in.ExpectedOldSHA != "" && info.HeadSHA == in.ExpectedOldSHA {
			return notApplied, nil
		}
		return holdForOwner, nil
	case model.IntentOperationMerge:
		info, found, err := e.deps.Forge.FindChangeByTaskKey(ctx, t.Key)
		if err != nil {
			return verdict{}, err
		}
		if found && info.Open && in.HeadSHA != "" && info.HeadSHA == in.HeadSHA {
			return notApplied, nil
		}
		// ChangeInfo cannot tell merged from closed.
		return holdForOwner, nil
	case model.IntentOperationCloseTask:
		var a taskCommentArgs
		if err := json.Unmarshal(in.Args, &a); err != nil || a.Source.SourceID == "" {
			return holdForOwner, nil
		}
		if slices.ContainsFunc(cands, func(c ports.CandidateTask) bool { return c.Source == a.Source }) {
			return notApplied, nil
		}
		return succeeded(a.Source.SourceID, ""), nil
	case model.IntentOperationPostReview, model.IntentOperationCreateTask,
		model.IntentOperationRevert, model.IntentOperationReopenTask:
		// No forge read proves the outcome.
		return holdForOwner, nil
	}
	return holdForOwner, nil
}

// reconcilePush compares the clean-clone branch of a push intent with its
// head and expected old SHA.
func (e *Engine) reconcilePush(ctx context.Context, in model.Intent) (verdict, error) {
	ref := in.TargetRef
	if !strings.HasPrefix(ref, "refs/heads/") || len(ref) == len("refs/heads/") || strings.ContainsAny(ref, "*?[\\ ") {
		return holdForOwner, nil
	}
	sha, found, err := e.cloneRef(ctx, ref)
	if err != nil {
		return verdict{}, err
	}
	switch {
	case found && in.HeadSHA != "" && sha == in.HeadSHA:
		return succeeded("", in.HeadSHA), nil
	case found && in.ExpectedOldSHA != "" && sha == in.ExpectedOldSHA, !found && in.ExpectedOldSHA == "":
		return notApplied, nil
	}
	return holdForOwner, nil
}

// cloneRef reads the exact ref of the clean clone; found is false when it
// does not exist.
func (e *Engine) cloneRef(ctx context.Context, ref string) (sha string, found bool, err error) {
	out, err := e.deps.Git.Run(ctx, e.settings.CloneDir, nil, "for-each-ref", "--format=%(refname) %(objectname)", ref)
	if err != nil {
		return "", false, fmt.Errorf("read clean-clone ref %s: %w", ref, err)
	}
	for line := range strings.Lines(string(out)) {
		name, obj, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && name == ref {
			return obj, true, nil
		}
	}
	return "", false, nil
}

// storedChange returns the stored change of kind change of key.
func (e *Engine) storedChange(ctx context.Context, key model.TaskKey) (model.Change, bool, error) {
	changes, err := e.deps.Store.ListChanges(ctx, key)
	if err != nil {
		return model.Change{}, false, err
	}
	for _, c := range changes {
		if c.Kind == model.ChangeKindChange {
			return c, true, nil
		}
	}
	return model.Change{}, false, nil
}

// reconcileOpenChange resolves an open-change intent whose change is open
// at its head, adopting the change into the store when it has none.
func (e *Engine) reconcileOpenChange(ctx context.Context, t model.Task, in model.Intent) (verdict, error) {
	info, found, err := e.deps.Forge.FindChangeByTaskKey(ctx, t.Key)
	if err != nil {
		return verdict{}, err
	}
	if !found {
		return notApplied, nil
	}
	if !info.Open || in.HeadSHA == "" || info.HeadSHA != in.HeadSHA {
		return holdForOwner, nil
	}
	_, stored, err := e.storedChange(ctx, t.Key)
	if err != nil {
		return verdict{}, err
	}
	if !stored {
		base, err := e.integrationTip(ctx)
		if err != nil {
			return verdict{}, err
		}
		// Adopt before resolving, so a failed resolve is adopted only once.
		if err := e.recordChange(ctx, t, info.ID, info.Branch, base, info.HeadSHA); err != nil {
			return verdict{}, err
		}
		e.deps.Logger.Info("published change adopted", "key", string(t.Key), "changeId", info.ID, "head", info.HeadSHA)
	}
	return succeeded(info.ID, ""), nil
}

// reconcileUpdateHead resolves an update-change intent that carries a head.
func (e *Engine) reconcileUpdateHead(ctx context.Context, t model.Task, in model.Intent) (verdict, error) {
	info, found, err := e.deps.Forge.FindChangeByTaskKey(ctx, t.Key)
	if err != nil {
		return verdict{}, err
	}
	if !found {
		return holdForOwner, nil
	}
	if info.Open && info.HeadSHA == in.HeadSHA {
		return succeeded(info.ID, ""), nil
	}
	c, stored, err := e.storedChange(ctx, t.Key)
	if err != nil {
		return verdict{}, err
	}
	if stored && c.HeadSHA != "" && info.HeadSHA == c.HeadSHA {
		return notApplied, nil
	}
	return holdForOwner, nil
}

// reconcileUpdateBody resolves a body-only update-change intent.
func (e *Engine) reconcileUpdateBody(ctx context.Context, t model.Task, in model.Intent) (verdict, error) {
	var a changeBodyArgs
	if err := json.Unmarshal(in.Args, &a); err != nil || a.ChangeID == "" {
		return holdForOwner, nil
	}
	info, found, err := e.deps.Forge.FindChangeByTaskKey(ctx, t.Key)
	if err != nil {
		return verdict{}, err
	}
	if !found || info.ID != a.ChangeID {
		return holdForOwner, nil
	}
	if info.Body == a.Body {
		return succeeded(a.ChangeID, ""), nil
	}
	return notApplied, nil
}
