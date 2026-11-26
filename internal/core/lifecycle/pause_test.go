package lifecycle_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
)

func TestParsePauseReason(t *testing.T) {
	for _, s := range []string{"owner", "credential", "revert-red", "runtime", "security"} {
		got, err := lifecycle.ParsePauseReason(s)
		if err != nil || string(got) != s {
			t.Fatalf("ParsePauseReason(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "Owner", "revert_red", "paused", " runtime"} {
		if _, err := lifecycle.ParsePauseReason(s); !errors.Is(err, lifecycle.ErrInvalidInput) {
			t.Fatalf("ParsePauseReason(%q) error = %v, want ErrInvalidInput", s, err)
		}
	}
}

func TestSetPause(t *testing.T) {
	var set []lifecycle.PauseReason
	if lifecycle.Paused(set) {
		t.Fatal("empty set is paused")
	}
	for _, r := range []lifecycle.PauseReason{lifecycle.PauseSecurity, lifecycle.PauseOwner, lifecycle.PauseRuntime, lifecycle.PauseOwner} {
		before := slices.Clone(set)
		next, err := lifecycle.SetPause(set, r)
		if err != nil {
			t.Fatalf("SetPause(%q): %v", r, err)
		}
		if !reflect.DeepEqual(set, before) {
			t.Fatal("input set mutated")
		}
		set = next
	}
	want := []lifecycle.PauseReason{lifecycle.PauseOwner, lifecycle.PauseRuntime, lifecycle.PauseSecurity}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("set = %v, want %v", set, want)
	}
	again, err := lifecycle.SetPause(set, lifecycle.PauseRuntime)
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("idempotent set = %v, %v; want %v", again, err, want)
	}
	if !lifecycle.Paused(set) {
		t.Fatal("non-empty set is not paused")
	}
	if _, err := lifecycle.SetPause(set, "bogus"); !errors.Is(err, lifecycle.ErrInvalidInput) {
		t.Fatalf("SetPause(bogus) error = %v, want ErrInvalidInput", err)
	}
}

func allReasons() []lifecycle.PauseReason {
	return []lifecycle.PauseReason{
		lifecycle.PauseCredential, lifecycle.PauseOwner, lifecycle.PauseRevertRed,
		lifecycle.PauseRuntime, lifecycle.PauseSecurity,
	}
}

func TestLiftAutomatic(t *testing.T) {
	pass := lifecycle.LiftChecks{Health: true, IsolationRules: true, Probes: true}
	rest := []lifecycle.PauseReason{lifecycle.PauseCredential, lifecycle.PauseOwner, lifecycle.PauseRevertRed, lifecycle.PauseSecurity}
	tests := []struct {
		name       string
		set        []lifecycle.PauseReason
		checks     lifecycle.LiftChecks
		want       []lifecycle.PauseReason
		wantNotify bool
	}{
		{name: "all pass: runtime only removed", set: allReasons(), checks: pass, want: rest, wantNotify: true},
		{name: "runtime alone: set empties", set: []lifecycle.PauseReason{lifecycle.PauseRuntime}, checks: pass, wantNotify: true},
		{name: "health fails: runtime kept", set: allReasons(), checks: lifecycle.LiftChecks{IsolationRules: true, Probes: true}, want: allReasons()},
		{name: "rule check fails: runtime kept", set: allReasons(), checks: lifecycle.LiftChecks{Health: true, Probes: true}, want: allReasons()},
		{name: "probes fail: runtime kept", set: allReasons(), checks: lifecycle.LiftChecks{Health: true, IsolationRules: true}, want: allReasons()},
		{name: "no runtime: nothing cleared, no notify", set: rest, checks: pass, want: rest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.set)
			got, notify, err := lifecycle.LiftAutomatic(tt.set, tt.checks)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) || notify != tt.wantNotify {
				t.Fatalf("lift = %v, %v; want %v, %v", got, notify, tt.want, tt.wantNotify)
			}
			if !reflect.DeepEqual(tt.set, before) {
				t.Fatal("input set mutated")
			}
		})
	}
	if _, _, err := lifecycle.LiftAutomatic([]lifecycle.PauseReason{"bogus"}, pass); !errors.Is(err, lifecycle.ErrInvalidInput) {
		t.Fatalf("invalid set error = %v, want ErrInvalidInput", err)
	}
}

func TestResume(t *testing.T) {
	set := allReasons()
	if got := lifecycle.Resume(set); len(got) != 0 || lifecycle.Paused(got) {
		t.Fatalf("Resume = %v, want empty", got)
	}
	if !reflect.DeepEqual(set, allReasons()) {
		t.Fatal("input set mutated")
	}
}

func TestIsolationPause(t *testing.T) {
	tests := []struct {
		name   string
		check  lifecycle.IsolationCheck
		want   lifecycle.PauseReason
		paused bool
	}{
		{name: "intact, probes pass: no pause", check: lifecycle.IsolationCheck{Rules: lifecycle.RulesIntact, ProbesPassed: true}},
		{name: "restart failed: runtime", check: lifecycle.IsolationCheck{RestartFailed: true, Rules: lifecycle.RulesIntact, ProbesPassed: true}, want: lifecycle.PauseRuntime, paused: true},
		{name: "absent, no restart: security even if re-apply passes", check: lifecycle.IsolationCheck{Rules: lifecycle.RulesAbsent, ReapplyPassed: true}, want: lifecycle.PauseSecurity, paused: true},
		{name: "changed, no restart: security even if re-apply passes", check: lifecycle.IsolationCheck{Rules: lifecycle.RulesChanged, ProbesPassed: true, ReapplyPassed: true}, want: lifecycle.PauseSecurity, paused: true},
		{name: "absent after restart, re-apply fails: runtime", check: lifecycle.IsolationCheck{RestartObserved: true, Rules: lifecycle.RulesAbsent}, want: lifecycle.PauseRuntime, paused: true},
		{name: "absent after restart, re-apply passes: no pause", check: lifecycle.IsolationCheck{RestartObserved: true, Rules: lifecycle.RulesAbsent, ReapplyPassed: true}},
		{name: "intact, probe fails, re-apply fails: runtime", check: lifecycle.IsolationCheck{Rules: lifecycle.RulesIntact}, want: lifecycle.PauseRuntime, paused: true},
		{name: "intact, probe fails, re-apply passes: no pause", check: lifecycle.IsolationCheck{Rules: lifecycle.RulesIntact, ReapplyPassed: true}},
		{name: "unverifiable, re-apply fails: runtime", check: lifecycle.IsolationCheck{Rules: lifecycle.RulesUnverifiable, ProbesPassed: true}, want: lifecycle.PauseRuntime, paused: true},
		{name: "restart observed, intact, re-apply fails: runtime", check: lifecycle.IsolationCheck{RestartObserved: true, Rules: lifecycle.RulesIntact, ProbesPassed: true}, want: lifecycle.PauseRuntime, paused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, paused, err := lifecycle.IsolationPause(tt.check)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want || paused != tt.paused {
				t.Fatalf("IsolationPause = %q, %v; want %q, %v", got, paused, tt.want, tt.paused)
			}
		})
	}
	if _, _, err := lifecycle.IsolationPause(lifecycle.IsolationCheck{Rules: "bogus"}); !errors.Is(err, lifecycle.ErrInvalidInput) {
		t.Fatalf("invalid rule state error = %v, want ErrInvalidInput", err)
	}
}
