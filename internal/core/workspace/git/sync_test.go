package git_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

const ci = ".github/workflows/ci.yml"

// world is a source repository whose root holds a.txt, the CI file and a
// .gitattributes naming undefined drivers, with commits forked from root.
type world struct {
	r    *git.Runner
	src  string
	root string
	c    map[string]string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	r, _ := newRunner(t)
	src := newSourceRepo(t, r)
	base := gitOut(t, r, src, nil, "rev-parse", "HEAD")
	w := &world{r: r, src: src, c: map[string]string{}}
	w.root = commitOn(t, r, src, base, map[string]string{"a.txt": "a\n", ci: "v1\n",
		".gitattributes": "* merge=evil diff=evil filter=evil\n"})
	for name, files := range map[string]map[string]string{
		"head":     {"h.txt": "head\n"},
		"headWS":   {"h.txt": "head \n"},
		"other":    {"o.txt": "other\n"},
		"ci":       {ci: "v2\n"},
		"notes":    {".github/workflows-notes.md": "n\n"},
		"docs":     {"docs/x.md": "x\n"},
		"ciDel":    {ci: deleted},
		"ciOut":    {ci: deleted, "ci.yml": "v1\n"},
		"ciIn":     {ci: deleted, ".github/workflows/ci2.yml": "v1\n"},
		"headConf": {"a.txt": "head\n"},
		"baseConf": {"a.txt": "base\n"},
		"confCI":   {"a.txt": "base\n", ci: "v2\n"},
		"headBin":  {"b.bin": "\x00head"},
		"baseBin":  {"b.bin": "\x00base"},
		"headBin2": {"b.bin": "\x00hea2"},
	} {
		w.c[name] = commitOn(t, r, src, w.root, files)
	}
	w.c["root"] = w.root
	tree := gitOut(t, r, src, nil, "rev-parse", w.root+"^{tree}")
	w.c["unrelated"] = keep(t, r, src, gitOut(t, r, src, nil, "commit-tree", "-m", "unrelated", tree))
	return w
}

func (w *world) input(head, base string) git.SyncInput {
	return git.SyncInput{Head: w.c[head], Base: w.c[base], WorkflowPaths: []string{".github/workflows"},
		Identity: git.Identity{Name: "DDF", Email: "ddf@example.invalid"},
		When:     time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC), Message: "Merge base into head"}
}

func refs(t *testing.T, r *git.Runner, dir string) string {
	t.Helper()
	return gitOut(t, r, dir, nil, "for-each-ref")
}

func TestSyncWithBase_Validation(t *testing.T) {
	w := newWorld(t)
	clone := cleanClone(t, w.r, w.src)
	before := refs(t, w.r, clone)
	cases := []struct {
		name string
		edit func(*git.SyncInput)
		want error
	}{
		{"short head", func(in *git.SyncInput) { in.Head = "abc" }, git.ErrInvalidSHA},
		{"uppercase head", func(in *git.SyncInput) { in.Head = strings.ToUpper(in.Head) }, git.ErrInvalidSHA},
		{"ref base", func(in *git.SyncInput) { in.Base = "master" }, git.ErrInvalidSHA},
		{"option base", func(in *git.SyncInput) { in.Base = "--upload-pack=x" }, git.ErrInvalidSHA},
		{"empty name", func(in *git.SyncInput) { in.Identity.Name = "" }, git.ErrInvalidIdentity},
		{"empty email", func(in *git.SyncInput) { in.Identity.Email = "" }, git.ErrInvalidIdentity},
		{"< in name", func(in *git.SyncInput) { in.Identity.Name = "a<b" }, git.ErrInvalidIdentity},
		{"> in email", func(in *git.SyncInput) { in.Identity.Email = "a>b" }, git.ErrInvalidIdentity},
		{"newline in name", func(in *git.SyncInput) { in.Identity.Name = "a\nb" }, git.ErrInvalidIdentity},
		{"zero when", func(in *git.SyncInput) { in.When = time.Time{} }, git.ErrInvalidSyncInput},
		{"empty message", func(in *git.SyncInput) { in.Message = "" }, git.ErrInvalidSyncInput},
		{"nil workflow paths", func(in *git.SyncInput) { in.WorkflowPaths = nil }, git.ErrNoWorkflowPaths},
	}
	for _, p := range []string{"", "/a", "a/../b", "./a", "a/"} {
		cases = append(cases, struct {
			name string
			edit func(*git.SyncInput)
			want error
		}{"workflow path " + strconv.Quote(p), func(in *git.SyncInput) { in.WorkflowPaths = []string{p} }, git.ErrInvalidWorkflowPath})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := w.input("head", "other")
			c.edit(&in)
			if _, err := w.r.SyncWithBase(t.Context(), clone, in); !errors.Is(err, c.want) {
				t.Fatalf("SyncWithBase = %v, want %v", err, c.want)
			}
		})
	}
	in := w.input("head", "other")
	in.Head = strings.Repeat("0", 40)
	if _, err := w.r.SyncWithBase(t.Context(), clone, in); err == nil {
		t.Error("SyncWithBase of a missing commit succeeded")
	}
	if _, err := w.r.PatchID(t.Context(), clone, "master", w.c["head"]); !errors.Is(err, git.ErrInvalidSHA) {
		t.Errorf("PatchID of a ref = %v, want ErrInvalidSHA", err)
	}
	if got := refs(t, w.r, clone); got != before {
		t.Errorf("refs changed:\n%s\nwant\n%s", got, before)
	}
}

func TestSyncWithBase_Outcomes(t *testing.T) {
	w := newWorld(t)
	clone := cleanClone(t, w.r, w.src)
	// Hooks named by the issue and drivers named by the base's
	// .gitattributes must not run.
	markers := t.TempDir()
	for _, h := range []string{"post-merge", "post-commit", "reference-transaction", "pre-auto-gc"} {
		markerScript(t, filepath.Join(clone, "hooks", h), markers, h)
	}
	before := refs(t, w.r, clone)
	wf := func(p ...string) git.SyncOutcome { return git.SyncWorkflowChanged{Paths: p} }
	cases := []struct {
		head, base string
		want       git.SyncOutcome
	}{
		{"head", "root", git.SyncUpToDate{}},
		{"head", "head", git.SyncUpToDate{}},
		{"head", "ci", wf(ci)},
		{"headConf", "confCI", wf(ci)},
		{"head", "ciDel", wf(ci)},
		{"head", "ciOut", wf(ci)},
		{"head", "ciIn", wf(ci, ".github/workflows/ci2.yml")},
		{"headConf", "baseConf", git.SyncConflict{Paths: []string{"a.txt"}}},
		{"headBin", "baseBin", git.SyncConflict{Paths: []string{"b.bin"}}},
	}
	for _, c := range cases {
		got, err := w.r.SyncWithBase(t.Context(), clone, w.input(c.head, c.base))
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("SyncWithBase(%s, %s) = %#v, %v; want %#v", c.head, c.base, got, err, c.want)
		}
	}
	for _, base := range []string{"notes", "docs", "other"} {
		got, err := w.r.SyncWithBase(t.Context(), clone, w.input("head", base))
		if _, ok := got.(git.SyncMerged); err != nil || !ok {
			t.Errorf("SyncWithBase(head, %s) = %#v, %v; want SyncMerged", base, got, err)
		}
	}
	if got := refs(t, w.r, clone); got != before {
		t.Errorf("refs changed:\n%s\nwant\n%s", got, before)
	}
	noMarkers(t, markers)

	for _, paths := range [][]string{{".github/workflows"}, {":(glob)**"}} {
		got, err := w.r.WorkflowFilesChanged(t.Context(), clone, w.c["other"], w.c["head"], paths)
		if err != nil || len(got) != 0 {
			t.Errorf("WorkflowFilesChanged(other, %q) = %q, %v; want empty", paths, got, err)
		}
	}
	got, err := w.r.WorkflowFilesChanged(t.Context(), clone, w.c["ci"], w.c["head"], []string{":(glob)**"})
	if err != nil || len(got) != 0 {
		t.Errorf("WorkflowFilesChanged(ci, :(glob)**) = %q, %v; want empty (literal match)", got, err)
	}
}

func TestSyncWithBase_Merged(t *testing.T) {
	w := newWorld(t)
	clone := cleanClone(t, w.r, w.src)
	in := w.input("head", "other")
	got, err := w.r.SyncWithBase(t.Context(), clone, in)
	merged, ok := got.(git.SyncMerged)
	if err != nil || !ok {
		t.Fatalf("SyncWithBase = %#v, %v", got, err)
	}
	m := merged.Merge
	if m.BaseSHA != in.Base || m.ParentHeadSHA != in.Head {
		t.Errorf("BaseMerge = %+v", m)
	}
	tree := strings.SplitN(gitOut(t, w.r, clone, nil, "merge-tree", "--write-tree", in.Head, in.Base), "\n", 2)[0]
	unix := strconv.FormatInt(in.When.Unix(), 10)
	for args, want := range map[string]string{
		"rev-list --parents -n 1 " + m.MergeSHA: m.MergeSHA + " " + in.Head + " " + in.Base,
		"rev-parse " + m.MergeSHA + "^{tree}":   tree,
		"log -1 --date=raw --format=%an|%ae|%ad|%cn|%ce|%cd|%B " + m.MergeSHA: "DDF|ddf@example.invalid|" + unix +
			" +0000|DDF|ddf@example.invalid|" + unix + " +0000|" + in.Message,
	} {
		if got := gitOut(t, w.r, clone, nil, strings.Fields(args)...); got != want {
			t.Errorf("%s = %q, want %q", args, got, want)
		}
	}
	again, err := w.r.SyncWithBase(t.Context(), cleanClone(t, w.r, w.src), in)
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Errorf("second clone: %#v, %v; want %#v", again, err, got)
	}
	in.When = in.When.Add(time.Second)
	later, err := w.r.SyncWithBase(t.Context(), clone, in)
	if l, ok := later.(git.SyncMerged); err != nil || !ok || l.Merge.MergeSHA == m.MergeSHA {
		t.Errorf("different When: %#v, %v; want a different merge", later, err)
	}

	// spec §6.6 rule 4: a clean base merge of another file keeps the patch-id.
	pid := func(base, head string) (string, error) { return w.r.PatchID(t.Context(), clone, base, head) }
	want, err := pid(w.c["root"], w.c["head"])
	if err != nil || len(want) != 40 {
		t.Fatalf("PatchID = %q, %v", want, err)
	}
	for name, pair := range map[string][2]string{
		"repeat":      {w.root, w.c["head"]},
		"after merge": {w.c["other"], m.MergeSHA},
	} {
		if got, err := pid(pair[0], pair[1]); err != nil || got != want {
			t.Errorf("PatchID %s = %q, %v; want %q", name, got, err, want)
		}
	}
	for name, pair := range map[string][2]string{"whitespace": {"head", "headWS"}, "binary": {"headBin", "headBin2"}} {
		a, errA := pid(w.root, w.c[pair[0]])
		b, errB := pid(w.root, w.c[pair[1]])
		if errA != nil || errB != nil || a == b {
			t.Errorf("PatchID %s: %q, %v and %q, %v; want two different ids", name, a, errA, b, errB)
		}
	}
	if _, err := pid(w.c["head"], w.root); !errors.Is(err, git.ErrEmptyChange) {
		t.Errorf("PatchID of an empty change = %v, want ErrEmptyChange", err)
	}
	if id, err := pid(w.c["unrelated"], w.c["head"]); err == nil || errors.Is(err, git.ErrEmptyChange) {
		t.Errorf("PatchID of unrelated histories = %q, %v; want an error", id, err)
	}
}
