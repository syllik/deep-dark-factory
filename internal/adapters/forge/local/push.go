package local

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// pushLease is the only push of the package (Task 10c agentDefault 8). It
// pushes the commit src to refs/heads/<branch> on origin from the clean
// clone, with a compare-and-swap lease: the remote ref must equal expect,
// or must not exist when expect is empty (create-only). src must be a full
// lowercase SHA, expect empty or a full lowercase SHA, and branch must pass
// refFormatOK; all are checked before the push runs. It never builds a
// plain or forced refspec. A refused push is a *git.CommandError the caller
// classifies by fetching and re-reading tips, never by parsing stderr.
func (f *Forge) pushLease(ctx context.Context, src, branch, expect string) error {
	if !validSHA(src) {
		return fmt.Errorf("push: source %q is not a full lowercase sha", src)
	}
	if expect != "" && !validSHA(expect) {
		return fmt.Errorf("push: expected sha %q is neither empty nor a full lowercase sha", expect)
	}
	if err := refFormatOK(ctx, f.cfg.Git, f.cfg.CloneDir, branch); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	ref := "refs/heads/" + branch
	if _, err := f.cfg.Git.Run(ctx, f.cfg.CloneDir, nil,
		"push", "--force-with-lease="+ref+":"+expect, "origin", src+":"+ref); err != nil {
		return fmt.Errorf("push %s to %s: %w", src, branch, err)
	}
	return nil
}

// isCommandError reports whether err is a git run that exited non-zero, as
// opposed to a refused argument or a cancelled context.
func isCommandError(err error) bool {
	var ce *git.CommandError
	return errors.As(err, &ce)
}

// commitMessage is the exact message of a commit the adapter writes (Task
// 10c agentDefault 4): the title and a newline when body is empty, else the
// title, a blank line and the body, ending in a newline. Title and body are
// used as stored.
func commitMessage(title, body string) string {
	if body == "" {
		return title + "\n"
	}
	msg := title + "\n\n" + body
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	return msg
}

// mergeTreeResult is the parsed output of
// `git merge-tree --write-tree --no-messages --name-only -z`.
type mergeTreeResult struct {
	// Tree is the merged tree; empty when Conflict.
	Tree string
	// Conflict is set for exit code 1.
	Conflict bool
	// Paths are the conflicting paths, sorted and unique. They are empty
	// when stdout was not available, which is the case through
	// git.Runner.Run: it returns no stdout with a *git.CommandError.
	Paths []string
}

// mergeTreeArgs are the arguments of the merge-tree run parseMergeTree
// reads; ours and theirs are full SHAs.
func mergeTreeArgs(ours, theirs string) []string {
	return []string{"merge-tree", "--write-tree", "--no-messages", "--name-only", "-z", ours, theirs}
}

// parseMergeTree reads the stdout and error of a mergeTreeArgs run. Exit 0
// must print exactly one full SHA, the tree. Exit 1 is a conflict; any
// NUL-separated fields after the first are its paths. Any other error, or
// unexpected output, is an error.
func parseMergeTree(out []byte, runErr error) (mergeTreeResult, error) {
	fields := slices.DeleteFunc(strings.Split(string(out), "\x00"), func(s string) bool { return s == "" })
	if runErr != nil {
		var ce *git.CommandError
		if !errors.As(runErr, &ce) || ce.ExitCode != 1 {
			return mergeTreeResult{}, fmt.Errorf("merge-tree: %w", runErr)
		}
		if len(fields) > 0 && !validSHA(fields[0]) {
			return mergeTreeResult{}, fmt.Errorf("merge-tree: unexpected conflict output %q", out)
		}
		var paths []string
		if len(fields) > 1 {
			paths = slices.Compact(slices.Sorted(slices.Values(fields[1:])))
		}
		return mergeTreeResult{Conflict: true, Paths: paths}, nil
	}
	if len(fields) != 1 || !validSHA(fields[0]) {
		return mergeTreeResult{}, fmt.Errorf("merge-tree: unexpected output %q", out)
	}
	return mergeTreeResult{Tree: fields[0]}, nil
}

// mergeTree runs merge-tree of ours and theirs in the clean clone and
// parses it. It writes only objects.
func (f *Forge) mergeTree(ctx context.Context, ours, theirs string) (mergeTreeResult, error) {
	if !validSHA(ours) || !validSHA(theirs) {
		return mergeTreeResult{}, fmt.Errorf("merge-tree: %q, %q are not full lowercase shas", ours, theirs)
	}
	out, err := f.cfg.Git.Run(ctx, f.cfg.CloneDir, nil, mergeTreeArgs(ours, theirs)...)
	return parseMergeTree(out, err)
}

// treeOf returns the tree SHA of commit in the clean clone.
func (f *Forge) treeOf(ctx context.Context, commit string) (string, error) {
	if !validSHA(commit) {
		return "", fmt.Errorf("tree of %q: not a full lowercase sha", commit)
	}
	out, err := f.cfg.Git.Run(ctx, f.cfg.CloneDir, nil, "rev-parse", "--verify", "--quiet", commit+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("tree of %s: %w", commit, err)
	}
	tree := strings.TrimSpace(string(out))
	if !validSHA(tree) {
		return "", fmt.Errorf("tree of %s: unexpected output %q", commit, tree)
	}
	return tree, nil
}
