package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/core/config"
	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/store"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// reviewSigningKeySecret is the Secrets port name of the daemon's
// review-signing key (Task 12a agentDefault, Issue #144).
const reviewSigningKeySecret = "review-signing-key"

// defaultChangeTemplate is the change body template used when the project
// sets no change.template (Task 12a agentDefault). It holds exactly the
// placeholders publisher.RenderBody requires.
const defaultChangeTemplate = `{task}

## Summary

{summary}

## Verify

{verify}

## Agent notes

{agentNotes}
`

// Config is what Open needs to start the engine for one connected project:
// the raw config bytes, the project id and the daemon's own paths. Every path
// is absolute and clean.
type Config struct {
	// ProjectYAML is the integration branch's .ddf/project.yaml.
	ProjectYAML []byte
	// ProjectsYAML is the operator's projects.yaml.
	ProjectsYAML []byte
	// RoutingYAML is the operator's routing.yaml.
	RoutingYAML []byte
	// ProjectID selects the connected project of ProjectsYAML.
	ProjectID string
	// StorePath is the SQLite store file.
	StorePath string
	// DataDir is the data mount that holds forge-local storage.
	DataDir string
	// CloneDir is the daemon's clean clone of the project remote.
	CloneDir string
	// RunDir holds forge-local's per-attempt check copies.
	RunDir string
	// ScratchDir holds the daemon's run copies and Publisher scratch.
	ScratchDir string
	// GitPath is the absolute path of the git binary.
	GitPath string
	// GitStateDir is the git runner's private state directory.
	GitStateDir string
	// Mode is the write mode of every forge write and push.
	Mode ports.WriteMode
	// Identity is the author and committer of every commit DDF writes.
	Identity git.Identity
}

// Ports are the externally provided ports of the engine.
type Ports struct {
	Agent   ports.Agent
	Sandbox ports.Sandbox
	Secrets ports.Secrets
	Clock   ports.Clock
	IDs     ports.IDSource
	Logger  *slog.Logger
}

// Daemon is an opened engine and the resources Close releases.
type Daemon struct {
	// Engine runs the ticks.
	Engine *Engine
	store  *store.Store
}

// Open loads the config through config.Loader, selects the connected
// project, opens the git runner, the store, forge-local and the Publisher,
// and builds the Engine. Config errors keep config.ErrInvalidConfig wrapped.
// On any error everything already opened is closed.
func Open(ctx context.Context, cfg Config, p Ports) (_ *Daemon, err error) {
	if err := p.check(); err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}
	mode, err := ports.ParseWriteMode(string(cfg.Mode))
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}

	loader, err := config.NewLoader()
	if err != nil {
		return nil, fmt.Errorf("open daemon: config loader: %w", err)
	}
	project, err := loader.Project(cfg.ProjectYAML)
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}
	projects, err := loader.Projects(cfg.ProjectsYAML)
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}
	routing, err := loader.Routing(cfg.RoutingYAML)
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}
	cp, err := connectedProject(projects, cfg.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}
	profile, err := project.PolicyProfile()
	if err != nil {
		return nil, fmt.Errorf("open daemon: policy profile: %w", err)
	}
	limits := lifecycle.DefaultLimits()
	limits.MaxRunsPerTask = projects.Limits.MaxRunsPerTask

	signer, verifier, err := reviewKeys(ctx, p.Secrets)
	if err != nil {
		return nil, fmt.Errorf("open daemon: review-signing key: %w", err)
	}

	runner, err := git.NewRunner(ctx, cfg.GitPath, cfg.GitStateDir)
	if err != nil {
		return nil, fmt.Errorf("open daemon: git runner: %w", err)
	}

	st, err := store.Open(ctx, cfg.StorePath)
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}
	defer func() {
		if err != nil {
			if cerr := st.Close(); cerr != nil {
				err = errors.Join(err, cerr)
			}
		}
	}()

	forge, err := local.Open(ctx, local.Config{
		ProjectID:         cp.ID,
		DataDir:           cfg.DataDir,
		RemoteURL:         cp.Remote,
		Clock:             p.Clock,
		IDs:               p.IDs,
		Logger:            p.Logger,
		IntegrationBranch: project.IntegrationBranch,
		CloneDir:          cfg.CloneDir,
		RunDir:            cfg.RunDir,
		Git:               runner,
		Sandbox:           p.Sandbox,
		Verify:            project.Verify,
		Egress:            project.Registries,
		BranchPattern:     project.Change.Branch,
		Identity:          cfg.Identity,
	})
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}

	template, err := changeTemplate(ctx, runner, cfg.CloneDir, project)
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}

	validator, err := model.NewResultValidator()
	if err != nil {
		return nil, fmt.Errorf("open daemon: result validator: %w", err)
	}
	pub := &publisher.Publisher{
		Git:     runner,
		Clone:   cfg.CloneDir,
		Scratch: cfg.ScratchDir,
		Sandbox: p.Sandbox,
		Clock:   p.Clock,
		IDs:     p.IDs,
	}
	logger := p.Logger
	ingest := func(ctx context.Context, mode ports.WriteMode) error {
		rejected, err := forge.Ingest(ctx, mode)
		if err != nil {
			return fmt.Errorf("ingest inbox: %w", err)
		}
		if len(rejected) > 0 {
			logger.Warn("inbox files rejected", "count", len(rejected))
		}
		return nil
	}

	engine, err := New(Settings{
		Project:        project,
		Profile:        profile,
		Routing:        routing,
		AllowedActors:  cp.AllowedActors,
		Limits:         limits,
		Mode:           mode,
		Identity:       cfg.Identity,
		CloneDir:       cfg.CloneDir,
		ScratchDir:     cfg.ScratchDir,
		ChangeTemplate: template,
		OperatorMax:    cp.MaxAutonomy,
	}, Deps{
		Store:     st,
		Forge:     forge,
		Agent:     p.Agent,
		Secrets:   p.Secrets,
		Clock:     p.Clock,
		IDs:       p.IDs,
		Git:       runner,
		Publisher: pub,
		Pusher:    NewGitPusher(runner, logger),
		Checks:    forge,
		Ingest:    ingest,
		Validator: validator,
		Signer:    signer,
		Verifier:  verifier,
		Logger:    logger,
	})
	if err != nil {
		return nil, fmt.Errorf("open daemon: %w", err)
	}
	return &Daemon{Engine: engine, store: st}, nil
}

// Close closes the store. It is safe on a nil Daemon or a Daemon whose store
// is already closed by an earlier Close.
func (d *Daemon) Close() error {
	if d == nil || d.store == nil {
		return nil
	}
	st := d.store
	d.store = nil
	if err := st.Close(); err != nil {
		return fmt.Errorf("close daemon: %w", err)
	}
	return nil
}

func (p Ports) check() error {
	switch {
	case p.Agent == nil:
		return errors.New("nil agent port")
	case p.Sandbox == nil:
		return errors.New("nil sandbox port")
	case p.Secrets == nil:
		return errors.New("nil secrets port")
	case p.Clock == nil:
		return errors.New("nil clock port")
	case p.IDs == nil:
		return errors.New("nil id source")
	case p.Logger == nil:
		return errors.New("nil logger")
	}
	return nil
}

// reviewKeys reads the review-signing key from the Secrets port and returns
// its signer and a verifier that trusts only its public key. Errors never
// contain the key.
func reviewKeys(ctx context.Context, secrets ports.Secrets) (*signature.Signer, *signature.Verifier, error) {
	secret, err := secrets.Read(ctx, reviewSigningKeySecret)
	if err != nil {
		return nil, nil, err
	}
	signer, err := signature.NewSigner(secret)
	if err != nil {
		return nil, nil, err
	}
	verifier, err := signature.NewVerifier(signer.Public())
	if err != nil {
		return nil, nil, err
	}
	return signer, verifier, nil
}

// connectedProject returns the projects.yaml entry with id. A missing id is
// an error, and so is any forge but local: forge-github is M4.
func connectedProject(ps config.Projects, id string) (config.ConnectedProject, error) {
	for _, cp := range ps.Projects {
		if cp.ID != id {
			continue
		}
		switch cp.Forge {
		case config.ForgeLocal:
			return cp, nil
		case config.ForgeGitHub:
			return config.ConnectedProject{}, fmt.Errorf("project %q: forge %q is not supported yet", id, cp.Forge)
		}
		return config.ConnectedProject{}, fmt.Errorf("project %q: unknown forge %q", id, cp.Forge)
	}
	return config.ConnectedProject{}, fmt.Errorf("project %q is not in projects.yaml", id)
}

// changeTemplate reads change.template at the integration branch tip of the
// clean clone, or returns the built-in default when the key is absent.
func changeTemplate(ctx context.Context, r *git.Runner, clone string, p config.Project) (string, error) {
	if p.Change.Template == "" {
		return defaultChangeTemplate, nil
	}
	out, err := r.Run(ctx, clone, nil, "show", "refs/heads/"+p.IntegrationBranch+":"+p.Change.Template)
	if err != nil {
		return "", fmt.Errorf("read change template %s: %w", p.Change.Template, err)
	}
	return string(out), nil
}
