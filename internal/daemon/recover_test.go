package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/store"
	"github.com/syllik/deep-dark-factory/internal/daemon"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

// recoverStore wraps the engine store: it records intent outcomes, can
// fail ResolveIntent for chosen operations (leaving the intent pending),
// can hide tasks from GetTask and counts every store write.
type recoverStore struct {
	daemon.Store
	mu          sync.Mutex
	ops         map[string]model.IntentOperation
	failResolve map[model.IntentOperation]bool
	hidden      map[model.TaskKey]bool
	statuses    map[string]model.IntentStatus
	outcomes    map[string]store.IntentOutcome
	writes      int
}

func newRecoverStore(inner daemon.Store) *recoverStore {
	return &recoverStore{Store: inner, ops: map[string]model.IntentOperation{}, failResolve: map[model.IntentOperation]bool{},
		hidden: map[model.TaskKey]bool{}, statuses: map[string]model.IntentStatus{}, outcomes: map[string]store.IntentOutcome{}}
}

func (s *recoverStore) wrote() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
}

func (s *recoverStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func (s *recoverStore) setFailResolve(op model.IntentOperation, fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failResolve[op] = fail
}

func (s *recoverStore) hide(key model.TaskKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hidden[key] = true
}

func (s *recoverStore) outcome(id string) (model.IntentStatus, store.IntentOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statuses[id], s.outcomes[id]
}

func (s *recoverStore) CreateIntent(ctx context.Context, in model.Intent) error {
	s.wrote()
	if err := s.Store.CreateIntent(ctx, in); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops[in.ID] = in.Operation
	return nil
}

func (s *recoverStore) ResolveIntent(ctx context.Context, id string, status model.IntentStatus, out store.IntentOutcome, at time.Time) error {
	s.wrote()
	s.mu.Lock()
	fail := s.failResolve[s.ops[id]]
	s.mu.Unlock()
	if fail {
		return errors.New("injected resolve fault")
	}
	if err := s.Store.ResolveIntent(ctx, id, status, out, at); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[id], s.outcomes[id] = status, out
	return nil
}

func (s *recoverStore) GetTask(ctx context.Context, key model.TaskKey) (model.Task, error) {
	s.mu.Lock()
	hidden := s.hidden[key]
	s.mu.Unlock()
	if hidden {
		return model.Task{}, fmt.Errorf("get task %s: %w", key, store.ErrNotFound)
	}
	return s.Store.GetTask(ctx, key)
}

func (s *recoverStore) CreateTask(ctx context.Context, t model.Task, ev model.Event) error {
	s.wrote()
	return s.Store.CreateTask(ctx, t, ev)
}

func (s *recoverStore) UpdateTask(ctx context.Context, t model.Task, ev model.Event) error {
	s.wrote()
	return s.Store.UpdateTask(ctx, t, ev)
}

func (s *recoverStore) CreateRun(ctx context.Context, r model.Run) error {
	s.wrote()
	return s.Store.CreateRun(ctx, r)
}

func (s *recoverStore) FinishRun(ctx context.Context, r model.Run) error {
	s.wrote()
	return s.Store.FinishRun(ctx, r)
}

func (s *recoverStore) CreateChange(ctx context.Context, c model.Change) error {
	s.wrote()
	return s.Store.CreateChange(ctx, c)
}

func (s *recoverStore) UpdateChange(ctx context.Context, c model.Change) error {
	s.wrote()
	return s.Store.UpdateChange(ctx, c)
}

func (s *recoverStore) CreateReview(ctx context.Context, r model.Review, findings []model.Finding) error {
	s.wrote()
	return s.Store.CreateReview(ctx, r, findings)
}

func (s *recoverStore) AppendEvent(ctx context.Context, ev model.Event) error {
	s.wrote()
	return s.Store.AppendEvent(ctx, ev)
}

// failingSandbox fails ListResources.
type failingSandbox struct {
	ports.Sandbox
}

func (failingSandbox) ListResources(context.Context, ports.ResourceFilter) ([]ports.Resource, error) {
	return nil, errors.New("injected list fault")
}

// recEnv is an engEnv whose first engine has no Lock, plus one sandbox
// fake Host and one fakes.Lock shared by every restarted engine.
type recEnv struct {
	*engEnv
	host *fake.Host
	lock *fakes.Lock
	rs   *recoverStore
	deps daemon.Deps
}

func newRecEnv(t *testing.T, mode ports.WriteMode) *recEnv {
	t.Helper()
	host := fake.NewHost(ports.Capacity{CPUMillis: 1 << 40, MemoryBytes: 1 << 50, DiskBytes: 1 << 50})
	r := &recEnv{host: host, lock: fakes.NewLock()}
	r.engEnv = newEngEnv(t, mode, func(_ *daemon.Settings, d *daemon.Deps) {
		r.rs = newRecoverStore(d.Store)
		d.Store = r.rs
		d.Sandbox = host.Sandbox("ddfd-1")
		r.deps = *d
	})
	return r
}

// restart returns a new engine on the same store, forge and host, as a
// restarted daemon instance named instance, with the shared Lock.
func (r *recEnv) restart(t *testing.T, instance string, adjust func(*daemon.Deps)) *daemon.Engine {
	t.Helper()
	d := r.deps
	d.Sandbox = r.host.Sandbox(instance)
	d.Lock = r.lock
	if adjust != nil {
		adjust(&d)
	}
	eng, err := daemon.New(r.settings, d)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return eng
}

// recoverOK runs Recover on eng and registers its release.
func recoverOK(t *testing.T, eng *daemon.Engine) func() error {
	t.Helper()
	rel, err := eng.Recover(t.Context())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	var once sync.Once
	release := func() error {
		var err error
		once.Do(func() { err = rel() })
		return err
	}
	t.Cleanup(func() { _ = release() })
	return release
}

// intake ticks the first engine once and returns the key of the new task.
func (r *recEnv) intake(t *testing.T) model.Task {
	t.Helper()
	r.addInbox(t)
	if err := r.engine.Tick(t.Context()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	r.key = r.candidateKey(t)
	task, err := r.store.GetTask(t.Context(), r.key)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// runningRun stores a running run of role for key.
func (r *recEnv) runningRun(t *testing.T, key model.TaskKey, id string, role model.Role, purpose model.RunPurpose) {
	t.Helper()
	run := model.Run{Key: key, ID: id, Role: role, Purpose: purpose, Attempt: 1, Status: model.RunStatusRunning,
		Provider: engProvider, Routing: model.RoutingEntry{Adapter: "fake", Provider: engProvider, Model: "fake-model"},
		StartedAt: r.clock.Now()}
	if err := r.store.CreateRun(t.Context(), run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
}

// cell creates a labelled cell on the shared host, as the crashed instance.
func (r *recEnv) cell(t *testing.T, key model.TaskKey, runID string) {
	t.Helper()
	if _, err := r.host.Sandbox("ddfd-1").CreateCell(t.Context(), ports.CellSpec{Key: key, RunID: runID}); err != nil {
		t.Fatalf("CreateCell: %v", err)
	}
}

// pendingIntent stores a pending intent in for the task key.
func (r *recEnv) pendingIntent(t *testing.T, in model.Intent) model.Intent {
	t.Helper()
	in.Status = model.IntentStatusPending
	in.Marker = daemon.Marker(in.Key)
	in.CreatedAt = r.clock.Now()
	if err := r.store.CreateIntent(t.Context(), in); err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	return in
}

func (r *recEnv) pending(t *testing.T) []model.Intent {
	t.Helper()
	p, err := r.store.PendingIntents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (r *recEnv) run(t *testing.T, id string) model.Run {
	t.Helper()
	run, err := r.store.GetRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (r *recEnv) countEvents(s string) int {
	n := 0
	for _, ev := range r.rec.eventStrings() {
		if ev == s {
			n++
		}
	}
	return n
}

// tickEngine ticks eng until the task of key is in want, at most n times.
func (r *recEnv) tickEngine(t *testing.T, eng *daemon.Engine, n int, want model.TaskState) model.Task {
	t.Helper()
	var task model.Task
	for i := range n {
		if err := eng.Tick(t.Context()); err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		var err error
		if task, err = r.store.GetTask(t.Context(), r.key); err != nil {
			t.Fatal(err)
		}
		if task.State == want {
			return task
		}
	}
	t.Fatalf("after %d ticks task is %s %v, want %s", n, task.State, task.Flags, want)
	return task
}

// tickUntilError ticks the first engine until a tick fails.
func (r *recEnv) tickUntilError(t *testing.T, n int) error {
	t.Helper()
	for range n {
		if err := r.engine.Tick(t.Context()); err != nil {
			return err
		}
		if r.key == "" {
			if cands, err := r.forge.ListCandidateTasks(t.Context()); err == nil && len(cands) == 1 {
				r.key = r.candidateKey(t)
			}
		}
	}
	t.Fatalf("no tick failed in %d ticks", n)
	return nil
}

func TestRecover_SecondInstanceLockedWritesNothing(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	task := r.intake(t)
	release := recoverOK(t, r.restart(t, "ddfd-2", nil))

	r.runningRun(t, task.Key, "run-live", model.RolePlanner, model.RunPurposePlanning)
	r.cell(t, task.Key, "run-live")
	writes, stats := r.rs.writeCount(), r.host.Stats()

	if _, err := r.restart(t, "ddfd-3", nil).Recover(t.Context()); !errors.Is(err, ports.ErrLocked) {
		t.Fatalf("second Recover err = %v, want ports.ErrLocked", err)
	}
	if got := r.rs.writeCount(); got != writes {
		t.Errorf("store writes = %d, want %d (none by the locked Recover)", got, writes)
	}
	if got := r.host.Stats(); got != stats {
		t.Errorf("host stats = %+v, want %+v", got, stats)
	}
	if run := r.run(t, "run-live"); run.Status != model.RunStatusRunning {
		t.Errorf("run status = %s, want running", run.Status)
	}

	// Once the holder releases, a new instance recovers.
	if err := release(); err != nil {
		t.Fatal(err)
	}
	recoverOK(t, r.restart(t, "ddfd-4", nil))
	if run := r.run(t, "run-live"); run.Status != model.RunStatusDiscarded {
		t.Errorf("run status after recovery = %s, want discarded", run.Status)
	}
}

func TestRecover_TickRequiresRecoverWithLock(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	r.addInbox(t)
	eng := r.restart(t, "ddfd-2", nil)
	if err := eng.Tick(t.Context()); err == nil {
		t.Fatal("Tick before Recover succeeded, want error")
	}
	if cands, err := r.forge.ListCandidateTasks(t.Context()); err != nil || len(cands) != 0 {
		t.Errorf("candidates = %v, %v; want none (refused tick ingests nothing)", cands, err)
	}
	recoverOK(t, eng)
	if err := eng.Tick(t.Context()); err != nil {
		t.Fatalf("Tick after Recover: %v", err)
	}
	// Without a Lock, Tick works without Recover.
	if err := r.engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick without a Lock: %v", err)
	}
}

func TestRecover_NilLockOrSandbox(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	if _, err := r.engine.Recover(t.Context()); err == nil {
		t.Error("Recover with nil Lock succeeded, want error")
	}
	d := r.deps
	d.Sandbox, d.Lock = nil, r.lock
	if _, err := daemon.New(r.settings, d); err == nil {
		t.Error("New with nil Sandbox succeeded, want error")
	}
	if rel, err := r.lock.TryAcquire(t.Context()); err != nil {
		t.Errorf("lock held after refused Recover: %v", err)
	} else if err := rel(); err != nil {
		t.Fatal(err)
	}
}

func TestRecover_ReadErrorFailsClosed(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	eng := r.restart(t, "ddfd-2", func(d *daemon.Deps) { d.Sandbox = failingSandbox{Sandbox: d.Sandbox} })
	if _, err := eng.Recover(t.Context()); err == nil || !strings.Contains(err.Error(), "injected list fault") {
		t.Fatalf("Recover err = %v, want the sandbox read error", err)
	}
	if err := eng.Tick(t.Context()); err == nil {
		t.Error("Tick after failed Recover succeeded, want error")
	}
	if rel, err := r.lock.TryAcquire(t.Context()); err != nil {
		t.Errorf("lock held after failed Recover: %v", err)
	} else if err := rel(); err != nil {
		t.Fatal(err)
	}
}

func TestRecover_RestartMidRun(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	r.addInbox(t)
	r.agent.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(plannerReady)})
	task := r.tickUntil(t, 10, model.TaskStateRunning)
	// The crashed instance stored a running executor run and its cell.
	r.runningRun(t, task.Key, "run-crashed", model.RoleExecutor, model.RunPurposeInitial)
	r.cell(t, task.Key, "run-crashed")

	eng := r.restart(t, "ddfd-2", nil)
	recoverOK(t, eng)
	if s := r.host.Stats(); s.Killed != 1 || s.Live != 0 {
		t.Errorf("host stats = %+v, want the crashed cell killed", s)
	}
	if run := r.run(t, "run-crashed"); run.Status != model.RunStatusDiscarded || run.FinishedAt == nil {
		t.Errorf("crashed run = %s finished %v, want discarded", run.Status, run.FinishedAt)
	}

	r.agent.Script(model.RoleExecutor, fakes.AgentStep{Result: []byte(executorComplete),
		Edits: []fakes.FileEdit{{Path: "hello.txt", Content: []byte("hello\n")}}})
	got := r.tickEngine(t, eng, 10, model.TaskStateReview)
	if len(got.Flags) != 0 {
		t.Errorf("task flags = %v, want none", got.Flags)
	}
	if cands, err := r.forge.ListCandidateTasks(t.Context()); err != nil || len(cands) != 1 {
		t.Errorf("candidates = %v, %v; want one task", cands, err)
	}
	if _, found, err := r.forge.FindChangeByTaskKey(t.Context(), task.Key); err != nil || !found {
		t.Errorf("FindChangeByTaskKey found=%v err=%v, want one change", found, err)
	}
	if changes, err := r.store.ListChanges(t.Context(), task.Key); err != nil || len(changes) != 1 {
		t.Errorf("stored changes = %v, %v; want one", changes, err)
	}
	if n := r.countEvents("create open-change dryRun=false"); n != 1 {
		t.Errorf("open-change intents = %d, want 1", n)
	}
}

// doneTask stores the intake task in done and gives it a cell.
func (r *recEnv) doneTask(t *testing.T) model.Task {
	t.Helper()
	task := r.intake(t)
	task.State, task.UpdatedAt = model.TaskStateDone, r.clock.Now()
	if err := r.store.UpdateTask(t.Context(), task, model.Event{Key: task.Key, Type: "transition", At: r.clock.Now()}); err != nil {
		t.Fatal(err)
	}
	r.cell(t, task.Key, "run-old")
	return task
}

func TestRecover_DoneTaskTeardown(t *testing.T) {
	t.Run("verified", func(t *testing.T) {
		r := newRecEnv(t, ports.WriteModeLive)
		task := r.doneTask(t)
		unknown, err := model.NewTaskKey(model.TaskSource{Forge: "local", Repository: "demo", SourceKind: "task", SourceID: "gone"})
		if err != nil {
			t.Fatal(err)
		}
		r.cell(t, unknown, "run-gone")
		recoverOK(t, r.restart(t, "ddfd-2", nil))
		if s := r.host.Stats(); s.Destroyed != 2 || s.Live != 0 {
			t.Errorf("host stats = %+v, want both cells destroyed", s)
		}
		if left, err := r.host.Sandbox("x").ListResources(t.Context(), ports.ResourceFilter{}); err != nil || len(left) != 0 {
			t.Errorf("resources left = %v, %v; want none", left, err)
		}
		got, err := r.store.GetTask(t.Context(), task.Key)
		if err != nil {
			t.Fatal(err)
		}
		if got.TeardownAt == nil || got.State != model.TaskStateDone {
			t.Errorf("task = %s teardownAt %v, want done with TeardownAt", got.State, got.TeardownAt)
		}
		events, err := r.store.ListEvents(t.Context(), task.Key)
		if err != nil {
			t.Fatal(err)
		}
		if last := events[len(events)-1]; last.Type != "teardown" {
			t.Errorf("last event = %q, want teardown", last.Type)
		}
	})
	t.Run("unverified", func(t *testing.T) {
		r := newRecEnv(t, ports.WriteModeLive)
		task := r.doneTask(t)
		r.host.FailNextDestroy(task.Key)
		recoverOK(t, r.restart(t, "ddfd-2", nil))
		got, err := r.store.GetTask(t.Context(), task.Key)
		if err != nil {
			t.Fatal(err)
		}
		if got.TeardownAt != nil {
			t.Errorf("TeardownAt = %v, want nil", got.TeardownAt)
		}
		global, err := r.store.ListEvents(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(global, func(ev model.Event) bool { return ev.Type == "teardown-unverified" }) {
			t.Errorf("global events = %v, want a teardown-unverified event", global)
		}
	})
}

func TestRecover_PendingOpenChangeAdopted(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	r.addInbox(t)
	r.scriptHappyPath()
	r.rs.setFailResolve(model.IntentOperationOpenChange, true)
	if err := r.tickUntilError(t, 10); !strings.Contains(err.Error(), "injected resolve fault") {
		t.Fatalf("tick error = %v, want the injected resolve fault", err)
	}
	r.rs.setFailResolve(model.IntentOperationOpenChange, false)
	pending := r.pending(t)
	if len(pending) != 1 || pending[0].Operation != model.IntentOperationOpenChange {
		t.Fatalf("pending intents = %+v, want one open-change", pending)
	}
	in := pending[0]
	if changes, err := r.store.ListChanges(t.Context(), r.key); err != nil || len(changes) != 0 {
		t.Fatalf("stored changes = %v, %v; want none before recovery", changes, err)
	}
	info, found, err := r.forge.FindChangeByTaskKey(t.Context(), r.key)
	if err != nil || !found {
		t.Fatalf("forge change found=%v err=%v", found, err)
	}

	eng := r.restart(t, "ddfd-2", nil)
	recoverOK(t, eng)
	if st, out := r.rs.outcome(in.ID); st != model.IntentStatusSucceeded || out.ObjectID != info.ID {
		t.Errorf("open-change outcome = %s %+v, want succeeded with %s", st, out, info.ID)
	}
	changes, err := r.store.ListChanges(t.Context(), r.key)
	if err != nil || len(changes) != 1 {
		t.Fatalf("stored changes = %v, %v; want one adopted", changes, err)
	}
	if c := changes[0]; c.ID != info.ID || c.Kind != model.ChangeKindChange || c.Revision != 1 || !c.Open ||
		c.HeadSHA != info.HeadSHA || c.Branch != info.Branch || c.PatchID == "" {
		t.Errorf("adopted change = %+v, want revision 1 of %+v", c, info)
	}
	for range 3 {
		if err := eng.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.countEvents("create open-change dryRun=false"); n != 1 {
		t.Errorf("open-change intents = %d, want 1", n)
	}
	if changes, err := r.store.ListChanges(t.Context(), r.key); err != nil || len(changes) != 1 {
		t.Errorf("stored changes = %v, %v; want one", changes, err)
	}
	if again, found, err := r.forge.FindChangeByTaskKey(t.Context(), r.key); err != nil || !found || again.ID != info.ID {
		t.Errorf("forge change = %+v found=%v err=%v, want only %s", again, found, err, info.ID)
	}
}

func TestRecover_PendingPush(t *testing.T) {
	t.Run("branch at head", func(t *testing.T) {
		r := newRecEnv(t, ports.WriteModeLive)
		r.addInbox(t)
		r.scriptHappyPath()
		r.rs.setFailResolve(model.IntentOperationPush, true)
		if err := r.tickUntilError(t, 10); !strings.Contains(err.Error(), "injected resolve fault") {
			t.Fatalf("tick error = %v, want the injected resolve fault", err)
		}
		r.rs.setFailResolve(model.IntentOperationPush, false)
		pending := r.pending(t)
		if len(pending) != 1 || pending[0].Operation != model.IntentOperationPush || pending[0].HeadSHA == "" {
			t.Fatalf("pending intents = %+v, want one push", pending)
		}
		in := pending[0]
		recoverOK(t, r.restart(t, "ddfd-2", nil))
		if st, out := r.rs.outcome(in.ID); st != model.IntentStatusSucceeded || out.NewSHA != in.HeadSHA {
			t.Errorf("push outcome = %s %+v, want succeeded with %s", st, out, in.HeadSHA)
		}
		if p := r.pending(t); len(p) != 0 {
			t.Errorf("pending intents = %+v, want none", p)
		}
	})
	t.Run("branch at expected old", func(t *testing.T) {
		r := newRecEnv(t, ports.WriteModeLive)
		task := r.intake(t)
		mainSHA := engGit(t, r.git, r.bare, "rev-parse", "refs/heads/"+engBranch)
		args, err := json.Marshal(map[string]string{"branch": engBranch, "oldSha": mainSHA, "newSha": strings.Repeat("b", 40)})
		if err != nil {
			t.Fatal(err)
		}
		in := r.pendingIntent(t, model.Intent{Key: task.Key, ID: "push-old", Operation: model.IntentOperationPush, Args: args,
			TargetRef: "refs/heads/" + engBranch, ExpectedOldSHA: mainSHA, HeadSHA: strings.Repeat("b", 40)})
		recoverOK(t, r.restart(t, "ddfd-2", nil))
		if st, out := r.rs.outcome(in.ID); st != model.IntentStatusFailed || out.ErrorClass != "not-applied" {
			t.Errorf("push outcome = %s %+v, want failed not-applied", st, out)
		}
	})
}

func TestRecover_PendingPostReviewNeedsOwner(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	task := r.intake(t)
	in := r.pendingIntent(t, model.Intent{Key: task.Key, ID: "review-1", Operation: model.IntentOperationPostReview,
		HeadSHA: strings.Repeat("c", 40)})
	release := recoverOK(t, r.restart(t, "ddfd-2", nil))
	got, err := r.store.GetTask(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) || got.State != task.State {
		t.Errorf("task = %s %v, want %s with needs-owner", got.State, got.Flags, task.State)
	}
	if p := r.pending(t); len(p) != 1 || p[0].ID != in.ID {
		t.Errorf("pending intents = %+v, want %s still pending", p, in.ID)
	}
	events, err := r.store.ListEvents(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	// A second recovery does not set the flag again.
	if err := release(); err != nil {
		t.Fatal(err)
	}
	recoverOK(t, r.restart(t, "ddfd-3", nil))
	again, err := r.store.ListEvents(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(events) {
		t.Errorf("events after second recovery = %d, want %d", len(again), len(events))
	}
}

func TestTick_PendingIntentHoldsStep(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	task := r.intake(t)
	in := r.pendingIntent(t, model.Intent{Key: task.Key, ID: "held-1", Operation: model.IntentOperationCreateTask})
	for range 2 {
		if err := r.engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.store.GetTask(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.TaskStateInbox {
		t.Errorf("task state = %s, want inbox (held by the pending intent)", got.State)
	}
	if err := r.store.ResolveIntent(t.Context(), in.ID, model.IntentStatusSucceeded, store.IntentOutcome{}, r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err = r.store.GetTask(t.Context(), task.Key); err != nil || got.State != model.TaskStatePlanning {
		t.Errorf("task = %s, %v; want planning once the intent resolved", got.State, err)
	}
}

func TestRecover_IntentOfUnknownTaskStaysPending(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	task := r.intake(t)
	in := r.pendingIntent(t, model.Intent{Key: task.Key, ID: "orphan-1", Operation: model.IntentOperationPostReview})
	r.rs.hide(task.Key)
	eng := r.restart(t, "ddfd-2", nil)
	_, err := eng.Recover(t.Context())
	if err == nil || !strings.Contains(err.Error(), in.ID) {
		t.Fatalf("Recover err = %v, want an error naming %s", err, in.ID)
	}
	if p := r.pending(t); len(p) != 1 || p[0].ID != in.ID {
		t.Errorf("pending intents = %+v, want %s still pending", p, in.ID)
	}
	if err := eng.Tick(t.Context()); err == nil {
		t.Error("Tick after failed Recover succeeded, want error")
	}
}

func TestRecover_DryRunIntentNotApplied(t *testing.T) {
	r := newRecEnv(t, ports.WriteModeLive)
	task := r.intake(t)
	in := r.pendingIntent(t, model.Intent{Key: task.Key, ID: "dry-1", Operation: model.IntentOperationOpenChange, DryRun: true,
		HeadSHA: strings.Repeat("d", 40)})
	recoverOK(t, r.restart(t, "ddfd-2", nil))
	if st, out := r.rs.outcome(in.ID); st != model.IntentStatusFailed || out.ErrorClass != "not-applied" {
		t.Errorf("dry-run outcome = %s %+v, want failed not-applied", st, out)
	}
	if got, err := r.store.GetTask(t.Context(), task.Key); err != nil || len(got.Flags) != 0 {
		t.Errorf("task flags = %v, %v; want none", got.Flags, err)
	}
}
