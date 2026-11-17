package publisher_test

// Go cannot import another package's _test.go files, so these helpers are a
// deliberate minimal copy of internal/core/workspace/patch/fixture_test.go.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// gitPath is the host git; tests fail, never skip, without it.
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

// newRunner returns a runner on the host git with a fresh state dir.
func newRunner(t *testing.T) *git.Runner {
	t.Helper()
	r, err := git.NewRunner(t.Context(), gitPath(t), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

// gitOut runs git through r in dir with the test identity and returns the
// trimmed stdout.
func gitOut(t *testing.T, r *git.Runner, dir string, stdin []byte, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=test", "-c", "user.email=test@example.invalid"}, args...)
	out, err := r.Run(t.Context(), dir, stdin, full...)
	if err != nil {
		var ce *git.CommandError
		errors.As(err, &ce)
		t.Fatalf("git %v: %v %+v", args, err, ce)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// newSourceRepo makes a repository with branch main holding one commit and
// returns its dir and that commit.
func newSourceRepo(t *testing.T, r *git.Runner) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "src")
	gitOut(t, r, filepath.Dir(dir), nil, "init", "-q", "--initial-branch=main", dir)
	writeFile(t, filepath.Join(dir, "README"), "base\n", 0o644)
	gitOut(t, r, dir, nil, "add", "--", "README")
	gitOut(t, r, dir, nil, "commit", "-q", "-m", "add README")
	return dir, gitOut(t, r, dir, nil, "rev-parse", "HEAD")
}

// commitOn commits files (name to content) on top of parent in repo, keeps
// it and returns its SHA.
func commitOn(t *testing.T, r *git.Runner, repo, parent string, files map[string]string) string {
	t.Helper()
	gitOut(t, r, repo, nil, "checkout", "-q", "--detach", parent)
	for name, content := range files {
		writeFile(t, filepath.Join(repo, name), content, 0o644)
	}
	gitOut(t, r, repo, nil, "add", "-A", "--", ".")
	gitOut(t, r, repo, nil, "commit", "-q", "--allow-empty", "-m", "commit")
	return keep(t, r, repo, gitOut(t, r, repo, nil, "rev-parse", "HEAD"))
}

// keep points the branch c/<sha> at sha so a clean clone gets it.
func keep(t *testing.T, r *git.Runner, repo, sha string) string {
	t.Helper()
	gitOut(t, r, repo, nil, "branch", "-f", "c/"+sha, sha)
	return sha
}

// cleanClone makes a clean clone of src in a fresh dir.
func cleanClone(t *testing.T, r *git.Runner, src string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clean.git")
	if err := r.CleanClone(t.Context(), src, dir); err != nil {
		t.Fatalf("CleanClone: %v", err)
	}
	return dir
}
