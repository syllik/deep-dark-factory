package git_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

func heads(t *testing.T, r *git.Runner, repo string) string {
	t.Helper()
	return gitOut(t, r, repo, nil, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")
}

func TestCleanClone_RefusesUnsupportedURLs(t *testing.T) {
	r, _ := newRunner(t)
	markers := t.TempDir()
	ext := "ext::sh -c touch% " + filepath.Join(markers, "ext")
	for _, url := range []string{ext, "ssh://example.invalid/x", "https://example.invalid/x", "relative/repo"} {
		if err := r.CleanClone(t.Context(), url, filepath.Join(t.TempDir(), "c")); !errors.Is(err, git.ErrUnsupportedURL) {
			t.Errorf("CleanClone(%q) = %v, want ErrUnsupportedURL", url, err)
		}
	}
	// protocol.allow=never is the backstop when a caller bypasses CleanClone.
	if _, err := r.Run(t.Context(), t.TempDir(), nil, "clone", "--bare", ext, filepath.Join(t.TempDir(), "c")); err == nil {
		t.Error("clone over ext:: succeeded")
	}
	noMarkers(t, markers)
	src := newSourceRepo(t, r)
	if err := r.CleanClone(t.Context(), src, t.TempDir()); err == nil {
		t.Error("CleanClone into an existing dir succeeded")
	}
}

func TestCleanClone_AndFetch(t *testing.T) {
	r, _ := newRunner(t)
	src := newSourceRepo(t, r)
	gitOut(t, r, src, nil, "branch", "gone")
	for name, url := range map[string]string{"path": src, "file url": "file://" + src} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "clean.git")
			if err := r.CleanClone(t.Context(), url, dir); err != nil {
				t.Fatalf("CleanClone: %v", err)
			}
			if got := gitOut(t, r, dir, nil, "rev-parse", "--is-bare-repository"); got != "true" {
				t.Errorf("is-bare-repository = %s", got)
			}
			if hooks, _ := os.ReadDir(filepath.Join(dir, "hooks")); len(hooks) > 0 {
				t.Errorf("clean clone has hooks: %v", hooks)
			}
			if got, want := heads(t, r, dir), heads(t, r, src); got != want {
				t.Errorf("clone heads = %q, want %q", got, want)
			}
		})
	}
	dir := filepath.Join(t.TempDir(), "clean.git")
	if err := r.CleanClone(t.Context(), src, dir); err != nil {
		t.Fatalf("CleanClone: %v", err)
	}
	commitFile(t, r, src, "next", "next\n")
	gitOut(t, r, src, nil, "branch", "-D", "gone")
	if err := r.Fetch(t.Context(), dir); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, want := heads(t, r, dir), heads(t, r, src); got != want {
		t.Errorf("heads after Fetch = %q, want %q", got, want)
	}
}

func TestNewRunCopy_Refuses(t *testing.T) {
	r, _ := newRunner(t)
	src := newSourceRepo(t, r)
	for _, sha := range []string{"abc", strings.Repeat("A", 40), strings.Repeat("a", 39)} {
		if err := r.NewRunCopy(t.Context(), src, sha, filepath.Join(t.TempDir(), "c")); !errors.Is(err, git.ErrInvalidSHA) {
			t.Errorf("NewRunCopy(%q) = %v, want ErrInvalidSHA", sha, err)
		}
	}
	head := gitOut(t, r, src, nil, "rev-parse", "HEAD")
	if err := r.NewRunCopy(t.Context(), src, head, t.TempDir()); err == nil {
		t.Error("NewRunCopy into an existing dir succeeded")
	}
	dst := filepath.Join(t.TempDir(), "c")
	if err := r.NewRunCopy(t.Context(), src, strings.Repeat("0", 40), dst); err == nil {
		t.Error("NewRunCopy of a missing commit succeeded")
	}
	if _, err := os.Lstat(dst); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("failed run copy left %s: %v", dst, err)
	}
}

func TestNewRunCopy_EndState(t *testing.T) {
	r, _ := newRunner(t)
	src := newSourceRepo(t, r)
	// spec §8.3: a symlink in the base tree must not become a host symlink.
	outside := filepath.Join(t.TempDir(), "secret")
	blob := gitOut(t, r, src, []byte(outside), "hash-object", "-w", "--stdin")
	gitOut(t, r, src, nil, "update-index", "--add", "--cacheinfo", "120000,"+blob+",leak")
	gitOut(t, r, src, nil, "commit", "-q", "-m", "add leak")
	commit := gitOut(t, r, src, nil, "rev-parse", "HEAD")
	dst := filepath.Join(t.TempDir(), "copy")
	if err := r.NewRunCopy(t.Context(), src, commit, dst); err != nil {
		t.Fatalf("NewRunCopy: %v", err)
	}
	for args, want := range map[string]string{
		"rev-parse HEAD":               commit,
		"symbolic-ref HEAD":            "refs/heads/ddf-run",
		"status --porcelain":           "",
		"config --local core.symlinks": "false",
	} {
		if got := gitOut(t, r, dst, nil, strings.Fields(args)...); got != want {
			t.Errorf("%s = %q, want %q", args, got, want)
		}
	}
	if out, err := r.Run(t.Context(), dst, nil, "config", "--local", "--get-regexp", `^remote\.`); err == nil {
		t.Errorf("run copy has remote config: %s", out)
	}
	for _, p := range []string{"FETCH_HEAD", "logs", "objects/info/alternates"} {
		if _, err := os.Lstat(filepath.Join(dst, ".git", p)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf(".git/%s exists: %v", p, err)
		}
	}
	err := filepath.WalkDir(filepath.Join(dst, ".git"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err == nil && bytes.Contains(data, []byte(src)) {
			t.Errorf("%s records the source path", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(dst, "leak"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("leak is not a regular file: %v %v", fi, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "leak")); string(data) != outside {
		t.Errorf("leak = %q, want the link text %q", data, outside)
	}
}

// spec §8.3: no hook, fsmonitor or filter, textconv or merge driver from the
// repository or its config runs on the host.
func TestNoRepositoryContentRuns(t *testing.T) {
	r, _ := newRunner(t)
	markers, scripts := t.TempDir(), t.TempDir()
	hooks := []string{"post-checkout", "post-merge", "reference-transaction", "pre-commit", "post-commit"}
	plantHooks := func(dir string) {
		for _, h := range hooks {
			markerScript(t, filepath.Join(dir, h), markers, h)
		}
	}
	src := newSourceRepo(t, r)
	commitFile(t, r, src, ".gitattributes", "* filter=evil diff=evil merge=evil\n")
	plantHooks(filepath.Join(src, ".git", "hooks"))
	plantHooks(filepath.Join(scripts, "hooks"))
	for _, key := range []string{"filter.evil.clean", "filter.evil.smudge", "filter.evil.process",
		"diff.evil.textconv", "merge.evil.driver", "core.fsmonitor"} {
		markerScript(t, filepath.Join(scripts, key), markers, key)
		gitOut(t, r, src, nil, "config", key, filepath.Join(scripts, key))
	}
	gitOut(t, r, src, nil, "config", "core.hooksPath", filepath.Join(scripts, "hooks"))
	commit := gitOut(t, r, src, nil, "rev-parse", "HEAD")

	clean := filepath.Join(t.TempDir(), "clean.git")
	copyDir := filepath.Join(t.TempDir(), "copy")
	if err := r.CleanClone(t.Context(), src, clean); err != nil {
		t.Fatalf("CleanClone: %v", err)
	}
	if err := r.Fetch(t.Context(), clean); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if err := r.NewRunCopy(t.Context(), clean, commit, copyDir); err != nil {
		t.Fatalf("NewRunCopy: %v", err)
	}
	plantHooks(filepath.Join(clean, "hooks"))
	plantHooks(filepath.Join(copyDir, ".git", "hooks"))
	gitOut(t, r, copyDir, nil, "config", "core.fsmonitor", filepath.Join(scripts, "core.fsmonitor"))
	gitOut(t, r, copyDir, nil, "config", "core.hooksPath", filepath.Join(scripts, "hooks"))
	gitOut(t, r, copyDir, nil, "status")
	gitOut(t, r, src, nil, "update-ref", "refs/heads/extra", commit)
	if err := r.Fetch(t.Context(), clean); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	noMarkers(t, markers)
}
