package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/store"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/daemon"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

const (
	projectID = "demo"
	branch    = "main"
	provider  = "example-provider"
	maxTicks  = 20
)

var identity = git.Identity{Name: "DDF E2E Daemon", Email: "daemon@example.invalid"}

// Scripted agent results; their shapes follow the role result schemas.
const (
	plannerReady = `{"schema":"ddf.planner.result/v1","verdict":"READY","spec":"Add hello.txt with a greeting.",` +
		`"acceptance":["hello.txt exists"],"riskProposal":"low","zones":["greetings"]}`
	executorComplete = `{"schema":"ddf.executor.result/v1","verdict":"COMPLETE","summary":"Added hello.txt.",` +
		`"verify":{"commands":["make verify"],"passed":true}}`
	reviewerApproveR1 = `{"schema":"ddf.reviewer.result/v1","role":"r1","verdict":"APPROVE","findings":[],` +
		`"hardStopChecklist":[{"item":"secrets","status":"clear"}]}`
	reviewerApproveR2 = `{"schema":"ddf.reviewer.result/v1","role":"r2","verdict":"APPROVE","findings":[],` +
		`"hardStopChecklist":[{"item":"secrets","status":"clear"}]}`
)

// env is one daemon opened through daemon.Open from the testdata config on
// a bare remote seeded with one commit, plus the outside observers.
type env struct {
	cfg    daemon.Config
	ports  daemon.Ports
	agent  *fakes.Agent
	daemon *daemon.Daemon
	git    *git.Runner
	bare   string
	seed   string
	gitBin string
	// obs is a second handle on the daemon's store file.
	obs *store.Store
	key model.TaskKey
}

func gitPath(t *testing.T) string {
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

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// signingKey returns a review-signing key as the Secrets port holds it: PEM
// PKCS#8 Ed25519 from a fixed test seed.
func signingKey(t *testing.T) string {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// runGit runs git through r in dir with a test identity and returns the
// trimmed stdout.
func runGit(t *testing.T, r *git.Runner, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=test", "-c", "user.email=test@example.invalid"}, args...)
	out, err := r.Run(context.Background(), dir, nil, full...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// newEnv seeds a bare remote, renders the testdata config with the project
// autonomy and the operator maximum, and opens the daemon.
func newEnv(t *testing.T, autonomy, maxAutonomy string) *env {
	t.Helper()
	ctx := t.Context()
	bin := gitPath(t)
	root := t.TempDir()
	r, err := git.NewRunner(ctx, bin, filepath.Join(root, "test-git-state"))
	if err != nil {
		t.Fatal(err)
	}
	bare, work := filepath.Join(root, "remote.git"), filepath.Join(root, "work")
	remote := (&url.URL{Scheme: "file", Path: bare}).String()
	runGit(t, r, root, "init", "-q", "--bare", "--initial-branch="+branch, bare)
	runGit(t, r, root, "init", "-q", "--initial-branch="+branch, work)
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, r, work, "add", "--", "README")
	runGit(t, r, work, "commit", "-q", "-m", "seed")
	runGit(t, r, work, "push", "-q", remote, "refs/heads/"+branch+":refs/heads/"+branch)
	seed := runGit(t, r, work, "rev-parse", "HEAD")

	daemonDir := filepath.Join(root, "daemon")
	dataDir := filepath.Join(daemonDir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	validator, err := model.NewResultValidator()
	if err != nil {
		t.Fatal(err)
	}
	agent := fakes.NewAgent(validator)
	e := &env{
		cfg: daemon.Config{
			ProjectYAML: bytes.ReplaceAll(readTestdata(t, "project.yaml"), []byte("AUTONOMY"), []byte(autonomy)),
			ProjectsYAML: []byte(strings.NewReplacer(
				"REMOTE_URL", remote, "MAX_AUTONOMY", maxAutonomy,
			).Replace(string(readTestdata(t, "projects.yaml")))),
			RoutingYAML: readTestdata(t, "routing.yaml"),
			ProjectID:   projectID,
			StorePath:   filepath.Join(daemonDir, "ddf.db"),
			DataDir:     dataDir,
			CloneDir:    filepath.Join(daemonDir, "clone"),
			RunDir:      filepath.Join(daemonDir, "runs"),
			ScratchDir:  filepath.Join(daemonDir, "scratch"),
			GitPath:     bin,
			GitStateDir: filepath.Join(daemonDir, "git-state"),
			Mode:        ports.WriteModeLive,
			Identity:    identity,
		},
		ports: daemon.Ports{
			Agent:   agent,
			Sandbox: fake.NewHost(ports.Capacity{CPUMillis: 1 << 40, MemoryBytes: 1 << 50, DiskBytes: 1 << 50}).Sandbox("ddfd"),
			Secrets: fakes.NewSecrets(map[string]string{
				"agent/" + provider:  "test-secret-agent",
				"review-signing-key": signingKey(t),
			}),
			Clock:  fakes.NewClock(time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)),
			IDs:    fakes.NewIDs("id"),
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
		agent:  agent,
		git:    r,
		bare:   bare,
		seed:   seed,
		gitBin: bin,
	}
	if err := os.Mkdir(e.cfg.ScratchDir, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := daemon.Open(ctx, e.cfg, e.ports)
	if err != nil {
		t.Fatalf("daemon.Open: %v", err)
	}
	e.daemon = d
	t.Cleanup(func() {
		if err := e.daemon.Close(); err != nil {
			t.Errorf("close daemon: %v", err)
		}
	})
	obs, err := store.Open(ctx, e.cfg.StorePath)
	if err != nil {
		t.Fatalf("open observer store: %v", err)
	}
	e.obs = obs
	t.Cleanup(func() {
		if err := obs.Close(); err != nil {
			t.Errorf("close observer store: %v", err)
		}
	})
	// The inbox file becomes forge-local task 1; its key is derived from
	// that source as every forge-local task key is.
	e.key, err = model.NewTaskKey(model.TaskSource{
		Forge: local.ForgeName, Repository: projectID, SourceKind: "task", SourceID: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// addInbox writes one task file into forge-local's inbox.
func (e *env) addInbox(t *testing.T) {
	t.Helper()
	p := filepath.Join(e.cfg.DataDir, "forge-local", projectID, "inbox", "0001-hello.task.md")
	if err := os.WriteFile(p, []byte("# Add hello\n\nPlease add hello.txt.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// script queues a READY plan, a COMPLETE executor run that writes
// hello.txt, and approvals from r1 and r2. A low-risk change needs only r1
// (spec §6.2); r2 is scripted too so the path holds for a standard class.
func (e *env) script() {
	e.agent.Script(model.RolePlanner, fakes.AgentStep{Result: []byte(plannerReady)})
	e.agent.Script(model.RoleExecutor, fakes.AgentStep{Result: []byte(executorComplete),
		Edits: []fakes.FileEdit{{Path: "hello.txt", Content: []byte("hello\n")}}})
	e.agent.Script(model.RoleR1, fakes.AgentStep{Result: []byte(reviewerApproveR1)})
	e.agent.Script(model.RoleR2, fakes.AgentStep{Result: []byte(reviewerApproveR2)})
}

func (e *env) tick(t *testing.T) {
	t.Helper()
	if err := e.daemon.Engine.Tick(t.Context()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

// task reads the task through the observer store handle; found is false
// before intake.
func (e *env) task(t *testing.T) (model.Task, bool) {
	t.Helper()
	task, err := e.obs.GetTask(t.Context(), e.key)
	if errors.Is(err, store.ErrNotFound) {
		return model.Task{}, false
	}
	if err != nil {
		t.Fatalf("observer GetTask: %v", err)
	}
	return task, true
}

// tickUntil ticks at most maxTicks times until the task is in want.
func (e *env) tickUntil(t *testing.T, want model.TaskState) model.Task {
	t.Helper()
	var last model.Task
	for range maxTicks {
		e.tick(t)
		task, found := e.task(t)
		if !found {
			continue
		}
		last = task
		if task.State == want {
			return task
		}
	}
	t.Fatalf("after %d ticks task is %q %v, want %s", maxTicks, last.State, last.Flags, want)
	return last
}

// integrationTip is the integration branch tip of the bare remote.
func (e *env) integrationTip(t *testing.T) string {
	t.Helper()
	return runGit(t, e.git, e.bare, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
}

// reopenForge closes the daemon and opens forge-local again on the same
// data directory, with its own clone and run directories, to read the
// forge's state from outside the engine.
func (e *env) reopenForge(t *testing.T) *local.Forge {
	t.Helper()
	if err := e.daemon.Close(); err != nil {
		t.Fatalf("close daemon: %v", err)
	}
	ctx := t.Context()
	dir := t.TempDir()
	r, err := git.NewRunner(ctx, e.gitBin, filepath.Join(dir, "git-state"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := local.Open(ctx, local.Config{
		ProjectID: projectID, DataDir: e.cfg.DataDir,
		RemoteURL: (&url.URL{Scheme: "file", Path: e.bare}).String(),
		Clock:     e.ports.Clock, IDs: fakes.NewIDs("observer"), Logger: e.ports.Logger,
		IntegrationBranch: branch, CloneDir: filepath.Join(dir, "clone"), RunDir: filepath.Join(dir, "runs"),
		Git: r, Sandbox: e.ports.Sandbox, Verify: []string{"make verify"},
		BranchPattern: "{type}/issue-{n}-{slug}", Identity: identity,
	})
	if err != nil {
		t.Fatalf("reopen forge-local: %v", err)
	}
	return f
}

// TestE2E_IntakeToMergeAtMergeIntegration: an inbox task on forge-local
// goes from intake through planning, execution, publishing, CI and review
// to a merge into the integration branch and a closed forge task, with the
// project and the operator both at autonomy merge-integration.
func TestE2E_IntakeToMergeAtMergeIntegration(t *testing.T) {
	e := newEnv(t, "merge-integration", "merge-integration")
	e.addInbox(t)
	e.script()

	task := e.tickUntil(t, model.TaskStateDone)
	if len(task.Flags) != 0 || task.HitStop {
		t.Errorf("task flags %v hitStop %v, want none", task.Flags, task.HitStop)
	}

	tip := e.integrationTip(t)
	if tip == e.seed {
		t.Fatalf("integration tip is still the seed %s, want a merge", e.seed)
	}
	if got := runGit(t, e.git, e.bare, "show", tip+":hello.txt"); got != "hello" {
		t.Errorf("hello.txt on the integration tip = %q, want %q", got, "hello")
	}
	if parent := runGit(t, e.git, e.bare, "rev-parse", tip+"^1"); parent != e.seed {
		t.Errorf("integration tip first parent = %s, want seed %s", parent, e.seed)
	}
	changes, err := e.obs.ListChanges(t.Context(), e.key)
	if err != nil || len(changes) != 1 {
		t.Fatalf("stored changes = %v, %v; want one", changes, err)
	}
	if c := changes[0]; c.MergeSHA != tip || c.Open {
		t.Errorf("stored change merge sha %q open %v, want merged at %s", c.MergeSHA, c.Open, tip)
	}

	f := e.reopenForge(t)
	if cands, err := f.ListCandidateTasks(t.Context()); err != nil || len(cands) != 0 {
		t.Errorf("forge candidates = %v, %v; want none (task closed)", cands, err)
	}
	if info, found, err := f.FindChangeByTaskKey(t.Context(), e.key); err != nil || !found || info.Open {
		t.Errorf("forge change = %+v found %v err %v, want found and closed", info, found, err)
	}
}

// TestE2E_ProposeStopsAtReadyChange: at autonomy propose the same task runs
// to a reviewed, ready change and stops in merge: the forge change stays
// open and the integration branch is not touched.
func TestE2E_ProposeStopsAtReadyChange(t *testing.T) {
	e := newEnv(t, "propose", "merge-integration")
	e.addInbox(t)
	e.script()

	e.tickUntil(t, model.TaskStateMerge)
	for range 2 {
		e.tick(t)
	}
	task, found := e.task(t)
	if !found {
		t.Fatal("task not in the store")
	}
	if task.State != model.TaskStateMerge || len(task.Flags) != 0 || task.HitStop {
		t.Errorf("task = %s %v hitStop %v, want merge without flags", task.State, task.Flags, task.HitStop)
	}
	if tip := e.integrationTip(t); tip != e.seed {
		t.Errorf("integration tip = %s, want the seed %s unchanged", tip, e.seed)
	}

	f := e.reopenForge(t)
	info, found, err := f.FindChangeByTaskKey(t.Context(), e.key)
	if err != nil || !found || !info.Open {
		t.Errorf("forge change = %+v found %v err %v, want open", info, found, err)
	}
	if cands, err := f.ListCandidateTasks(t.Context()); err != nil || len(cands) != 1 {
		t.Errorf("forge candidates = %v, %v; want the one open task", cands, err)
	}
}
