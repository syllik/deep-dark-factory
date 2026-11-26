package local

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// ErrFormat is returned by Open when the storage directory is not empty and
// holds no valid format marker (docs/adapters/forge-local.md#storage).
var ErrFormat = errors.New("forge-local storage format")

// ErrUnsupportedRemote is returned by Open for a remote that is not a
// file: URL naming an existing bare repository.
var ErrUnsupportedRemote = errors.New("unsupported forge-local remote")

// ForgeName is the forge field of every forge-local task source.
const ForgeName = "local"

const (
	localOperatorName = "local operator"
	daemonName        = "DDF daemon"
)

// LocalOperator is the intake actor of inbox records and the author of
// answer files.
var LocalOperator = ports.Identity{ID: string(actorLocalOperator), Name: localOperatorName}

// Daemon is the intake actor of records DDF creates and the author of
// daemon comments.
var Daemon = ports.Identity{ID: string(actorDaemon), Name: daemonName}

var projectIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Config configures one forge-local project.
type Config struct {
	// ProjectID is the project id; it names the storage directory and is
	// the repository of every task source.
	ProjectID string
	// DataDir is the absolute, clean path of the data mount.
	DataDir string
	// RemoteURL is the file: URL of the project's bare repository.
	RemoteURL string
	Clock     ports.Clock
	IDs       ports.IDSource
	Logger    *slog.Logger

	// IntegrationBranch is the branch changes target; it must pass
	// `git check-ref-format refs/heads/<branch>`.
	IntegrationBranch string
	// CloneDir is the absolute, clean path of the daemon's clean clone of
	// the remote; Open clones it when absent and fetches it otherwise.
	CloneDir string
	// RunDir is the absolute, clean directory of per-run copies of check
	// attempts (RunDir/<run id>); Open creates it with mode 0700.
	RunDir string
	// Git is the only way the adapter runs git.
	Git *git.Runner
	// Sandbox runs the verify check of each head.
	Sandbox ports.Sandbox
	// Verify are the profile's verify commands, run in order; at least one.
	Verify []string
	// Limits are the check cell's limits; each is >= 0.
	Limits ports.CellLimits
	// Egress are the check cell's allowed egress destinations.
	Egress []string

	// BranchPattern is the profile's change.branch, for example
	// `{type}/issue-{n}-{slug}`. It may use only the placeholders {type},
	// {n} and {slug}, and must use {n} and {slug} (Task 10c agentDefault 5).
	// Revert names its branch with it.
	BranchPattern string
	// Identity is the author and committer of every commit the adapter
	// writes: squash merges, base merges and reverts.
	Identity git.Identity

	// beforeMergePush is the test hook of the lease window (Task 10c
	// agentDefault 9): Merge calls it with f.mu held, after CommitTree and
	// before the final fetch and change-tip re-check. It is unexported, so
	// it is nil in production; only export_test.go sets it.
	beforeMergePush func()
}

// Branch pattern placeholders (Task 10c agentDefault 5).
const (
	placeholderType = "{type}"
	placeholderN    = "{n}"
	placeholderSlug = "{slug}"
)

// validateBranchPattern refuses an empty pattern, a `{` that does not open
// {type}, {n} or {slug}, a stray `}`, and a pattern without {n} or without
// {slug} (Task 10c agentDefault 5).
func validateBranchPattern(p string) error {
	if p == "" {
		return errors.New("empty branch pattern")
	}
	seen := map[string]bool{}
	for rest := p; rest != ""; {
		i := strings.IndexAny(rest, "{}")
		if i < 0 {
			break
		}
		if rest[i] == '}' {
			return fmt.Errorf("branch pattern %q: stray '}'", p)
		}
		matched := ""
		for _, ph := range []string{placeholderType, placeholderN, placeholderSlug} {
			if strings.HasPrefix(rest[i:], ph) {
				matched = ph
				break
			}
		}
		if matched == "" {
			return fmt.Errorf("branch pattern %q: unknown placeholder or stray '{'", p)
		}
		seen[matched] = true
		rest = rest[i+len(matched):]
	}
	for _, ph := range []string{placeholderN, placeholderSlug} {
		if !seen[ph] {
			return fmt.Errorf("branch pattern %q: missing %s", p, ph)
		}
	}
	return nil
}

// expandBranchPattern replaces every occurrence of each placeholder of a
// validated pattern literally (Task 10c agentDefault 5). The result still
// has to pass refFormatOK.
func expandBranchPattern(p, typ, n, slug string) string {
	return strings.NewReplacer(placeholderType, typ, placeholderN, n, placeholderSlug, slug).Replace(p)
}

// validateIdentity applies git.CommitTree's identity rule early: Name and
// Email non-empty, with no `<`, `>`, newline or NUL (Task 10c agentDefault
// 5).
func validateIdentity(id git.Identity) error {
	for _, f := range []struct{ name, v string }{{"name", id.Name}, {"email", id.Email}} {
		if f.v == "" {
			return fmt.Errorf("empty identity %s", f.name)
		}
		if strings.ContainsAny(f.v, "<>\n\x00") {
			return fmt.Errorf("identity %s %q holds '<', '>', a newline or NUL", f.name, f.v)
		}
	}
	return nil
}

func (c Config) validate() error {
	switch {
	case c.ProjectID == "":
		return errors.New("config: empty project id")
	case !projectIDPattern.MatchString(c.ProjectID):
		return fmt.Errorf("config: invalid project id %q", c.ProjectID)
	case c.DataDir == "":
		return errors.New("config: empty data dir")
	case !filepath.IsAbs(c.DataDir) || filepath.Clean(c.DataDir) != c.DataDir:
		return fmt.Errorf("config: data dir %q is not absolute and clean", c.DataDir)
	case c.RemoteURL == "":
		return errors.New("config: empty remote url")
	case c.Clock == nil:
		return errors.New("config: nil clock")
	case c.IDs == nil:
		return errors.New("config: nil id source")
	case c.Logger == nil:
		return errors.New("config: nil logger")
	case c.IntegrationBranch == "":
		return errors.New("config: empty integration branch")
	case c.Git == nil:
		return errors.New("config: nil git runner")
	case c.Sandbox == nil:
		return errors.New("config: nil sandbox")
	case len(c.Verify) == 0:
		return errors.New("config: no verify command")
	case c.Limits.CPUMillis < 0 || c.Limits.MemoryBytes < 0 || c.Limits.DiskBytes < 0:
		return fmt.Errorf("config: negative cell limits %+v", c.Limits)
	}
	for i, v := range c.Verify {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("config: verify command %d is empty", i)
		}
	}
	for i, e := range c.Egress {
		if e == "" {
			return fmt.Errorf("config: egress entry %d is empty", i)
		}
	}
	if err := validateBranchPattern(c.BranchPattern); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if err := validateIdentity(c.Identity); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	for _, d := range []struct{ name, path string }{{"clone dir", c.CloneDir}, {"run dir", c.RunDir}} {
		if d.path == "" || !filepath.IsAbs(d.path) || filepath.Clean(d.path) != d.path {
			return fmt.Errorf("config: %s %q is not absolute and clean", d.name, d.path)
		}
	}
	return nil
}
