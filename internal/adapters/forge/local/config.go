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
	for _, d := range []struct{ name, path string }{{"clone dir", c.CloneDir}, {"run dir", c.RunDir}} {
		if d.path == "" || !filepath.IsAbs(d.path) || filepath.Clean(d.path) != d.path {
			return fmt.Errorf("config: %s %q is not absolute and clean", d.name, d.path)
		}
	}
	return nil
}
