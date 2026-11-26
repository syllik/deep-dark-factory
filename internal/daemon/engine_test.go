package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/config"
	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/store"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/daemon"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

const (
	engProject  = "demo"
	engBranch   = "main"
	engProvider = "fakeprov"
	engTemplate = "{task}\n\n{summary}\n\n{verify}\n\n{agentNotes}\n"
)

var (
	engStart    = time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	engIdentity = git.Identity{Name: "DDF Test Daemon", Email: "daemon@example.invalid"}
)

const (
	plannerReady = `{"schema":"ddf.planner.result/v1","verdict":"READY","spec":"Add hello.txt with a greeting.",` +
		`"acceptance":["hello.txt exists"],"riskProposal":"low","zones":["greetings"],` +
		`"agentDefaults":[{"question":"Which greeting?","choice":"hello"}]}`
	executorComplete = `{"schema":"ddf.executor.result/v1","verdict":"COMPLETE","summary":"Added hello.txt.",` +
		`"verify":{"commands":["make verify"],"passed":true}}`
)

// engGitPath is the host git; tests fail, never skip, without it.
func engGitPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err == nil {
		p, err = filepath.Abs(p)
	}
	if err != nil {
		t.Fatalf("git not found: %v", err)
	}
	return p
}

func engRunner(t *testing.T) *git.Runner {
	t.Helper()
	r, err := git.NewRunner(t.Context(), engGitPath(t), filepath.Join(t.TempDir(), "git-state"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

// engGit runs git through r in dir with a test identity and returns the
// trimmed stdout.
func engGit(t *testing.T, r *git.Runner, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=test", "-c", "user.email=test@example.invalid"}, args...)
	out, err := r.Run(context.Background(), dir, nil, full...)
	if err != nil {
		var ce *git.CommandError
		errors.As(err, &ce)
		t.Fatalf("git %v: %v %+v", args, err, ce)
	}
	return strings.TrimSpace(string(out))
}

func engFileURL(p string) string { return (&url.URL{Scheme: "file", Path: p}).String() }

// engSeedRepo makes a bare remote with one commit on the integration branch
// and returns its path and that commit.
func engSeedRepo(t *testing.T, r *git.Runner) (bare, mainSHA string) {
	t.Helper()
	dir := t.TempDir()
	bare, work := filepath.Join(dir, "remote.git"), filepath.Join(dir, "work")
	engGit(t, r, dir, "init", "-q", "--bare", "--initial-branch="+engBranch, bare)
	engGit(t, r, dir, "init", "-q", "--initial-branch="+engBranch, work)
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engGit(t, r, work, "add", "--", "README")
	engGit(t, r, work, "commit", "-q", "-m", "seed")
	engGit(t, r, work, "push", "-q", engFileURL(bare), "refs/heads/"+engBranch+":refs/heads/"+engBranch)
	return bare, engGit(t, r, work, "rev-parse", "HEAD")
}

// remoteHeads lists refs/heads of the bare repository with their SHAs.
func remoteHeads(t *testing.T, r *git.Runner, bare string) string {
	t.Helper()
	return engGit(t, r, bare, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/")
}

// recEvent is one recorded intent or write call.
type recEvent struct {
	kind   string // create, resolve or write
	op     model.IntentOperation
	mode   ports.WriteMode
	dryRun bool
	status model.IntentStatus
}

func (e recEvent) String() string {
	switch e.kind {
	case "create":
		return fmt.Sprintf("create %s dryRun=%v", e.op, e.dryRun)
	case "resolve":
		return fmt.Sprintf("resolve %s %s", e.op, e.status)
	}
	return fmt.Sprintf("write %s %s", e.op, e.mode)
}

// recorder records, in call order, every intent the engine creates and
// resolves, every forge and pusher write and every stored task state.
type recorder struct {
	mu      sync.Mutex
	events  []recEvent
	ops     map[string]model.IntentOperation
	pending map[string]bool
	states  []model.TaskState
	// outside counts writes made with no pending intent of their operation.
	outside []string
}

func newRecorder() *recorder {
	return &recorder{ops: map[string]model.IntentOperation{}, pending: map[string]bool{}}
}

func (r *recorder) write(op model.IntentOperation, mode ports.WriteMode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recEvent{kind: "write", op: op, mode: mode})
	for id, open := range r.pending {
		if open && r.ops[id] == op {
			return
		}
	}
	r.outside = append(r.outside, string(op))
}

func (r *recorder) eventStrings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.String())
	}
	return out
}

// recStore wraps the file store.
type recStore struct {
	*store.Store
	rec *recorder
}

func (s recStore) CreateIntent(ctx context.Context, in model.Intent) error {
	if err := s.Store.CreateIntent(ctx, in); err != nil {
		return err
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.ops[in.ID] = in.Operation
	s.rec.pending[in.ID] = true
	s.rec.events = append(s.rec.events, recEvent{kind: "create", op: in.Operation, dryRun: in.DryRun})
	return nil
}

func (s recStore) ResolveIntent(ctx context.Context, id string, status model.IntentStatus, out store.IntentOutcome, at time.Time) error {
	if err := s.Store.ResolveIntent(ctx, id, status, out, at); err != nil {
		return err
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.pending[id] = false
	s.rec.events = append(s.rec.events, recEvent{kind: "resolve", op: s.rec.ops[id], status: status})
	return nil
}

func (s recStore) CreateTask(ctx context.Context, t model.Task, ev model.Event) error {
	if err := s.Store.CreateTask(ctx, t, ev); err != nil {
		return err
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.states = append(s.rec.states, t.State)
	return nil
}

func (s recStore) UpdateTask(ctx context.Context, t model.Task, ev model.Event) error {
	if err := s.Store.UpdateTask(ctx, t, ev); err != nil {
		return err
	}
	s.rec.mu.Lock()
	defer s.rec.mu.Unlock()
	s.rec.states = append(s.rec.states, t.State)
	return nil
}

// recForge wraps the forge and records every write. RerunChecks and
// SetProjectionLabels have no intent operation; they record as an empty
// operation, which never has a pending intent.
type recForge struct {
	ports.Forge
	rec *recorder
}

func (f recForge) CreateTask(ctx context.Context, mode ports.WriteMode, t ports.NewTask) (model.TaskSource, error) {
	f.rec.write(model.IntentOperationCreateTask, mode)
	return f.Forge.CreateTask(ctx, mode, t)
}

func (f recForge) PublishChange(ctx context.Context, mode ports.WriteMode, r ports.PublishRequest) (ports.ChangeInfo, error) {
	f.rec.write(model.IntentOperationOpenChange, mode)
	return f.Forge.PublishChange(ctx, mode, r)
}

func (f recForge) UpdateChangeBody(ctx context.Context, mode ports.WriteMode, changeID, body string) error {
	f.rec.write(model.IntentOperationUpdateChange, mode)
	return f.Forge.UpdateChangeBody(ctx, mode, changeID, body)
}

func (f recForge) UpdateBranchFromBase(ctx context.Context, mode ports.WriteMode, changeID string) (string, error) {
	f.rec.write(model.IntentOperationUpdateBranch, mode)
	return f.Forge.UpdateBranchFromBase(ctx, mode, changeID)
}

func (f recForge) RerunChecks(ctx context.Context, mode ports.WriteMode, headSHA string) error {
	f.rec.write("", mode)
	return f.Forge.RerunChecks(ctx, mode, headSHA)
}

func (f recForge) PostReview(ctx context.Context, mode ports.WriteMode, r ports.ReviewPost) error {
	f.rec.write(model.IntentOperationPostReview, mode)
	return f.Forge.PostReview(ctx, mode, r)
}

func (f recForge) Merge(ctx context.Context, mode ports.WriteMode, changeID, headSHA string) (string, error) {
	f.rec.write(model.IntentOperationMerge, mode)
	return f.Forge.Merge(ctx, mode, changeID, headSHA)
}

func (f recForge) Revert(ctx context.Context, mode ports.WriteMode, key model.TaskKey, mergeSHA string) (ports.ChangeInfo, error) {
	f.rec.write(model.IntentOperationRevert, mode)
	return f.Forge.Revert(ctx, mode, key, mergeSHA)
}

func (f recForge) CloseTask(ctx context.Context, mode ports.WriteMode, src model.TaskSource, comment string) error {
	f.rec.write(model.IntentOperationCloseTask, mode)
	return f.Forge.CloseTask(ctx, mode, src, comment)
}

func (f recForge) ReopenTask(ctx context.Context, mode ports.WriteMode, src model.TaskSource, comment string) error {
	f.rec.write(model.IntentOperationReopenTask, mode)
	return f.Forge.ReopenTask(ctx, mode, src, comment)
}

func (f recForge) SetProjectionLabels(ctx context.Context, mode ports.WriteMode, src model.TaskSource, labels []string) error {
	f.rec.write("", mode)
	return f.Forge.SetProjectionLabels(ctx, mode, src, labels)
}

// recPusher wraps the daemon's git pusher.
type recPusher struct {
	inner publisher.Pusher
	rec   *recorder
}

func (p recPusher) Push(ctx context.Context, mode ports.WriteMode, r publisher.PushRequest) error {
	p.rec.write(model.IntentOperationPush, mode)
	return p.inner.Push(ctx, mode, r)
}

// engEnv is one engine on forge-local over a bare repository, with
// sandbox-fake, the scripted fake agent, a fake clock, IDs and secrets and
// a file store.
type engEnv struct {
	git      *git.Runner
	bare     string
	dataDir  string
	forge    *local.Forge
	store    *store.Store
	rec      *recorder
	agent    *fakes.Agent
	clock    *fakes.Clock
	settings daemon.Settings
	engine   *daemon.Engine
}

// newEngEnv builds an engine in mode; adjust, when set, changes the
// settings and deps before New.
func newEngEnv(t *testing.T, mode ports.WriteMode, adjust func(*daemon.Settings, *daemon.Deps)) *engEnv {
	t.Helper()
	ctx := t.Context()
	r := engRunner(t)
	bare, _ := engSeedRepo(t, r)
	daemonDir := t.TempDir()
	scratch := filepath.Join(daemonDir, "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	cloneDir := filepath.Join(daemonDir, "clone.git")
	host := fake.NewHost(ports.Capacity{CPUMillis: 1 << 40, MemoryBytes: 1 << 50, DiskBytes: 1 << 50})
	sb := host.Sandbox("ddfd")
	clock := fakes.NewClock(engStart)
	ids := fakes.NewIDs("id")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dataDir := t.TempDir()
	f, err := local.Open(ctx, local.Config{
		ProjectID: engProject, DataDir: dataDir, RemoteURL: engFileURL(bare), Clock: clock, IDs: ids, Logger: logger,
		IntegrationBranch: engBranch, CloneDir: cloneDir, RunDir: filepath.Join(daemonDir, "runs"), Git: r, Sandbox: sb,
		Verify: []string{"make verify"}, BranchPattern: "{type}/issue-{n}-{slug}", Identity: engIdentity,
	})
	if err != nil {
		t.Fatalf("open forge-local: %v", err)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "ddf.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	v, err := model.NewResultValidator()
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	agent := fakes.NewAgent(v)

	var project config.Project
	project.IntegrationBranch = engBranch
	project.Verify = []string{"make verify"}
	project.Planning.MaxAutoChildren = 3
	var routing config.Routing
	routing.Tiers.Standard = []config.Entry{{Adapter: "fake", Provider: engProvider, Model: "fake-model"}}
	routing.Limits.MaxSubagentsPerRun = 3
	s := daemon.Settings{
		Project: project, Profile: policy.Profile{Autonomy: policy.AutonomyPropose, LowMaxLines: policy.DefaultLowMaxLines},
		Routing: routing, AllowedActors: []string{local.LocalOperator.ID}, Limits: lifecycle.DefaultLimits(),
		Mode: mode, Identity: engIdentity, CloneDir: cloneDir, ScratchDir: scratch, ChangeTemplate: engTemplate,
	}
	d := daemon.Deps{
		Store: recStore{Store: st, rec: rec}, Forge: recForge{Forge: f, rec: rec}, Agent: agent,
		Secrets: fakes.NewSecrets(map[string]string{"agent/" + engProvider: "test-secret-agent"}),
		Clock:   clock, IDs: ids, Git: r,
		Publisher: &publisher.Publisher{Git: r, Clone: cloneDir, Scratch: scratch, Sandbox: sb, Clock: clock, IDs: ids},
		Pusher:    recPusher{inner: daemon.NewGitPusher(r, logger), rec: rec},
		Checks:    f,
		Ingest: func(ctx context.Context, mode ports.WriteMode) error {
			_, err := f.Ingest(ctx, mode)
			return err
		},
		Validator: v, Logger: logger,
	}
	if adjust != nil {
		adjust(&s, &d)
	}
	e, err := daemon.New(s, d)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &engEnv{git: r, bare: bare, dataDir: dataDir, forge: f, store: st, rec: rec, agent: agent, clock: clock,
		settings: s, engine: e}
}

// addInbox writes one inbox task file.
func (e *engEnv) addInbox(t *testing.T) {
	t.Helper()
	p := filepath.Join(e.dataDir, "forge-local", engProject, "inbox", "0001-hello.task.md")
	if err := os.WriteFile(p, []byte("# Add hello\n\nPlease add hello.txt.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// candidateKey returns the key of the only forge candidate.
func (e *engEnv) candidateKey(t *testing.T) model.TaskKey {
	t.Helper()
	cands, err := e.forge.ListCandidateTasks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("candidates = %d, want 1", len(cands))
	}
	key, err := model.NewTaskKey(cands[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func (e *engEnv) scriptHappyPath() {
	e.agent.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(plannerReady),
		Usage: ports.AgentUsage{ModelID: "fake-model", InputTokens: 10, OutputTokens: 5}})
	e.agent.Script(model.RoleExecutor, fakes.AgentStep{Result: []byte(executorComplete),
		Edits: []fakes.FileEdit{{Path: "hello.txt", Content: []byte("hello\n")}}})
}

// tickUntil ticks until the task of key is in want, at most n times, and
// returns the task.
func (e *engEnv) tickUntil(t *testing.T, n int, want model.TaskState) model.Task {
	t.Helper()
	var task model.Task
	for i := range n {
		if err := e.engine.Tick(t.Context()); err != nil {
			t.Fatalf("tick %d: %v", i+1, err)
		}
		cands, err := e.forge.ListCandidateTasks(t.Context())
		if err != nil || len(cands) != 1 {
			t.Fatalf("candidates: %v %v", cands, err)
		}
		key, err := model.NewTaskKey(cands[0].Source)
		if err != nil {
			t.Fatal(err)
		}
		task, err = e.store.GetTask(t.Context(), key)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if task.State == want {
			return task
		}
	}
	t.Fatalf("after %d ticks task is %s %v, want %s", n, task.State, task.Flags, want)
	return task
}

func TestTick_HappyPathToReview(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	e.addInbox(t)
	e.scriptHappyPath()
	task := e.tickUntil(t, 10, model.TaskStateReview)

	wantStates := []model.TaskState{model.TaskStateInbox, model.TaskStatePlanning, model.TaskStateReady,
		model.TaskStateRunning, model.TaskStatePublishing, model.TaskStateCI, model.TaskStateReview}
	if !slices.Equal(e.rec.states, wantStates) {
		t.Errorf("stored states = %v, want %v", e.rec.states, wantStates)
	}
	events, err := e.store.ListEvents(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	wantTypes := []string{"intake", "transition", "transition", "transition", "transition", "transition", "transition"}
	if !slices.Equal(types, wantTypes) {
		t.Errorf("event types = %v, want %v", types, wantTypes)
	}
	if ev := events[2]; ev.Role != model.RolePlanner || ev.Model != "fake-model" || ev.Usage == nil || ev.Usage.InputTokens != 10 {
		t.Errorf("planning event = %+v, want planner run with model and usage", ev)
	}
	if ev := events[4]; ev.Role != model.RoleExecutor {
		t.Errorf("publishing event role = %q, want executor", ev.Role)
	}
	if len(task.Flags) != 0 || task.HitStop {
		t.Errorf("task flags %v hitStop %v, want none", task.Flags, task.HitStop)
	}
	if task.Spec == "" || task.PlannerRisk != model.RiskClassLow || !slices.Equal(task.Zones, []string{"greetings"}) ||
		len(task.AgentDefaults) != 1 || task.AgentDefaults[0].Choice != "hello" || task.Risk == "" {
		t.Errorf("planner fields not stored: %+v", task)
	}

	change, found, err := e.forge.FindChangeByTaskKey(t.Context(), task.Key)
	if err != nil || !found {
		t.Fatalf("FindChangeByTaskKey: found %v, %v", found, err)
	}
	branch := "ddf/task-" + string(task.Key)[:12]
	tip := engGit(t, e.git, e.bare, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if !change.Open || change.Branch != branch || change.HeadSHA != tip {
		t.Errorf("change = %+v, want open on %s with head %s", change, branch, tip)
	}
	if got := engGit(t, e.git, e.bare, "show", tip+":hello.txt"); got != "hello" {
		t.Errorf("pushed hello.txt = %q", got)
	}

	wantEvents := []string{
		"create push dryRun=false", "write push live", "resolve push succeeded",
		"create open-change dryRun=false", "write open-change live", "resolve open-change succeeded",
	}
	if got := e.rec.eventStrings(); !slices.Equal(got, wantEvents) {
		t.Errorf("intent and write calls = %v, want %v", got, wantEvents)
	}
	if len(e.rec.outside) != 0 {
		t.Errorf("writes outside an intent: %v", e.rec.outside)
	}
	pending, err := e.store.PendingIntents(t.Context())
	if err != nil || len(pending) != 0 {
		t.Errorf("pending intents = %v, %v; want none", pending, err)
	}
	if n := len(e.agent.Calls()); n != 2 {
		t.Errorf("agent calls = %d, want 2", n)
	}
	for _, c := range e.agent.Calls() {
		if c.Entry.Provider != engProvider || c.Key != task.Key || c.Limits.MaxSubagentsPerRun != 3 {
			t.Errorf("agent request = %+v", c)
		}
	}
	if entries, err := os.ReadDir(e.settings.ScratchDir); err != nil || len(entries) != 0 {
		t.Errorf("scratch dir entries = %v, %v; want none (run copy removed)", entries, err)
	}
}

func TestTick_DryRunNoForgeWrites(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeDryRun, nil)
	e.addInbox(t)
	// Ingest writes forge-local records, so the inbox is ingested live
	// before the dry-run engine runs.
	if _, err := e.forge.Ingest(t.Context(), ports.WriteModeLive); err != nil {
		t.Fatal(err)
	}
	e.scriptHappyPath()
	headsBefore := remoteHeads(t, e.git, e.bare)
	task := e.tickUntil(t, 10, model.TaskStateCI)
	// More ticks leave it in ci: no change exists to check.
	for range 2 {
		if err := e.engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	task, err := e.store.GetTask(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateCI || len(task.Flags) != 0 {
		t.Errorf("task = %s %v, want ci without flags", task.State, task.Flags)
	}
	if _, found, err := e.forge.FindChangeByTaskKey(t.Context(), task.Key); err != nil || found {
		t.Errorf("FindChangeByTaskKey found=%v err=%v, want not found", found, err)
	}
	if after := remoteHeads(t, e.git, e.bare); after != headsBefore {
		t.Errorf("remote heads changed:\n%s\nwant\n%s", after, headsBefore)
	}
	wantEvents := []string{
		"create push dryRun=true", "write push dry-run", "resolve push succeeded",
		"create open-change dryRun=true", "write open-change dry-run", "resolve open-change succeeded",
	}
	if got := e.rec.eventStrings(); !slices.Equal(got, wantEvents) {
		t.Errorf("intent and write calls = %v, want %v", got, wantEvents)
	}
	for _, ev := range e.rec.events {
		if ev.kind == "write" && ev.mode != ports.WriteModeDryRun {
			t.Errorf("write %s in mode %s, want dry-run", ev.op, ev.mode)
		}
		if ev.kind == "create" && !ev.dryRun {
			t.Errorf("intent %s has dryRun false", ev.op)
		}
	}
}

func TestTick_ActorNotAllowed(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, _ *daemon.Deps) {
		s.AllowedActors = []string{"someone-else"}
	})
	e.addInbox(t)
	e.scriptHappyPath()
	for range 2 {
		if err := e.engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	key := e.candidateKey(t)
	if _, err := e.store.GetTask(t.Context(), key); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetTask err = %v, want store.ErrNotFound", err)
	}
	if n := len(e.agent.Calls()); n != 0 {
		t.Errorf("agent calls = %d, want 0", n)
	}
}

func TestTick_PlannerAskSetsNeedsOwner(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	e.addInbox(t)
	e.agent.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(
		`{"schema":"ddf.planner.result/v1","verdict":"ASK","question":"Which file?"}`)})
	for range 3 {
		if err := e.engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	task, err := e.store.GetTask(t.Context(), e.candidateKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStatePlanning || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
		t.Errorf("task = %s %v, want planning with needs-owner", task.State, task.Flags)
	}
	// A flagged task is left alone.
	if err := e.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := len(e.agent.Calls()); n != 1 {
		t.Errorf("agent calls = %d, want 1", n)
	}
}

func TestTick_ExecutorBlockedSetsNeedsOwner(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	e.addInbox(t)
	e.agent.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(plannerReady)})
	e.agent.Script(model.RoleExecutor, fakes.AgentStep{Result: []byte(
		`{"schema":"ddf.executor.result/v1","verdict":"BLOCKED","summary":"Cannot.","blockedReason":"unclear",` +
			`"verify":{"commands":[],"passed":false}}`)})
	for range 5 {
		if err := e.engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	task, err := e.store.GetTask(t.Context(), e.candidateKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateRunning || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
		t.Errorf("task = %s %v, want running with needs-owner", task.State, task.Flags)
	}
	if got := e.rec.eventStrings(); len(got) != 0 {
		t.Errorf("intent and write calls = %v, want none", got)
	}
}

func TestNew_RefusesInvalidInputs(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	cases := []struct {
		name   string
		adjust func(*daemon.Settings, *daemon.Deps)
	}{
		{"zero write mode", func(s *daemon.Settings, _ *daemon.Deps) { s.Mode = "" }},
		{"relative clone dir", func(s *daemon.Settings, _ *daemon.Deps) { s.CloneDir = "clone.git" }},
		{"relative scratch dir", func(s *daemon.Settings, _ *daemon.Deps) { s.ScratchDir = "scratch" }},
		{"empty integration branch", func(s *daemon.Settings, _ *daemon.Deps) { s.Project.IntegrationBranch = "" }},
		{"zero planner timeout", func(s *daemon.Settings, _ *daemon.Deps) { s.Limits.PlannerTimeout = 0 }},
		{"invalid profile", func(s *daemon.Settings, _ *daemon.Deps) { s.Profile.Autonomy = "" }},
		{"nil store", func(_ *daemon.Settings, d *daemon.Deps) { d.Store = nil }},
		{"nil forge", func(_ *daemon.Settings, d *daemon.Deps) { d.Forge = nil }},
		{"nil agent", func(_ *daemon.Settings, d *daemon.Deps) { d.Agent = nil }},
		{"nil pusher", func(_ *daemon.Settings, d *daemon.Deps) { d.Pusher = nil }},
		{"nil validator", func(_ *daemon.Settings, d *daemon.Deps) { d.Validator = nil }},
		{"nil logger", func(_ *daemon.Settings, d *daemon.Deps) { d.Logger = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := e.settings
			d := daemon.Deps{Store: e.store, Forge: e.forge, Agent: e.agent, Secrets: fakes.NewSecrets(nil), Clock: e.clock,
				IDs: fakes.NewIDs("x"), Git: e.git, Publisher: &publisher.Publisher{}, Pusher: daemon.NewGitPusher(e.git, slog.Default()),
				Validator: &model.ResultValidator{}, Logger: slog.Default()}
			if _, err := daemon.New(s, d); err != nil {
				t.Fatalf("baseline New: %v", err)
			}
			c.adjust(&s, &d)
			if _, err := daemon.New(s, d); err == nil {
				t.Error("New succeeded, want error")
			}
		})
	}
}
