package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// IntentStore records forge-write intents. *store.Store implements it.
type IntentStore interface {
	CreateIntent(ctx context.Context, in model.Intent) error
	ResolveIntent(ctx context.Context, id string, status model.IntentStatus, out store.IntentOutcome, at time.Time) error
}

var _ IntentStore = (*store.Store)(nil)

// markerPrefix is the hidden task-key marker of spec §4.5.
const markerPrefix = "ddf:task-key="

// Marker returns the hidden marker that ties a forge object to key
// (spec §4.5).
func Marker(key model.TaskKey) string {
	return markerPrefix + string(key)
}

// Write is one forge or push write: the task, the operation, the call's
// arguments and the reconciliation evidence recorded in its intent.
type Write struct {
	Key                                model.TaskKey
	Op                                 model.IntentOperation
	Args                               any
	TargetRef, ExpectedOldSHA, HeadSHA string
}

// WriteResult is the outcome of a successful write: the forge object ID and
// the new SHA.
type WriteResult struct {
	ObjectID, NewSHA string
}

// errorClass is the error class a failed intent records (spec §4.5 Tables).
type errorClass string

// errorClass values.
const (
	errorClassHeadMoved errorClass = "head-moved"
	errorClassCanceled  errorClass = "canceled"
	errorClassError     errorClass = "error"
)

// classifyError returns the errorClass of a failed write.
func classifyError(err error) errorClass {
	switch {
	case errors.Is(err, ports.ErrHeadMoved):
		return errorClassHeadMoved
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errorClassCanceled
	default:
		return errorClassError
	}
}

// Intents wraps every forge and push write in a store intent: the intent
// is committed before the call and resolved after it (spec §4.5 SQL rules).
type Intents struct {
	store IntentStore
	clock ports.Clock
	ids   ports.IDSource
	mode  ports.WriteMode
}

// NewIntents returns Intents over s, c and ids that runs every write in
// mode. A nil dependency or a mode ports.ParseWriteMode rejects, the zero
// value included, is an error.
func NewIntents(s IntentStore, c ports.Clock, ids ports.IDSource, mode ports.WriteMode) (*Intents, error) {
	if s == nil || c == nil || ids == nil {
		return nil, errors.New("new intents: nil store, clock or id source")
	}
	if _, err := ports.ParseWriteMode(string(mode)); err != nil {
		return nil, fmt.Errorf("new intents: %w", err)
	}
	return &Intents{store: s, clock: c, ids: ids, mode: mode}, nil
}

// Do records a pending intent for w, runs call once in the Intents mode and
// resolves the intent with its outcome. Nothing is written when w is
// invalid, and call is not run when the intent cannot be created. A call
// error resolves the intent failed and is returned joined with any resolve
// error. A resolve error after a successful call is returned and leaves the
// intent pending for recovery. A write is never retried.
func (in *Intents) Do(ctx context.Context, w Write, call func(ctx context.Context, mode ports.WriteMode) (WriteResult, error)) (WriteResult, error) {
	if call == nil {
		return WriteResult{}, errors.New("intent: nil call")
	}
	op, err := model.ParseIntentOperation(string(w.Op))
	if err != nil {
		return WriteResult{}, fmt.Errorf("intent: %w", err)
	}
	args, err := json.Marshal(w.Args)
	if err != nil {
		return WriteResult{}, fmt.Errorf("intent %s: encode args: %w", op, err)
	}
	id := in.ids.NewID()
	intent := model.Intent{
		ID:             id,
		Key:            w.Key,
		Operation:      op,
		Status:         model.IntentStatusPending,
		DryRun:         in.mode == ports.WriteModeDryRun,
		Args:           args,
		Marker:         Marker(w.Key),
		TargetRef:      w.TargetRef,
		ExpectedOldSHA: w.ExpectedOldSHA,
		HeadSHA:        w.HeadSHA,
		CreatedAt:      in.clock.Now(),
	}
	// spec §4.5 SQL rules: the intent commits before the forge call
	if err := in.store.CreateIntent(ctx, intent); err != nil {
		return WriteResult{}, fmt.Errorf("intent %s %s: %w", op, id, err)
	}
	res, callErr := call(ctx, in.mode)
	// The outcome is recorded even when ctx was canceled during the call,
	// so a canceled write is not left pending without need.
	rctx := context.WithoutCancel(ctx)
	if callErr != nil {
		callErr = fmt.Errorf("intent %s %s: %w", op, id, callErr)
		out := store.IntentOutcome{ErrorClass: string(classifyError(callErr))}
		if err := in.store.ResolveIntent(rctx, id, model.IntentStatusFailed, out, in.clock.Now()); err != nil {
			return WriteResult{}, errors.Join(callErr, fmt.Errorf("intent %s %s: %w", op, id, err))
		}
		return WriteResult{}, callErr
	}
	out := store.IntentOutcome{ObjectID: res.ObjectID, NewSHA: res.NewSHA}
	if intent.DryRun {
		out = store.IntentOutcome{}
	}
	if err := in.store.ResolveIntent(rctx, id, model.IntentStatusSucceeded, out, in.clock.Now()); err != nil {
		return res, fmt.Errorf("intent %s %s: resolve after successful call: %w", op, id, err)
	}
	return res, nil
}

// ForgeWrites exposes each ports.Forge write that has a spec §4.5 intent
// operation, each run once inside Intents.Do in the Intents mode.
// RerunChecks and SetProjectionLabels have no intent operation and are not
// exposed.
type ForgeWrites struct {
	forge ports.Forge
	in    *Intents
}

// Forge returns the intent-wrapped writes of f.
func (in *Intents) Forge(f ports.Forge) *ForgeWrites {
	return &ForgeWrites{forge: f, in: in}
}

func (f *ForgeWrites) do(ctx context.Context, w Write, call func(ctx context.Context, mode ports.WriteMode) (WriteResult, error)) error {
	if f == nil || f.forge == nil || f.in == nil {
		return fmt.Errorf("forge write %s: nil forge or intents", w.Op)
	}
	_, err := f.in.Do(ctx, w, call)
	return err
}

func branchRef(branch string) string {
	return "refs/heads/" + branch
}

// CreateTask creates t on the forge (create-task). The intent records the
// new source ID as its object.
func (f *ForgeWrites) CreateTask(ctx context.Context, key model.TaskKey, t ports.NewTask) (model.TaskSource, error) {
	var src model.TaskSource
	err := f.do(ctx, Write{Key: key, Op: model.IntentOperationCreateTask, Args: t},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			var err error
			src, err = f.forge.CreateTask(ctx, mode, t)
			return WriteResult{ObjectID: src.SourceID}, err
		})
	return src, err
}

// PublishChange opens the change for r (open-change), or updates it
// (update-change) when existing.
func (f *ForgeWrites) PublishChange(ctx context.Context, key model.TaskKey, r ports.PublishRequest, existing bool) (ports.ChangeInfo, error) {
	op := model.IntentOperationOpenChange
	if existing {
		op = model.IntentOperationUpdateChange
	}
	var info ports.ChangeInfo
	err := f.do(ctx, Write{Key: key, Op: op, Args: r, TargetRef: branchRef(r.Branch), HeadSHA: r.HeadSHA},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			var err error
			info, err = f.forge.PublishChange(ctx, mode, r)
			return WriteResult{ObjectID: info.ID}, err
		})
	return info, err
}

type changeBodyArgs struct {
	ChangeID string `json:"changeId"`
	Body     string `json:"body"`
}

// UpdateChangeBody replaces the body of changeID (update-change).
func (f *ForgeWrites) UpdateChangeBody(ctx context.Context, key model.TaskKey, changeID, body string) error {
	return f.do(ctx, Write{Key: key, Op: model.IntentOperationUpdateChange, Args: changeBodyArgs{ChangeID: changeID, Body: body}},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			return WriteResult{ObjectID: changeID}, f.forge.UpdateChangeBody(ctx, mode, changeID, body)
		})
}

type updateBranchArgs struct {
	ChangeID string `json:"changeId"`
	OldHead  string `json:"oldHead"`
}

// UpdateBranchFromBase updates the branch of changeID, whose head is
// oldHead, from its base (update-branch) and returns the new head SHA.
func (f *ForgeWrites) UpdateBranchFromBase(ctx context.Context, key model.TaskKey, changeID, oldHead string) (string, error) {
	var head string
	err := f.do(ctx, Write{Key: key, Op: model.IntentOperationUpdateBranch, Args: updateBranchArgs{ChangeID: changeID, OldHead: oldHead}, ExpectedOldSHA: oldHead},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			var err error
			head, err = f.forge.UpdateBranchFromBase(ctx, mode, changeID)
			return WriteResult{ObjectID: changeID, NewSHA: head}, err
		})
	return head, err
}

// PostReview posts r (post-review).
func (f *ForgeWrites) PostReview(ctx context.Context, key model.TaskKey, r ports.ReviewPost) error {
	return f.do(ctx, Write{Key: key, Op: model.IntentOperationPostReview, Args: r, HeadSHA: r.HeadSHA},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			return WriteResult{ObjectID: r.ChangeID}, f.forge.PostReview(ctx, mode, r)
		})
}

type mergeArgs struct {
	ChangeID string `json:"changeId"`
	HeadSHA  string `json:"headSha"`
}

// Merge merges changeID pinned to headSHA (merge) and returns the merge
// SHA.
func (f *ForgeWrites) Merge(ctx context.Context, key model.TaskKey, changeID, headSHA string) (string, error) {
	var sha string
	err := f.do(ctx, Write{Key: key, Op: model.IntentOperationMerge, Args: mergeArgs{ChangeID: changeID, HeadSHA: headSHA}, HeadSHA: headSHA},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			var err error
			sha, err = f.forge.Merge(ctx, mode, changeID, headSHA)
			return WriteResult{ObjectID: changeID, NewSHA: sha}, err
		})
	return sha, err
}

type revertArgs struct {
	MergeSHA string `json:"mergeSha"`
}

// Revert opens a change reverting mergeSHA (revert).
func (f *ForgeWrites) Revert(ctx context.Context, key model.TaskKey, mergeSHA string) (ports.ChangeInfo, error) {
	var info ports.ChangeInfo
	err := f.do(ctx, Write{Key: key, Op: model.IntentOperationRevert, Args: revertArgs{MergeSHA: mergeSHA}},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			var err error
			info, err = f.forge.Revert(ctx, mode, key, mergeSHA)
			return WriteResult{ObjectID: info.ID, NewSHA: info.HeadSHA}, err
		})
	return info, err
}

type taskCommentArgs struct {
	Source  model.TaskSource `json:"source"`
	Comment string           `json:"comment"`
}

// CloseTask closes src with comment (close-task).
func (f *ForgeWrites) CloseTask(ctx context.Context, key model.TaskKey, src model.TaskSource, comment string) error {
	return f.do(ctx, Write{Key: key, Op: model.IntentOperationCloseTask, Args: taskCommentArgs{Source: src, Comment: comment}},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			return WriteResult{ObjectID: src.SourceID}, f.forge.CloseTask(ctx, mode, src, comment)
		})
}

// ReopenTask reopens src with comment (reopen-task).
func (f *ForgeWrites) ReopenTask(ctx context.Context, key model.TaskKey, src model.TaskSource, comment string) error {
	return f.do(ctx, Write{Key: key, Op: model.IntentOperationReopenTask, Args: taskCommentArgs{Source: src, Comment: comment}},
		func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
			return WriteResult{ObjectID: src.SourceID}, f.forge.ReopenTask(ctx, mode, src, comment)
		})
}

// checkMode refuses a caller mode other than the Intents mode: a write
// runs only in the mode the intent records.
func (in *Intents) checkMode(mode ports.WriteMode) error {
	if mode != in.mode {
		return fmt.Errorf("write mode %q, intents run in %q", mode, in.mode)
	}
	return nil
}

type pushArgs struct {
	Branch string `json:"branch"`
	OldSHA string `json:"oldSha"`
	NewSHA string `json:"newSha"`
}

type intentPusher struct {
	in  *Intents
	key model.TaskKey
	p   publisher.Pusher
}

// Pusher returns p wrapped in a push intent for key.
func (in *Intents) Pusher(key model.TaskKey, p publisher.Pusher) publisher.Pusher {
	return &intentPusher{in: in, key: key, p: p}
}

// Push implements publisher.Pusher.
func (ip *intentPusher) Push(ctx context.Context, mode ports.WriteMode, r publisher.PushRequest) error {
	if ip.in == nil || ip.p == nil {
		return errors.New("push: nil intents or pusher")
	}
	if err := ip.in.checkMode(mode); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	w := Write{
		Key:            ip.key,
		Op:             model.IntentOperationPush,
		Args:           pushArgs{Branch: r.Branch, OldSHA: r.OldSHA, NewSHA: r.NewSHA},
		TargetRef:      branchRef(r.Branch),
		ExpectedOldSHA: r.OldSHA,
		HeadSHA:        r.NewSHA,
	}
	_, err := ip.in.Do(ctx, w, func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
		return WriteResult{NewSHA: r.NewSHA}, ip.p.Push(ctx, mode, r)
	})
	return err
}

type intentChangeWriter struct {
	in       *Intents
	key      model.TaskKey
	w        *ForgeWrites
	existing bool
}

// ChangeWriter returns a publisher.ChangeWriter for key that delegates to
// w.PublishChange with existing.
func (in *Intents) ChangeWriter(key model.TaskKey, w *ForgeWrites, existing bool) publisher.ChangeWriter {
	return &intentChangeWriter{in: in, key: key, w: w, existing: existing}
}

// PublishChange implements publisher.ChangeWriter.
func (cw *intentChangeWriter) PublishChange(ctx context.Context, mode ports.WriteMode, r ports.PublishRequest) (ports.ChangeInfo, error) {
	if cw.in == nil || cw.w == nil {
		return ports.ChangeInfo{}, errors.New("publish change: nil intents or forge writes")
	}
	if err := cw.in.checkMode(mode); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish change: %w", err)
	}
	if r.Key != cw.key {
		return ports.ChangeInfo{}, fmt.Errorf("publish change: request key %s, want %s", r.Key, cw.key)
	}
	return cw.w.PublishChange(ctx, cw.key, r, cw.existing)
}
