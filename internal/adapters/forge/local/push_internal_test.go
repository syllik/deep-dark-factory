package local

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// pushRepos is a bare remote with branch main, a work repository with
// origin set to it, and a Forge whose clean clone is a clone of it.
type pushRepos struct {
	r            *git.Runner
	remote, work string
	f            *Forge
	main, side   string
}

func runGit(t *testing.T, r *git.Runner, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=test", "-c", "user.email=test@example.invalid"}, args...)
	out, err := r.Run(context.Background(), dir, nil, full...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func writeWork(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newPushRepos builds the repositories: main has one commit with file
// f.txt; side is a pushed branch with one commit on top of main changing
// f.txt.
func newPushRepos(t *testing.T) *pushRepos {
	t.Helper()
	p, err := exec.LookPath("git")
	if err == nil {
		p, err = filepath.Abs(p)
	}
	if err != nil {
		t.Fatalf("git not found: %v", err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	r, err := git.NewRunner(ctx, p, filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	remote, work, clone := filepath.Join(dir, "remote.git"), filepath.Join(dir, "work"), filepath.Join(dir, "clone.git")
	runGit(t, r, dir, "init", "-q", "--bare", "--initial-branch=main", remote)
	runGit(t, r, dir, "init", "-q", "--initial-branch=main", work)
	runGit(t, r, work, "remote", "add", "origin", "file://"+remote)
	writeWork(t, work, "f.txt", "base\n")
	runGit(t, r, work, "add", "--", "f.txt")
	runGit(t, r, work, "commit", "-q", "-m", "base")
	main := runGit(t, r, work, "rev-parse", "HEAD")
	runGit(t, r, work, "checkout", "-q", "-b", "side")
	writeWork(t, work, "f.txt", "side\n")
	runGit(t, r, work, "commit", "-q", "-am", "side")
	side := runGit(t, r, work, "rev-parse", "HEAD")
	runGit(t, r, work, "push", "-q", "origin", "refs/heads/main:refs/heads/main", "refs/heads/side:refs/heads/side")
	if err := r.CleanClone(ctx, "file://"+remote, clone); err != nil {
		t.Fatal(err)
	}
	return &pushRepos{r: r, remote: remote, work: work, f: &Forge{cfg: Config{Git: r, CloneDir: clone}},
		main: main, side: side}
}

func (p *pushRepos) refs(t *testing.T) string {
	t.Helper()
	return runGit(t, p.r, p.remote, "for-each-ref", "--format=%(refname) %(objectname)")
}

func TestPushLease_RefusesBeforeGit(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct{ name, src, branch, expect string }{
		{"empty src", "", "b", ""},
		{"short src", sha[:12], "b", ""},
		{"upper-case src", strings.ToUpper(sha), "b", ""},
		{"ref name src", "refs/heads/main", "b", ""},
		{"plus src", "+" + sha[1:], "b", ""},
		{"short expect", sha, "b", sha[:12]},
		{"upper-case expect", sha, "b", strings.ToUpper(sha)},
		{"ref name expect", sha, "b", "main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A nil runner panics if git ran.
			f := &Forge{}
			if err := f.pushLease(context.Background(), tc.src, tc.branch, tc.expect); err == nil {
				t.Fatal("push succeeded, want a refusal")
			}
		})
	}
}

func TestPushLease_RefusesWithRefsUnchanged(t *testing.T) {
	p := newPushRepos(t)
	before := p.refs(t)
	for _, tc := range []struct{ name, src, branch, expect string }{
		{"short src", p.side[:12], "new", ""},
		{"upper-case src", strings.ToUpper(p.side), "new", ""},
		{"bad expect", p.side, "main", "main"},
		{"short expect", p.side, "main", p.main[:7]},
		{"empty branch", p.side, "", ""},
		{"branch with double dot", p.side, "a..b", ""},
		{"branch with space", p.side, "a b", ""},
		{"branch ending in .lock", p.side, "x.lock", ""},
		{"branch with colon", p.side, "a:b", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.f.pushLease(context.Background(), tc.src, tc.branch, tc.expect); err == nil {
				t.Fatal("push succeeded, want a refusal")
			}
			if got := p.refs(t); got != before {
				t.Errorf("remote refs changed:\n%s\nwant\n%s", got, before)
			}
		})
	}
}

func TestPushLease_CompareAndSwap(t *testing.T) {
	p := newPushRepos(t)
	ctx := context.Background()
	// Create-only: a new branch.
	if err := p.f.pushLease(ctx, p.side, "new", ""); err != nil {
		t.Fatalf("create-only push: %v", err)
	}
	if got := runGit(t, p.r, p.remote, "rev-parse", "refs/heads/new"); got != p.side {
		t.Fatalf("new = %s, want %s", got, p.side)
	}
	// Create-only on an existing branch is refused.
	before := p.refs(t)
	err := p.f.pushLease(ctx, p.main, "new", "")
	if !isCommandError(err) {
		t.Fatalf("create-only push over an existing branch: %v, want a git exit", err)
	}
	if got := p.refs(t); got != before {
		t.Errorf("refs changed on a refused create-only push")
	}
	// Stale expect is refused, even for a fast-forward.
	err = p.f.pushLease(ctx, p.side, "main", p.side)
	if !isCommandError(err) {
		t.Fatalf("push with a stale lease: %v, want a git exit", err)
	}
	if got := p.refs(t); got != before {
		t.Errorf("refs changed on a refused lease")
	}
	// Matching expect updates the branch.
	if err := p.f.pushLease(ctx, p.side, "main", p.main); err != nil {
		t.Fatalf("compare-and-swap push: %v", err)
	}
	if got := runGit(t, p.r, p.remote, "rev-parse", "refs/heads/main"); got != p.side {
		t.Fatalf("main = %s, want %s", got, p.side)
	}
	// A non-fast-forward with a matching lease is also a plain lease
	// update: the lease, not fast-forwarding, guards the ref.
	if err := p.f.pushLease(ctx, p.main, "new", p.side); err != nil {
		t.Fatalf("lease push of an older commit: %v", err)
	}
	if got := runGit(t, p.r, p.remote, "rev-parse", "refs/heads/new"); got != p.main {
		t.Fatalf("new = %s, want %s", got, p.main)
	}
}

func TestPushLease_CancelledContextIsNotCommandError(t *testing.T) {
	p := newPushRepos(t)
	before := p.refs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.f.pushLease(ctx, p.side, "new", "")
	if err == nil || isCommandError(err) {
		t.Fatalf("push with a cancelled context: %v, want a non-exit error", err)
	}
	if got := p.refs(t); got != before {
		t.Errorf("refs changed")
	}
}

func TestCommitMessage(t *testing.T) {
	for _, tc := range []struct{ title, body, want string }{
		{"T", "", "T\n"},
		{"T", "B", "T\n\nB\n"},
		{"T", "B\n", "T\n\nB\n"},
		{"T", "B\n\n", "T\n\nB\n\n"},
		{"Add a", "line 1\nline 2", "Add a\n\nline 1\nline 2\n"},
		{" T ", " B", " T \n\n B\n"},
	} {
		if got := commitMessage(tc.title, tc.body); got != tc.want {
			t.Errorf("commitMessage(%q, %q) = %q, want %q", tc.title, tc.body, got, tc.want)
		}
	}
}

func TestParseMergeTree(t *testing.T) {
	const tree = "0123456789abcdef0123456789abcdef01234567"
	exit := func(code int) error { return &git.CommandError{Args: []string{"merge-tree"}, ExitCode: code} }
	for _, tc := range []struct {
		name    string
		out     string
		err     error
		want    mergeTreeResult
		wantErr bool
	}{
		{"clean", tree + "\x00", nil, mergeTreeResult{Tree: tree}, false},
		{"clean without terminator", tree, nil, mergeTreeResult{Tree: tree}, false},
		{"conflict with paths", tree + "\x00b.txt\x00a.txt\x00b.txt\x00", exit(1),
			mergeTreeResult{Conflict: true, Paths: []string{"a.txt", "b.txt"}}, false},
		{"conflict without stdout", "", exit(1), mergeTreeResult{Conflict: true}, false},
		{"conflict with a bad tree", "nope\x00a.txt\x00", exit(1), mergeTreeResult{}, true},
		{"other exit", tree + "\x00", exit(128), mergeTreeResult{}, true},
		{"not a git exit", "", errors.New("boom"), mergeTreeResult{}, true},
		{"empty clean output", "", nil, mergeTreeResult{}, true},
		{"clean with paths", tree + "\x00a.txt\x00", nil, mergeTreeResult{}, true},
		{"clean with a short sha", tree[:12] + "\x00", nil, mergeTreeResult{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMergeTree([]byte(tc.out), tc.err)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got.Tree != tc.want.Tree || got.Conflict != tc.want.Conflict || !slices.Equal(got.Paths, tc.want.Paths) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMergeTree_Run(t *testing.T) {
	p := newPushRepos(t)
	ctx := context.Background()
	// side on main: clean, the tree of side.
	got, err := p.f.mergeTree(ctx, p.main, p.side)
	if err != nil {
		t.Fatal(err)
	}
	if want := runGit(t, p.r, p.remote, "rev-parse", p.side+"^{tree}"); got.Tree != want || got.Conflict {
		t.Fatalf("clean merge-tree = %+v, want tree %s", got, want)
	}
	// other changes f.txt differently on main: a conflict.
	runGit(t, p.r, p.work, "checkout", "-q", "main")
	writeWork(t, p.work, "f.txt", "other\n")
	runGit(t, p.r, p.work, "commit", "-q", "-am", "other")
	other := runGit(t, p.r, p.work, "rev-parse", "HEAD")
	runGit(t, p.r, p.work, "push", "-q", "origin", "refs/heads/main:refs/heads/other")
	if err := p.r.Fetch(ctx, p.f.cfg.CloneDir); err != nil {
		t.Fatal(err)
	}
	got, err = p.f.mergeTree(ctx, other, p.side)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Conflict || got.Tree != "" {
		t.Fatalf("conflicting merge-tree = %+v, want a conflict", got)
	}
	if _, err := p.f.mergeTree(ctx, "main", p.side); err == nil {
		t.Fatal("merge-tree of a ref name succeeded")
	}
}

func TestValidateBranchPattern(t *testing.T) {
	for _, ok := range []string{"{type}/issue-{n}-{slug}", "{n}-{slug}", "x/{slug}/{n}/{n}", "{type}{n}{slug}"} {
		if err := validateBranchPattern(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "{type}/{x}-{n}-{slug}", "{type}/issue-{n}-{slug", "{type}/issue-{slug}",
		"{type}/issue-{n}", "{n}-{slug}}", "}{n}{slug}", "{{n}{slug}", "{N}{slug}", "{n}{slug}{"} {
		if err := validateBranchPattern(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got := expandBranchPattern("{type}/issue-{n}-{slug}/{n}", "revert", "7", "revert-0123456789ab"); got != "revert/issue-7-revert-0123456789ab/7" {
		t.Errorf("expand = %q", got)
	}
}
