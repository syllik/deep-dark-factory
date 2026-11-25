package publisher

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// PushRequest pushes NewSHA to Branch of the remote of Clone. OldSHA empty
// creates the branch; the pusher must refuse unless the remote branch
// equals OldSHA (absent when empty). It is never a force push.
type PushRequest struct {
	Clone, Branch, OldSHA, NewSHA string
}

// Pusher pushes one revision. The daemon wires it per forge and wraps it
// in a 'push' intent (spec §4.5).
type Pusher interface {
	Push(ctx context.Context, mode ports.WriteMode, r PushRequest) error
}

// ChangeWriter opens or updates the change for a pushed revision.
type ChangeWriter interface {
	PublishChange(ctx context.Context, mode ports.WriteMode, r ports.PublishRequest) (ports.ChangeInfo, error)
}

var _ ChangeWriter = ports.Forge(nil)

// Publish publishes a Ready revision: exactly one Push, then exactly one
// PublishChange. An invalid mode or Ready is an error and nothing is
// called; a Push error is returned and PublishChange is not called.
func Publish(ctx context.Context, mode ports.WriteMode, pu Pusher, cw ChangeWriter, r Ready) (ports.ChangeInfo, error) {
	if _, err := ports.ParseWriteMode(string(mode)); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish: %w", err)
	}
	if pu == nil || cw == nil {
		return ports.ChangeInfo{}, errors.New("publish: nil pusher or change writer")
	}
	if err := validateReady(r); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish: %w", err)
	}
	if err := pu.Push(ctx, mode, r.Push); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish: push: %w", err)
	}
	info, err := cw.PublishChange(ctx, mode, r.Change)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish: publish change: %w", err)
	}
	return info, nil
}

func validateReady(r Ready) error {
	p := r.Push
	if !filepath.IsAbs(p.Clone) {
		return fmt.Errorf("clone %q is not absolute", p.Clone)
	}
	for _, b := range []string{p.Branch, r.Change.BaseBranch} {
		if err := validBranchName(b); err != nil {
			return err
		}
	}
	if p.Branch == r.Change.BaseBranch {
		return fmt.Errorf("push branch %q is the base branch", p.Branch)
	}
	if p.Branch != r.Change.Branch {
		return fmt.Errorf("push branch %q differs from change branch %q", p.Branch, r.Change.Branch)
	}
	if p.OldSHA != "" && !isFullSHA(p.OldSHA) {
		return fmt.Errorf("old sha %q is not a full sha", p.OldSHA)
	}
	if !isFullSHA(p.NewSHA) {
		return fmt.Errorf("new sha %q is not a full sha", p.NewSHA)
	}
	if p.NewSHA != r.Change.HeadSHA {
		return fmt.Errorf("new sha %q differs from change head %q", p.NewSHA, r.Change.HeadSHA)
	}
	return nil
}

// isFullSHA reports whether s is a full lowercase SHA-1 or SHA-256 hex id.
func isFullSHA(s string) bool {
	return (len(s) == 40 || len(s) == 64) && isLowerHex(s)
}
