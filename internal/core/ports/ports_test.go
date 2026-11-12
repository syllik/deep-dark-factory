package ports_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

var (
	ctxType   = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType = reflect.TypeOf((*error)(nil)).Elem()
	modeType  = reflect.TypeOf(ports.WriteMode(""))
)

func TestForge_MethodSet(t *testing.T) {
	writes := map[string]bool{
		"ListCandidateTasks": false, "ReadIntakeActor": false, "FindChangeByTaskKey": false, "ReadChecks": false, "ReadAnswers": false,
		"CreateTask": true, "PublishChange": true, "UpdateChangeBody": true, "UpdateBranchFromBase": true, "RerunChecks": true,
		"PostReview": true, "Merge": true, "Revert": true, "CloseTask": true, "ReopenTask": true, "SetProjectionLabels": true,
	}
	typ := reflect.TypeOf((*ports.Forge)(nil)).Elem()
	if typ.NumMethod() != len(writes) {
		t.Fatalf("Forge has %d methods, want %d", typ.NumMethod(), len(writes))
	}
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		write, ok := writes[m.Name]
		if !ok {
			t.Errorf("unexpected Forge method %s", m.Name)
			continue
		}
		hasMode := m.Type.NumIn() > 1 && m.Type.In(1) == modeType
		if hasMode != write {
			t.Errorf("%s: WriteMode second = %v, want %v (spec §4.2 dry-run on every write)", m.Name, hasMode, write)
		}
	}
}

func TestPorts_ContextFirstErrorLast(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
	}{
		{"forge", reflect.TypeOf((*ports.Forge)(nil)).Elem()},
		{"agent", reflect.TypeOf((*ports.Agent)(nil)).Elem()},
		{"sandbox", reflect.TypeOf((*ports.Sandbox)(nil)).Elem()},
		{"notify", reflect.TypeOf((*ports.Notify)(nil)).Elem()},
		{"secrets", reflect.TypeOf((*ports.Secrets)(nil)).Elem()},
		{"lock", reflect.TypeOf((*ports.Lock)(nil)).Elem()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := range tt.typ.NumMethod() {
				m := tt.typ.Method(i)
				if m.Type.NumIn() == 0 || m.Type.In(0) != ctxType {
					t.Errorf("%s: first parameter is not context.Context", m.Name)
				}
				if n := m.Type.NumOut(); n == 0 || m.Type.Out(n-1) != errorType {
					t.Errorf("%s: last result is not error", m.Name)
				}
			}
		})
	}
}

func TestParseEnums(t *testing.T) {
	parseMode := func(s string) error { _, err := ports.ParseWriteMode(s); return err }
	parseClass := func(s string) error { _, err := ports.ParseAgentFailureClass(s); return err }
	tests := []struct {
		name  string
		parse func(string) error
		input string
		ok    bool
	}{
		{"write mode live", parseMode, "live", true},
		{"write mode dry-run", parseMode, "dry-run", true},
		{"write mode unknown", parseMode, "dryrun", false},
		{"write mode empty fails closed", parseMode, "", false},
		{"class credential", parseClass, "credential", true},
		{"class rate-limited", parseClass, "rate-limited", true},
		{"class retryable", parseClass, "retryable", true},
		{"class fatal", parseClass, "fatal", true},
		{"class unknown", parseClass, "transient", false},
		{"class empty", parseClass, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.parse(tt.input); (err == nil) != tt.ok {
				t.Errorf("parse %q: err = %v, want ok = %v", tt.input, err, tt.ok)
			}
		})
	}
}

func TestSecret_NeverPrintsValue(t *testing.T) {
	const value = "test-secret-value"
	s := ports.NewSecret([]byte(value))
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		t.Run(verb, func(t *testing.T) {
			if out := fmt.Sprintf(verb, s); strings.Contains(out, value) {
				t.Errorf("%s printed %q", verb, out)
			}
		})
	}
	t.Run("json", func(t *testing.T) {
		b, err := json.Marshal(struct{ Token ports.Secret }{s})
		if err == nil || strings.Contains(string(b)+err.Error(), value) {
			t.Errorf("json.Marshal = %q, %v; want a refusal without the value", b, err)
		}
	})
}

func TestAgentFailure_ErrorsAs(t *testing.T) {
	err := fmt.Errorf("run planner: %w", &ports.AgentFailure{Class: ports.AgentFailureRetryable})
	var f *ports.AgentFailure
	if !errors.As(err, &f) || f.Class != ports.AgentFailureRetryable {
		t.Fatalf("errors.As did not find *AgentFailure in %v", err)
	}
}
