package patch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// Reason is why Apply refused a patch. Every Reason is a failed run for the
// Publisher (spec §8.3).
type Reason string

// Reason values.
const (
	ReasonEmpty             Reason = "empty"
	ReasonTooLarge          Reason = "too-large"
	ReasonDoesNotApply      Reason = "does-not-apply"
	ReasonOutsideRepository Reason = "outside-repository"
	ReasonGitDir            Reason = "git-dir"
	ReasonGitlink           Reason = "gitlink"
	ReasonNodeModules       Reason = "node-modules"
)

// ParseReason returns the Reason spelled s; an unknown or empty value is an
// error.
func ParseReason(s string) (Reason, error) {
	r := Reason(s)
	if slices.Contains([]Reason{ReasonEmpty, ReasonTooLarge, ReasonDoesNotApply, ReasonOutsideRepository,
		ReasonGitDir, ReasonGitlink, ReasonNodeModules}, r) {
		return r, nil
	}
	return "", fmt.Errorf("unknown patch refusal reason %q", s)
}

// MaxPatchBytes bounds the patch Apply accepts.
const MaxPatchBytes = 64 << 20

// Input is one patch to apply.
type Input struct {
	// Base is the commit the patch was exported against (the run copy's
	// commit).
	Base string
	// ChangedFrom is the commit the changed paths are measured from: the
	// merge base of the integration branch and the change
	// (policy.md#changed-paths rule 1). It equals Base for a first revision
	// on an unmoved base.
	ChangedFrom string
	Patch       []byte
}

// Outcome is the result of Apply: Applied or Refused.
type Outcome interface{ outcome() }

// Applied: the patch applied onto Base as Tree, whose objects stay
// unreachable in the clean clone until the Publisher commits them.
type Applied struct {
	Tree         string
	ChangedPaths []policy.ChangedPath
}

// Refused: the patch is not published. Paths is sorted and unique, and
// empty for ReasonEmpty, ReasonTooLarge and ReasonDoesNotApply.
type Refused struct {
	Reason Reason
	Paths  []string
}

func (Applied) outcome() {}
func (Refused) outcome() {}

// pathOrder is the precedence of the path reasons.
var pathOrder = []Reason{ReasonGitDir, ReasonOutsideRepository, ReasonGitlink, ReasonNodeModules}

// Apply applies in.Patch onto in.Base in the bare clean clone, using a
// per-call linked worktree at scratch (absolute, must not exist) for its
// index, and removes it before returning (spec §8.3). It never changes the
// clone's refs, HEAD, config, hooks or index file. A refusal is a Refused
// with a nil error; an error means no decision could be made.
func Apply(ctx context.Context, r *git.Runner, clone, scratch string, in Input) (out Outcome, err error) {
	if !validSHA(in.Base) || !validSHA(in.ChangedFrom) {
		return nil, fmt.Errorf("apply onto %q from %q: %w", in.Base, in.ChangedFrom, git.ErrInvalidSHA)
	}
	if !filepath.IsAbs(clone) || !filepath.IsAbs(scratch) {
		return nil, fmt.Errorf("apply: clone %q and scratch %q must be absolute", clone, scratch)
	}
	if _, err := os.Lstat(scratch); err == nil {
		return nil, fmt.Errorf("apply: scratch %s: %w", scratch, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("apply: scratch: %w", err)
	}
	switch {
	case len(in.Patch) == 0:
		return Refused{Reason: ReasonEmpty}, nil
	case len(in.Patch) > MaxPatchBytes:
		return Refused{Reason: ReasonTooLarge}, nil
	}
	for _, sha := range []string{in.Base, in.ChangedFrom} {
		if _, err := r.Run(ctx, clone, nil, "rev-parse", "--verify", "--quiet", sha+"^{commit}"); err != nil {
			return nil, fmt.Errorf("apply: commit %s: %w", sha, err)
		}
	}
	// Pre-check git's own parse of every path before any object is written.
	num, err := r.Run(ctx, clone, in.Patch, "apply", "--numstat", "-z")
	if isCommandError(err) {
		return Refused{Reason: ReasonDoesNotApply}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	stats, err := parseNumstat(num)
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	hits := map[Reason][]string{}
	for _, s := range stats {
		addPathHit(hits, s.path)
		if s.oldPath != "" {
			addPathHit(hits, s.oldPath)
		}
	}
	if ref, ok := firstRefusal(hits); ok {
		return ref, nil
	}

	if _, err := r.Run(ctx, clone, nil, "worktree", "add", "--detach", "--no-checkout", scratch, in.Base); err != nil {
		return nil, fmt.Errorf("apply: add worktree: %w", err)
	}
	defer func() {
		// Cleanup runs even after cancellation; an error never carries an
		// outcome.
		if cErr := removeWorktree(context.WithoutCancel(ctx), r, clone, scratch); cErr != nil {
			out, err = nil, errors.Join(err, cErr)
		}
	}()
	return applyInWorktree(ctx, r, clone, scratch, in)
}

// applyInWorktree runs steps (e) to (g) of Apply in the linked worktree.
func applyInWorktree(ctx context.Context, r *git.Runner, clone, scratch string, in Input) (Outcome, error) {
	if _, err := r.Run(ctx, scratch, nil, "read-tree", in.Base); err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	_, err := r.Run(ctx, scratch, in.Patch, "apply", "--cached", "--whitespace=nowarn")
	if isCommandError(err) {
		return Refused{Reason: ReasonDoesNotApply}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	out, err := r.Run(ctx, scratch, nil, "write-tree")
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	tree := strings.TrimSuffix(string(out), "\n")
	if !validSHA(tree) {
		return nil, fmt.Errorf("apply: unexpected write-tree output %q", out)
	}
	out, err = r.Run(ctx, clone, nil, "diff-tree", "-r", "-z", "--raw", "--no-abbrev", "--no-renames", in.Base, tree)
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	entries, err := parseRaw(out)
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	if len(entries) == 0 {
		return Refused{Reason: ReasonEmpty}, nil
	}
	hits := map[Reason][]string{}
	for _, e := range entries {
		addPathHit(hits, e.Path)
		if e.Status != policy.ChangeStatusDeleted && e.NewMode == policy.ModeGitlink {
			hits[ReasonGitlink] = append(hits[ReasonGitlink], e.Path)
		}
		if e.Status == policy.ChangeStatusAdded && slices.Contains(strings.Split(e.Path, "/"), "node_modules") {
			hits[ReasonNodeModules] = append(hits[ReasonNodeModules], e.Path)
		}
	}
	if ref, ok := firstRefusal(hits); ok {
		return ref, nil
	}
	paths, err := ChangedPaths(ctx, r, clone, in.ChangedFrom, tree)
	if err != nil {
		return nil, fmt.Errorf("apply: %w", err)
	}
	return Applied{Tree: tree, ChangedPaths: paths}, nil
}

// addPathHit records p under ReasonGitDir or ReasonOutsideRepository when it
// breaks a path rule.
func addPathHit(hits map[Reason][]string, p string) {
	segs := strings.Split(p, "/")
	switch {
	case slices.ContainsFunc(segs, func(s string) bool { return strings.EqualFold(s, ".git") }):
		hits[ReasonGitDir] = append(hits[ReasonGitDir], p)
	case p == "" || strings.HasPrefix(p, "/") ||
		slices.ContainsFunc(segs, func(s string) bool { return s == "" || s == "." || s == ".." }):
		hits[ReasonOutsideRepository] = append(hits[ReasonOutsideRepository], p)
	}
}

// firstRefusal returns the first reason of pathOrder with hits, with its
// paths sorted and unique.
func firstRefusal(hits map[Reason][]string) (Refused, bool) {
	for _, reason := range pathOrder {
		if ps := hits[reason]; len(ps) > 0 {
			return Refused{Reason: reason, Paths: slices.Compact(slices.Sorted(slices.Values(ps)))}, true
		}
	}
	return Refused{}, false
}

// removeWorktree removes the linked worktree, falling back to deleting the
// directory and pruning its registration.
func removeWorktree(ctx context.Context, r *git.Runner, clone, scratch string) error {
	_, err := r.Run(ctx, clone, nil, "worktree", "remove", "--force", scratch)
	if err == nil {
		return nil
	}
	err = fmt.Errorf("apply: remove worktree: %w", err)
	if rmErr := os.RemoveAll(scratch); rmErr != nil {
		return errors.Join(err, fmt.Errorf("apply: remove scratch: %w", rmErr))
	}
	if _, pErr := r.Run(ctx, clone, nil, "worktree", "prune"); pErr != nil {
		return errors.Join(err, fmt.Errorf("apply: prune worktrees: %w", pErr))
	}
	return err
}

func isCommandError(err error) bool {
	var ce *git.CommandError
	return errors.As(err, &ce)
}
