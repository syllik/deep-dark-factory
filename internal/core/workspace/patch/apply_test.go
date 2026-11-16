package patch_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

func TestParseReason(t *testing.T) {
	for _, want := range []patch.Reason{patch.ReasonEmpty, patch.ReasonTooLarge, patch.ReasonDoesNotApply,
		patch.ReasonOutsideRepository, patch.ReasonGitDir, patch.ReasonGitlink, patch.ReasonNodeModules} {
		if got, err := patch.ParseReason(string(want)); got != want || err != nil {
			t.Errorf("ParseReason(%q) = %q, %v", want, got, err)
		}
	}
	for _, bad := range []string{"", "other"} {
		if _, err := patch.ParseReason(bad); err == nil {
			t.Errorf("ParseReason(%q) succeeded", bad)
		}
	}
}

// applyFixture is a source repository and its clean clone. mb precedes base,
// which changes earlier.txt.
type applyFixture struct {
	r                    *git.Runner
	src, clone, mb, base string
}

func newApplyFixture(t *testing.T) applyFixture {
	t.Helper()
	r := newRunner(t)
	src, root := newSourceRepo(t, r)
	mb := commitOn(t, r, src, root, map[string]string{"earlier.txt": "e\n", "later.txt": "l\n",
		"node_modules/vendored.js": "v\n", "edit.txt": "1\n2\n", "del.txt": "d\n", "move/a.txt": "m1\nm2\nm3\n"}, nil)
	base := commitOn(t, r, src, mb, map[string]string{"earlier.txt": "E\n"}, nil)
	return applyFixture{r: r, src: src, clone: cleanClone(t, r, src), mb: mb, base: base}
}

// patchTo commits files (and edit) on base in the source and returns the
// diff from base.
func (f applyFixture) patchTo(t *testing.T, files map[string]string, edit func()) []byte {
	t.Helper()
	return diffOf(t, f.r, f.src, f.base, commitOn(t, f.r, f.src, f.base, files, edit))
}

// cacheinfo returns an edit that stages content at path with mode.
func (f applyFixture) cacheinfo(t *testing.T, mode, path, content string) func() {
	return func() {
		oid := content
		if mode != "160000" {
			oid = gitOut(t, f.r, f.src, []byte(content), "hash-object", "-w", "--stdin")
		}
		gitOut(t, f.r, f.src, nil, "update-index", "--add", "--cacheinfo", mode+","+oid+","+path)
	}
}

// state is what Apply must never change in the clean clone.
func (f applyFixture) state(t *testing.T) string {
	t.Helper()
	config, err := os.ReadFile(filepath.Join(f.clone, "config"))
	if err != nil {
		t.Fatal(err)
	}
	var hooks []string
	entries, err := os.ReadDir(filepath.Join(f.clone, "hooks"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, e := range entries {
		hooks = append(hooks, e.Name())
	}
	return strings.Join([]string{gitOut(t, f.r, f.clone, nil, "for-each-ref"),
		gitOut(t, f.r, f.clone, nil, "symbolic-ref", "HEAD"), string(config), strings.Join(hooks, ",")}, "\n--\n")
}

// apply runs Apply with scratch and checks the clean-clone invariants.
func (f applyFixture) apply(t *testing.T, scratch string, in patch.Input) (patch.Outcome, error) {
	t.Helper()
	_, statErr := os.Lstat(scratch)
	before := f.state(t)
	got, err := patch.Apply(t.Context(), f.r, f.clone, scratch, in)
	if _, after := os.Lstat(scratch); statErr != nil && after == nil {
		t.Errorf("scratch %s exists after Apply", scratch)
	}
	wt := gitOut(t, f.r, f.clone, nil, "worktree", "list", "--porcelain")
	if n := strings.Count("\n"+wt, "\nworktree "); n != 1 {
		t.Errorf("%d worktrees registered:\n%s", n, wt)
	}
	if _, err := os.Lstat(filepath.Join(f.clone, "index")); err == nil {
		t.Error("clone/index exists")
	}
	if after := f.state(t); after != before {
		t.Errorf("clone state changed:\n%s\nwas:\n%s", after, before)
	}
	return got, err
}

func (f applyFixture) scratch(t *testing.T) string { return filepath.Join(t.TempDir(), "scratch") }

func pathsOf(cs []policy.ChangedPath) []string {
	var paths []string
	for _, c := range cs {
		paths = append(paths, c.Path)
	}
	return paths
}

// newFile is a hand-written patch adding path with one line.
func newFile(path string) string {
	return "diff --git a/" + path + " b/" + path + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + path +
		"\n@@ -0,0 +1 @@\n+x\n"
}

func TestApply_InputErrors(t *testing.T) {
	f := newApplyFixture(t)
	existing := t.TempDir()
	writeFile(t, filepath.Join(existing, "keep"), "k\n", 0o644)
	p := []byte(newFile("n.txt"))
	cases := []struct {
		name, base, from, clone, scratch string
		sha                              bool
	}{
		{"invalid base", "master", f.base, f.clone, f.scratch(t), true},
		{"invalid changed-from", f.base, strings.Repeat("A", 40), f.clone, f.scratch(t), true},
		{"relative clone", f.base, f.base, "clean.git", f.scratch(t), false},
		{"relative scratch", f.base, f.base, f.clone, "scratch", false},
		{"existing scratch", f.base, f.base, f.clone, existing, false},
		{"unknown base", strings.Repeat("1", len(f.base)), f.base, f.clone, f.scratch(t), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !filepath.IsAbs(c.clone) {
				got, err := patch.Apply(t.Context(), f.r, c.clone, c.scratch, patch.Input{Base: c.base, ChangedFrom: c.from, Patch: p})
				if wt := gitOut(t, f.r, f.clone, nil, "worktree", "list", "--porcelain"); err == nil || strings.Count(wt, "worktree ") != 1 {
					t.Fatalf("Apply = %+v, %v; worktrees:\n%s", got, err, wt)
				}
				return
			}
			got, err := f.apply(t, c.scratch, patch.Input{Base: c.base, ChangedFrom: c.from, Patch: p})
			if err == nil || got != nil || errors.Is(err, git.ErrInvalidSHA) != c.sha {
				t.Fatalf("Apply = %+v, %v; want an error (ErrInvalidSHA %v)", got, err, c.sha)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(existing, "keep")); err != nil {
		t.Errorf("existing scratch contents removed: %v", err)
	}
}

func TestApply_Refusals(t *testing.T) {
	f := newApplyFixture(t)
	changed := commitOn(t, f.r, f.src, f.base, map[string]string{"README": "changed\n"}, nil)
	undo := append(diffOf(t, f.r, f.src, f.base, changed), diffOf(t, f.r, f.src, changed, f.base)...)
	gitlink := f.patchTo(t, nil, f.cacheinfo(t, "160000", "vendor/sub", strings.Repeat("ab", 20)))
	nodeAdds := f.patchTo(t, map[string]string{"node_modules/x/index.js": "x\n", "pkg/node_modules/y.js": "y\n"}, nil)
	cases := []struct {
		name  string
		patch []byte
		want  patch.Refused
	}{
		{"spec §8.3 empty bytes", nil, patch.Refused{Reason: patch.ReasonEmpty}},
		{"too large", make([]byte, patch.MaxPatchBytes+1), patch.Refused{Reason: patch.ReasonTooLarge}},
		{"not a patch", []byte("not a patch\n"), patch.Refused{Reason: patch.ReasonDoesNotApply}},
		{"context mismatch", []byte("diff --git a/README b/README\n--- a/README\n+++ b/README\n@@ -1 +1 @@\n-nope\n+x\n"),
			patch.Refused{Reason: patch.ReasonDoesNotApply}},
		{"change undone", undo, patch.Refused{Reason: patch.ReasonEmpty}},
		{"git dir", []byte(newFile(".git/config") + newFile("sub/.git/hooks/pre-commit") + newFile(".GIT/config") +
			"diff --git a/.Git/HEAD b/.Git/HEAD\n--- a/.Git/HEAD\n+++ b/.Git/HEAD\n@@ -1 +1 @@\n-a\n+b\n"),
			patch.Refused{Reason: patch.ReasonGitDir, Paths: []string{".GIT/config", ".Git/HEAD", ".git/config", "sub/.git/hooks/pre-commit"}}},
		{"parent segment", []byte(newFile("../outside.txt")), patch.Refused{Reason: patch.ReasonOutsideRepository, Paths: []string{"../outside.txt"}}},
		{"inner parent segment", []byte(newFile("dir/../../outside.txt")),
			patch.Refused{Reason: patch.ReasonOutsideRepository, Paths: []string{"dir/../../outside.txt"}}},
		{"gitlink", gitlink, patch.Refused{Reason: patch.ReasonGitlink, Paths: []string{"vendor/sub"}}},
		{"node_modules additions", nodeAdds,
			patch.Refused{Reason: patch.ReasonNodeModules, Paths: []string{"node_modules/x/index.js", "pkg/node_modules/y.js"}}},
		{"git dir before node_modules", []byte(newFile(".git/config") + newFile("node_modules/a.js")),
			patch.Refused{Reason: patch.ReasonGitDir, Paths: []string{".git/config"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scratch := f.scratch(t)
			got, err := f.apply(t, scratch, patch.Input{Base: f.base, ChangedFrom: f.base, Patch: c.patch})
			ref, ok := got.(patch.Refused)
			if err != nil || !ok || ref.Reason != c.want.Reason || !slices.Equal(ref.Paths, c.want.Paths) {
				t.Fatalf("Apply = %+v, %v; want %+v", got, err, c.want)
			}
			for _, d := range []string{f.clone, scratch} {
				for _, up := range []string{filepath.Dir(d), filepath.Dir(filepath.Dir(d))} {
					if _, err := os.Lstat(filepath.Join(up, "outside.txt")); err == nil {
						t.Errorf("%s/outside.txt exists", up)
					}
				}
			}
		})
	}
}

func TestApply_Applied(t *testing.T) {
	f := newApplyFixture(t)
	check := func(t *testing.T, got []policy.ChangedPath) policy.PathResult {
		res, err := policy.CheckPaths(policy.Profile{Autonomy: policy.AutonomyPropose, LowMaxLines: 200}, got, nil)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	cases := []struct {
		name  string
		patch []byte
		want  []string
		check func(*testing.T, []policy.ChangedPath)
	}{
		{"node_modules changes that are not additions", f.patchTo(t, map[string]string{
			"node_modules/vendored.js": "V\n", "node_modulesx/a.js": "a\n", "my_node_modules/b.js": "b\n"}, nil),
			[]string{"my_node_modules/b.js", "node_modules/vendored.js", "node_modulesx/a.js"}, nil},
		{"symlink is a path-policy outcome", f.patchTo(t, nil, f.cacheinfo(t, "120000", "link", "../../etc/passwd")),
			[]string{"link"}, func(t *testing.T, got []policy.ChangedPath) {
				res := check(t, got)
				if got[0].NewMode != policy.ModeSymlink || !slices.Equal(res.Symlinks, []string{"link"}) || res.Passes() {
					t.Errorf("symlink: %+v, %+v", got[0], res)
				}
			}},
		{"forbidden paths are a path-policy outcome",
			f.patchTo(t, map[string]string{".claude/settings.json": "{}\n", "pkg/.opencode/plugin.js": "p\n"}, nil),
			[]string{".claude/settings.json", "pkg/.opencode/plugin.js"}, func(t *testing.T, got []policy.ChangedPath) {
				if res := check(t, got); !slices.Contains(res.Forbidden, ".claude/settings.json") ||
					!slices.Contains(res.Forbidden, "pkg/.opencode/plugin.js") {
					t.Errorf("Forbidden = %q", res.Forbidden)
				}
			}},
		{"tree paths named like clone files", f.patchTo(t, map[string]string{"hooks/post-checkout": "#!/bin/sh\n", "config": "c\n"},
			func() {
				if err := os.Chmod(filepath.Join(f.src, "hooks/post-checkout"), 0o755); err != nil {
					t.Fatal(err)
				}
				gitOut(t, f.r, f.src, nil, "add", "--", "hooks/post-checkout")
			}), []string{"config", "hooks/post-checkout"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := f.apply(t, f.scratch(t), patch.Input{Base: f.base, ChangedFrom: f.base, Patch: c.patch})
			a, ok := got.(patch.Applied)
			if err != nil || !ok {
				t.Fatalf("Apply = %+v, %v; want Applied", got, err)
			}
			if paths := pathsOf(a.ChangedPaths); !slices.Equal(paths, c.want) {
				t.Errorf("changed paths %q, want %q", paths, c.want)
			}
			if c.check != nil {
				c.check(t, a.ChangedPaths)
			}
		})
	}
	err := filepath.WalkDir(filepath.Dir(filepath.Dir(f.clone)), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink != 0 {
			t.Errorf("symlink %s exists", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApply_RoundTrip(t *testing.T) {
	f := newApplyFixture(t)
	cp := filepath.Join(t.TempDir(), "copy")
	if err := f.r.NewRunCopy(t.Context(), f.clone, f.base, cp); err != nil {
		t.Fatalf("NewRunCopy: %v", err)
	}
	writeFile(t, filepath.Join(cp, "edit.txt"), "1\nTWO\n", 0o644)
	writeFile(t, filepath.Join(cp, "run.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(cp, "blob.bin"), "\x00\xff\x00\xfe", 0o644)
	if err := errors.Join(os.Remove(filepath.Join(cp, "del.txt")),
		os.Rename(filepath.Join(cp, "move/a.txt"), filepath.Join(cp, "moved.txt"))); err != nil {
		t.Fatal(err)
	}
	p := exportPatch(t, f.r, cp, f.base)
	got, err := f.apply(t, f.scratch(t), patch.Input{Base: f.base, ChangedFrom: f.base, Patch: p})
	a, ok := got.(patch.Applied)
	if err != nil || !ok {
		t.Fatalf("Apply = %+v, %v; want Applied", got, err)
	}
	if want := gitOut(t, f.r, cp, nil, "write-tree"); a.Tree != want {
		t.Errorf("tree %s, want the copy's %s", a.Tree, want)
	}
	want, err := patch.ChangedPaths(t.Context(), f.r, f.clone, f.base, a.Tree)
	if err != nil || !slices.Equal(a.ChangedPaths, want) || len(want) != 5 {
		t.Errorf("changed paths %+v, want %+v (%v)", a.ChangedPaths, want, err)
	}
}

// policy.md#changed-paths rule 1: changed paths run from the merge base.
func TestApply_ChangedFrom(t *testing.T) {
	f := newApplyFixture(t)
	p := f.patchTo(t, map[string]string{"later.txt": "L\n"}, nil)
	for _, c := range []struct {
		from string
		want []string
	}{{f.mb, []string{"earlier.txt", "later.txt"}}, {f.base, []string{"later.txt"}}} {
		got, err := f.apply(t, f.scratch(t), patch.Input{Base: f.base, ChangedFrom: c.from, Patch: p})
		a, ok := got.(patch.Applied)
		if err != nil || !ok {
			t.Fatalf("Apply = %+v, %v; want Applied", got, err)
		}
		if paths := pathsOf(a.ChangedPaths); !slices.Equal(paths, c.want) {
			t.Errorf("ChangedFrom %s: paths %q, want %q", c.from, paths, c.want)
		}
	}
}
