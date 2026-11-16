package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

func TestNewRunner_ChecksPathsAndVersion(t *testing.T) {
	fake := func(script string) string {
		p := filepath.Join(t.TempDir(), "git")
		writeFile(t, p, "#!/bin/sh\n"+script+"\n", 0o755)
		return p
	}
	state := func() string { return filepath.Join(t.TempDir(), "state") }
	cases := []struct {
		name           string
		gitPath, dir   string
		tooOld, wantOK bool
	}{
		{"too old", fake("echo 'git version 2.38.1'"), state(), true, false},
		{"unparseable", fake("echo 'not git'"), state(), false, false},
		{"relative git path", "git", state(), false, false},
		{"relative state dir", gitPath(t), "state", false, false},
		{"host git", gitPath(t), state(), false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := git.NewRunner(t.Context(), c.gitPath, c.dir)
			if (err == nil) != c.wantOK || errors.Is(err, git.ErrGitTooOld) != c.tooOld {
				t.Fatalf("NewRunner: %v", err)
			}
			if c.tooOld && !strings.Contains(err.Error(), "2.38.1") || c.tooOld && !strings.Contains(err.Error(), "2.39.0") {
				t.Fatalf("error %q does not name both versions", err)
			}
		})
	}
}

// spec §8.3: no system, global or inherited config reaches git.
func TestRun_IgnoresInheritedConfig(t *testing.T) {
	home := t.TempDir()
	evil := "[core]\n\thooksPath = /tmp/evil\n\tfsmonitor = /tmp/evil\n" +
		"[filter \"x\"]\n\tclean = /tmp/evil\n[diff \"x\"]\n\ttextconv = /tmp/evil\n[merge \"x\"]\n\tdriver = /tmp/evil\n"
	writeFile(t, filepath.Join(home, ".gitconfig"), evil, 0o644)
	writeFile(t, filepath.Join(home, "git", "config"), evil, 0o644)
	for k, v := range map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": home, "GIT_CONFIG_PARAMETERS": "'core.evil=1'",
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.evil2", "GIT_CONFIG_VALUE_0": "1",
		"GIT_DIR": "/nonexistent", "GIT_EXEC_PATH": "/nonexistent",
	} {
		t.Setenv(k, v)
	}
	r, _ := newRunner(t)
	out, err := r.Run(t.Context(), t.TempDir(), nil, "config", "--list", "--show-origin")
	if err != nil {
		t.Fatalf("config --list: %v", err)
	}
	var keys []string
	for line := range strings.Lines(strings.TrimSpace(string(out))) {
		rest, ok := strings.CutPrefix(line, "command line:")
		if !ok {
			t.Fatalf("config from outside the command line: %q", line)
		}
		key, _, _ := strings.Cut(strings.TrimSpace(rest), "=")
		keys = append(keys, key)
	}
	want := []string{"core.hookspath", "core.fsmonitor", "core.attributesfile", "core.symlinks", "core.protecthfs",
		"core.protectntfs", "protocol.allow", "protocol.file.allow", "gc.auto", "maintenance.auto"}
	if !slices.Equal(keys, want) {
		t.Fatalf("config keys = %v, want %v", keys, want)
	}
}

func TestRun_Failures(t *testing.T) {
	r, state := newRunner(t)
	repo := newSourceRepo(t, r)
	nested := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := r.Run(t.Context(), nested, nil, "rev-parse", "--git-dir"); err == nil {
		t.Error("rev-parse in a dir nested in a worktree succeeded: GIT_CEILING_DIRECTORIES not applied")
	}
	if _, err := r.Run(t.Context(), "relative", nil, "version"); err == nil {
		t.Error("Run accepted a relative dir")
	}
	if _, err := r.Run(cancelled, repo, nil, "status"); !errors.Is(err, context.Canceled) {
		t.Errorf("Run with a cancelled context = %v, want context.Canceled", err)
	}
	_, err := r.Run(t.Context(), repo, nil, "rev-parse", "--verify", "nosuchref^{commit}")
	var ce *git.CommandError
	if !errors.As(err, &ce) || ce.ExitCode != 128 || ce.Stderr == "" || len(ce.Stderr) > 2048 ||
		!strings.Contains(ce.Error(), "rev-parse") {
		t.Errorf("rev-parse of a missing ref = %v (%+v)", err, ce)
	}

	// A planted hook in the hooks dir stops git before it starts.
	markers := t.TempDir()
	markerScript(t, filepath.Join(state, "empty", "reference-transaction"), markers, "hook")
	if _, err := r.Run(t.Context(), repo, nil, "update-ref", "refs/heads/x", "HEAD"); err == nil {
		t.Error("Run started git with a non-empty hooks dir")
	}
	noMarkers(t, markers)
	if _, err := git.NewRunner(t.Context(), gitPath(t), state); err == nil {
		t.Error("NewRunner accepted a non-empty hooks dir")
	}
}

func TestRun_BoundsStderr(t *testing.T) {
	p := filepath.Join(t.TempDir(), "git")
	writeFile(t, p, "#!/bin/sh\ncase \"$*\" in *' version') echo 'git version 2.56.0';;\n"+
		"*) head -c 5000 /dev/zero | tr '\\0' x >&2; exit 3;; esac\n", 0o755)
	r, err := git.NewRunner(t.Context(), p, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	_, err = r.Run(t.Context(), t.TempDir(), nil, "-c", "user.name=x", "status")
	var ce *git.CommandError
	if !errors.As(err, &ce) || ce.ExitCode != 3 || len(ce.Stderr) != 2048 || ce.Error() != "git status: exit code 3" {
		t.Fatalf("Run = %v (%+v)", err, ce)
	}
}

func TestRun_GlobalOptions(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	p := filepath.Join(t.TempDir(), "git")
	writeFile(t, p, "#!/bin/sh\ncase \"$*\" in *' version') echo 'git version 2.56.0';; *) touch '"+marker+"';; esac\n", 0o755)
	r, err := git.NewRunner(t.Context(), p, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	cases := []struct {
		args    string
		refused bool
	}{
		{"-C /tmp status", true},
		{"--exec-path=/tmp status", true},
		{"--git-dir=/tmp status", true},
		{"--work-tree=/tmp status", true},
		{"--config-env=core.hooksPath=X status", true},
		{"--namespace=x status", true},
		{"-c core.hooksPath=/tmp status", true},
		{"-c protocol.allow=always status", true},
		{"-c core.fsmonitor=/tmp status", true},
		{"-c user.name status", true},
		{"-c", true},
		{"-c user.name=x --git-dir=/tmp status", true},
		{"-c user.name=x -c user.email=y commit-tree x", false},
		{"-c core.logAllRefUpdates=false fetch x", false},
		{"-c USER.NAME=x status", false},
		{"status --git-dir=/tmp", false},
	}
	for _, c := range cases {
		_, err := r.Run(t.Context(), t.TempDir(), nil, strings.Fields(c.args)...)
		_, statErr := os.Stat(marker)
		if errors.Is(err, git.ErrGlobalOption) != c.refused || (err == nil) == c.refused || (statErr == nil) == c.refused {
			t.Errorf("Run(%s) = %v, git started: %v; want refused %v", c.args, err, statErr == nil, c.refused)
		}
		_ = os.Remove(marker)
	}
}
