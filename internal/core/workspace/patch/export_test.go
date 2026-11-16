package patch_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

func TestExportArgs(t *testing.T) {
	for _, base := range []string{strings.Repeat("a1", 20), strings.Repeat("0f", 32)} {
		got, err := patch.ExportArgs(base)
		want := [][]string{{"add", "-A"}, {"diff", "--cached", "--binary", "--full-index", "--no-renames",
			"--no-ext-diff", "--no-textconv", "--no-color", "--no-relative", "--src-prefix=a/", "--dst-prefix=b/", base, "--"}}
		if err != nil || len(got) != 2 || !slices.Equal(got[0], want[0]) || !slices.Equal(got[1], want[1]) {
			t.Errorf("ExportArgs(%q) = %q, %v; want %q", base, got, err, want)
		}
	}
	for _, base := range []string{"", "abc", strings.Repeat("a", 39), strings.Repeat("A", 40), "master", "--output=x"} {
		if _, err := patch.ExportArgs(base); !errors.Is(err, git.ErrInvalidSHA) {
			t.Errorf("ExportArgs(%q) = %v, want ErrInvalidSHA", base, err)
		}
	}
}

// TestExportArgs_Run runs the export steps in a copy the test wrote (spec §8.3).
func TestExportArgs_Run(t *testing.T) {
	r := newRunner(t)
	src, root := newSourceRepo(t, r)
	base := commitOn(t, r, src, root, map[string]string{"keep.txt": "a\nb\n", "gone.txt": "bye\n"}, nil)
	cp := filepath.Join(t.TempDir(), "copy")
	if err := r.NewRunCopy(t.Context(), cleanClone(t, r, src), base, cp); err != nil {
		t.Fatalf("NewRunCopy: %v", err)
	}
	// Copy-local config the fixed options and export flags must neutralise.
	markers := filepath.Join(t.TempDir(), "markers")
	for key, name := range map[string]string{"core.fsmonitor": "fsmonitor", "diff.external": "extdiff"} {
		script := filepath.Join(t.TempDir(), name)
		markerScript(t, script, markers, name)
		gitOut(t, r, cp, nil, "config", key, script)
	}
	gitOut(t, r, cp, nil, "config", "diff.noprefix", "true")
	writeFile(t, filepath.Join(cp, "keep.txt"), "a\nB\n", 0o644)
	if err := os.Remove(filepath.Join(cp, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cp, "run.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(cp, "blob.bin"), "\x00\xff\x00\xfe", 0o644)

	args, err := patch.ExportArgs(base)
	if err != nil {
		t.Fatal(err)
	}
	export := func() []byte {
		var out []byte
		for _, a := range args {
			if out, err = r.Run(t.Context(), cp, nil, a...); err != nil {
				t.Fatalf("git %v: %v", a, err)
			}
		}
		return out
	}
	got := export()
	noMarkers(t, markers)
	headers := regexp.MustCompile(`(?m)^diff --git (.*)$`).FindAllStringSubmatch(string(got), -1)
	for _, h := range headers {
		n := (len(h[1]) - 5) / 2 // len(<p>) if h[1] is 'a/<p> b/<p>'
		if n < 1 || h[1] != "a/"+h[1][2:2+n]+" b/"+h[1][2:2+n] {
			t.Errorf("diff header %q is not 'diff --git a/<p> b/<p>'", h[0])
		}
	}
	if len(headers) != 4 {
		t.Errorf("patch has %d diff headers, want 4:\n%s", len(headers), got)
	}
	for _, s := range []string{"GIT binary patch", "new file mode 100755", "deleted file mode 100644"} {
		if !bytes.Contains(got, []byte(s)) {
			t.Errorf("patch lacks %q:\n%s", s, got)
		}
	}
	if bytes.Contains(got, []byte("rename from")) {
		t.Errorf("patch has a rename:\n%s", got)
	}
	if again := export(); !bytes.Equal(again, got) {
		t.Errorf("second export differs:\n%s\nfirst:\n%s", again, got)
	}
}
