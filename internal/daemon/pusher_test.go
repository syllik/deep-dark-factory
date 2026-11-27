package daemon_test

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/daemon"
)

// pushEnv is a bare remote, its clean clone and two revision commits that
// exist in the clone only.
type pushEnv struct {
	git          *git.Runner
	bare, clone  string
	main, c1, c2 string
}

func newPushEnv(t *testing.T) *pushEnv {
	t.Helper()
	r := engRunner(t)
	bare, main := engSeedRepo(t, r)
	clone := filepath.Join(t.TempDir(), "clone.git")
	if err := r.CleanClone(t.Context(), engFileURL(bare), clone); err != nil {
		t.Fatal(err)
	}
	commit := func(msg string, parent string) string {
		sha, err := r.CommitTree(t.Context(), clone, git.CommitInput{Tree: engGit(t, r, clone, "rev-parse", main+"^{tree}"),
			Parents: []string{parent}, Identity: engIdentity, When: engStart, Message: msg})
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	c1 := commit("revision 1\n", main)
	return &pushEnv{git: r, bare: bare, clone: clone, main: main, c1: c1, c2: commit("revision 2\n", c1)}
}

func (p *pushEnv) tip(t *testing.T, branch string) string {
	t.Helper()
	out, err := p.git.Run(t.Context(), p.bare, nil, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestGitPusher_LeasedPush(t *testing.T) {
	p := newPushEnv(t)
	var logs bytes.Buffer
	pu := daemon.NewGitPusher(p.git, slog.New(slog.NewTextHandler(&logs, nil)))
	const branch = "ddf/task-0123456789ab"
	req := func(old, next string) publisher.PushRequest {
		return publisher.PushRequest{Clone: p.clone, Branch: branch, OldSHA: old, NewSHA: next}
	}
	steps := []struct {
		name    string
		req     publisher.PushRequest
		wantErr bool
		wantTip string
	}{
		{"empty old sha creates the branch", req("", p.c1), false, p.c1},
		{"empty old sha refuses an existing branch", req("", p.c2), true, p.c1},
		{"wrong old sha is refused", req(p.main, p.c2), true, p.c1},
		{"matching old sha moves the branch", req(p.c1, p.c2), false, p.c2},
	}
	for _, s := range steps {
		err := pu.Push(t.Context(), ports.WriteModeLive, s.req)
		if (err != nil) != s.wantErr {
			t.Errorf("%s: err = %v, want error %v", s.name, err, s.wantErr)
		}
		if got := p.tip(t, branch); got != s.wantTip {
			t.Errorf("%s: remote tip = %q, want %q", s.name, got, s.wantTip)
		}
	}
}

func TestGitPusher_DryRunWritesNothing(t *testing.T) {
	p := newPushEnv(t)
	var logs bytes.Buffer
	pu := daemon.NewGitPusher(p.git, slog.New(slog.NewTextHandler(&logs, nil)))
	before := remoteHeads(t, p.git, p.bare)
	err := pu.Push(t.Context(), ports.WriteModeDryRun, publisher.PushRequest{Clone: p.clone, Branch: "ddf/task-x", NewSHA: p.c1})
	if err != nil {
		t.Fatal(err)
	}
	if after := remoteHeads(t, p.git, p.bare); after != before {
		t.Errorf("remote heads changed in dry-run: %s", after)
	}
	if !strings.Contains(logs.String(), p.c1) || !strings.Contains(logs.String(), "ddf/task-x") {
		t.Errorf("dry-run log %q does not name the branch and sha", logs.String())
	}
}

func TestGitPusher_RefusesInvalidRequests(t *testing.T) {
	p := newPushEnv(t)
	pu := daemon.NewGitPusher(p.git, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	ok := publisher.PushRequest{Clone: p.clone, Branch: "ddf/task-y", NewSHA: p.c1}
	cases := []struct {
		name string
		mode ports.WriteMode
		mut  func(*publisher.PushRequest)
	}{
		{"zero mode", "", func(*publisher.PushRequest) {}},
		{"unknown mode", "maybe", func(*publisher.PushRequest) {}},
		{"empty new sha", ports.WriteModeLive, func(r *publisher.PushRequest) { r.NewSHA = "" }},
		{"short new sha", ports.WriteModeLive, func(r *publisher.PushRequest) { r.NewSHA = p.c1[:12] }},
		{"upper-case new sha", ports.WriteModeLive, func(r *publisher.PushRequest) { r.NewSHA = strings.ToUpper(p.c1) }},
		{"ref as old sha", ports.WriteModeLive, func(r *publisher.PushRequest) { r.OldSHA = "main" }},
		{"empty branch", ports.WriteModeLive, func(r *publisher.PushRequest) { r.Branch = "" }},
		{"relative clone", ports.WriteModeLive, func(r *publisher.PushRequest) { r.Clone = "clone.git" }},
	}
	before := remoteHeads(t, p.git, p.bare)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ok
			c.mut(&r)
			if err := pu.Push(t.Context(), c.mode, r); err == nil {
				t.Error("Push succeeded, want error")
			}
		})
	}
	if after := remoteHeads(t, p.git, p.bare); after != before {
		t.Errorf("remote heads changed: %s", after)
	}
}
