package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/store"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

var intentTestStart = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

var intentTestSource = model.TaskSource{Forge: "local", Repository: "demo", SourceKind: "task", SourceID: "1"}

func intentTestKey(t *testing.T) model.TaskKey {
	t.Helper()
	key, err := model.NewTaskKey(intentTestSource)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func openIntentStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "ddf.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	// An intent row references its task.
	key := intentTestKey(t)
	task := model.Task{
		Key: key, Forge: intentTestSource.Forge, Repository: intentTestSource.Repository,
		SourceKind: intentTestSource.SourceKind, SourceID: intentTestSource.SourceID,
		Kind: model.TaskKindTask, State: model.TaskStateInbox, Priority: model.PriorityNormal,
		CreatedAt: intentTestStart, UpdatedAt: intentTestStart,
	}
	if err := s.CreateTask(context.Background(), task, model.Event{Key: key, Type: "intake", At: intentTestStart}); err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestIntents(t *testing.T, s IntentStore, mode ports.WriteMode) *Intents {
	t.Helper()
	in, err := NewIntents(s, fakes.NewClock(intentTestStart), fakes.NewIDs("intent"), mode)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func pendingOne(t *testing.T, s *store.Store) model.Intent {
	t.Helper()
	pending, err := s.PendingIntents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending intents = %d, want 1", len(pending))
	}
	return pending[0]
}

func pendingNone(t *testing.T, s *store.Store) {
	t.Helper()
	pending, err := s.PendingIntents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending intents = %d, want 0", len(pending))
	}
}

// resolveCall is one recorded ResolveIntent call.
type resolveCall struct {
	id     string
	status model.IntentStatus
	out    store.IntentOutcome
}

// recordingStore wraps an IntentStore, records creates and resolves and can
// inject errors.
type recordingStore struct {
	next       IntentStore
	createErr  error
	resolveErr error
	created    []model.Intent
	resolved   []resolveCall
}

func (r *recordingStore) CreateIntent(ctx context.Context, in model.Intent) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.created = append(r.created, in)
	return r.next.CreateIntent(ctx, in)
}

func (r *recordingStore) ResolveIntent(ctx context.Context, id string, status model.IntentStatus, out store.IntentOutcome, at time.Time) error {
	if r.resolveErr != nil {
		return r.resolveErr
	}
	r.resolved = append(r.resolved, resolveCall{id: id, status: status, out: out})
	return r.next.ResolveIntent(ctx, id, status, out, at)
}

func TestNewIntents_RefusesInvalidInputs(t *testing.T) {
	s := openIntentStore(t)
	c, ids := fakes.NewClock(intentTestStart), fakes.NewIDs("intent")
	cases := []struct {
		name  string
		s     IntentStore
		c     ports.Clock
		ids   ports.IDSource
		mode  ports.WriteMode
		valid bool
	}{
		{"zero write mode", s, c, ids, "", false},
		{"unknown write mode", s, c, ids, "maybe", false},
		{"nil store", nil, c, ids, ports.WriteModeLive, false},
		{"nil clock", s, nil, ids, ports.WriteModeLive, false},
		{"nil id source", s, c, nil, ports.WriteModeLive, false},
		{"live", s, c, ids, ports.WriteModeLive, true},
		{"dry-run", s, c, ids, ports.WriteModeDryRun, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, err := NewIntents(tc.s, tc.c, tc.ids, tc.mode)
			if tc.valid && (err != nil || in == nil) {
				t.Fatalf("NewIntents = %v, %v; want intents", in, err)
			}
			if !tc.valid && (err == nil || in != nil) {
				t.Fatalf("NewIntents = %v, %v; want error", in, err)
			}
		})
	}
}

func TestMarker(t *testing.T) {
	key := intentTestKey(t)
	if got, want := Marker(key), "ddf:task-key="+string(key); got != want {
		t.Fatalf("Marker = %q, want %q", got, want)
	}
}

// TestIntents_Do_EveryOperation covers spec §4.5 SQL rules: the intent is
// pending during the call and resolved after it, for every operation.
func TestIntents_Do_EveryOperation(t *testing.T) {
	ops := []model.IntentOperation{
		model.IntentOperationCreateTask, model.IntentOperationPush, model.IntentOperationOpenChange,
		model.IntentOperationUpdateChange, model.IntentOperationPostReview, model.IntentOperationUpdateBranch,
		model.IntentOperationMerge, model.IntentOperationRevert, model.IntentOperationCloseTask,
		model.IntentOperationReopenTask,
	}
	key := intentTestKey(t)
	for _, op := range ops {
		t.Run(string(op), func(t *testing.T) {
			s := openIntentStore(t)
			in := newTestIntents(t, s, ports.WriteModeLive)
			calls := 0
			res, err := in.Do(context.Background(), Write{Key: key, Op: op, Args: map[string]string{"op": string(op)}},
				func(ctx context.Context, mode ports.WriteMode) (WriteResult, error) {
					calls++
					if mode != ports.WriteModeLive {
						t.Errorf("mode = %q, want live", mode)
					}
					p := pendingOne(t, s)
					if p.Operation != op || p.Marker != "ddf:task-key="+string(key) || p.DryRun || p.Key != key {
						t.Errorf("pending intent = %+v", p)
					}
					return WriteResult{ObjectID: "obj-1", NewSHA: "sha"}, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls = %d, want 1", calls)
			}
			if res != (WriteResult{ObjectID: "obj-1", NewSHA: "sha"}) {
				t.Fatalf("result = %+v", res)
			}
			pendingNone(t, s)
		})
	}
}

func TestIntents_Do_SucceededOutcome(t *testing.T) {
	rec := &recordingStore{next: openIntentStore(t)}
	in := newTestIntents(t, rec, ports.WriteModeLive)
	_, err := in.Do(context.Background(), Write{Key: intentTestKey(t), Op: model.IntentOperationMerge, HeadSHA: "h"},
		func(context.Context, ports.WriteMode) (WriteResult, error) {
			return WriteResult{ObjectID: "c-1", NewSHA: "m"}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.created) != 1 || rec.created[0].HeadSHA != "h" || rec.created[0].CreatedAt != intentTestStart {
		t.Fatalf("created = %+v", rec.created)
	}
	want := resolveCall{id: rec.created[0].ID, status: model.IntentStatusSucceeded, out: store.IntentOutcome{ObjectID: "c-1", NewSHA: "m"}}
	if len(rec.resolved) != 1 || rec.resolved[0] != want {
		t.Fatalf("resolved = %+v, want %+v", rec.resolved, want)
	}
}

func TestIntents_Do_InvalidWriteNothingRecorded(t *testing.T) {
	cases := []struct {
		name string
		w    Write
	}{
		{"unknown operation", Write{Key: intentTestKey(t), Op: "rerun-checks"}},
		{"unencodable args", Write{Key: intentTestKey(t), Op: model.IntentOperationPush, Args: make(chan int)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingStore{next: openIntentStore(t)}
			in := newTestIntents(t, rec, ports.WriteModeLive)
			called := false
			_, err := in.Do(context.Background(), tc.w, func(context.Context, ports.WriteMode) (WriteResult, error) {
				called = true
				return WriteResult{}, nil
			})
			if err == nil || called || len(rec.created) != 0 {
				t.Fatalf("err = %v, called = %v, created = %d; want error, no call, nothing created", err, called, len(rec.created))
			}
		})
	}
}

func TestIntents_Do_CreateIntentFailsNoCall(t *testing.T) {
	errCreate := errors.New("disk full")
	rec := &recordingStore{next: openIntentStore(t), createErr: errCreate}
	in := newTestIntents(t, rec, ports.WriteModeLive)
	called := false
	_, err := in.Do(context.Background(), Write{Key: intentTestKey(t), Op: model.IntentOperationMerge},
		func(context.Context, ports.WriteMode) (WriteResult, error) {
			called = true
			return WriteResult{}, nil
		})
	if !errors.Is(err, errCreate) {
		t.Fatalf("err = %v, want %v", err, errCreate)
	}
	if called {
		t.Fatal("call invoked after CreateIntent failed")
	}
}

func TestIntents_Do_HeadMovedResolvesFailed(t *testing.T) {
	s := openIntentStore(t)
	rec := &recordingStore{next: s}
	in := newTestIntents(t, rec, ports.WriteModeLive)
	_, err := in.Do(context.Background(), Write{Key: intentTestKey(t), Op: model.IntentOperationMerge},
		func(context.Context, ports.WriteMode) (WriteResult, error) {
			return WriteResult{}, fmt.Errorf("merge c-1: %w", ports.ErrHeadMoved)
		})
	if !errors.Is(err, ports.ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrHeadMoved", err)
	}
	want := resolveCall{id: rec.created[0].ID, status: model.IntentStatusFailed, out: store.IntentOutcome{ErrorClass: "head-moved"}}
	if len(rec.resolved) != 1 || rec.resolved[0] != want {
		t.Fatalf("resolved = %+v, want %+v", rec.resolved, want)
	}
	pendingNone(t, s)
}

func TestIntents_Do_CallAndResolveErrorsJoined(t *testing.T) {
	errCall, errResolve := errors.New("forge down"), errors.New("store locked")
	rec := &recordingStore{next: openIntentStore(t), resolveErr: errResolve}
	in := newTestIntents(t, rec, ports.WriteModeLive)
	_, err := in.Do(context.Background(), Write{Key: intentTestKey(t), Op: model.IntentOperationRevert},
		func(context.Context, ports.WriteMode) (WriteResult, error) { return WriteResult{}, errCall })
	if !errors.Is(err, errCall) || !errors.Is(err, errResolve) {
		t.Fatalf("err = %v, want both errors", err)
	}
}

func TestIntents_Do_ResolveErrorAfterSuccessStaysPending(t *testing.T) {
	s := openIntentStore(t)
	errResolve := errors.New("store locked")
	rec := &recordingStore{next: s, resolveErr: errResolve}
	in := newTestIntents(t, rec, ports.WriteModeLive)
	calls := 0
	_, err := in.Do(context.Background(), Write{Key: intentTestKey(t), Op: model.IntentOperationOpenChange},
		func(context.Context, ports.WriteMode) (WriteResult, error) {
			calls++
			return WriteResult{ObjectID: "c-1"}, nil
		})
	if !errors.Is(err, errResolve) {
		t.Fatalf("err = %v, want %v", err, errResolve)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (never retried)", calls)
	}
	if p := pendingOne(t, s); p.Operation != model.IntentOperationOpenChange {
		t.Fatalf("pending = %+v", p)
	}
}

func TestIntents_Do_DryRun(t *testing.T) {
	s := openIntentStore(t)
	rec := &recordingStore{next: s}
	in := newTestIntents(t, rec, ports.WriteModeDryRun)
	_, err := in.Do(context.Background(), Write{Key: intentTestKey(t), Op: model.IntentOperationPush},
		func(_ context.Context, mode ports.WriteMode) (WriteResult, error) {
			if mode != ports.WriteModeDryRun {
				t.Errorf("mode = %q, want dry-run", mode)
			}
			if p := pendingOne(t, s); !p.DryRun {
				t.Errorf("pending intent DryRun = false, want true")
			}
			return WriteResult{ObjectID: "ignored", NewSHA: "ignored"}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	want := resolveCall{id: rec.created[0].ID, status: model.IntentStatusSucceeded}
	if len(rec.resolved) != 1 || rec.resolved[0] != want {
		t.Fatalf("resolved = %+v, want %+v", rec.resolved, want)
	}
	pendingNone(t, s)
}

func TestIntents_Do_CanceledResolvesFailed(t *testing.T) {
	s := openIntentStore(t)
	rec := &recordingStore{next: s}
	in := newTestIntents(t, rec, ports.WriteModeLive)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := in.Do(ctx, Write{Key: intentTestKey(t), Op: model.IntentOperationPush},
		func(ctx context.Context, _ ports.WriteMode) (WriteResult, error) {
			cancel()
			return WriteResult{}, ctx.Err()
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if len(rec.resolved) != 1 || rec.resolved[0].out.ErrorClass != "canceled" {
		t.Fatalf("resolved = %+v, want error class canceled", rec.resolved)
	}
	pendingNone(t, s)
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want errorClass
	}{
		{"head moved", fmt.Errorf("merge: %w", ports.ErrHeadMoved), errorClassHeadMoved},
		{"canceled", fmt.Errorf("push: %w", context.Canceled), errorClassCanceled},
		{"deadline", fmt.Errorf("push: %w", context.DeadlineExceeded), errorClassCanceled},
		{"other", errors.New("boom"), errorClassError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyError(tc.err); got != tc.want {
				t.Fatalf("classifyError = %q, want %q", got, tc.want)
			}
		})
	}
	// The spellings are the spec §4.5 error classes.
	spell := map[errorClass]string{errorClassHeadMoved: "head-moved", errorClassCanceled: "canceled", errorClassError: "error"}
	for c, s := range spell {
		if string(c) != s {
			t.Errorf("error class %q, want %q", c, s)
		}
	}
}

// stubForge is a test-local ports.Forge that records each write, its mode
// and the pending intents at the time of the call.
type stubForge struct {
	t       *testing.T
	s       *store.Store
	calls   map[string]int
	modes   []ports.WriteMode
	pending []model.Intent
}

func newStubForge(t *testing.T, s *store.Store) *stubForge {
	return &stubForge{t: t, s: s, calls: map[string]int{}}
}

func (f *stubForge) record(name string, mode ports.WriteMode) {
	f.calls[name]++
	f.modes = append(f.modes, mode)
	p, err := f.s.PendingIntents(context.Background())
	if err != nil {
		f.t.Error(err)
	}
	f.pending = append(f.pending, p...)
}

func (f *stubForge) ListCandidateTasks(context.Context) ([]ports.CandidateTask, error) {
	return nil, errors.New("not used")
}

func (f *stubForge) ReadIntakeActor(context.Context, model.TaskSource) (ports.Identity, error) {
	return ports.Identity{}, errors.New("not used")
}

func (f *stubForge) FindChangeByTaskKey(context.Context, model.TaskKey) (ports.ChangeInfo, bool, error) {
	return ports.ChangeInfo{}, false, errors.New("not used")
}

func (f *stubForge) ReadChecks(context.Context, string) ([]ports.Check, error) {
	return nil, errors.New("not used")
}

func (f *stubForge) ReadAnswers(context.Context, model.TaskSource) ([]ports.Answer, error) {
	return nil, errors.New("not used")
}

func (f *stubForge) CreateTask(_ context.Context, mode ports.WriteMode, _ ports.NewTask) (model.TaskSource, error) {
	f.record("CreateTask", mode)
	return model.TaskSource{SourceID: "7"}, nil
}

func (f *stubForge) PublishChange(_ context.Context, mode ports.WriteMode, _ ports.PublishRequest) (ports.ChangeInfo, error) {
	f.record("PublishChange", mode)
	return ports.ChangeInfo{ID: "c-1"}, nil
}

func (f *stubForge) UpdateChangeBody(_ context.Context, mode ports.WriteMode, _, _ string) error {
	f.record("UpdateChangeBody", mode)
	return nil
}

func (f *stubForge) UpdateBranchFromBase(_ context.Context, mode ports.WriteMode, _ string) (string, error) {
	f.record("UpdateBranchFromBase", mode)
	return "new-head", nil
}

func (f *stubForge) RerunChecks(_ context.Context, mode ports.WriteMode, _ string) error {
	f.record("RerunChecks", mode)
	return nil
}

func (f *stubForge) PostReview(_ context.Context, mode ports.WriteMode, _ ports.ReviewPost) error {
	f.record("PostReview", mode)
	return nil
}

func (f *stubForge) Merge(_ context.Context, mode ports.WriteMode, _, _ string) (string, error) {
	f.record("Merge", mode)
	return "merge-sha", nil
}

func (f *stubForge) Revert(_ context.Context, mode ports.WriteMode, _ model.TaskKey, _ string) (ports.ChangeInfo, error) {
	f.record("Revert", mode)
	return ports.ChangeInfo{ID: "c-2"}, nil
}

func (f *stubForge) CloseTask(_ context.Context, mode ports.WriteMode, _ model.TaskSource, _ string) error {
	f.record("CloseTask", mode)
	return nil
}

func (f *stubForge) ReopenTask(_ context.Context, mode ports.WriteMode, _ model.TaskSource, _ string) error {
	f.record("ReopenTask", mode)
	return nil
}

func (f *stubForge) SetProjectionLabels(_ context.Context, mode ports.WriteMode, _ model.TaskSource, _ []string) error {
	f.record("SetProjectionLabels", mode)
	return nil
}

var _ ports.Forge = (*stubForge)(nil)

func TestForgeWrites_EachMethodOneCallInIntent(t *testing.T) {
	key := intentTestKey(t)
	src := intentTestSource
	cases := []struct {
		name   string
		method string
		op     model.IntentOperation
		run    func(ctx context.Context, w *ForgeWrites) error
	}{
		{"create task", "CreateTask", model.IntentOperationCreateTask, func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.CreateTask(ctx, key, ports.NewTask{Key: key, Title: "t"})
			return err
		}},
		{"open change", "PublishChange", model.IntentOperationOpenChange, func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.PublishChange(ctx, key, ports.PublishRequest{Key: key, Branch: "ddf/x", HeadSHA: "h"}, false)
			return err
		}},
		{"update change", "PublishChange", model.IntentOperationUpdateChange, func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.PublishChange(ctx, key, ports.PublishRequest{Key: key, Branch: "ddf/x", HeadSHA: "h"}, true)
			return err
		}},
		{"update change body", "UpdateChangeBody", model.IntentOperationUpdateChange, func(ctx context.Context, w *ForgeWrites) error {
			return w.UpdateChangeBody(ctx, key, "c-1", "body")
		}},
		{"update branch", "UpdateBranchFromBase", model.IntentOperationUpdateBranch, func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.UpdateBranchFromBase(ctx, key, "c-1", "old")
			return err
		}},
		{"post review", "PostReview", model.IntentOperationPostReview, func(ctx context.Context, w *ForgeWrites) error {
			return w.PostReview(ctx, key, ports.ReviewPost{ChangeID: "c-1", HeadSHA: "h", Verdict: model.ReviewVerdictApprove})
		}},
		{"merge", "Merge", model.IntentOperationMerge, func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.Merge(ctx, key, "c-1", "h")
			return err
		}},
		{"revert", "Revert", model.IntentOperationRevert, func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.Revert(ctx, key, "m")
			return err
		}},
		{"close task", "CloseTask", model.IntentOperationCloseTask, func(ctx context.Context, w *ForgeWrites) error {
			return w.CloseTask(ctx, key, src, "done")
		}},
		{"reopen task", "ReopenTask", model.IntentOperationReopenTask, func(ctx context.Context, w *ForgeWrites) error {
			return w.ReopenTask(ctx, key, src, "again")
		}},
	}
	for _, mode := range []ports.WriteMode{ports.WriteModeLive, ports.WriteModeDryRun} {
		for _, tc := range cases {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				s := openIntentStore(t)
				f := newStubForge(t, s)
				w := newTestIntents(t, s, mode).Forge(f)
				if err := tc.run(context.Background(), w); err != nil {
					t.Fatal(err)
				}
				if len(f.calls) != 1 || f.calls[tc.method] != 1 {
					t.Fatalf("forge calls = %v, want one %s", f.calls, tc.method)
				}
				if len(f.modes) != 1 || f.modes[0] != mode {
					t.Fatalf("modes = %v, want [%s]", f.modes, mode)
				}
				if len(f.pending) != 1 || f.pending[0].Operation != tc.op || f.pending[0].Key != key {
					t.Fatalf("pending at call = %+v, want one %s intent", f.pending, tc.op)
				}
				pendingNone(t, s)
			})
		}
	}
}

func TestForgeWrites_Evidence(t *testing.T) {
	key := intentTestKey(t)
	cases := []struct {
		name string
		run  func(ctx context.Context, w *ForgeWrites) error
		want model.Intent
		out  store.IntentOutcome
	}{
		{"publish change", func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.PublishChange(ctx, key, ports.PublishRequest{Key: key, Branch: "ddf/x", HeadSHA: "h"}, false)
			return err
		}, model.Intent{TargetRef: "refs/heads/ddf/x", HeadSHA: "h"}, store.IntentOutcome{ObjectID: "c-1"}},
		{"update branch", func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.UpdateBranchFromBase(ctx, key, "c-1", "old")
			return err
		}, model.Intent{ExpectedOldSHA: "old"}, store.IntentOutcome{ObjectID: "c-1", NewSHA: "new-head"}},
		{"merge", func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.Merge(ctx, key, "c-1", "h")
			return err
		}, model.Intent{HeadSHA: "h"}, store.IntentOutcome{ObjectID: "c-1", NewSHA: "merge-sha"}},
		{"create task", func(ctx context.Context, w *ForgeWrites) error {
			_, err := w.CreateTask(ctx, key, ports.NewTask{Key: key})
			return err
		}, model.Intent{}, store.IntentOutcome{ObjectID: "7"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openIntentStore(t)
			rec := &recordingStore{next: s}
			w := newTestIntents(t, rec, ports.WriteModeLive).Forge(newStubForge(t, s))
			if err := tc.run(context.Background(), w); err != nil {
				t.Fatal(err)
			}
			c := rec.created[0]
			if c.TargetRef != tc.want.TargetRef || c.HeadSHA != tc.want.HeadSHA || c.ExpectedOldSHA != tc.want.ExpectedOldSHA {
				t.Fatalf("created = %+v, want evidence %+v", c, tc.want)
			}
			if rec.resolved[0].out != tc.out {
				t.Fatalf("outcome = %+v, want %+v", rec.resolved[0].out, tc.out)
			}
		})
	}
}

func TestForgeWrites_NilForgeRefused(t *testing.T) {
	s := openIntentStore(t)
	w := newTestIntents(t, s, ports.WriteModeLive).Forge(nil)
	if err := w.CloseTask(context.Background(), intentTestKey(t), model.TaskSource{}, ""); err == nil {
		t.Fatal("CloseTask on nil forge: want error")
	}
	pendingNone(t, s)
}

// stubPusher records pushes and the pending intents at push time.
type stubPusher struct {
	s       *store.Store
	reqs    []publisher.PushRequest
	modes   []ports.WriteMode
	pending []model.Intent
	err     error
}

func (p *stubPusher) Push(ctx context.Context, mode ports.WriteMode, r publisher.PushRequest) error {
	p.reqs = append(p.reqs, r)
	p.modes = append(p.modes, mode)
	pending, err := p.s.PendingIntents(ctx)
	if err != nil {
		return err
	}
	p.pending = append(p.pending, pending...)
	return p.err
}

func TestIntents_Pusher(t *testing.T) {
	key := intentTestKey(t)
	s := openIntentStore(t)
	rec := &recordingStore{next: s}
	p := &stubPusher{s: s}
	pu := newTestIntents(t, rec, ports.WriteModeLive).Pusher(key, p)
	req := publisher.PushRequest{Clone: "/clone", Branch: "ddf/x", OldSHA: "old", NewSHA: "new"}
	if err := pu.Push(context.Background(), ports.WriteModeLive, req); err != nil {
		t.Fatal(err)
	}
	if len(p.reqs) != 1 || p.reqs[0] != req || p.modes[0] != ports.WriteModeLive {
		t.Fatalf("pushes = %+v modes = %v", p.reqs, p.modes)
	}
	if len(p.pending) != 1 || p.pending[0].Operation != model.IntentOperationPush {
		t.Fatalf("pending at push = %+v", p.pending)
	}
	c := rec.created[0]
	if c.TargetRef != "refs/heads/ddf/x" || c.ExpectedOldSHA != "old" || c.HeadSHA != "new" {
		t.Fatalf("created = %+v", c)
	}
	if string(c.Args) != `{"branch":"ddf/x","oldSha":"old","newSha":"new"}` {
		t.Fatalf("args = %s, want no clone", c.Args)
	}
	if rec.resolved[0].out != (store.IntentOutcome{NewSHA: "new"}) || rec.resolved[0].status != model.IntentStatusSucceeded {
		t.Fatalf("resolved = %+v", rec.resolved[0])
	}
	pendingNone(t, s)
}

func TestIntents_Pusher_RefusesOtherMode(t *testing.T) {
	s := openIntentStore(t)
	p := &stubPusher{s: s}
	pu := newTestIntents(t, s, ports.WriteModeDryRun).Pusher(intentTestKey(t), p)
	if err := pu.Push(context.Background(), ports.WriteModeLive, publisher.PushRequest{Branch: "b"}); err == nil {
		t.Fatal("Push in another mode: want error")
	}
	if len(p.reqs) != 0 {
		t.Fatal("inner pusher called")
	}
	pendingNone(t, s)
}

func TestIntents_ChangeWriter(t *testing.T) {
	key := intentTestKey(t)
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint("existing=", existing), func(t *testing.T) {
			s := openIntentStore(t)
			f := newStubForge(t, s)
			in := newTestIntents(t, s, ports.WriteModeLive)
			cw := in.ChangeWriter(key, in.Forge(f), existing)
			info, err := cw.PublishChange(context.Background(), ports.WriteModeLive, ports.PublishRequest{Key: key, Branch: "ddf/x", HeadSHA: "h"})
			if err != nil {
				t.Fatal(err)
			}
			want := model.IntentOperationOpenChange
			if existing {
				want = model.IntentOperationUpdateChange
			}
			if info.ID != "c-1" || f.calls["PublishChange"] != 1 || len(f.pending) != 1 || f.pending[0].Operation != want {
				t.Fatalf("info = %+v calls = %v pending = %+v", info, f.calls, f.pending)
			}
		})
	}
}

func TestIntents_ChangeWriter_RefusesMismatch(t *testing.T) {
	key := intentTestKey(t)
	other, err := model.NewTaskKey(model.TaskSource{Forge: "local", Repository: "demo", SourceKind: "task", SourceID: "2"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		mode ports.WriteMode
		key  model.TaskKey
	}{
		{"other mode", ports.WriteModeDryRun, key},
		{"other key", ports.WriteModeLive, other},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openIntentStore(t)
			f := newStubForge(t, s)
			in := newTestIntents(t, s, ports.WriteModeLive)
			cw := in.ChangeWriter(key, in.Forge(f), false)
			if _, err := cw.PublishChange(context.Background(), tc.mode, ports.PublishRequest{Key: tc.key, Branch: "b"}); err == nil {
				t.Fatal("want error")
			}
			if len(f.calls) != 0 {
				t.Fatalf("forge calls = %v, want none", f.calls)
			}
		})
	}
}
