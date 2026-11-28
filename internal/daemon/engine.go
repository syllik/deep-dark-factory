package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"

	"github.com/syllik/deep-dark-factory/internal/core/config"
	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/store"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// Store is the part of the store the engine uses; *store.Store implements
// it.
type Store interface {
	IntentStore
	CreateTask(ctx context.Context, t model.Task, ev model.Event) error
	UpdateTask(ctx context.Context, t model.Task, ev model.Event) error
	GetTask(ctx context.Context, key model.TaskKey) (model.Task, error)
	CreateRun(ctx context.Context, r model.Run) error
	FinishRun(ctx context.Context, r model.Run) error
	GetRun(ctx context.Context, id string) (model.Run, error)
	ListRuns(ctx context.Context, key model.TaskKey) ([]model.Run, error)
	CreateChange(ctx context.Context, c model.Change) error
	UpdateChange(ctx context.Context, c model.Change) error
	ListChanges(ctx context.Context, key model.TaskKey) ([]model.Change, error)
	NextReviewRound(ctx context.Context, key model.TaskKey, changeID string, role model.Role) (int, error)
	CreateReview(ctx context.Context, r model.Review, findings []model.Finding) error
	ListReviews(ctx context.Context, key model.TaskKey, changeID string) ([]model.Review, error)
	ListBaseMerges(ctx context.Context, key model.TaskKey, changeID string) ([]store.BaseMerge, error)
	PendingIntents(ctx context.Context) ([]model.Intent, error)
	AppendEvent(ctx context.Context, ev model.Event) error
}

var _ Store = (*store.Store)(nil)

// CheckRunner runs the pending checks of a forge that checks locally;
// *local.Forge implements it.
type CheckRunner interface {
	RunChecks(ctx context.Context) error
}

// Settings are the engine's project settings, read once at start.
type Settings struct {
	Project        config.Project
	Profile        policy.Profile
	Routing        config.Routing
	AllowedActors  []string
	Limits         lifecycle.Limits
	Mode           ports.WriteMode
	Identity       git.Identity
	CloneDir       string
	ScratchDir     string
	ChangeTemplate string
	// OperatorMax is the operator's maxAutonomy of the connected project;
	// the merge gate never goes beyond it.
	OperatorMax policy.Autonomy
}

// Deps are the engine's ports and collaborators. Checks, Ingest and Lock
// may be nil; Sandbox is required. Signer signs the stored reviews;
// Verifier is the only key set the merge gate trusts. Sandbox is what
// recovery lists, kills and tears down; Recover refuses a nil Lock. With a
// Lock set, Tick runs only after Recover succeeded on the engine (plan
// OD-9).
type Deps struct {
	Store     Store
	Forge     ports.Forge
	Sandbox   ports.Sandbox
	Lock      ports.Lock
	Agent     ports.Agent
	Secrets   ports.Secrets
	Clock     ports.Clock
	IDs       ports.IDSource
	Git       *git.Runner
	Publisher *publisher.Publisher
	Pusher    publisher.Pusher
	Checks    CheckRunner
	Ingest    func(ctx context.Context, mode ports.WriteMode) error
	Validator *model.ResultValidator
	Signer    *signature.Signer
	Verifier  *signature.Verifier
	Logger    *slog.Logger
}

// Engine moves the tasks of one project along the conveyor, one Tick at a
// time (spec §4.4, §5).
type Engine struct {
	settings Settings
	deps     Deps
	intents  *Intents
	// recovered is set once Recover succeeded on this engine.
	recovered atomic.Bool
}

// New validates s and d and returns an Engine. Every forge write and the
// push of the engine go through intents built on d.Store.
func New(s Settings, d Deps) (*Engine, error) {
	mode, err := ports.ParseWriteMode(string(s.Mode))
	if err != nil {
		return nil, fmt.Errorf("new engine: %w", err)
	}
	s.Mode = mode
	if err := d.validate(); err != nil {
		return nil, fmt.Errorf("new engine: %w", err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("new engine: %w", err)
	}
	in, err := NewIntents(d.Store, d.Clock, d.IDs, s.Mode)
	if err != nil {
		return nil, fmt.Errorf("new engine: %w", err)
	}
	return &Engine{settings: s, deps: d, intents: in}, nil
}

func (d Deps) validate() error {
	nils := []struct {
		name string
		nil  bool
	}{
		{"store", d.Store == nil},
		{"forge", d.Forge == nil},
		{"sandbox", d.Sandbox == nil},
		{"agent", d.Agent == nil},
		{"secrets", d.Secrets == nil},
		{"clock", d.Clock == nil},
		{"id source", d.IDs == nil},
		{"git runner", d.Git == nil},
		{"publisher", d.Publisher == nil},
		{"pusher", d.Pusher == nil},
		{"result validator", d.Validator == nil},
		{"review signer", d.Signer == nil},
		{"review verifier", d.Verifier == nil},
		{"logger", d.Logger == nil},
	}
	var errs []error
	for _, n := range nils {
		if n.nil {
			errs = append(errs, fmt.Errorf("nil %s", n.name))
		}
	}
	return errors.Join(errs...)
}

func (s Settings) validate() error {
	for _, p := range []struct{ name, path string }{{"clone dir", s.CloneDir}, {"scratch dir", s.ScratchDir}} {
		if !filepath.IsAbs(p.path) || filepath.Clean(p.path) != p.path {
			return fmt.Errorf("%s %q is not an absolute, clean path", p.name, p.path)
		}
	}
	if s.Project.IntegrationBranch == "" {
		return errors.New("empty integration branch")
	}
	if s.Limits.PlannerTimeout <= 0 || s.Limits.ExecutorTimeout <= 0 || s.Limits.ReviewerTimeout <= 0 {
		return fmt.Errorf("planner timeout %s, executor timeout %s and reviewer timeout %s must be positive",
			s.Limits.PlannerTimeout, s.Limits.ExecutorTimeout, s.Limits.ReviewerTimeout)
	}
	if _, err := policy.ParseAutonomy(string(s.OperatorMax)); err != nil {
		return fmt.Errorf("operator max autonomy: %w", err)
	}
	if err := s.Profile.Validate(); err != nil {
		return err
	}
	return nil
}
