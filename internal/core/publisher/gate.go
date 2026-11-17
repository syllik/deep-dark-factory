package publisher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// ErrNoVerify is a local gate with no verify commands: nothing would prove
// the commit, so the gate refuses to run (fail closed).
var ErrNoVerify = errors.New("no verify commands")

// GateInput is one local gate run on Commit, a commit in the clean clone.
type GateInput struct {
	Key    model.TaskKey
	RunID  string
	Commit string
	// CopyDir is the absolute path of the per-run copy; it must not exist
	// and is removed before LocalGate returns.
	CopyDir string
	// Verify are the profile's verify commands, run in order.
	Verify []string
	Limits ports.CellLimits
	// Egress are the profile's registries, the cell's only egress.
	Egress []string
}

// GateResult is the local gate's decision. Failed is the index of the
// failing verify command and Outcome its outcome; Failed is -1 and Outcome
// zero when every command passed.
type GateResult struct {
	Passed  bool
	Failed  int
	Outcome ports.CellRunOutcome
}

// LocalGate runs in.Verify on in.Commit inside a sandbox cell before any
// push (spec §8.3): a per-run copy of the commit becomes the cell's
// workspace and each command runs there as 'sh -c', in order, stopping at
// the first non-zero exit or timeout. The host runs no verify command. A
// failing command is a GateResult with a nil error; an error means the gate
// could not decide and nothing may be published.
func LocalGate(ctx context.Context, r *git.Runner, sb ports.Sandbox, clone string, in GateInput) (res GateResult, err error) {
	if err := validateGate(in); err != nil {
		return GateResult{}, fmt.Errorf("local gate: %w", err)
	}
	// NewRunCopy refuses an existing CopyDir, creates it and removes it on
	// its own failure, so the cleanup below only removes what this call made.
	if err := r.NewRunCopy(ctx, clone, in.Commit, in.CopyDir); err != nil {
		return GateResult{}, fmt.Errorf("local gate: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(in.CopyDir); rmErr != nil {
			res, err = GateResult{}, errors.Join(err, fmt.Errorf("local gate: remove run copy: %w", rmErr))
		}
	}()
	cell, err := sb.CreateCell(ctx, ports.CellSpec{Key: in.Key, RunID: in.RunID, Limits: in.Limits,
		Network: ports.NetworkRules{AllowedEgress: slices.Clone(in.Egress)}})
	if err != nil {
		return GateResult{}, fmt.Errorf("local gate: create cell: %w", err)
	}
	defer func() {
		if killErr := sb.Kill(context.WithoutCancel(ctx), cell); killErr != nil {
			res, err = GateResult{}, errors.Join(err, fmt.Errorf("local gate: kill cell %s: %w", cell, killErr))
		}
	}()
	if err := sb.PutContent(ctx, cell, ports.CellContent{Workspace: os.DirFS(in.CopyDir)}); err != nil {
		return GateResult{}, fmt.Errorf("local gate: put content: %w", err)
	}
	for i, cmd := range in.Verify {
		out, err := sb.Run(ctx, cell, ports.CellRun{Argv: []string{"sh", "-c", cmd}})
		if err != nil {
			return GateResult{}, fmt.Errorf("local gate: verify command %d: %w", i, err)
		}
		if out.ExitCode != 0 || out.TimedOut {
			return GateResult{Passed: false, Failed: i, Outcome: out}, nil
		}
	}
	return GateResult{Passed: true, Failed: -1}, nil
}

func validateGate(in GateInput) error {
	if _, err := model.ParseTaskKey(string(in.Key)); err != nil {
		return err
	}
	if in.RunID == "" {
		return errors.New("empty run id")
	}
	if (len(in.Commit) != 40 && len(in.Commit) != 64) || !isLowerHex(in.Commit) {
		return fmt.Errorf("commit %q: %w", in.Commit, git.ErrInvalidSHA)
	}
	if !filepath.IsAbs(in.CopyDir) {
		return fmt.Errorf("copy dir %q is not absolute", in.CopyDir)
	}
	if len(in.Verify) == 0 {
		return ErrNoVerify
	}
	for i, cmd := range in.Verify {
		if cmd == "" {
			return fmt.Errorf("verify command %d is empty", i)
		}
	}
	return nil
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
