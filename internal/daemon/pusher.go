package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// gitPusher pushes a revision from the clean clone to its origin with a
// lease on the old branch head.
type gitPusher struct {
	git    *git.Runner
	logger *slog.Logger
}

// NewGitPusher returns a publisher.Pusher that pushes from the request's
// clone with git. A live push is leased on the branch's old head (absent
// when OldSHA is empty), so it never overwrites a head it did not expect;
// dry-run only logs.
func NewGitPusher(r *git.Runner, l *slog.Logger) publisher.Pusher {
	return &gitPusher{git: r, logger: l}
}

// Push implements publisher.Pusher.
func (p *gitPusher) Push(ctx context.Context, mode ports.WriteMode, req publisher.PushRequest) error {
	mode, err := ports.ParseWriteMode(string(mode))
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	if p.git == nil || p.logger == nil {
		return errors.New("push: nil git runner or logger")
	}
	if !isPushSHA(req.NewSHA) {
		return fmt.Errorf("push: new sha %q: %w", req.NewSHA, git.ErrInvalidSHA)
	}
	if req.OldSHA != "" && !isPushSHA(req.OldSHA) {
		return fmt.Errorf("push: old sha %q: %w", req.OldSHA, git.ErrInvalidSHA)
	}
	if req.Branch == "" {
		return errors.New("push: empty branch")
	}
	if !filepath.IsAbs(req.Clone) {
		return fmt.Errorf("push: clone %q is not absolute", req.Clone)
	}
	switch mode {
	case ports.WriteModeDryRun:
		p.logger.Info("dry-run: push skipped", "branch", req.Branch, "oldSha", req.OldSHA, "newSha", req.NewSHA)
		return nil
	case ports.WriteModeLive:
	}
	// The lease is explicit, so an empty OldSHA requires the branch to be
	// absent; it is never a plain or '+' refspec.
	if _, err := p.git.Run(ctx, req.Clone, nil, "push",
		"--force-with-lease=refs/heads/"+req.Branch+":"+req.OldSHA,
		"origin", req.NewSHA+":refs/heads/"+req.Branch); err != nil {
		return fmt.Errorf("push %s to %s: %w", req.NewSHA, req.Branch, err)
	}
	p.logger.Info("pushed revision", "branch", req.Branch, "oldSha", req.OldSHA, "newSha", req.NewSHA)
	return nil
}

// isPushSHA reports whether s is a full lowercase SHA-1 or SHA-256 hex id.
func isPushSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
