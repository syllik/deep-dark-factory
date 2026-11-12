package model_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/schemas"
)

func newValidator(t *testing.T) *model.ResultValidator {
	t.Helper()
	v, err := model.NewResultValidator()
	if err != nil {
		t.Fatalf("NewResultValidator: %v", err)
	}
	return v
}

func TestValidate_AcceptsExamples(t *testing.T) {
	v := newValidator(t)
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "schemas", "examples", "*.result.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob examples: %v, %d files", err, len(paths))
	}
	for _, p := range paths {
		name := filepath.Base(p)
		t.Run(name, func(t *testing.T) {
			kind, err := model.ParseResultKind(strings.SplitN(strings.TrimSuffix(name, ".result.json"), "-", 2)[0])
			if err != nil {
				t.Fatalf("kind of %s: %v", name, err)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			if err := v.Validate(kind, data); err != nil {
				t.Errorf("Validate(%s, %s) = %v", kind, name, err)
			}
		})
	}
}

func TestValidate_RejectsInvalidResults(t *testing.T) {
	const validPlanner = `{"schema":"ddf.planner.result/v1","verdict":"READY","spec":"s","acceptance":["a"],"riskProposal":"low"}`
	const validExecutor = `{"schema":"ddf.executor.result/v1","verdict":"COMPLETE","summary":"s","verify":{"commands":["go test ./..."],"passed":true}}`
	v := newValidator(t)
	for _, tt := range []struct {
		name string
		kind model.ResultKind
		data string
	}{
		{"planner READY without acceptance", model.ResultKindPlanner, `{"schema":"ddf.planner.result/v1","verdict":"READY","spec":"s","riskProposal":"low"}`},
		{"executor COMPLETE with verify failed", model.ResultKindExecutor, strings.Replace(validExecutor, `"passed":true`, `"passed":false`, 1)},
		{"reviewer APPROVE with P1 finding (spec §6.3)", model.ResultKindReviewer, `{"schema":"ddf.reviewer.result/v1","role":"r1","verdict":"APPROVE","findings":[{"id":"r1-1","severity":"P1","category":"correctness","evidence":"e"}]}`},
		{"trailing data", model.ResultKindExecutor, validExecutor + ` {}`},
		{"planner result as executor", model.ResultKindExecutor, validPlanner},
		{"not JSON", model.ResultKindPlanner, `{`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := v.Validate(tt.kind, []byte(tt.data)); !errors.Is(err, model.ErrInvalidResult) {
				t.Errorf("Validate = %v, want ErrInvalidResult", err)
			}
		})
	}
	// Controls: the bases of the cases above are valid.
	if err := v.Validate(model.ResultKindPlanner, []byte(validPlanner)); err != nil {
		t.Errorf("valid planner: %v", err)
	}
	if err := v.Validate(model.ResultKindExecutor, []byte(validExecutor)); err != nil {
		t.Errorf("valid executor: %v", err)
	}
}

func TestValidate_UnknownKind(t *testing.T) {
	v := newValidator(t)
	if err := v.Validate(model.ResultKind("other"), []byte(`{}`)); err == nil {
		t.Error("Validate with unknown kind: want error")
	}
}

func TestResultSchemaID_MatchesEmbeddedSchemas(t *testing.T) {
	kinds := []model.ResultKind{model.ResultKindPlanner, model.ResultKindExecutor, model.ResultKindReviewer}
	byID := map[model.SchemaID]model.ResultKind{}
	for _, k := range kinds {
		id, err := model.ResultSchemaID(k)
		if err != nil {
			t.Fatalf("ResultSchemaID(%s): %v", k, err)
		}
		if prev, dup := byID[id]; dup {
			t.Fatalf("schema id %s maps to %s and %s", id, prev, k)
		}
		byID[id] = k
	}
	names, err := fs.Glob(schemas.FS(), "*.result.json")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	seen := map[model.ResultKind]string{}
	for _, name := range names {
		raw, err := fs.ReadFile(schemas.FS(), name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var doc struct {
			ID model.SchemaID `json:"$id"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		k, ok := byID[doc.ID]
		if !ok {
			t.Errorf("%s: $id %q is no role-result schema id", name, doc.ID)
			continue
		}
		if prev, dup := seen[k]; dup {
			t.Errorf("kind %s has two files: %s and %s", k, prev, name)
		}
		seen[k] = name
	}
	for _, k := range kinds {
		if _, ok := seen[k]; !ok {
			t.Errorf("kind %s has no embedded schema file", k)
		}
	}
}
