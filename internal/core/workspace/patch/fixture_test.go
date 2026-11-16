package patch_test

// Go cannot import another package's _test.go files, so these helpers are a
// deliberate minimal copy of internal/core/workspace/git/fixture_test.go.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
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

// deleted as a commitOn content removes the file.
const deleted = "\x00deleted"

// commitOn commits files (name to content, or deleted) on top of parent in
// repo, keeps it and returns its SHA. edit, if not nil, runs after the files
// are staged and before the commit.
func commitOn(t *testing.T, r *git.Runner, repo, parent string, files map[string]string, edit func()) string {
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
	if edit != nil {
		edit()
	}
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

// exportPatch runs the ExportArgs steps through r in the copy cp, which the
// test wrote, and returns the patch.
func exportPatch(t *testing.T, r *git.Runner, cp, base string) []byte {
	t.Helper()
	args, err := patch.ExportArgs(base)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, a := range args {
		if out, err = r.Run(t.Context(), cp, nil, a...); err != nil {
			t.Fatalf("git %v: %v", a, err)
		}
	}
	return out
}

// diffOf returns the binary diff from..to in repo, untrimmed.
func diffOf(t *testing.T, r *git.Runner, repo, from, to string) []byte {
	t.Helper()
	out, err := r.Run(t.Context(), repo, nil, "diff", "--binary", "--full-index", "--no-renames", "--no-ext-diff", from, to)
	if err != nil {
		t.Fatalf("diff %s..%s: %v", from, to, err)
	}
	return out
}
