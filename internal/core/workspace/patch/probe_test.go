package patch_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

// Spec §14.1 probe 4: planted git hooks and config in the Executor copy never
// run on the host, through the clean clone, Apply, the base merge and
// ChangedPaths.
func TestProbe4_PlantedHooksAndConfigNeverRun(t *testing.T) {
	r := newRunner(t)
	markers, scripts := filepath.Join(t.TempDir(), "markers"), t.TempDir()
	hooks := []string{"post-checkout", "post-merge", "post-commit", "pre-commit", "reference-transaction",
		"post-index-change", "pre-applypatch", "post-applypatch", "applypatch-msg", "pre-auto-gc"}
	keys := []string{"core.fsmonitor", "filter.evil.clean", "filter.evil.smudge", "filter.evil.process",
		"diff.evil.textconv", "diff.external", "merge.evil.driver"}
	plantHooks := func(dir string) {
		for _, h := range hooks {
			markerScript(t, filepath.Join(dir, h), markers, h)
		}
	}
	plantHooks(filepath.Join(scripts, "hooks"))
	for _, k := range keys {
		markerScript(t, filepath.Join(scripts, k), markers, k)
	}
	plantConfig := func(repo string) {
		gitOut(t, r, repo, nil, "config", "core.hooksPath", filepath.Join(scripts, "hooks"))
		for _, k := range keys {
			gitOut(t, r, repo, nil, "config", k, filepath.Join(scripts, k))
		}
	}

	// 1. Every source commit comes before any planting.
	src, root := newSourceRepo(t, r)
	base := commitOn(t, r, src, root, map[string]string{".gitattributes": "* filter=evil diff=evil merge=evil\n",
		"tracked.txt": "one\n", "other.txt": "o\n"}, nil)
	cleanBase := commitOn(t, r, src, base, map[string]string{"other.txt": "O\n"}, nil)
	conflictBase := commitOn(t, r, src, base, map[string]string{"tracked.txt": "conflict\n"}, nil)
	// 2-3. Hooks and config in the source.
	plantHooks(filepath.Join(src, ".git", "hooks"))
	plantConfig(src)
	// 4. The Publisher's clean clone, then hooks in it.
	clone := cleanClone(t, r, src)
	plantHooks(filepath.Join(clone, "hooks"))
	// 5. The run copy and its export; only then hooks and config in it.
	cp := filepath.Join(t.TempDir(), "copy")
	if err := r.NewRunCopy(t.Context(), clone, base, cp); err != nil {
		t.Fatalf("NewRunCopy: %v", err)
	}
	writeFile(t, filepath.Join(cp, "tracked.txt"), "two\n", 0o644)
	writeFile(t, filepath.Join(cp, "new.txt"), "new\n", 0o644)
	exported := exportPatch(t, r, cp, base)
	plantHooks(filepath.Join(cp, ".git", "hooks"))
	plantConfig(cp)
	// 6. The same keys and bogus locations through the process environment.
	home := t.TempDir()
	gitOut(t, r, home, nil, "config", "--file", filepath.Join(home, ".gitconfig"), "core.hooksPath", filepath.Join(scripts, "hooks"))
	for _, k := range keys {
		gitOut(t, r, home, nil, "config", "--file", filepath.Join(home, ".gitconfig"), k, filepath.Join(scripts, k))
	}
	global, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "git", "config"), string(global), 0o644)
	for k, v := range map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_PARAMETERS": "'core.hooksPath=" + filepath.Join(scripts, "hooks") + "'",
		"GIT_CONFIG_COUNT":      "1", "GIT_CONFIG_KEY_0": "core.fsmonitor", "GIT_CONFIG_VALUE_0": filepath.Join(scripts, "core.fsmonitor"),
		"GIT_DIR": "/nonexistent/git-dir", "GIT_WORK_TREE": "/nonexistent/work-tree",
		"GIT_INDEX_FILE": "/nonexistent/index", "GIT_OBJECT_DIRECTORY": "/nonexistent/objects",
	} {
		t.Setenv(k, v)
	}

	apply := func(p []byte) patch.Outcome {
		got, err := patch.Apply(t.Context(), r, clone, filepath.Join(t.TempDir(), "scratch"),
			patch.Input{Base: base, ChangedFrom: base, Patch: p})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return got
	}
	applied, ok := apply(exported).(patch.Applied)
	if !ok {
		t.Fatal("Apply of the exported patch did not return Applied")
	}
	if ref, ok := apply([]byte(newFile(".git/hooks/post-checkout"))).(patch.Refused); !ok || ref.Reason != patch.ReasonGitDir {
		t.Errorf("Apply of .git/hooks/post-checkout = %+v, want git-dir", ref)
	}
	head := gitOut(t, r, clone, nil, "commit-tree", applied.Tree, "-p", base, "-m", "change")
	sync := func(b string) git.SyncOutcome {
		got, err := r.SyncWithBase(t.Context(), clone, git.SyncInput{Head: head, Base: b,
			WorkflowPaths: []string{".github/workflows"}, Identity: git.Identity{Name: "ddf", Email: "ddf@example.invalid"},
			When: time.Unix(1700000000, 0).UTC(), Message: "merge base"})
		if err != nil {
			t.Fatalf("SyncWithBase: %v", err)
		}
		return got
	}
	merged, ok := sync(cleanBase).(git.SyncMerged)
	if !ok {
		t.Fatal("SyncWithBase against the clean base did not merge")
	}
	if got, ok := sync(conflictBase).(git.SyncConflict); !ok {
		t.Errorf("SyncWithBase against the conflicting base = %+v, want SyncConflict", got)
	}
	if _, err := patch.ChangedPaths(t.Context(), r, clone, base, merged.Merge.MergeSHA); err != nil {
		t.Errorf("ChangedPaths: %v", err)
	}
	noMarkers(t, markers)
}
