package policy

import (
	"errors"
	"fmt"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// ErrInvalidRiskClass reports an empty, unknown or disallowed risk class.
var ErrInvalidRiskClass = errors.New("invalid risk class")

// Classification is the risk class computed from a change and the profile,
// with the changed-line count it used.
type Classification struct {
	Class        model.RiskClass
	ChangedLines int  // added+deleted over the entries that are not lockfiles
	HasBinary    bool // a counted entry is binary
}

// Classify computes the risk class of a change from its changed paths and
// the profile (spec §6.2). It never returns revert.
func Classify(p Profile, paths []ChangedPath) (Classification, error) {
	if err := p.Validate(); err != nil {
		return Classification{}, err
	}
	if err := validateChangedPaths(paths); err != nil {
		return Classification{}, err
	}
	// policy.md risk classes: forbidden paths, risk.sensitive, ciEntrypoints
	// and hard-stop globs force sensitive.
	tight := slices.Concat(p.Sensitive, p.CIEntrypoints, hardStopGlobs(), forbiddenGlobs(), p.Forbidden)
	agent, locks := agentInstructionGlobs(), lockfileGlobs()
	var c Classification
	sensitive, docs := false, true
	for i, cp := range paths {
		lockfile := true
		for _, name := range cp.candidates() {
			s, err := matchAny(tight, name, true)
			if err != nil {
				return Classification{}, fmt.Errorf("paths[%d]: %w", i, err)
			}
			d, err := matchAny(p.Docs, name, false)
			if err != nil {
				return Classification{}, fmt.Errorf("paths[%d]: %w", i, err)
			}
			a, err := matchAny(agent, name, true)
			if err != nil {
				return Classification{}, fmt.Errorf("paths[%d]: %w", i, err)
			}
			l, err := matchAny(locks, name, false)
			if err != nil {
				return Classification{}, fmt.Errorf("paths[%d]: %w", i, err)
			}
			sensitive = sensitive || s
			docs = docs && d && !a // agent-instruction files are never docs
			lockfile = lockfile && l
		}
		// policy.md#changed-lines: lockfiles are skipped, binary rule included.
		if !lockfile {
			c.ChangedLines += cp.Added + cp.Deleted
			c.HasBinary = c.HasBinary || cp.Binary
		}
	}
	switch {
	case sensitive:
		c.Class = model.RiskClassSensitive
	case docs:
		c.Class = model.RiskClassDocs
	case !c.HasBinary && c.ChangedLines <= p.LowMaxLines:
		c.Class = model.RiskClassLow
	default:
		c.Class = model.RiskClassStandard
	}
	return c, nil
}

func riskRank(c model.RiskClass) (int, error) {
	switch c {
	case model.RiskClassDocs, model.RiskClassRevert:
		return 0, nil
	case model.RiskClassLow:
		return 1, nil
	case model.RiskClassStandard:
		return 2, nil
	case model.RiskClassSensitive:
		return 3, nil
	}
	return 0, fmt.Errorf("%w: %q", ErrInvalidRiskClass, c)
}

// Raise returns base raised by every raise of strictly higher rank; it never
// lowers (spec §6.2 rule 2). A raise of revert, empty or an unknown value is
// an error.
func Raise(base model.RiskClass, raises ...model.RiskClass) (model.RiskClass, error) {
	rank, err := riskRank(base)
	if err != nil {
		return "", fmt.Errorf("base: %w", err)
	}
	out := base
	for i, r := range raises {
		if r == model.RiskClassRevert {
			return "", fmt.Errorf("raises[%d]: %w: revert is not a raise", i, ErrInvalidRiskClass)
		}
		rr, err := riskRank(r)
		if err != nil {
			return "", fmt.Errorf("raises[%d]: %w", i, err)
		}
		if rr > rank {
			out, rank = r, rr
		}
	}
	return out, nil
}

// Reviewer is one reviewer a risk class requires.
type Reviewer struct {
	Role              model.Role
	Deep              bool // runs at the deep tier
	HardStopChecklist bool // also runs the hard-stop checklist
}

// Reviewers returns the reviewer set of a risk class (spec §6.2).
func Reviewers(c model.RiskClass) ([]Reviewer, error) {
	switch c {
	case model.RiskClassDocs, model.RiskClassLow, model.RiskClassRevert:
		// spec §6.2 rule 3: without R2, R1 runs the hard-stop checklist.
		return []Reviewer{{Role: model.RoleR1, HardStopChecklist: true}}, nil
	case model.RiskClassStandard:
		return []Reviewer{{Role: model.RoleR1}, {Role: model.RoleR2, HardStopChecklist: true}}, nil
	case model.RiskClassSensitive:
		return []Reviewer{{Role: model.RoleR1}, {Role: model.RoleR2, Deep: true, HardStopChecklist: true}}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrInvalidRiskClass, c)
}
