package model_test

import (
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// TestFailureClass_MatchesAgentFailureClass pins model.FailureClass to the
// spellings of ports.AgentFailureClass, which model cannot import.
func TestFailureClass_MatchesAgentFailureClass(t *testing.T) {
	modelValues := []model.FailureClass{model.FailureClassCredential, model.FailureClassRateLimited, model.FailureClassRetryable, model.FailureClassFatal}
	portValues := []ports.AgentFailureClass{ports.AgentFailureCredential, ports.AgentFailureRateLimited, ports.AgentFailureRetryable, ports.AgentFailureFatal}
	if len(modelValues) != len(portValues) {
		t.Fatalf("model has %d failure classes, ports %d", len(modelValues), len(portValues))
	}
	for _, v := range modelValues {
		if _, err := ports.ParseAgentFailureClass(string(v)); err != nil {
			t.Errorf("model value %q: %v", v, err)
		}
	}
	for _, v := range portValues {
		if _, err := model.ParseFailureClass(string(v)); err != nil {
			t.Errorf("ports value %q: %v", v, err)
		}
	}
}
