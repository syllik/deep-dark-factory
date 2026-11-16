package git_test

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
func newRunner(t *testing.T) (*git.Runner, string) {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	r, err := git.NewRunner(t.Context(), gitPath(t), state)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r, state
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

// newSourceRepo makes a repository with branch main holding one commit.
func newSourceRepo(t *testing.T, r *git.Runner) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "src")
	gitOut(t, r, filepath.Dir(dir), nil, "init", "-q", "--initial-branch=main", dir)
	commitFile(t, r, dir, "README", "base\n")
	return dir
}

// commitFile writes name and commits it on the current branch; it returns
// the commit SHA.
func commitFile(t *testing.T, r *git.Runner, repo, name, content string) string {
	t.Helper()
	writeFile(t, filepath.Join(repo, name), content, 0o644)
	gitOut(t, r, repo, nil, "add", "--", name)
	gitOut(t, r, repo, nil, "commit", "-q", "-m", "add "+name)
	return gitOut(t, r, repo, nil, "rev-parse", "HEAD")
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

// markerScript writes an executable script at path that creates
// markers/<name> when run.
func markerScript(t *testing.T, path, markers, name string) {
	t.Helper()
	writeFile(t, path, "#!/bin/sh\ntouch '"+filepath.Join(markers, name)+"'\n", 0o755)
}

// noMarkers fails the test if any marker file exists.
func noMarkers(t *testing.T, markers string) {
	t.Helper()
	entries, err := os.ReadDir(markers)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("marker %s exists: repository content ran", e.Name())
	}
}

// deleted as a commitOn content removes the file.
const deleted = "\x00deleted"

// commitOn commits files (name to content, or deleted) on top of parent in
// repo, keeps it and returns its SHA.
func commitOn(t *testing.T, r *git.Runner, repo, parent string, files map[string]string) string {
	t.Helper()
	gitOut(t, r, repo, nil, "checkout", "-q", "--detach", parent)
	for name, content := range files {
		p := filepath.Join(repo, name)
		if content == deleted {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		writeFile(t, p, content, 0o644)
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
