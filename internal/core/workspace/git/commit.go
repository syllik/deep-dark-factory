package git

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ErrInvalidCommitInput is a CommitInput with a bad parent count, a
// duplicate parent, a zero time or an empty or NUL-holding message.
var ErrInvalidCommitInput = errors.New("invalid commit input")

// CommitInput is one commit of an existing tree; Tree and Parents are full
// SHAs and When gives both the author and the committer date.
type CommitInput struct {
	Tree     string
	Parents  []string
	Identity Identity
	When     time.Time
	Message  string
}

// CommitTree writes the commit object of in.Tree with in.Parents, in order,
// in the clean clone dir and returns its SHA. The same input gives the same
// SHA: the dates come from in.When, never from the wall clock. It writes
// only objects: no ref, index, HEAD or reflog changes and no push.
func (r *Runner) CommitTree(ctx context.Context, dir string, in CommitInput) (string, error) {
	if err := validateCommit(in); err != nil {
		return "", fmt.Errorf("commit tree: %w", err)
	}
	// The peeled object must be the input itself, so a commit or tag never
	// stands in for a tree and a tag never stands in for a parent.
	checks := [][2]string{{in.Tree, "tree"}}
	for _, p := range in.Parents {
		checks = append(checks, [2]string{p, "commit"})
	}
	for _, c := range checks {
		out, err := r.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", c[0]+"^{"+c[1]+"}")
		if err != nil {
			return "", fmt.Errorf("commit tree: %s %s: %w", c[1], c[0], err)
		}
		if got, ok := singleLine(out); !ok || got != c[0] {
			return "", fmt.Errorf("commit tree: %s is not a %s", c[0], c[1])
		}
	}
	date := fmt.Sprintf("@%d +0000", in.When.Unix())
	env := []string{"GIT_AUTHOR_NAME=" + in.Identity.Name, "GIT_AUTHOR_EMAIL=" + in.Identity.Email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + in.Identity.Name, "GIT_COMMITTER_EMAIL=" + in.Identity.Email, "GIT_COMMITTER_DATE=" + date}
	args := []string{"commit-tree", "--no-gpg-sign", in.Tree}
	for _, p := range in.Parents {
		args = append(args, "-p", p)
	}
	args = append(args, "-F", "-")
	out, err := r.run(ctx, dir, []byte(in.Message), env, args)
	if err != nil {
		return "", fmt.Errorf("commit tree: %w", err)
	}
	sha, ok := singleLine(out)
	if !ok || !validSHA(sha) {
		return "", fmt.Errorf("commit tree: unexpected commit-tree output %q", out)
	}
	return sha, nil
}

func validateCommit(in CommitInput) error {
	if !validSHA(in.Tree) {
		return fmt.Errorf("tree %q: %w", in.Tree, ErrInvalidSHA)
	}
	for _, p := range in.Parents {
		if !validSHA(p) {
			return fmt.Errorf("parent %q: %w", p, ErrInvalidSHA)
		}
	}
	if n := len(in.Parents); n < 1 || n > 2 {
		return fmt.Errorf("%d parents: %w", n, ErrInvalidCommitInput)
	}
	if len(slices.Compact(slices.Sorted(slices.Values(in.Parents)))) != len(in.Parents) {
		return fmt.Errorf("duplicate parent: %w", ErrInvalidCommitInput)
	}
	for _, s := range []string{in.Identity.Name, in.Identity.Email} {
		if s == "" || strings.ContainsAny(s, "<>\n\x00") {
			return fmt.Errorf("%q: %w", s, ErrInvalidIdentity)
		}
	}
	if in.When.IsZero() {
		return fmt.Errorf("zero time: %w", ErrInvalidCommitInput)
	}
	if in.Message == "" || strings.Contains(in.Message, "\x00") {
		return fmt.Errorf("message: %w", ErrInvalidCommitInput)
	}
	return nil
}
