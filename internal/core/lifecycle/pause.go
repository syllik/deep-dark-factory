package lifecycle

import (
	"fmt"
	"slices"
)

// PauseReason is one reason of the global pause (lifecycle.md#global-pause).
type PauseReason string

// PauseReason values.
const (
	PauseOwner      PauseReason = "owner"      // ddf pause or /pause
	PauseCredential PauseReason = "credential" // agent authentication failure
	PauseRevertRed  PauseReason = "revert-red" // second DDF-attributed red within 24 h
	PauseRuntime    PauseReason = "runtime"    // container runtime unavailable
	PauseSecurity   PauseReason = "security"   // isolation rules tampered with
)

// ParsePauseReason returns the PauseReason spelled s; an unknown or empty
// value is ErrInvalidInput.
func ParsePauseReason(s string) (PauseReason, error) {
	r := PauseReason(s)
	switch r {
	case PauseOwner, PauseCredential, PauseRevertRed, PauseRuntime, PauseSecurity:
		return r, nil
	}
	return "", fmt.Errorf("%w: pause reason %q", ErrInvalidInput, s)
}

// normalizePause returns a sorted, duplicate-free copy of set after
// checking every reason; nil when set is empty.
func normalizePause(set []PauseReason) ([]PauseReason, error) {
	for _, r := range set {
		if _, err := ParsePauseReason(string(r)); err != nil {
			return nil, err
		}
	}
	out := slices.Clone(set)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// Paused reports whether the daemon is paused: the set is non-empty.
func Paused(set []PauseReason) bool { return len(set) > 0 }

// SetPause returns set with r added; it is idempotent and the result is
// sorted and duplicate-free.
func SetPause(set []PauseReason, r PauseReason) ([]PauseReason, error) {
	if _, err := ParsePauseReason(string(r)); err != nil {
		return nil, err
	}
	return normalizePause(append(slices.Clone(set), r))
}

// LiftChecks are the checks that gate the automatic lift of runtime.
type LiftChecks struct {
	Health         bool // the runtime health check passed
	IsolationRules bool // the isolation-rule check passed
	Probes         bool // the isolation probes passed
}

// LiftAutomatic returns set without runtime when every check passes; notify
// is true when runtime was removed. No other reason is ever cleared here.
func LiftAutomatic(set []PauseReason, c LiftChecks) (out []PauseReason, notify bool, err error) {
	out, err = normalizePause(set)
	if err != nil {
		return nil, false, err
	}
	if !c.Health || !c.IsolationRules || !c.Probes || !slices.Contains(out, PauseRuntime) {
		return out, false, nil
	}
	out = slices.DeleteFunc(out, func(r PauseReason) bool { return r == PauseRuntime })
	if len(out) == 0 {
		out = nil
	}
	return out, true, nil
}

// Resume clears the whole set (ddf resume or /resume), runtime included.
func Resume([]PauseReason) []PauseReason { return nil }

// RuleState is the isolation-rule check result before any re-apply,
// compared with the fingerprint recorded when the rules were applied.
type RuleState string

// RuleState values.
const (
	RulesIntact       RuleState = "intact"       // present and matching the fingerprint
	RulesAbsent       RuleState = "absent"       // not present
	RulesChanged      RuleState = "changed"      // present, fingerprint mismatch
	RulesUnverifiable RuleState = "unverifiable" // the check could not verify them
)

// IsolationCheck is what the daemon observed before a container start
// (spec §8.4, pause reason for isolation rules).
type IsolationCheck struct {
	// RestartFailed reports that the daemon's own runtime restart attempts
	// failed.
	RestartFailed bool
	// RestartObserved reports a changed runtime instance identifier since
	// the rules were last applied.
	RestartObserved bool
	Rules           RuleState
	ProbesPassed    bool
	// ReapplyPassed reports that after the one re-apply the rules are present
	// and verifiable and the probes pass; read only when a re-apply was due.
	ReapplyPassed bool
}

// IsolationPause maps an isolation check to the pause reason it sets, if
// any (spec §8.4 precedence): a failed runtime restart is runtime; rules
// absent or changed with no observed restart are security, even when a
// re-apply would succeed; after an observed restart, a probe failure or an
// unverifiable check, a failed re-apply is runtime.
func IsolationPause(c IsolationCheck) (PauseReason, bool, error) {
	if c.RestartFailed {
		return PauseRuntime, true, nil
	}
	reapply := c.RestartObserved
	switch c.Rules {
	case RulesIntact:
		reapply = reapply || !c.ProbesPassed
	case RulesAbsent, RulesChanged:
		if !c.RestartObserved {
			return PauseSecurity, true, nil
		}
	case RulesUnverifiable:
		reapply = true
	default:
		return "", false, fmt.Errorf("%w: rule state %q", ErrInvalidInput, c.Rules)
	}
	if reapply && !c.ReapplyPassed {
		return PauseRuntime, true, nil
	}
	return "", false, nil
}
