package patch_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

const odd = "sp ace/ü\tt.txt"

// changedFixture returns a clean clone holding base and head, kept on
// branches of the source.
func changedFixture(t *testing.T) (r *git.Runner, clone, base, head string) {
	t.Helper()
	r = newRunner(t)
	src, root := newSourceRepo(t, r)
	base = commitOn(t, r, src, root, map[string]string{
		"mod.txt": "1\n2\n3\n4\n5\n", "del.txt": "gone\n", "exec.sh": "#!/bin/sh\n", "link": "old\n",
		"old/name.txt": "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n",
	}, nil)
	head = commitOn(t, r, src, base, map[string]string{
		"added.txt": "fresh\n", "mod.txt": "1\nTWO\n3\nFOUR\n5\n", "del.txt": deleted,
		"old/name.txt": deleted, "new/name.txt": "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nL10\n",
		"bin.dat": "\x00\x01\x00bin", odd: "odd\n",
	}, func() {
		if err := os.Chmod(filepath.Join(src, "exec.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		gitOut(t, r, src, nil, "add", "--", "exec.sh")
		oid := gitOut(t, r, src, []byte("new-target"), "hash-object", "-w", "--stdin")
		gitOut(t, r, src, nil, "update-index", "--add", "--cacheinfo", "120000,"+oid+",link")
	})
	return r, cleanClone(t, r, src), base, head
}

func TestChangedPaths(t *testing.T) {
	r, clone, base, head := changedFixture(t)
	zero := strings.Repeat("0", len(base))
	oid := func(commit, path string) string {
		if path == "" {
			return zero
		}
		return gitOut(t, r, clone, nil, "rev-parse", commit+":"+path)
	}
	A, M, D, T, R := policy.ChangeStatusAdded, policy.ChangeStatusModified, policy.ChangeStatusDeleted,
		policy.ChangeStatusTypeChanged, policy.ChangeStatusRenamed
	// Each row: status, path, old path (renames), base path, head path, modes, counts.
	rows := []struct {
		st                policy.ChangeStatus
		path, old, bp, hp string
		om, nm            policy.Mode
		add, del          int
		bin               bool
	}{
		{A, "added.txt", "", "", "added.txt", "000000", "100644", 1, 0, false},
		{M, "mod.txt", "", "mod.txt", "mod.txt", "100644", "100644", 2, 2, false},
		{D, "del.txt", "", "del.txt", "", "100644", "000000", 0, 1, false},
		{M, "exec.sh", "", "exec.sh", "exec.sh", "100644", "100755", 0, 0, false},
		{T, "link", "", "link", "link", "100644", "120000", 1, 1, false},
		{R, "new/name.txt", "old/name.txt", "old/name.txt", "new/name.txt", "100644", "100644", 1, 1, false},
		{A, "bin.dat", "", "", "bin.dat", "000000", "100644", 0, 0, true},
		{A, odd, "", "", odd, "000000", "100644", 1, 0, false},
	}
	got, err := patch.ChangedPaths(t.Context(), r, clone, base, head)
	if err != nil {
		t.Fatalf("ChangedPaths: %v", err)
	}
	if len(got) != len(rows) {
		t.Fatalf("got %d paths, want %d: %+v", len(got), len(rows), got)
	}
	for _, w := range rows {
		want := policy.ChangedPath{Status: w.st, Path: w.path, OldPath: w.old, OldMode: w.om, NewMode: w.nm,
			OldBlob: oid(base, w.bp), NewBlob: oid(head, w.hp), Added: w.add, Deleted: w.del, Binary: w.bin}
		i := slices.IndexFunc(got, func(c policy.ChangedPath) bool { return c.Path == w.path })
		if i < 0 || got[i] != want {
			t.Errorf("%q: got %+v, want %+v", w.path, got[max(i, 0)], want)
		}
	}
	if exec := got[slices.IndexFunc(got, func(c policy.ChangedPath) bool { return c.Path == "exec.sh" })]; exec.OldBlob != exec.NewBlob {
		t.Errorf("exec.sh mode-only change has blobs %s -> %s", exec.OldBlob, exec.NewBlob)
	}
	tree := gitOut(t, r, clone, nil, "rev-parse", head+"^{tree}")
	if byTree, err := patch.ChangedPaths(t.Context(), r, clone, base, tree); err != nil || !slices.Equal(byTree, got) {
		t.Errorf("ChangedPaths(base, tree) = %+v, %v; want the head's paths", byTree, err)
	}
	res, err := policy.CheckPaths(policy.Profile{Autonomy: policy.AutonomyPropose, LowMaxLines: 200}, got, nil)
	if err != nil || !slices.Equal(res.Symlinks, []string{"link"}) {
		t.Errorf("CheckPaths = %+v, %v; want Symlinks [link]", res, err)
	}
}

func TestChangedPaths_EqualAndInvalid(t *testing.T) {
	r, clone, base, head := changedFixture(t)
	if got, err := patch.ChangedPaths(t.Context(), r, clone, base, base); err != nil || len(got) != 0 {
		t.Errorf("ChangedPaths(base, base) = %+v, %v; want empty, nil", got, err)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("A", 40), strings.Repeat("a", 39), "master", "--output=x"} {
		if _, err := patch.ChangedPaths(t.Context(), r, clone, bad, head); !errors.Is(err, git.ErrInvalidSHA) {
			t.Errorf("ChangedPaths(%q, head) = %v, want ErrInvalidSHA", bad, err)
		}
		if _, err := patch.ChangedPaths(t.Context(), r, clone, base, bad); !errors.Is(err, git.ErrInvalidSHA) {
			t.Errorf("ChangedPaths(base, %q) = %v, want ErrInvalidSHA", bad, err)
		}
	}
	unknown := strings.Repeat("1", len(base))
	if got, err := patch.ChangedPaths(t.Context(), r, clone, base, unknown); err == nil {
		t.Errorf("ChangedPaths(base, unknown) = %+v, nil; want an error", got)
	}
}
