package fakes_test

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syllik/deep-dark-factory/test/fakes"
)

// gitEnv is an explicit git environment: no system or global config.
func gitEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"HOME=" + t.TempDir(),
		"PATH=" + os.Getenv("PATH"),
	}
}

func runGit(t *testing.T, git, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// gitRepo writes files into a new committed git repository and returns its
// directory.
func gitRepo(t *testing.T, git string, env []string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, git, dir, env, "init", "--quiet", "--initial-branch=main")
	runGit(t, git, dir, env, "add", "-A")
	runGit(t, git, dir, env, "-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"commit", "--quiet", "--allow-empty", "-m", "base")
	return dir
}

// treeFiles reads every regular file under dir outside .git.
func treeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		b, err := fs.ReadFile(os.DirFS(dir), p)
		if err != nil {
			return err
		}
		got[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBuildPatch_GitApply(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required: %v", err)
	}
	tests := []struct {
		name  string
		base  map[string]string
		edits []fakes.FileEdit
		want  map[string]string
	}{
		{
			name:  "add a new file",
			base:  map[string]string{"keep.txt": "k\n"},
			edits: []fakes.FileEdit{{Path: "dir/new.txt", Content: []byte("one\ntwo\n")}},
			want:  map[string]string{"keep.txt": "k\n", "dir/new.txt": "one\ntwo\n"},
		},
		{
			name:  "modify a file",
			base:  map[string]string{"a.txt": "one\ntwo\nthree\n"},
			edits: []fakes.FileEdit{{Path: "a.txt", Content: []byte("one\n2\n")}},
			want:  map[string]string{"a.txt": "one\n2\n"},
		},
		{
			name:  "delete a file",
			base:  map[string]string{"a.txt": "gone\n", "b.txt": "b\n"},
			edits: []fakes.FileEdit{{Path: "a.txt", Delete: true}},
			want:  map[string]string{"b.txt": "b\n"},
		},
		{
			name:  "add an empty file (no hunk)",
			base:  map[string]string{"keep.txt": "k\n"},
			edits: []fakes.FileEdit{{Path: "empty.txt", Content: []byte{}}},
			want:  map[string]string{"keep.txt": "k\n", "empty.txt": ""},
		},
		{
			name:  "delete an empty file (no hunk)",
			base:  map[string]string{"empty.txt": "", "keep.txt": "k\n"},
			edits: []fakes.FileEdit{{Path: "empty.txt", Delete: true}},
			want:  map[string]string{"keep.txt": "k\n"},
		},
		{
			name:  "empty a file",
			base:  map[string]string{"a.txt": "x\n"},
			edits: []fakes.FileEdit{{Path: "a.txt", Content: []byte{}}},
			want:  map[string]string{"a.txt": ""},
		},
		{
			name:  "fill an empty file",
			base:  map[string]string{"a.txt": ""},
			edits: []fakes.FileEdit{{Path: "a.txt", Content: []byte("x\n")}},
			want:  map[string]string{"a.txt": "x\n"},
		},
		{
			name:  "old last line without newline",
			base:  map[string]string{"a.txt": "one\ntwo"},
			edits: []fakes.FileEdit{{Path: "a.txt", Content: []byte("one\ntwo\n")}},
			want:  map[string]string{"a.txt": "one\ntwo\n"},
		},
		{
			name:  "new last line without newline",
			base:  map[string]string{"a.txt": "one\n"},
			edits: []fakes.FileEdit{{Path: "a.txt", Content: []byte("one\ntwo")}},
			want:  map[string]string{"a.txt": "one\ntwo"},
		},
		{
			name:  "new file and deleted file without final newline",
			base:  map[string]string{"old.txt": "x"},
			edits: []fakes.FileEdit{{Path: "old.txt", Delete: true}, {Path: "new.txt", Content: []byte("y")}},
			want:  map[string]string{"new.txt": "y"},
		},
		{
			name: "multi-file, edits out of path order",
			base: map[string]string{"b.txt": "b\n", "c/d.txt": "d\n", "e.txt": "e\n"},
			edits: []fakes.FileEdit{
				{Path: "e.txt", Delete: true},
				{Path: "a.txt", Content: []byte("a\n")},
				{Path: "c/d.txt", Content: []byte("D\nD2\n")},
			},
			want: map[string]string{"a.txt": "a\n", "b.txt": "b\n", "c/d.txt": "D\nD2\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := gitEnv(t)
			dir := gitRepo(t, git, env, tt.base)
			patch, err := fakes.BuildPatch(os.DirFS(dir), tt.edits)
			if err != nil {
				t.Fatalf("BuildPatch: %v", err)
			}
			pf := filepath.Join(t.TempDir(), "change.patch")
			if err := os.WriteFile(pf, patch, 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, git, dir, env, "apply", "--check", pf)
			runGit(t, git, dir, env, "apply", pf)
			if got := treeFiles(t, dir); !maps.Equal(got, tt.want) {
				t.Errorf("tree after apply = %q, want %q\npatch:\n%s", got, tt.want, patch)
			}
		})
	}
}

func TestBuildPatch_Format(t *testing.T) {
	ws := fstest.MapFS{"a.txt": {Data: []byte("x\ny")}}
	got, err := fakes.BuildPatch(ws, []fakes.FileEdit{
		{Path: "a.txt", Content: []byte("z\n")},
		{Path: "b.txt", Content: []byte("n\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,1 @@\n-x\n-y\n\\ No newline at end of file\n+z\n" +
		"diff --git a/b.txt b/b.txt\nnew file mode 100644\n--- /dev/null\n+++ b/b.txt\n@@ -0,0 +1,1 @@\n+n\n"
	if string(got) != want {
		t.Errorf("patch =\n%s\nwant\n%s", got, want)
	}
}

func TestBuildPatch_Refuses(t *testing.T) {
	ws := fstest.MapFS{
		"a.txt":   {Data: []byte("a\n")},
		"bin.dat": {Data: []byte("a\x00b")},
		"dir/x":   {Data: []byte("x\n")},
	}
	tests := []struct {
		name  string
		ws    fs.FS
		edits []fakes.FileEdit
	}{
		{"absolute path", ws, []fakes.FileEdit{{Path: "/etc/x", Content: []byte("x\n")}}},
		{"dot-dot path", ws, []fakes.FileEdit{{Path: "../x", Content: []byte("x\n")}}},
		{"inner dot-dot path", ws, []fakes.FileEdit{{Path: "dir/../x", Content: []byte("x\n")}}},
		{"empty path", ws, []fakes.FileEdit{{Path: "", Content: []byte("x\n")}}},
		{"root path", ws, []fakes.FileEdit{{Path: ".", Content: []byte("x\n")}}},
		{"path git would quote", ws, []fakes.FileEdit{{Path: "a\tb", Content: []byte("x\n")}}},
		{"binary new content", ws, []fakes.FileEdit{{Path: "n.bin", Content: []byte("a\x00")}}},
		{"binary modified content", ws, []fakes.FileEdit{{Path: "a.txt", Content: []byte("a\x00")}}},
		{"binary workspace file", ws, []fakes.FileEdit{{Path: "bin.dat", Delete: true}}},
		{"delete of a missing file", ws, []fakes.FileEdit{{Path: "missing.txt", Delete: true}}},
		{"delete with content", ws, []fakes.FileEdit{{Path: "a.txt", Delete: true, Content: []byte("x")}}},
		{"no-op edit", ws, []fakes.FileEdit{{Path: "a.txt", Content: []byte("a\n")}}},
		{"duplicate paths", ws, []fakes.FileEdit{{Path: "a.txt", Content: []byte("b\n")}, {Path: "a.txt", Delete: true}}},
		{"directory path", ws, []fakes.FileEdit{{Path: "dir", Content: []byte("x\n")}}},
		{"nil workspace", nil, []fakes.FileEdit{{Path: "a.txt", Content: []byte("x\n")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if p, err := fakes.BuildPatch(tt.ws, tt.edits); err == nil {
				t.Errorf("BuildPatch = %q, nil; want an error", p)
			}
		})
	}
}

func TestBuildPatch_DoesNotReorderInput(t *testing.T) {
	edits := []fakes.FileEdit{{Path: "b", Content: []byte("b\n")}, {Path: "a", Content: []byte("a\n")}}
	if _, err := fakes.BuildPatch(fstest.MapFS{}, edits); err != nil {
		t.Fatal(err)
	}
	if edits[0].Path != "b" || !bytes.Equal(edits[1].Content, []byte("a\n")) {
		t.Errorf("BuildPatch reordered its input: %+v", edits)
	}
}
