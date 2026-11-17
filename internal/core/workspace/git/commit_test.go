package git_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// commitWorld is a clean clone holding a root commit, two children of it
// and the tree of the first child.
type commitWorld struct {
	r                       *git.Runner
	clone                   string
	root, left, right, tree string
}

func newCommitWorld(t *testing.T) *commitWorld {
	t.Helper()
	r, _ := newRunner(t)
	src := newSourceRepo(t, r)
	root := keep(t, r, src, gitOut(t, r, src, nil, "rev-parse", "HEAD"))
	left := commitOn(t, r, src, root, map[string]string{"src/a.go": "package a\n"})
	right := commitOn(t, r, src, root, map[string]string{"b.txt": "b\n"})
	w := &commitWorld{r: r, clone: cleanClone(t, r, src), root: root, left: left, right: right}
	w.tree = gitOut(t, r, w.clone, nil, "rev-parse", left+"^{tree}")
	return w
}

// when is in a non-UTC zone so the test proves the date is written in UTC.
var when = time.Date(2026, 11, 17, 10, 0, 0, 0, time.FixedZone("plus2", 2*3600))

func (w *commitWorld) input(parents ...string) git.CommitInput {
	return git.CommitInput{Tree: w.tree, Parents: parents,
		Identity: git.Identity{Name: "DDF Test", Email: "ddf@example.invalid"},
		When:     when, Message: "subject line\n\nbody text\n"}
}

// refState is everything a commit must not change: the refs, HEAD and
// packed-refs of the clean clone.
func refState(t *testing.T, w *commitWorld) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(gitOut(t, w.r, w.clone, nil, "for-each-ref"))
	for _, name := range []string{"HEAD", "packed-refs"} {
		data, err := os.ReadFile(filepath.Join(w.clone, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		b.WriteString("\n--" + name + "--\n" + string(data))
	}
	if _, err := os.Stat(filepath.Join(w.clone, "logs")); err == nil {
		b.WriteString("\n--logs present--")
	}
	return b.String()
}

func TestCommitTree_WritesDeterministicCommit(t *testing.T) {
	w := newCommitWorld(t)
	stamp := strconv.FormatInt(when.Unix(), 10) + " +0000"
	for _, tc := range []struct {
		name    string
		parents []string
	}{
		{"one parent", []string{w.left}},
		{"two parents in given order", []string{w.left, w.right}},
		{"two parents reversed", []string{w.right, w.left}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := refState(t, w)
			in := w.input(tc.parents...)
			sha, err := w.r.CommitTree(t.Context(), w.clone, in)
			if err != nil {
				t.Fatalf("CommitTree: %v", err)
			}
			again, err := w.r.CommitTree(t.Context(), w.clone, in)
			if err != nil {
				t.Fatalf("CommitTree again: %v", err)
			}
			if again != sha {
				t.Errorf("same input gave %s then %s", sha, again)
			}
			out, err := w.r.Run(t.Context(), w.clone, nil, "cat-file", "-p", sha)
			if err != nil {
				t.Fatal(err)
			}
			want := "tree " + w.tree + "\n"
			for _, p := range tc.parents {
				want += "parent " + p + "\n"
			}
			want += "author DDF Test <ddf@example.invalid> " + stamp + "\n" +
				"committer DDF Test <ddf@example.invalid> " + stamp + "\n\n" + in.Message
			if string(out) != want {
				t.Errorf("cat-file -p %s:\n%s\nwant:\n%s", sha, out, want)
			}
			if after := refState(t, w); after != before {
				t.Errorf("refs changed:\n%s\nwant:\n%s", after, before)
			}
		})
	}
}

func TestCommitTree_RefusesInvalidInputBeforeGit(t *testing.T) {
	w := newCommitWorld(t)
	for _, tc := range []struct {
		name string
		edit func(*git.CommitInput)
		want error
	}{
		{"tree not a full sha", func(in *git.CommitInput) { in.Tree = w.tree[:12] }, git.ErrInvalidSHA},
		{"tree upper case", func(in *git.CommitInput) { in.Tree = strings.ToUpper(w.tree) }, git.ErrInvalidSHA},
		{"parent is a ref", func(in *git.CommitInput) { in.Parents = []string{"main"} }, git.ErrInvalidSHA},
		{"no parent", func(in *git.CommitInput) { in.Parents = nil }, git.ErrInvalidCommitInput},
		{"three parents", func(in *git.CommitInput) { in.Parents = []string{w.root, w.left, w.right} }, git.ErrInvalidCommitInput},
		{"duplicate parent", func(in *git.CommitInput) { in.Parents = []string{w.left, w.left} }, git.ErrInvalidCommitInput},
		{"'<' in name", func(in *git.CommitInput) { in.Identity.Name = "a<b" }, git.ErrInvalidIdentity},
		{"empty email", func(in *git.CommitInput) { in.Identity.Email = "" }, git.ErrInvalidIdentity},
		{"newline in email", func(in *git.CommitInput) { in.Identity.Email = "a@b\nc" }, git.ErrInvalidIdentity},
		{"zero time", func(in *git.CommitInput) { in.When = time.Time{} }, git.ErrInvalidCommitInput},
		{"empty message", func(in *git.CommitInput) { in.Message = "" }, git.ErrInvalidCommitInput},
		{"NUL in message", func(in *git.CommitInput) { in.Message = "a\x00b" }, git.ErrInvalidCommitInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := gitOut(t, w.r, w.clone, nil, "count-objects", "-v")
			in := w.input(w.left)
			tc.edit(&in)
			_, err := w.r.CommitTree(t.Context(), w.clone, in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CommitTree error %v, want %v", err, tc.want)
			}
			if after := gitOut(t, w.r, w.clone, nil, "count-objects", "-v"); after != before {
				t.Errorf("objects changed:\n%s\nwant:\n%s", after, before)
			}
		})
	}
}

func TestCommitTree_RefusesWrongObjectType(t *testing.T) {
	w := newCommitWorld(t)
	missing := strings.Repeat("0", len(w.tree))
	for _, tc := range []struct {
		name string
		edit func(*git.CommitInput)
	}{
		{"parent is a tree", func(in *git.CommitInput) { in.Parents = []string{w.tree} }},
		{"parent is missing", func(in *git.CommitInput) { in.Parents = []string{missing} }},
		{"tree is a commit", func(in *git.CommitInput) { in.Tree = w.left }},
		{"tree is missing", func(in *git.CommitInput) { in.Tree = missing }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := gitOut(t, w.r, w.clone, nil, "count-objects", "-v")
			in := w.input(w.left)
			tc.edit(&in)
			if sha, err := w.r.CommitTree(t.Context(), w.clone, in); err == nil {
				t.Fatalf("CommitTree gave %s, want an error", sha)
			}
			if after := gitOut(t, w.r, w.clone, nil, "count-objects", "-v"); after != before {
				t.Errorf("objects changed:\n%s\nwant:\n%s", after, before)
			}
		})
	}
}
