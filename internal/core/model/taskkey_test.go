package model_test

import (
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

func TestNewTaskKey_Golden(t *testing.T) {
	// SHA-256 of "5:local12:example/repo5:issue1:1" (spec §4.5 task key).
	const want = model.TaskKey("4d0c7cf4aa4701d8a09cdf0d1cbae0bb0e58152fcde06ab77307e6c90b3669c3")
	got, err := model.NewTaskKey(model.TaskSource{Forge: "local", Repository: "example/repo", SourceKind: "issue", SourceID: "1"})
	if err != nil || got != want {
		t.Fatalf("NewTaskKey = %q, %v; want %q", got, err, want)
	}
	if _, err := model.ParseTaskKey(string(got)); err != nil {
		t.Errorf("ParseTaskKey(NewTaskKey) = %v", err)
	}
}

func TestNewTaskKey_FieldShiftDiffers(t *testing.T) {
	tests := []struct {
		name string
		a, b model.TaskSource
	}{
		{"forge/repository", model.TaskSource{Forge: "a", Repository: "bc", SourceKind: "k", SourceID: "1"}, model.TaskSource{Forge: "ab", Repository: "c", SourceKind: "k", SourceID: "1"}},
		{"repository/kind", model.TaskSource{Forge: "f", Repository: "a", SourceKind: "bc", SourceID: "1"}, model.TaskSource{Forge: "f", Repository: "ab", SourceKind: "c", SourceID: "1"}},
		{"kind/id", model.TaskSource{Forge: "f", Repository: "r", SourceKind: "a", SourceID: "bc"}, model.TaskSource{Forge: "f", Repository: "r", SourceKind: "ab", SourceID: "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ka, errA := model.NewTaskKey(tt.a)
			kb, errB := model.NewTaskKey(tt.b)
			if errA != nil || errB != nil {
				t.Fatalf("NewTaskKey errors: %v, %v", errA, errB)
			}
			if ka == kb {
				t.Errorf("shifted fields give the same key %q", ka)
			}
		})
	}
}

func TestNewTaskKey_RejectsEmptyField(t *testing.T) {
	full := model.TaskSource{Forge: "local", Repository: "example/repo", SourceKind: "issue", SourceID: "1"}
	tests := []struct {
		name  string
		clear func(*model.TaskSource)
	}{
		{"forge", func(s *model.TaskSource) { s.Forge = "" }},
		{"repository", func(s *model.TaskSource) { s.Repository = "" }},
		{"source kind", func(s *model.TaskSource) { s.SourceKind = "" }},
		{"source id", func(s *model.TaskSource) { s.SourceID = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := full
			tt.clear(&src)
			if k, err := model.NewTaskKey(src); err == nil {
				t.Errorf("NewTaskKey = %q, want error", k)
			}
		})
	}
}

func TestParseTaskKey_Rejects(t *testing.T) {
	valid := strings.Repeat("0123456789abcdef", 4)
	tests := []struct {
		name, in string
	}{
		{"upper case", strings.ToUpper(valid)},
		{"too short", valid[:63]},
		{"too long", valid + "0"},
		{"empty", ""},
		{"non-hex", valid[:63] + "g"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if k, err := model.ParseTaskKey(tt.in); err == nil {
				t.Errorf("ParseTaskKey(%q) = %q, want error", tt.in, k)
			}
		})
	}
	if _, err := model.ParseTaskKey(valid); err != nil {
		t.Errorf("ParseTaskKey(valid) = %v", err)
	}
}
