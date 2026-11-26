package local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

const (
	projectID         = "demo"
	integrationBranch = "main"
)

var testStart = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

// testIdentity is the Config.Identity of every test project.
var testIdentity = git.Identity{Name: "DDF Test Daemon", Email: "daemon@example.invalid"}

// testBranchPattern is the Config.BranchPattern of every test project.
const testBranchPattern = "{type}/issue-{n}-{slug}"

// env is one test project: a data dir, a real bare remote seeded through a
// work repository, the clean clone and run dirs, a fake sandbox host, a
// fixed clock and a captured logger.
type env struct {
	dataDir string
	remote  string
	work    string
	git     *git.Runner
	host    *fake.Host
	sandbox *fake.Sandbox
	clock   *fakes.Clock
	logs    *bytes.Buffer
	// mainSHA is the seed commit on the integration branch.
	mainSHA string
	cfg     local.Config
}

// seedRepos is built once per test binary by TestMain: a git.Runner on the
// host git and a bare remote seeded with one commit on the integration
// branch through a work repository. newEnv copies both.
var seedRepos struct {
	bare, work, mainSHA string
	git                 *git.Runner
}

func TestMain(m *testing.M) {
	code, err := runWithSeed(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "forge-local test seed:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// runWithSeed builds seedRepos in a temp dir, runs the tests and removes
// the dir. Git missing is an error: tests fail, never skip.
func runWithSeed(m *testing.M) (int, error) {
	dir, err := os.MkdirTemp("", "forge-local-seed-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(dir) }() // best effort cleanup of the seed
	p, err := exec.LookPath("git")
	if err == nil {
		p, err = filepath.Abs(p)
	}
	if err != nil {
		return 0, fmt.Errorf("git not found: %w", err)
	}
	ctx := context.Background()
	r, err := git.NewRunner(ctx, p, filepath.Join(dir, "git-state"))
	if err != nil {
		return 0, err
	}
	bare, work := filepath.Join(dir, "remote.git"), filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("base\n"), 0o644); err != nil {
		return 0, err
	}
	steps := []struct {
		dir  string
		args []string
	}{
		{dir, []string{"init", "-q", "--bare", "--initial-branch=" + integrationBranch, bare}},
		{dir, []string{"init", "-q", "--initial-branch=" + integrationBranch, work}},
		{work, []string{"remote", "add", "origin", fileURL(bare)}},
		{work, []string{"add", "--", "README"}},
		{work, []string{"-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "seed"}},
		{work, []string{"push", "-q", "origin", "refs/heads/" + integrationBranch + ":refs/heads/" + integrationBranch}},
	}
	for _, st := range steps {
		if _, err := r.Run(ctx, st.dir, nil, st.args...); err != nil {
			return 0, fmt.Errorf("git %v: %w", st.args, err)
		}
	}
	out, err := r.Run(ctx, work, nil, "rev-parse", "HEAD")
	if err != nil {
		return 0, err
	}
	seedRepos.bare, seedRepos.work, seedRepos.git = bare, work, r
	seedRepos.mainSHA = strings.TrimSpace(string(out))
	return m.Run(), nil
}

// newEnv copies the seed repositories into fresh temp dirs and configures
// a project on them, with the clean clone and run dirs under another temp
// dir and Sandbox an instance of a fresh fake host.
func newEnv(t *testing.T) *env {
	t.Helper()
	if seedRepos.git == nil {
		t.Fatal("seed repositories missing: TestMain did not run")
	}
	dataDir := t.TempDir()
	repos := t.TempDir()
	bare := filepath.Join(repos, "remote.git")
	work := filepath.Join(repos, "work")
	for _, c := range [][2]string{{seedRepos.bare, bare}, {seedRepos.work, work}} {
		if err := os.CopyFS(c[1], os.DirFS(c[0])); err != nil {
			t.Fatal(err)
		}
	}
	r := seedRepos.git
	gitOut(t, r, work, "remote", "set-url", "origin", fileURL(bare))
	host := fake.NewHost(ports.Capacity{CPUMillis: 1 << 40, MemoryBytes: 1 << 50, DiskBytes: 1 << 50})
	e := &env{
		dataDir: dataDir, remote: bare, work: work, git: r, host: host, sandbox: host.Sandbox("a"),
		clock: fakes.NewClock(testStart), logs: &bytes.Buffer{}, mainSHA: seedRepos.mainSHA,
	}
	daemon := t.TempDir()
	e.cfg = local.Config{
		ProjectID:         projectID,
		DataDir:           dataDir,
		RemoteURL:         fileURL(bare),
		Clock:             e.clock,
		IDs:               fakes.NewIDs("id"),
		Logger:            slog.New(slog.NewTextHandler(e.logs, nil)),
		IntegrationBranch: integrationBranch,
		CloneDir:          filepath.Join(daemon, "clone.git"),
		RunDir:            filepath.Join(daemon, "runs"),
		Git:               r,
		Sandbox:           e.sandbox,
		Verify:            []string{"make verify"},
		BranchPattern:     testBranchPattern,
		Identity:          testIdentity,
	}
	return e
}

// gitOut runs git through r in dir with a test identity and returns the
// trimmed stdout.
func gitOut(t *testing.T, r *git.Runner, dir string, args ...string) string {
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

// commitOn commits name with content on branch in the work repository
// (creating the branch from the integration branch when missing), pushes
// the branch to the bare remote and returns the commit SHA.
func (e *env) commitOn(t *testing.T, branch, name, content string) string {
	t.Helper()
	if _, err := e.git.Run(context.Background(), e.work, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		gitOut(t, e.git, e.work, "checkout", "-q", branch)
	} else {
		gitOut(t, e.git, e.work, "checkout", "-q", "-b", branch)
	}
	p := filepath.Join(e.work, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, e.git, e.work, "add", "--", name)
	gitOut(t, e.git, e.work, "commit", "-q", "-m", "add "+name)
	sha := gitOut(t, e.git, e.work, "rev-parse", "HEAD")
	gitOut(t, e.git, e.work, "push", "-q", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	return sha
}

// pushTo pushes the existing commit sha from the work repository to
// refs/heads/<branch> of the bare remote; the update must be a creation or
// a fast-forward.
func (e *env) pushTo(t *testing.T, sha, branch string) {
	t.Helper()
	gitOut(t, e.git, e.work, "push", "-q", "origin", sha+":refs/heads/"+branch)
}

// commitTreeOn writes, in the work repository, a commit of the tree of
// treeOf with the given parents and message, and returns its SHA. The work
// repository must have both objects (push from it or fetch first).
func (e *env) commitTreeOn(t *testing.T, treeOf string, msg string, parents ...string) string {
	t.Helper()
	args := []string{"commit-tree", treeOf + "^{tree}", "-m", msg}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return gitOut(t, e.git, e.work, args...)
}

// fetchWork fetches every branch of the bare remote into refs/remotes/
// origin/ of the work repository, so it holds commits the Forge pushed.
func (e *env) fetchWork(t *testing.T) {
	t.Helper()
	gitOut(t, e.git, e.work, "fetch", "-q", "origin")
}

// remoteTip returns the SHA refs/heads/<branch> names on the bare remote.
func (e *env) remoteTip(t *testing.T, branch string) string {
	t.Helper()
	return gitOut(t, e.git, e.remote, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
}

// rawCommit returns the header and the message of commit sha on the bare
// remote, split at the first blank line; the message keeps its bytes.
func (e *env) rawCommit(t *testing.T, sha string) (header, message string) {
	t.Helper()
	out, err := e.git.Run(context.Background(), e.remote, nil, "cat-file", "commit", sha)
	if err != nil {
		t.Fatalf("cat-file commit %s: %v", sha, err)
	}
	h, m, ok := strings.Cut(string(out), "\n\n")
	if !ok {
		t.Fatalf("commit %s has no message separator: %q", sha, out)
	}
	return h, m
}

// parents returns the parent SHAs of commit sha on the bare remote, in
// order.
func (e *env) parents(t *testing.T, sha string) []string {
	t.Helper()
	fields := strings.Fields(gitOut(t, e.git, e.remote, "rev-list", "--parents", "-n", "1", sha))
	if len(fields) == 0 || fields[0] != sha {
		t.Fatalf("rev-list --parents %s: %v", sha, fields)
	}
	return fields[1:]
}

// treeOn returns the tree SHA of commit sha on the bare remote.
func (e *env) treeOn(t *testing.T, sha string) string {
	t.Helper()
	return gitOut(t, e.git, e.remote, "rev-parse", "--verify", sha+"^{tree}")
}

// checkHeads returns the names under checks/, one per head with an
// attempt.
func (e *env) checkHeads(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(e.path("checks"))
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, en := range entries {
		out = append(out, en.Name())
	}
	return out
}

// remoteRefs lists every ref of the bare remote with its object name.
func (e *env) remoteRefs(t *testing.T) string {
	t.Helper()
	return gitOut(t, e.git, e.remote, "for-each-ref", "--format=%(refname) %(objectname)")
}

// makeBare creates the filesystem shape of a bare repository at p. Only
// tests where Open fails before cloning use it as the remote.
func makeBare(t *testing.T, p string) {
	t.Helper()
	for _, d := range []string{p, filepath.Join(p, "objects"), filepath.Join(p, "refs")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(p, "HEAD"), []byte("ref: refs/heads/master\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fileURL(p string) string { return (&url.URL{Scheme: "file", Path: p}).String() }

func (e *env) root() string { return filepath.Join(e.dataDir, "forge-local", projectID) }

func (e *env) path(elem ...string) string {
	return filepath.Join(append([]string{e.root()}, elem...)...)
}

func (e *env) open(t *testing.T) *local.Forge {
	t.Helper()
	f, err := local.Open(context.Background(), e.cfg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return f
}

// drop writes an inbox file as an operator would.
func (e *env) drop(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(e.path("inbox", name), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeFile writes a storage file with mode 0600, creating parents 0700.
func (e *env) writeFile(t *testing.T, rel string, content []byte) {
	t.Helper()
	p := e.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) ingest(t *testing.T, f *local.Forge) []local.Rejection {
	t.Helper()
	rej, err := f.Ingest(context.Background(), ports.WriteModeLive)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return rej
}

// taskFiles returns the names under tasks/.
func (e *env) taskFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(e.path("tasks"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, en := range entries {
		out = append(out, en.Name())
	}
	return out
}

// record decodes tasks/<n>.json into raw fields.
func (e *env) record(t *testing.T, n int) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(e.path("tasks", strconv.Itoa(n)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func rawString(t *testing.T, m map[string]json.RawMessage, key string) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(m[key], &s); err != nil {
		t.Fatalf("field %s: %v", key, err)
	}
	return s
}

// snapshot captures every path under dir with its mode and bytes.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		v := info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			v += " -> " + target
		case info.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			v += " " + string(b)
		}
		out[p] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSnapshot(t *testing.T, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed: before %q, after %q", k, v, after[k])
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("%s appeared", k)
		}
	}
}

func source(kind string, n int) model.TaskSource {
	return model.TaskSource{Forge: "local", Repository: projectID, SourceKind: kind, SourceID: strconv.Itoa(n)}
}

func testKey(t *testing.T, id string) model.TaskKey {
	t.Helper()
	k, err := model.NewTaskKey(model.TaskSource{Forge: "github", Repository: "o/r", SourceKind: "issue", SourceID: id})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func taskText(title, body string) []byte { return []byte(fmt.Sprintf("# %s\n\n%s", title, body)) }
