package policy_test

import (
	"errors"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

func TestEffectiveAutonomy(t *testing.T) {
	p, m := policy.AutonomyPropose, policy.AutonomyMergeIntegration
	tests := []struct {
		name          string
		operator, req policy.Autonomy
		want          policy.Autonomy
		wantErr       bool
	}{
		{"min: propose/propose", p, p, p, false},
		{"min: propose operator caps merge request", p, m, p, false},
		{"min: merge operator keeps propose request", m, p, p, false},
		{"min: merge/merge", m, m, m, false},
		{"empty operator", "", p, "", true},
		{"empty request", m, "", "", true},
		{"unknown operator", "auto", p, "", true},
		{"unknown request", m, "Propose", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.EffectiveAutonomy(tt.operator, tt.req)
			if tt.wantErr != errors.Is(err, policy.ErrInvalidAutonomy) || (!tt.wantErr && err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseAutonomy(t *testing.T) {
	for _, s := range []string{"propose", "merge-integration"} {
		if got, err := policy.ParseAutonomy(s); err != nil || string(got) != s {
			t.Errorf("ParseAutonomy(%q) = %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "merge", "PROPOSE", "production"} {
		if _, err := policy.ParseAutonomy(s); !errors.Is(err, policy.ErrInvalidAutonomy) {
			t.Errorf("ParseAutonomy(%q) err = %v", s, err)
		}
	}
	var a policy.Autonomy
	if err := a.UnmarshalText([]byte("merge-integration")); err != nil || a != policy.AutonomyMergeIntegration {
		t.Errorf("UnmarshalText = %q, %v", a, err)
	}
	if err := a.UnmarshalText([]byte("x")); !errors.Is(err, policy.ErrInvalidAutonomy) || a != policy.AutonomyMergeIntegration {
		t.Errorf("UnmarshalText(x) = %q, %v", a, err)
	}
}
