package local

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
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
	}
	return nil
}
