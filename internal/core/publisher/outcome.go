package publisher

import (
	"fmt"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

// Outcome is the Publisher's decision on one revision. It is sealed: only
// Ready, Failed, GateFailed and NeedsOwner implement it.
type Outcome interface{ outcome() }

// Ready is a revision that passed every check: Commit is the one revision
// commit (objects only, nothing pushed) and Push and Change are what
// Publish sends. HardStops are all approved; non-empty means the task
// continues at propose.
type Ready struct {
	Commit, Tree  string
	Class         model.RiskClass
	ChangedPaths  []policy.ChangedPath
	HardStops     []policy.HardStopEntry
	Push          PushRequest
	Change        ports.PublishRequest
	SquashMessage string
}

// Failed is a failed run: nothing is published. Paths are the offending
// paths, empty when the reason names none.
type Failed struct {
	Reason FailReason
	Paths  []string
}

// GateFailed is a local gate failure: verify command Index (Command, after
// SanitizeLine) ended with Outcome. The task goes back to the Executor as a
// correction batch.
type GateFailed struct {
	Command string
	Index   int
	Outcome ports.CellRunOutcome
}

// NeedsOwner is a hard stop: nothing is published and the owner must
// answer Question. HitStop is always true; the daemon sets the needs-owner
// flag and the sticky Task.HitStop. FailedRun marks a stop that also fails
// the run (secrets). Question is DDF-written and redacted.
type NeedsOwner struct {
	Stop           Stop
	Category       string
	Forbidden      []string
	Symlinks       []string
	HardStops      []policy.HardStopEntry
	SecretPatterns []string
	Paths          []string
	HitStop        bool
	FailedRun      bool
	Question       string
}

func (Ready) outcome()      {}
func (Failed) outcome()     {}
func (GateFailed) outcome() {}
func (NeedsOwner) outcome() {}

// FailReason is why a revision is a failed run.
type FailReason string

// FailReason values. All but FailScanTooLarge map 1:1 from patch.Reason.
const (
	FailEmpty             FailReason = "empty"
	FailTooLarge          FailReason = "too-large"
	FailDoesNotApply      FailReason = "does-not-apply"
	FailOutsideRepository FailReason = "outside-repository"
	FailGitDir            FailReason = "git-dir"
	FailGitlink           FailReason = "gitlink"
	FailNodeModules       FailReason = "node-modules"
	FailScanTooLarge      FailReason = "scan-too-large"
)

// ParseFailReason returns the FailReason spelled s; an unknown or empty
// value is an error.
func ParseFailReason(s string) (FailReason, error) {
	r := FailReason(s)
	if slices.Contains([]FailReason{FailEmpty, FailTooLarge, FailDoesNotApply, FailOutsideRepository,
		FailGitDir, FailGitlink, FailNodeModules, FailScanTooLarge}, r) {
		return r, nil
	}
	return "", fmt.Errorf("unknown fail reason %q", s)
}

// failReasonFromPatch maps a patch refusal reason to its FailReason; an
// unknown reason is an error.
func failReasonFromPatch(r patch.Reason) (FailReason, error) {
	switch r {
	case patch.ReasonEmpty:
		return FailEmpty, nil
	case patch.ReasonTooLarge:
		return FailTooLarge, nil
	case patch.ReasonDoesNotApply:
		return FailDoesNotApply, nil
	case patch.ReasonOutsideRepository:
		return FailOutsideRepository, nil
	case patch.ReasonGitDir:
		return FailGitDir, nil
	case patch.ReasonGitlink:
		return FailGitlink, nil
	case patch.ReasonNodeModules:
		return FailNodeModules, nil
	}
	return "", fmt.Errorf("unknown patch refusal reason %q", r)
}

// Stop is the kind of hard stop behind a NeedsOwner.
type Stop string

// Stop values.
const (
	StopSecrets    Stop = "secrets"
	StopPathPolicy Stop = "path-policy"
	StopHardStop   Stop = "hard-stop"
)

// ParseStop returns the Stop spelled s; an unknown or empty value is an
// error.
func ParseStop(s string) (Stop, error) {
	st := Stop(s)
	if slices.Contains([]Stop{StopSecrets, StopPathPolicy, StopHardStop}, st) {
		return st, nil
	}
	return "", fmt.Errorf("unknown stop %q", s)
}
