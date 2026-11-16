package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

var testKey = strings.Repeat("ab", 32)

type validator interface{ Validate() error }

// decode returns a decoder of JSON into a record of type T.
func decode[T validator]() func([]byte) (validator, error) {
	return func(b []byte) (validator, error) {
		var v T
		err := json.Unmarshal(b, &v)
		return v, err
	}
}

// recordCase is one record type: a valid document without optional enums,
// its required enum fields and the decoder.
type recordCase struct {
	name     string
	valid    map[string]any
	required []string
	optional map[string]string
	decode   func([]byte) (validator, error)
}

func recordCases() []recordCase {
	return []recordCase{
		{"Task", map[string]any{"key": testKey, "kind": "task", "state": "inbox", "priority": "normal"},
			[]string{"kind", "state", "priority"},
			map[string]string{"parentKey": testKey, "risk": "low", "plannerRisk": "docs"}, decode[model.Task]()},
		{"Run", map[string]any{"key": testKey, "role": "executor", "purpose": "initial", "status": "running"},
			[]string{"role", "purpose", "status"}, map[string]string{"failureClass": "fatal"}, decode[model.Run]()},
		{"Change", map[string]any{"key": testKey, "kind": "change"},
			[]string{"kind"}, nil, decode[model.Change]()},
		{"Review", map[string]any{"key": testKey, "role": "r1", "verdict": "APPROVE"},
			[]string{"role", "verdict"}, map[string]string{"raiseRisk": "sensitive"}, decode[model.Review]()},
		{"Finding", map[string]any{"key": testKey, "role": "r2", "severity": "P1", "category": "security"},
			[]string{"role", "severity", "category"},
			map[string]string{"securityLevel": "High", "routed": "fixing"}, decode[model.Finding]()},
		{"Question", map[string]any{"key": testKey}, nil, nil, decode[model.Question]()},
		{"Decision", map[string]any{"key": testKey}, nil, nil, decode[model.Decision]()},
		{"Intent", map[string]any{"key": testKey, "operation": "merge", "status": "pending"},
			[]string{"operation", "status"}, nil, decode[model.Intent]()},
		{"Event", map[string]any{"type": "paused"}, nil, map[string]string{"key": testKey, "role": "planner"}, decode[model.Event]()},
	}
}

// with returns a copy of doc with field set to v, or removed when v is
// remove, encoded as JSON.
func with(t *testing.T, doc map[string]any, field string, v any) []byte {
	t.Helper()
	m := make(map[string]any, len(doc)+1)
	for k, x := range doc {
		m[k] = x
	}
	if v == remove {
		delete(m, field)
	} else {
		m[field] = v
	}
	return mustJSON(t, m)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const remove = "\x00remove"

func TestRecordValidate_RequiredAndOptionalFields(t *testing.T) {
	for _, rc := range recordCases() {
		t.Run(rc.name, func(t *testing.T) {
			rec, err := rc.decode(mustJSON(t, rc.valid))
			if err != nil || rec.Validate() != nil {
				t.Fatalf("valid record: decode %v, validate %v", err, rec.Validate())
			}
			for field, v := range rc.optional {
				rec, err := rc.decode(with(t, rc.valid, field, v))
				if err != nil || rec.Validate() != nil {
					t.Errorf("optional %s=%q: decode %v, validate %v", field, v, err, rec.Validate())
				}
			}
			for _, field := range rc.required {
				for _, c := range []struct {
					name string
					v    any
				}{{"missing", remove}, {"null", nil}} {
					rec, err := rc.decode(with(t, rc.valid, field, c.v))
					if err != nil {
						t.Fatalf("%s %s: decode: %v", c.name, field, err)
					}
					if rec.Validate() == nil {
						t.Errorf("%s %s: Validate passed", c.name, field)
					}
				}
				if _, err := rc.decode(with(t, rc.valid, field, "")); err == nil {
					t.Errorf(`"" %s: decode passed`, field)
				}
			}
			if _, ok := rc.valid["key"]; ok {
				for _, bad := range []any{remove, nil, "", "not-a-key"} {
					rec, err := rc.decode(with(t, rc.valid, "key", bad))
					if err == nil && rec.Validate() == nil {
						t.Errorf("key %q: Validate passed", bad)
					}
				}
			}
		})
	}
}

func TestTaskValidate_FlagsAndAgentDefaults(t *testing.T) {
	valid := model.Task{Key: model.TaskKey(testKey), Kind: model.TaskKindTask, State: model.TaskStateReady, Priority: model.PriorityHigh,
		Flags: []model.TaskFlag{model.TaskFlagHold, model.TaskFlagNeedsOwner}, AgentDefaults: []model.Decision{{Key: model.TaskKey(testKey)}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid task: %v", err)
	}
	dup := valid
	dup.Flags = []model.TaskFlag{model.TaskFlagHold, model.TaskFlagHold}
	if err := dup.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate flag: got %v", err)
	}
	bad := valid
	bad.AgentDefaults = []model.Decision{{}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "agentDefaults[0]") {
		t.Errorf("invalid agent default: got %v", err)
	}
}
