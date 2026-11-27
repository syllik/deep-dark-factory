package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/config"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/daemon"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

const wireTemplate = "{task}\n\n{summary}\n\n{verify}\n\n{agentNotes}\n"

// wireEnv is one Open fixture: a bare remote seeded with one commit on main
// (README and .ddf/change-template.md), the daemon's directories under
// t.TempDir() and the test fakes.
type wireEnv struct {
	cfg   daemon.Config
	ports daemon.Ports
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newWireEnv(t *testing.T) *wireEnv {
	t.Helper()
	ctx := context.Background()
	gitPath, err := exec.LookPath("git")
	if err == nil {
		gitPath, err = filepath.Abs(gitPath)
	}
	if err != nil {
		t.Fatalf("git not found: %v", err)
	}
	root := t.TempDir()
	seedRunner, err := git.NewRunner(ctx, gitPath, filepath.Join(root, "seed-git-state"))
	if err != nil {
		t.Fatal(err)
	}
	bare, work := filepath.Join(root, "remote.git"), filepath.Join(root, "work")
	if err := os.MkdirAll(filepath.Join(work, ".ddf"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"README": "base\n", ".ddf/change-template.md": wireTemplate} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	remote := (&url.URL{Scheme: "file", Path: bare}).String()
	for _, st := range []struct {
		dir  string
		args []string
	}{
		{root, []string{"init", "-q", "--bare", "--initial-branch=main", bare}},
		{root, []string{"init", "-q", "--initial-branch=main", work}},
		{work, []string{"remote", "add", "origin", remote}},
		{work, []string{"add", "--", "README", ".ddf/change-template.md"}},
		{work, []string{"-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "seed"}},
		{work, []string{"push", "-q", "origin", "refs/heads/main:refs/heads/main"}},
	} {
		if _, err := seedRunner.Run(ctx, st.dir, nil, st.args...); err != nil {
			t.Fatalf("git %v: %v", st.args, err)
		}
	}
	daemonDir := filepath.Join(root, "daemon")
	dataDir := filepath.Join(daemonDir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	validator, err := model.NewResultValidator()
	if err != nil {
		t.Fatal(err)
	}
	return &wireEnv{
		cfg: daemon.Config{
			ProjectYAML:  readTestdata(t, "project.yaml"),
			ProjectsYAML: bytes.ReplaceAll(readTestdata(t, "projects.yaml"), []byte("REMOTE_URL"), []byte(remote)),
			RoutingYAML:  readTestdata(t, "routing.yaml"),
			ProjectID:    "demo",
			StorePath:    filepath.Join(daemonDir, "ddf.db"),
			DataDir:      dataDir,
			CloneDir:     filepath.Join(daemonDir, "clone"),
			RunDir:       filepath.Join(daemonDir, "runs"),
			ScratchDir:   filepath.Join(daemonDir, "scratch"),
			GitPath:      gitPath,
			GitStateDir:  filepath.Join(daemonDir, "git-state"),
			Mode:         ports.WriteModeLive,
			Identity:     git.Identity{Name: "DDF Test Daemon", Email: "daemon@example.invalid"},
		},
		ports: daemon.Ports{
			Agent:   fakes.NewAgent(validator),
			Sandbox: fake.NewHost(ports.Capacity{CPUMillis: 4000, MemoryBytes: 8 << 30, DiskBytes: 64 << 30}).Sandbox("ddfd"),
			Secrets: fakes.NewSecrets(map[string]string{"agent/example-provider": "test-secret-agent"}),
			Clock:   fakes.NewClock(time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)),
			IDs:     fakes.NewIDs("id"),
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	}
}

func TestOpen_ValidConfig(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*daemon.Config)
	}{
		{"change template read from the integration branch", func(*daemon.Config) {}},
		{"built-in change template when change.template is absent", func(c *daemon.Config) {
			c.ProjectYAML = bytes.ReplaceAll(c.ProjectYAML, []byte("  template: .ddf/change-template.md\n"), nil)
		}},
		{"dry-run mode", func(c *daemon.Config) { c.Mode = ports.WriteModeDryRun }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newWireEnv(t)
			tc.mutate(&e.cfg)
			d, err := daemon.Open(context.Background(), e.cfg, e.ports)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if d == nil || d.Engine == nil {
				t.Fatal("Open returned a nil Engine")
			}
			if err := d.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if err := d.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
		})
	}
}

func TestOpen_InvalidProjectYAML(t *testing.T) {
	e := newWireEnv(t)
	e.cfg.ProjectYAML = readTestdata(t, "project-invalid.yaml")
	d, err := daemon.Open(context.Background(), e.cfg, e.ports)
	if !errors.Is(err, config.ErrInvalidConfig) {
		t.Fatalf("Open error = %v, want config.ErrInvalidConfig", err)
	}
	if d != nil {
		t.Fatal("Open returned a Daemon with an error")
	}
}

func TestOpen_RefusesProject(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"unknown project id", "missing"},
		{"forge github (M4)", "example-app"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newWireEnv(t)
			e.cfg.ProjectID = tc.id
			d, err := daemon.Open(context.Background(), e.cfg, e.ports)
			if err == nil {
				_ = d.Close() // the test fails anyway; release the store
				t.Fatal("Open succeeded, want an error")
			}
			if d != nil {
				t.Fatal("Open returned a Daemon with an error")
			}
			if _, err := os.Stat(e.cfg.StorePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("store file stat = %v, want not created", err)
			}
		})
	}
}

// TestOpen_ClosesStoreOnLaterError makes forge-local refuse to open after
// the store is open; Open returns the error, and a second Open on the same
// store path then succeeds, so nothing was left half-open in the way.
func TestOpen_ClosesStoreOnLaterError(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*daemon.Config)
	}{
		{"forge-local refuses a relative clone dir", func(c *daemon.Config) { c.CloneDir = "clone" }},
		{"change template missing on the integration branch", func(c *daemon.Config) {
			c.ProjectYAML = bytes.ReplaceAll(c.ProjectYAML, []byte(".ddf/change-template.md"), []byte(".ddf/missing.md"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newWireEnv(t)
			good := e.cfg
			tc.mutate(&e.cfg)
			if d, err := daemon.Open(context.Background(), e.cfg, e.ports); err == nil {
				_ = d.Close() // the test fails anyway; release the store
				t.Fatal("Open succeeded, want an error")
			}
			if _, err := os.Stat(e.cfg.StorePath); err != nil {
				t.Fatalf("store was not opened before the failing step: %v", err)
			}
			d, err := daemon.Open(context.Background(), good, e.ports)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			if err := d.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

func TestOpen_RefusesNilPortsAndBadMode(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*wireEnv)
	}{
		{"nil agent", func(e *wireEnv) { e.ports.Agent = nil }},
		{"nil sandbox", func(e *wireEnv) { e.ports.Sandbox = nil }},
		{"nil secrets", func(e *wireEnv) { e.ports.Secrets = nil }},
		{"nil clock", func(e *wireEnv) { e.ports.Clock = nil }},
		{"nil id source", func(e *wireEnv) { e.ports.IDs = nil }},
		{"nil logger", func(e *wireEnv) { e.ports.Logger = nil }},
		{"zero write mode", func(e *wireEnv) { e.cfg.Mode = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newWireEnv(t)
			tc.mutate(e)
			if d, err := daemon.Open(context.Background(), e.cfg, e.ports); err == nil {
				_ = d.Close() // the test fails anyway; release the store
				t.Fatal("Open succeeded, want an error")
			}
		})
	}
}
