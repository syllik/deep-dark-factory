package local_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

func TestOpen_CreatesMissingStorage(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	b, err := os.ReadFile(e.path("format"))
	if err != nil || string(b) != "ddf.forge-local/v1\n" {
		t.Fatalf("format = %q, %v", b, err)
	}
	for _, d := range []string{"inbox", "inbox/rejected", "tasks", "answers"} {
		info, err := os.Lstat(e.path(d))
		if err != nil || !info.IsDir() {
			t.Errorf("%s: not a directory (%v)", d, err)
		}
	}
}

func TestOpen_InitialisesEmptyDir(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
	}{
		{"empty", nil},
		{"only adapter temp files", []string{".ddf-tmp-123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if err := os.MkdirAll(e.root(), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, f := range tc.files {
				e.writeFile(t, f, []byte("x"))
			}
			e.open(t)
			if _, err := os.Stat(e.path("format")); err != nil {
				t.Fatalf("format: %v", err)
			}
			for _, f := range tc.files {
				if _, err := os.Lstat(e.path(f)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s not removed: %v", f, err)
				}
			}
		})
	}
}

func TestOpen_RefusesFormat(t *testing.T) {
	for _, tc := range []struct {
		name, file, content string
	}{
		{"non-empty without format", "notes.txt", "x"},
		{"format holding another value", "format", "ddf.forge-local/v2\n"},
		{"format without newline", "format", "ddf.forge-local/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.writeFile(t, tc.file, []byte(tc.content))
			_, err := local.Open(context.Background(), e.cfg)
			if !errors.Is(err, local.ErrFormat) {
				t.Fatalf("err = %v, want ErrFormat", err)
			}
		})
	}
}

func TestOpen_RemovesStrayTempFiles(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	stray := []string{".ddf-tmp-a", "tasks/.ddf-tmp-b", "inbox/rejected/.ddf-tmp-c"}
	for _, s := range stray {
		e.writeFile(t, s, []byte("partial"))
	}
	// agentDefault 8: dot files in inbox/ and answers/<n>/ are in-flight CLI
	// writes and stay.
	kept := []string{"inbox/.ddf-tmp-ask", "answers/1/.ddf-tmp-answer"}
	for _, s := range kept {
		e.writeFile(t, s, []byte("in flight"))
	}
	e.open(t)
	for _, s := range stray {
		if _, err := os.Lstat(e.path(s)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s not removed: %v", s, err)
		}
	}
	for _, s := range kept {
		if _, err := os.Lstat(e.path(s)); err != nil {
			t.Errorf("%s removed: %v", s, err)
		}
	}
}

func TestOpen_RefusesUnsupportedRemote(t *testing.T) {
	nonBare := t.TempDir()
	for _, tc := range []struct {
		name, url string
	}{
		{"ssh", "ssh://h/r.git"},
		{"http", "http://h/r.git"},
		{"relative path", "relative/r.git"},
		{"plain absolute path", "/srv/r.git"},
		{"file url with host", "file://host/srv/r.git"},
		{"file url to a missing path", fileURL(filepath.Join(t.TempDir(), "missing.git"))},
		{"file url to a non-bare directory", fileURL(nonBare)},
		{"file url with query", fileURL(nonBare) + "?x=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.cfg.RemoteURL = tc.url
			_, err := local.Open(context.Background(), e.cfg)
			if !errors.Is(err, local.ErrUnsupportedRemote) {
				t.Fatalf("err = %v, want ErrUnsupportedRemote", err)
			}
		})
	}
}

func TestOpen_AcceptsBareRemote(t *testing.T) {
	e := newEnv(t)
	if _, err := local.Open(context.Background(), e.cfg); err != nil {
		t.Fatalf("open: %v", err)
	}
}

func TestOpen_RefusesStorageOverlappingRemote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote func(e *env) string
	}{
		{"equal", func(e *env) string { return e.root() }},
		{"inside", func(e *env) string { return filepath.Join(e.dataDir, "forge-local") }},
		{"containing", func(e *env) string { return e.path("remote.git") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			remote := tc.remote(e)
			makeBare(t, remote)
			e.cfg.RemoteURL = fileURL(remote)
			if _, err := local.Open(context.Background(), e.cfg); err == nil {
				t.Fatal("open succeeded, want an overlap error")
			}
		})
	}
}

func TestOpen_RefusesSymlinkComponent(t *testing.T) {
	e := newEnv(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(e.dataDir, "forge-local")); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), e.cfg); err == nil {
		t.Fatal("open succeeded through a symlinked storage component")
	}
}

func TestOpen_RefusesConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(c *local.Config)
	}{
		{"empty project id", func(c *local.Config) { c.ProjectID = "" }},
		{"upper-case project id", func(c *local.Config) { c.ProjectID = "Demo" }},
		{"project id with leading dash", func(c *local.Config) { c.ProjectID = "-demo" }},
		{"project id with underscore", func(c *local.Config) { c.ProjectID = "de_mo" }},
		{"project id with slash", func(c *local.Config) { c.ProjectID = "a/b" }},
		{"empty data dir", func(c *local.Config) { c.DataDir = "" }},
		{"relative data dir", func(c *local.Config) { c.DataDir = "data" }},
		{"unclean data dir", func(c *local.Config) { c.DataDir += "/../x" }},
		{"empty remote url", func(c *local.Config) { c.RemoteURL = "" }},
		{"nil clock", func(c *local.Config) { c.Clock = nil }},
		{"nil id source", func(c *local.Config) { c.IDs = nil }},
		{"nil logger", func(c *local.Config) { c.Logger = nil }},
		{"nil git", func(c *local.Config) { c.Git = nil }},
		{"nil sandbox", func(c *local.Config) { c.Sandbox = nil }},
		{"no verify command", func(c *local.Config) { c.Verify = nil }},
		{"empty verify command", func(c *local.Config) { c.Verify = []string{"make verify", ""} }},
		{"blank verify command", func(c *local.Config) { c.Verify = []string{"  "} }},
		{"negative cpu limit", func(c *local.Config) { c.Limits.CPUMillis = -1 }},
		{"negative memory limit", func(c *local.Config) { c.Limits.MemoryBytes = -1 }},
		{"negative disk limit", func(c *local.Config) { c.Limits.DiskBytes = -1 }},
		{"empty egress entry", func(c *local.Config) { c.Egress = []string{"registry.example", ""} }},
		{"empty clone dir", func(c *local.Config) { c.CloneDir = "" }},
		{"relative clone dir", func(c *local.Config) { c.CloneDir = "clone.git" }},
		{"unclean clone dir", func(c *local.Config) { c.CloneDir += "/../clone.git" }},
		{"empty run dir", func(c *local.Config) { c.RunDir = "" }},
		{"relative run dir", func(c *local.Config) { c.RunDir = "runs" }},
		{"unclean run dir", func(c *local.Config) { c.RunDir += "/." }},
		{"clone dir parent missing", func(c *local.Config) { c.CloneDir = filepath.Join(c.CloneDir, "x", "clone.git") }},
		{"run dir parent missing", func(c *local.Config) { c.RunDir = filepath.Join(c.RunDir, "x", "runs") }},
		{"empty integration branch", func(c *local.Config) { c.IntegrationBranch = "" }},
		{"integration branch with double dot", func(c *local.Config) { c.IntegrationBranch = "a..b" }},
		{"integration branch with space", func(c *local.Config) { c.IntegrationBranch = "a b" }},
		{"integration branch ending in .lock", func(c *local.Config) { c.IntegrationBranch = "main.lock" }},
		{"integration branch with trailing slash", func(c *local.Config) { c.IntegrationBranch = "main/" }},
		{"empty branch pattern", func(c *local.Config) { c.BranchPattern = "" }},
		{"branch pattern with unknown placeholder", func(c *local.Config) { c.BranchPattern = "{type}/{x}-{n}-{slug}" }},
		{"branch pattern with stray open brace", func(c *local.Config) { c.BranchPattern = "{type}/issue-{n}-{slug" }},
		{"branch pattern with stray close brace", func(c *local.Config) { c.BranchPattern = "{type}/issue-{n}-{slug}}" }},
		{"branch pattern without n", func(c *local.Config) { c.BranchPattern = "{type}/issue-{slug}" }},
		{"branch pattern without slug", func(c *local.Config) { c.BranchPattern = "{type}/issue-{n}" }},
		{"empty identity name", func(c *local.Config) { c.Identity.Name = "" }},
		{"empty identity email", func(c *local.Config) { c.Identity.Email = "" }},
		{"identity name with <", func(c *local.Config) { c.Identity.Name = "a<b" }},
		{"identity name with >", func(c *local.Config) { c.Identity.Name = "a>b" }},
		{"identity name with newline", func(c *local.Config) { c.Identity.Name = "a\nb" }},
		{"identity name with NUL", func(c *local.Config) { c.Identity.Name = "a\x00b" }},
		{"identity email with <", func(c *local.Config) { c.Identity.Email = "<a@example.invalid" }},
		{"identity email with >", func(c *local.Config) { c.Identity.Email = "a@example.invalid>" }},
		{"identity email with newline", func(c *local.Config) { c.Identity.Email = "a@example.invalid\n" }},
		{"identity email with NUL", func(c *local.Config) { c.Identity.Email = "a\x00@example.invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.mutate(&e.cfg)
			if _, err := local.Open(context.Background(), e.cfg); err == nil {
				t.Fatal("open succeeded, want a config error")
			}
			if _, err := os.Lstat(filepath.Join(e.dataDir, "forge-local")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("storage written despite a config error: %v", err)
			}
		})
	}
}

func TestOpen_AcceptsBranchPatternAndIdentity(t *testing.T) {
	e := newEnv(t)
	e.cfg.BranchPattern = "{type}/issue-{n}-{slug}"
	e.cfg.Identity = git.Identity{Name: "DDF Daemon", Email: "ddf@example.invalid"}
	if _, err := local.Open(context.Background(), e.cfg); err != nil {
		t.Fatalf("open: %v", err)
	}
}

func TestOpen_RefusesOverlappingDirs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(e *env)
	}{
		{"clone dir equal to storage", func(e *env) { e.cfg.CloneDir = e.root() }},
		{"clone dir inside storage", func(e *env) { e.cfg.CloneDir = e.path("clone.git") }},
		{"clone dir containing storage", func(e *env) { e.cfg.CloneDir = e.dataDir }},
		{"run dir equal to storage", func(e *env) { e.cfg.RunDir = e.root() }},
		{"run dir inside storage", func(e *env) { e.cfg.RunDir = e.path("runs") }},
		{"run dir containing storage", func(e *env) { e.cfg.RunDir = filepath.Join(e.dataDir, "forge-local") }},
		{"clone dir equal to remote", func(e *env) { e.cfg.CloneDir = e.remote }},
		{"clone dir inside remote", func(e *env) { e.cfg.CloneDir = filepath.Join(e.remote, "clone.git") }},
		{"clone dir containing remote", func(e *env) { e.cfg.CloneDir = filepath.Dir(e.remote) }},
		{"run dir equal to remote", func(e *env) { e.cfg.RunDir = e.remote }},
		{"run dir inside remote", func(e *env) { e.cfg.RunDir = filepath.Join(e.remote, "runs") }},
		{"run dir containing remote", func(e *env) { e.cfg.RunDir = filepath.Dir(e.remote) }},
		{"run dir equal to clone dir", func(e *env) { e.cfg.RunDir = e.cfg.CloneDir }},
		{"run dir inside clone dir", func(e *env) { e.cfg.RunDir = filepath.Join(e.cfg.CloneDir, "runs") }},
		{"run dir containing clone dir", func(e *env) { e.cfg.RunDir = filepath.Dir(e.cfg.CloneDir) }},
		{"clone dir through a symlink into storage", func(e *env) {
			link := filepath.Join(t.TempDir(), "link")
			if err := os.MkdirAll(e.root(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(e.root(), link); err != nil {
				t.Fatal(err)
			}
			e.cfg.CloneDir = filepath.Join(link, "clone.git")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.mutate(e)
			before := snapshot(t, e.dataDir)
			_, err := local.Open(context.Background(), e.cfg)
			if err == nil || !strings.Contains(err.Error(), "overlaps") {
				t.Fatalf("err = %v, want an overlap error", err)
			}
			sameSnapshot(t, before, snapshot(t, e.dataDir))
		})
	}
}

func TestOpen_CreatesCleanClone(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	if got := gitOut(t, e.git, e.cfg.CloneDir, "rev-parse", "--is-bare-repository"); got != "true" {
		t.Fatalf("clone is bare = %q", got)
	}
	if got := gitOut(t, e.git, e.cfg.CloneDir, "rev-parse", "refs/heads/main"); got != e.mainSHA {
		t.Fatalf("clone main = %s, want %s", got, e.mainSHA)
	}
	if got := gitOut(t, e.git, e.cfg.CloneDir, "config", "--get", "remote.origin.url"); got != e.cfg.RemoteURL {
		t.Fatalf("clone origin = %q", got)
	}
}

func TestOpen_FetchesExistingClone(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	head := e.commitOn(t, "feature", "f.txt", "feature\n")
	refs := e.remoteRefs(t)
	if _, err := e.git.Run(context.Background(), e.cfg.CloneDir, nil, "rev-parse", "--verify", "--quiet", "refs/heads/feature"); err == nil {
		t.Fatal("clone has the branch before reopening")
	}
	e.open(t)
	if got := gitOut(t, e.git, e.cfg.CloneDir, "rev-parse", "refs/heads/feature"); got != head {
		t.Fatalf("clone feature = %s, want %s", got, head)
	}
	if got := e.remoteRefs(t); got != refs {
		t.Errorf("remote refs changed by Open: before %q, after %q", refs, got)
	}
}

func TestOpen_RefusesWrongCloneDir(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T, e *env)
	}{
		{"empty directory", func(t *testing.T, e *env) {
			if err := os.Mkdir(e.cfg.CloneDir, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"regular file", func(t *testing.T, e *env) {
			if err := os.WriteFile(e.cfg.CloneDir, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink to a clone", func(t *testing.T, e *env) {
			target := filepath.Join(t.TempDir(), "clone.git")
			gitOut(t, e.git, filepath.Dir(target), "clone", "-q", "--bare", e.cfg.RemoteURL, target)
			if err := os.Symlink(target, e.cfg.CloneDir); err != nil {
				t.Fatal(err)
			}
		}},
		{"non-bare repository", func(t *testing.T, e *env) {
			gitOut(t, e.git, filepath.Dir(e.cfg.CloneDir), "clone", "-q", e.cfg.RemoteURL, e.cfg.CloneDir)
		}},
		{"bare clone of another remote", func(t *testing.T, e *env) {
			other := filepath.Join(t.TempDir(), "other.git")
			gitOut(t, e.git, filepath.Dir(other), "init", "-q", "--bare", other)
			gitOut(t, e.git, filepath.Dir(e.cfg.CloneDir), "clone", "-q", "--bare", fileURL(other), e.cfg.CloneDir)
		}},
		{"bare repository without origin", func(t *testing.T, e *env) {
			gitOut(t, e.git, filepath.Dir(e.cfg.CloneDir), "init", "-q", "--bare", e.cfg.CloneDir)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.make(t, e)
			if _, err := local.Open(context.Background(), e.cfg); err == nil {
				t.Fatal("open succeeded on a wrong clone dir")
			}
		})
	}
}

func TestOpen_CreatesDirsWithMode0700(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	for _, p := range []string{e.cfg.RunDir, e.path("changes"), e.path("reviews"), e.path("checks")} {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			t.Fatalf("%s: not a directory (%v)", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s: mode %v, want 0700", p, perm)
		}
	}
}

func TestOpen_RefusesRunDirSymlink(t *testing.T) {
	e := newEnv(t)
	if err := os.Symlink(t.TempDir(), e.cfg.RunDir); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Open(context.Background(), e.cfg); err == nil {
		t.Fatal("open succeeded with a symlinked run dir")
	}
}

func TestOpen_RemovesNestedStrayTempFiles(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	stray := []string{
		"changes/.ddf-tmp-a",
		"checks/" + e.mainSHA + "/.ddf-tmp-b",
		"reviews/c1/" + e.mainSHA + "/.ddf-tmp-c",
	}
	for _, s := range stray {
		e.writeFile(t, s, []byte("partial"))
	}
	e.open(t)
	for _, s := range stray {
		if _, err := os.Lstat(e.path(s)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s not removed: %v", s, err)
		}
	}
}

// attemptJSON is a check attempt record as a test writes it.
func attemptJSON(head string, n int, status, runID, marker string) map[string]any {
	m := map[string]any{
		"schema": "ddf.forge-local.check/v1", "name": "verify", "headSha": head, "attempt": n,
		"status": status, "runId": runID, "marker": marker, "startedAt": "2026-11-25T09:00:00Z",
	}
	if status != "running" {
		m["finishedAt"] = "2026-11-25T09:30:00Z"
	}
	return m
}

// readAttempt decodes checks/<head>/<n>.json into raw fields.
func (e *env) readAttempt(t *testing.T, head string, n int) map[string]any {
	t.Helper()
	b, err := os.ReadFile(e.path("checks", head, strconv.Itoa(n)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (e *env) attemptCount(t *testing.T, head string) int {
	t.Helper()
	entries, err := os.ReadDir(e.path("checks", head))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestOpen_RecoversRunningAttempt(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	key := testKey(t, "1")
	marker := "ddf:task-key=" + string(key)
	head := e.mainSHA
	e.writeFile(t, "checks/"+head+"/1.json", mustJSON(t, attemptJSON(head, 1, "running", "old-run", marker)))
	// A live cell left by the previous instance, and a cell of another run.
	if _, err := e.sandbox.CreateCell(ctx, ports.CellSpec{Key: key, RunID: "old-run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sandbox.CreateCell(ctx, ports.CellSpec{Key: key, RunID: "other-run"}); err != nil {
		t.Fatal(err)
	}
	containers := 0
	res, err := e.sandbox.ListResources(ctx, ports.ResourceFilter{Key: key, RunID: "old-run"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Kind == fake.KindContainer {
			containers++
		}
	}
	copyDir := filepath.Join(e.cfg.RunDir, "old-run")
	if err := os.MkdirAll(filepath.Join(copyDir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := e.host.Stats()

	// A new instance on the same storage and sandbox host.
	e.cfg.Sandbox = e.host.Sandbox("b")
	e.open(t)

	if got := e.host.Stats().Killed - before.Killed; got != containers || containers == 0 {
		t.Fatalf("killed %d cells, want %d (> 0)", got, containers)
	}
	if e.host.Stats().Live != 1 {
		t.Errorf("live cells = %d, want only the other run's", e.host.Stats().Live)
	}
	lost := e.readAttempt(t, head, 1)
	if lost["status"] != "lost" || lost["finishedAt"] == nil || lost["finishedAt"] == "" {
		t.Fatalf("attempt 1 = %v, want lost with finishedAt", lost)
	}
	if lost["runId"] != "old-run" {
		t.Errorf("attempt 1 run id = %v", lost["runId"])
	}
	next := e.readAttempt(t, head, 2)
	if next["status"] != "running" || next["finishedAt"] != nil {
		t.Fatalf("attempt 2 = %v, want running", next)
	}
	if next["runId"] == "old-run" || next["runId"] == "" {
		t.Errorf("attempt 2 run id = %v, want a new one", next["runId"])
	}
	if next["marker"] != marker {
		t.Errorf("attempt 2 marker = %v, want %s", next["marker"], marker)
	}
	if n := e.attemptCount(t, head); n != 2 {
		t.Errorf("%d attempts, want 2", n)
	}
	if _, err := os.Lstat(copyDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("run copy of the lost attempt not removed: %v", err)
	}
}

func TestOpen_RestartsLostAttemptWithoutSuccessor(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	marker := "ddf:task-key=" + string(testKey(t, "1"))
	head := e.mainSHA
	e.writeFile(t, "checks/"+head+"/1.json", mustJSON(t, attemptJSON(head, 1, "lost", "old-run", marker)))
	e.open(t)
	next := e.readAttempt(t, head, 2)
	if next["status"] != "running" || next["marker"] != marker || next["runId"] == "old-run" {
		t.Fatalf("attempt 2 = %v, want a new running attempt", next)
	}
	if n := e.attemptCount(t, head); n != 2 {
		t.Errorf("%d attempts, want 2", n)
	}
}

func TestOpen_LeavesFinishedAttempts(t *testing.T) {
	for _, status := range []string{"passed", "failed"} {
		t.Run(status, func(t *testing.T) {
			e := newEnv(t)
			e.open(t)
			marker := "ddf:task-key=" + string(testKey(t, "1"))
			head := e.mainSHA
			e.writeFile(t, "checks/"+head+"/1.json", mustJSON(t, attemptJSON(head, 1, "lost", "r1", marker)))
			e.writeFile(t, "checks/"+head+"/2.json", mustJSON(t, attemptJSON(head, 2, status, "r2", marker)))
			before := snapshot(t, e.path("checks"))
			e.open(t)
			sameSnapshot(t, before, snapshot(t, e.path("checks")))
		})
	}
}

func TestOpen_RecoveryFailsClosed(t *testing.T) {
	e := newEnv(t)
	e.open(t)
	head := e.mainSHA
	e.writeFile(t, "checks/"+head+"/1.json", mustJSON(t, attemptJSON(head, 1, "running", "old-run", "ddf:task-key="+string(testKey(t, "1")))))
	e.writeFile(t, "checks/not-a-sha/1.json", []byte("{}"))
	if _, err := local.Open(context.Background(), e.cfg); err == nil {
		t.Fatal("open succeeded with an unexpected entry under checks/")
	}
	if got := e.readAttempt(t, head, 1)["status"]; got != "running" {
		t.Errorf("attempt 1 status = %v, want running", got)
	}
}
