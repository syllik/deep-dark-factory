package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrEmptyChange is a head that adds nothing relative to its merge base.
var ErrEmptyChange = errors.New("change is empty against its merge base")

// PatchID is the 'git patch-id --verbatim' of the whole change from the
// merge base of base and head to head in the clean clone dir, so a clean base
// merge touching other files leaves it unchanged (spec §6.6 rule 4).
func (r *Runner) PatchID(ctx context.Context, dir, base, head string) (string, error) {
	if !validSHA(base) || !validSHA(head) {
		return "", fmt.Errorf("patch-id of %q..%q: %w", base, head, ErrInvalidSHA)
	}
	mb, err := r.mergeBase(ctx, dir, base, head)
	if err != nil {
		return "", fmt.Errorf("patch-id: %w", err)
	}
	diff, err := r.Run(ctx, dir, nil,
		"diff", "--binary", "--full-index", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color", mb, head)
	if err != nil {
		return "", fmt.Errorf("patch-id: %w", err)
	}
	if len(diff) == 0 {
		return "", fmt.Errorf("patch-id of %s: %w", head, ErrEmptyChange)
	}
	out, err := r.Run(ctx, dir, diff, "patch-id", "--verbatim")
	if err != nil {
		return "", fmt.Errorf("patch-id: %w", err)
	}
	line, ok := singleLine(out)
	if fields := strings.Fields(line); ok && len(fields) > 0 && validSHA(fields[0]) {
		return fields[0], nil
	}
	return "", fmt.Errorf("patch-id: unexpected output %q", out)
}

// mergeBase is the single merge base of a and b; none is an error.
func (r *Runner) mergeBase(ctx context.Context, dir, a, b string) (string, error) {
	out, err := r.Run(ctx, dir, nil, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("merge base of %s and %s: %w", a, b, err)
	}
	if mb, ok := singleLine(out); ok && validSHA(mb) {
		return mb, nil
	}
	return "", fmt.Errorf("merge base of %s and %s: unexpected output %q", a, b, out)
}

// singleLine returns out without its trailing newline when it is exactly
// one line.
func singleLine(out []byte) (string, bool) {
	s, ok := strings.CutSuffix(string(out), "\n")
	return s, ok && !strings.Contains(s, "\n")
}
