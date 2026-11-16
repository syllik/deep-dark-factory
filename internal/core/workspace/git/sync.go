package git

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

// Sentinels for SyncWithBase and WorkflowFilesChanged input.
var (
	ErrInvalidIdentity     = errors.New("invalid merge identity")
	ErrInvalidSyncInput    = errors.New("invalid sync input")
	ErrNoWorkflowPaths     = errors.New("no workflow paths")
	ErrInvalidWorkflowPath = errors.New("invalid workflow path")
)

// Identity is the author and committer of a daemon base merge.
type Identity struct {
	Name, Email string
}

// SyncInput is one daemon base merge request; Head and Base are full SHAs.
type SyncInput struct {
	Head, Base    string
	WorkflowPaths []string
	Identity      Identity
	When          time.Time
	Message       string
}

// BaseMerge holds the base_merges columns merge_sha, base_sha and
// parent_head_sha (spec §4.5).
type BaseMerge struct {
	MergeSHA, BaseSHA, ParentHeadSHA string
}

// SyncOutcome is the result of SyncWithBase: SyncUpToDate,
// SyncWorkflowChanged, SyncConflict or SyncMerged.
type SyncOutcome interface{ syncOutcome() }

// SyncUpToDate: Base is already an ancestor of Head.
type SyncUpToDate struct{}

// SyncWorkflowChanged: the base changed CI configuration since the merge
// base, so no merge is attempted (spec §5.2).
type SyncWorkflowChanged struct{ Paths []string }

// SyncConflict: merging Base into Head conflicts on Paths.
type SyncConflict struct{ Paths []string }

// SyncMerged: the merge commit was written; no ref points at it yet.
type SyncMerged struct{ Merge BaseMerge }

func (SyncUpToDate) syncOutcome()        {}
func (SyncWorkflowChanged) syncOutcome() {}
func (SyncConflict) syncOutcome()        {}
func (SyncMerged) syncOutcome()          {}

// SyncWithBase merges in.Base into in.Head in the clean clone dir (spec §5.2
// "Sync with base"). It writes only objects: no ref, index or worktree
// changes and no push; the daemon records the returned BaseMerge.
func (r *Runner) SyncWithBase(ctx context.Context, dir string, in SyncInput) (SyncOutcome, error) {
	if err := validateSync(in); err != nil {
		return nil, fmt.Errorf("sync with base: %w", err)
	}
	for _, sha := range []string{in.Head, in.Base} {
		if _, err := r.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", sha+"^{commit}"); err != nil {
			return nil, fmt.Errorf("sync with base: commit %s: %w", sha, err)
		}
	}
	_, err := r.Run(ctx, dir, nil, "merge-base", "--is-ancestor", in.Base, in.Head)
	if err == nil {
		return SyncUpToDate{}, nil
	}
	if !exitCode(err, 1) {
		return nil, fmt.Errorf("sync with base: %w", err)
	}
	changed, err := r.WorkflowFilesChanged(ctx, dir, in.Base, in.Head, in.WorkflowPaths)
	if err != nil {
		return nil, fmt.Errorf("sync with base: %w", err)
	}
	if len(changed) > 0 {
		return SyncWorkflowChanged{Paths: changed}, nil
	}
	out, err := r.run(ctx, dir, nil, nil,
		[]string{"merge-tree", "--write-tree", "--no-messages", "--name-only", "-z", in.Head, in.Base})
	fields := nulFields(out)
	if exitCode(err, 1) && len(fields) > 1 {
		paths := slices.Compact(slices.Sorted(slices.Values(fields[1:])))
		return SyncConflict{Paths: paths}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sync with base: %w", err)
	}
	if len(fields) != 1 || !validSHA(fields[0]) {
		return nil, fmt.Errorf("sync with base: unexpected merge-tree output %q", out)
	}
	date := fmt.Sprintf("@%d +0000", in.When.Unix())
	env := []string{"GIT_AUTHOR_NAME=" + in.Identity.Name, "GIT_AUTHOR_EMAIL=" + in.Identity.Email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=" + in.Identity.Name, "GIT_COMMITTER_EMAIL=" + in.Identity.Email, "GIT_COMMITTER_DATE=" + date}
	out, err = r.run(ctx, dir, nil, env,
		[]string{"commit-tree", fields[0], "-p", in.Head, "-p", in.Base, "-m", in.Message})
	if err != nil {
		return nil, fmt.Errorf("sync with base: %w", err)
	}
	merge, ok := singleLine(out)
	if !ok || !validSHA(merge) {
		return nil, fmt.Errorf("sync with base: unexpected commit-tree output %q", out)
	}
	return SyncMerged{Merge: BaseMerge{MergeSHA: merge, BaseSHA: in.Base, ParentHeadSHA: in.Head}}, nil
}

// WorkflowFilesChanged lists, sorted, the paths under the forge's CI paths
// (GitHub: ".github/workflows") that base changed since its merge base with
// head (spec §5.2). Paths match literally and a rename counts on both sides.
func (r *Runner) WorkflowFilesChanged(ctx context.Context, dir, base, head string, paths []string) ([]string, error) {
	if !validSHA(base) || !validSHA(head) {
		return nil, fmt.Errorf("workflow files of %q..%q: %w", base, head, ErrInvalidSHA)
	}
	if err := validateWorkflowPaths(paths); err != nil {
		return nil, fmt.Errorf("workflow files: %w", err)
	}
	mb, err := r.mergeBase(ctx, dir, base, head)
	if err != nil {
		return nil, fmt.Errorf("workflow files: %w", err)
	}
	// The :(literal) magic is the per-pathspec form of --literal-pathspecs,
	// which Run refuses as a leading global option.
	args := []string{"diff", "--name-only", "-z", "--no-renames", mb, base, "--"}
	for _, p := range paths {
		args = append(args, ":(literal)"+p)
	}
	out, err := r.Run(ctx, dir, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("workflow files: %w", err)
	}
	return slices.Sorted(slices.Values(nulFields(out))), nil
}

func validateSync(in SyncInput) error {
	if !validSHA(in.Head) || !validSHA(in.Base) {
		return fmt.Errorf("head %q, base %q: %w", in.Head, in.Base, ErrInvalidSHA)
	}
	for _, s := range []string{in.Identity.Name, in.Identity.Email} {
		if s == "" || strings.ContainsAny(s, "<>\n\x00") {
			return fmt.Errorf("%q: %w", s, ErrInvalidIdentity)
		}
	}
	if in.When.IsZero() {
		return fmt.Errorf("zero time: %w", ErrInvalidSyncInput)
	}
	if in.Message == "" || strings.Contains(in.Message, "\x00") {
		return fmt.Errorf("message: %w", ErrInvalidSyncInput)
	}
	return validateWorkflowPaths(in.WorkflowPaths)
}

// validateWorkflowPaths accepts only clean repository-relative paths.
func validateWorkflowPaths(paths []string) error {
	if len(paths) == 0 {
		return ErrNoWorkflowPaths
	}
	for _, p := range paths {
		if p == "" || strings.Contains(p, "\x00") || strings.HasPrefix(p, "/") || path.Clean(p) != p ||
			slices.Contains(strings.Split(p, "/"), "..") {
			return fmt.Errorf("%q: %w", p, ErrInvalidWorkflowPath)
		}
	}
	return nil
}

// exitCode reports whether err is a *CommandError with exit code code.
func exitCode(err error, code int) bool {
	var ce *CommandError
	return errors.As(err, &ce) && ce.ExitCode == code
}

// nulFields splits NUL-terminated git output, dropping empty fields.
func nulFields(out []byte) []string {
	return slices.DeleteFunc(strings.Split(string(out), "\x00"), func(s string) bool { return s == "" })
}
