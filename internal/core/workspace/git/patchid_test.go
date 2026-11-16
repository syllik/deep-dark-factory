package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run accepts only the identity keys in extraEnv and passes them to git.
func TestRun_ExtraEnv(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	p := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\ncase \"$*\" in *' version') echo 'git version 2.56.0';; *) touch '" + marker +
		"'; echo \"$GIT_AUTHOR_NAME|$GIT_COMMITTER_DATE\";; esac\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(t.Context(), p, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	for _, kv := range []string{"GIT_DIR=/tmp", "GIT_EXEC_PATH=/tmp", "git_author_name=x", "GIT_AUTHOR_NAME"} {
		if _, err := r.run(t.Context(), t.TempDir(), nil, []string{kv}, []string{"commit-tree"}); err == nil {
			t.Errorf("run with %q succeeded", kv)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("run with %q started git", kv)
		}
	}
	out, err := r.run(t.Context(), t.TempDir(), nil, []string{"GIT_AUTHOR_NAME=a", "GIT_COMMITTER_DATE=@1 +0000"}, []string{"commit-tree"})
	if err != nil || strings.TrimSpace(string(out)) != "a|@1 +0000" {
		t.Errorf("run = %q, %v", out, err)
	}
}
