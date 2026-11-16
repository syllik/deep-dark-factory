package fake_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

var bigHost = ports.Capacity{CPUMillis: 8000, MemoryBytes: 8 << 30, DiskBytes: 100 << 30}

func taskKey(t *testing.T, id string) model.TaskKey {
	t.Helper()
	k, err := model.NewTaskKey(model.TaskSource{Forge: "github", Repository: "owner/repo", SourceKind: "issue", SourceID: id})
	if err != nil {
		t.Fatalf("new task key: %v", err)
	}
	return k
}

func newCell(t *testing.T, s *fake.Sandbox, key model.TaskKey, runID string, l ports.CellLimits) string {
	t.Helper()
	id, err := s.CreateCell(context.Background(), ports.CellSpec{Key: key, RunID: runID, Limits: l})
	if err != nil {
		t.Fatalf("create cell: %v", err)
	}
	return id
}

func readyCell(t *testing.T, s *fake.Sandbox, key model.TaskKey, runID string) string {
	t.Helper()
	id := newCell(t, s, key, runID, ports.CellLimits{})
	if err := s.PutContent(context.Background(), id, ports.CellContent{Brief: []byte("brief")}); err != nil {
		t.Fatalf("put content: %v", err)
	}
	return id
}

var argv = ports.CellRun{Argv: []string{"agent", "run"}}

func TestSandbox_RunReturnsScriptedOutcomes(t *testing.T) {
	boom := errors.New("scripted failure")
	tests := []struct {
		name  string
		steps []fake.RunStep
		want  []ports.CellRunOutcome
		errs  []error
	}{
		{"default success when nothing scripted", nil,
			[]ports.CellRunOutcome{{ExitCode: 0}, {ExitCode: 0}}, []error{nil, nil}},
		{"scripted outcomes in order then default",
			[]fake.RunStep{{Outcome: ports.CellRunOutcome{ExitCode: 3}}, {Outcome: ports.CellRunOutcome{TimedOut: true}}},
			[]ports.CellRunOutcome{{ExitCode: 3}, {TimedOut: true}, {ExitCode: 0}}, []error{nil, nil, nil}},
		{"scripted error is returned and consumed",
			[]fake.RunStep{{Err: boom}, {Outcome: ports.CellRunOutcome{ExitCode: 1}}},
			[]ports.CellRunOutcome{{}, {ExitCode: 1}}, []error{boom, nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := fake.NewHost(bigHost)
			s := h.Sandbox("a")
			key, other := taskKey(t, "1"), taskKey(t, "2")
			h.ScriptRun(other, fake.RunStep{Outcome: ports.CellRunOutcome{ExitCode: 99}})
			h.ScriptRun(key, tt.steps...)
			id := readyCell(t, s, key, "run-1")
			for i, want := range tt.want {
				got, err := s.Run(context.Background(), id, argv)
				if !errors.Is(err, tt.errs[i]) || (tt.errs[i] == nil && err != nil) {
					t.Fatalf("run %d: err %v, want %v", i, err, tt.errs[i])
				}
				if got != want {
					t.Errorf("run %d: outcome %+v, want %+v", i, got, want)
				}
			}
			runs := h.Runs()
			if len(runs) != len(tt.want) || runs[0].CellID != id || !reflect.DeepEqual(runs[0].Argv, argv.Argv) {
				t.Errorf("runs = %+v", runs)
			}
			otherID := readyCell(t, s, other, "run-2")
			if got, _ := s.Run(context.Background(), otherID, argv); got.ExitCode != 99 {
				t.Errorf("other key outcome %+v, want exit 99 (queues are per key)", got)
			}
		})
	}
}

func TestSandbox_RunAndPutContentRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, s *fake.Sandbox) string
		run   ports.CellRun
		put   bool
		want  error
	}{
		{"run on unknown cell", func(*testing.T, *fake.Sandbox) string { return "cell-404" }, argv, false, fake.ErrUnknownCell},
		{"put content on unknown cell", func(*testing.T, *fake.Sandbox) string { return "cell-404" }, argv, true, fake.ErrUnknownCell},
		{"run on killed cell", func(t *testing.T, s *fake.Sandbox) string {
			id := readyCell(t, s, taskKey(t, "1"), "run-1")
			if err := s.Kill(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			return id
		}, argv, false, fake.ErrCellKilled},
		{"put content on killed cell", func(t *testing.T, s *fake.Sandbox) string {
			id := newCell(t, s, taskKey(t, "1"), "run-1", ports.CellLimits{})
			if err := s.Kill(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			return id
		}, argv, true, fake.ErrCellKilled},
		{"run before content", func(t *testing.T, s *fake.Sandbox) string {
			return newCell(t, s, taskKey(t, "1"), "run-1", ports.CellLimits{})
		}, argv, false, fake.ErrNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := fake.NewHost(bigHost)
			s := h.Sandbox("a")
			id := tt.setup(t, s)
			var err error
			if tt.put {
				err = s.PutContent(context.Background(), id, ports.CellContent{})
			} else {
				_, err = s.Run(context.Background(), id, tt.run)
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSandbox_RunRefusesEmptyArgv(t *testing.T) {
	s := fake.NewHost(bigHost).Sandbox("a")
	id := readyCell(t, s, taskKey(t, "1"), "run-1")
	if _, err := s.Run(context.Background(), id, ports.CellRun{}); err == nil {
		t.Fatal("empty argv accepted")
	}
}

func TestSandbox_GetFilesReturnsProducedFiles(t *testing.T) {
	h := fake.NewHost(bigHost)
	s := h.Sandbox("a")
	key := taskKey(t, "1")
	h.ScriptRun(key, fake.RunStep{Files: map[string][]byte{"result.json": []byte(`{"ok":true}`), "change.patch": []byte("diff")}})
	id := readyCell(t, s, key, "run-1")
	if _, err := s.Run(context.Background(), id, argv); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		kill  bool
		names []string
		want  map[string][]byte
	}{
		{"requested present files", false, []string{"result.json", "change.patch"},
			map[string][]byte{"result.json": []byte(`{"ok":true}`), "change.patch": []byte("diff")}},
		{"missing name omitted", false, []string{"result.json", "missing.txt"},
			map[string][]byte{"result.json": []byte(`{"ok":true}`)}},
		{"no names", false, nil, map[string][]byte{}},
		{"killed cell keeps files", true, []string{"change.patch"}, map[string][]byte{"change.patch": []byte("diff")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.kill {
				if err := s.Kill(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.GetFiles(context.Background(), id, tt.names)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("files = %q, want %q", got, tt.want)
			}
			for _, v := range got {
				if len(v) > 0 {
					v[0] = 'X' // mutating the copy must not change the cell
				}
			}
			again, _ := s.GetFiles(context.Background(), id, tt.names)
			if !reflect.DeepEqual(again, tt.want) {
				t.Errorf("files changed through returned copy: %q", again)
			}
		})
	}
	if _, err := s.GetFiles(context.Background(), "cell-404", nil); !errors.Is(err, fake.ErrUnknownCell) {
		t.Errorf("unknown cell err = %v", err)
	}
}

func TestSandbox_NeverExecutesWorkspace(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	ws := fstest.MapFS{
		"verify.sh":   {Data: []byte(script), Mode: 0o755},
		"src/main.go": {Data: []byte("package main\n")},
	}
	bundle := fstest.MapFS{"prompts/executor.md": {Data: []byte("# Executor\n")}}
	h := fake.NewHost(bigHost)
	s := h.Sandbox("a")
	key := taskKey(t, "1")
	h.ScriptRun(key, fake.RunStep{Outcome: ports.CellRunOutcome{ExitCode: 7}})
	id := newCell(t, s, key, "run-1", ports.CellLimits{})
	if err := s.PutContent(context.Background(), id, ports.CellContent{Brief: []byte("brief"), Workspace: ws, RoleBundle: bundle}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Run(context.Background(), id, ports.CellRun{Argv: []string{"sh", "verify.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != (ports.CellRunOutcome{ExitCode: 7}) {
		t.Errorf("outcome %+v, want the scripted exit 7", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temp dir has %d entries after Run; the workspace was executed", len(entries))
	}
	rec, ok := h.Content(id)
	want := fake.CellContentRecord{Brief: []byte("brief"), Workspace: []string{"src/main.go", "verify.sh"}, RoleBundle: []string{"prompts/executor.md"}}
	if !ok || !reflect.DeepEqual(rec, want) {
		t.Errorf("content = %+v, %v; want %+v", rec, ok, want)
	}
}

func TestSandbox_Capacity(t *testing.T) {
	total := ports.Capacity{CPUMillis: 4000, MemoryBytes: 4000, DiskBytes: 4000}
	half := ports.CellLimits{CPUMillis: 2000, MemoryBytes: 2000, DiskBytes: 2000}
	tests := []struct {
		name    string
		release func(t *testing.T, s *fake.Sandbox, id string, key model.TaskKey)
	}{
		{"freed by kill", func(t *testing.T, s *fake.Sandbox, id string, _ model.TaskKey) {
			if err := s.Kill(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}},
		{"freed by destroy", func(t *testing.T, s *fake.Sandbox, _ string, key model.TaskKey) {
			if err := s.DestroyByTaskKey(context.Background(), key); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fake.NewHost(total).Sandbox("a")
			ctx := context.Background()
			k1, k2 := taskKey(t, "1"), taskKey(t, "2")
			first := newCell(t, s, k1, "run-1", half)
			if c, _ := s.HostCapacity(ctx); c != (ports.Capacity{CPUMillis: 2000, MemoryBytes: 2000, DiskBytes: 2000}) {
				t.Errorf("capacity after one cell = %+v", c)
			}
			newCell(t, s, k2, "run-2", ports.CellLimits{CPUMillis: 1000, MemoryBytes: 2000})
			if c, _ := s.HostCapacity(ctx); c != (ports.Capacity{CPUMillis: 1000, MemoryBytes: 0, DiskBytes: 2000}) {
				t.Errorf("capacity after two cells = %+v", c)
			}
			_, err := s.CreateCell(ctx, ports.CellSpec{Key: k1, RunID: "run-3", Limits: ports.CellLimits{MemoryBytes: 1}})
			if !errors.Is(err, fake.ErrNoCapacity) {
				t.Fatalf("over capacity err = %v, want ErrNoCapacity", err)
			}
			tt.release(t, s, first, k1)
			if c, _ := s.HostCapacity(ctx); c != (ports.Capacity{CPUMillis: 3000, MemoryBytes: 2000, DiskBytes: 4000}) {
				t.Errorf("capacity after release = %+v", c)
			}
			if _, err := s.CreateCell(ctx, ports.CellSpec{Key: k1, RunID: "run-3", Limits: ports.CellLimits{MemoryBytes: 2000}}); err != nil {
				t.Errorf("create after release: %v", err)
			}
		})
	}
}

func TestSandbox_StatsAfterSequence(t *testing.T) {
	h := fake.NewHost(bigHost)
	s := h.Sandbox("a")
	ctx := context.Background()
	k1, k2, k3 := taskKey(t, "1"), taskKey(t, "2"), taskKey(t, "3")
	c1 := newCell(t, s, k1, "run-1", ports.CellLimits{})
	newCell(t, s, k1, "run-2", ports.CellLimits{})
	c3 := newCell(t, s, k2, "run-3", ports.CellLimits{})
	newCell(t, s, k3, "run-4", ports.CellLimits{})
	steps := []struct {
		name string
		do   func() error
		want fake.Stats
	}{
		{"after create", func() error { return nil }, fake.Stats{Created: 4, Live: 4}},
		{"kill one", func() error { return s.Kill(ctx, c1) }, fake.Stats{Created: 4, Killed: 1, Live: 3}},
		{"kill twice counts once", func() error { return s.Kill(ctx, c1) }, fake.Stats{Created: 4, Killed: 1, Live: 3}},
		{"destroy key with killed and live cell", func() error { return s.DestroyByTaskKey(ctx, k1) }, fake.Stats{Created: 4, Killed: 1, Destroyed: 2, Live: 2}},
		{"kill then destroy", func() error {
			if err := s.Kill(ctx, c3); err != nil {
				return err
			}
			return s.DestroyByTaskKey(ctx, k2)
		}, fake.Stats{Created: 4, Killed: 2, Destroyed: 3, Live: 1}},
		{"destroy key without resources", func() error { return s.DestroyByTaskKey(ctx, k2) }, fake.Stats{Created: 4, Killed: 2, Destroyed: 3, Live: 1}},
	}
	for _, st := range steps {
		if err := st.do(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if got := h.Stats(); got != st.want {
			t.Errorf("%s: stats %+v, want %+v", st.name, got, st.want)
		}
	}
	if err := s.Kill(ctx, "cell-404"); !errors.Is(err, fake.ErrUnknownCell) {
		t.Errorf("kill unknown err = %v", err)
	}
}

func TestSandbox_ListResourcesFilters(t *testing.T) {
	s := fake.NewHost(bigHost).Sandbox("a")
	k1, k2 := taskKey(t, "1"), taskKey(t, "2")
	newCell(t, s, k1, "run-1", ports.CellLimits{})
	newCell(t, s, k1, "run-2", ports.CellLimits{})
	newCell(t, s, k2, "run-1", ports.CellLimits{})
	tests := []struct {
		name   string
		filter ports.ResourceFilter
		want   []string
	}{
		{"none", ports.ResourceFilter{}, []string{"cell-1", "cell-1-volume", "cell-2", "cell-2-volume", "cell-3", "cell-3-volume"}},
		{"key only", ports.ResourceFilter{Key: k1}, []string{"cell-1", "cell-1-volume", "cell-2", "cell-2-volume"}},
		{"run id only", ports.ResourceFilter{RunID: "run-1"}, []string{"cell-1", "cell-1-volume", "cell-3", "cell-3-volume"}},
		{"both", ports.ResourceFilter{Key: k1, RunID: "run-2"}, []string{"cell-2", "cell-2-volume"}},
		{"no match", ports.ResourceFilter{Key: k2, RunID: "run-2"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ListResources(context.Background(), tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, r := range got {
				ids = append(ids, r.ID)
				if (r.Kind == fake.KindVolume) != strings.HasSuffix(r.ID, "-volume") {
					t.Errorf("resource %s has kind %s", r.ID, r.Kind)
				}
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Errorf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestSandbox_SecondInstanceRecoversFirstInstanceResources(t *testing.T) {
	h := fake.NewHost(bigHost)
	ctx := context.Background()
	a, b := h.Sandbox("a"), h.Sandbox("b")
	k1, k2 := taskKey(t, "1"), taskKey(t, "2")
	c1 := newCell(t, a, k1, "run-1", ports.CellLimits{CPUMillis: 1000})
	c2 := newCell(t, a, k2, "run-2", ports.CellLimits{CPUMillis: 1000})
	got, err := b.ListResources(ctx, ports.ResourceFilter{})
	if err != nil || len(got) != 4 {
		t.Fatalf("instance b lists %d resources, err %v; want 4", len(got), err)
	}
	for _, id := range []string{c1, c2} {
		if err := b.Kill(ctx, id); err != nil {
			t.Fatalf("instance b kill %s: %v", id, err)
		}
		if inst, ok := h.CellInstance(id); !ok || inst != "a" {
			t.Errorf("CellInstance(%s) = %q, %v; want a", id, inst, ok)
		}
	}
	if c, _ := b.HostCapacity(ctx); c != bigHost {
		t.Errorf("capacity after kills = %+v, want %+v", c, bigHost)
	}
	if err := b.DestroyByTaskKey(ctx, k1); err != nil {
		t.Fatalf("instance b destroy: %v", err)
	}
	left, _ := a.ListResources(ctx, ports.ResourceFilter{})
	if len(left) != 2 || left[0].ID != c2 || left[0].Key != k2 {
		t.Errorf("instance a lists %+v after destroy, want only cell %s", left, c2)
	}
	if _, ok := h.CellInstance(c1); ok {
		t.Errorf("destroyed cell %s still has an instance", c1)
	}
}

func TestSandbox_DestroyByTaskKeyVerifies(t *testing.T) {
	h := fake.NewHost(bigHost)
	s := h.Sandbox("a")
	ctx := context.Background()
	k1, k2 := taskKey(t, "1"), taskKey(t, "2")
	newCell(t, s, k1, "run-1", ports.CellLimits{})
	newCell(t, s, k2, "run-2", ports.CellLimits{})
	h.FailNextDestroy(k1)
	err := s.DestroyByTaskKey(ctx, k1)
	if !errors.Is(err, fake.ErrTeardownUnverified) {
		t.Fatalf("scripted failure err = %v, want ErrTeardownUnverified", err)
	}
	if !strings.Contains(err.Error(), "cell-1-volume") {
		t.Errorf("error %q does not name the remaining volume", err)
	}
	if left, _ := s.ListResources(ctx, ports.ResourceFilter{Key: k1}); len(left) != 1 || left[0].Kind != fake.KindVolume {
		t.Errorf("after failed destroy: %+v, want the volume only", left)
	}
	if err := s.DestroyByTaskKey(ctx, k1); err != nil {
		t.Fatalf("next destroy: %v", err)
	}
	if left, _ := s.ListResources(ctx, ports.ResourceFilter{Key: k1}); len(left) != 0 {
		t.Errorf("after destroy: %+v remain", left)
	}
	if other, _ := s.ListResources(ctx, ports.ResourceFilter{Key: k2}); len(other) != 2 {
		t.Errorf("other key lost resources: %+v", other)
	}
}

func TestSandbox_DestroyKeepsScriptedSteps(t *testing.T) {
	h := fake.NewHost(bigHost)
	s := h.Sandbox("a")
	ctx := context.Background()
	key := taskKey(t, "1")
	h.ScriptRun(key, fake.RunStep{Outcome: ports.CellRunOutcome{ExitCode: 5}})
	newCell(t, s, key, "run-1", ports.CellLimits{})
	if err := s.DestroyByTaskKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	id := readyCell(t, s, key, "run-2")
	if got, err := s.Run(ctx, id, argv); err != nil || got.ExitCode != 5 {
		t.Errorf("run after destroy = %+v, %v; want scripted exit 5", got, err)
	}
}

func TestSandbox_CreateCellValidatesSpec(t *testing.T) {
	key := taskKey(t, "1")
	tests := []struct {
		name string
		spec ports.CellSpec
	}{
		{"empty task key", ports.CellSpec{RunID: "run-1"}},
		{"invalid task key", ports.CellSpec{Key: "NOT-A-KEY", RunID: "run-1"}},
		{"uppercase task key", ports.CellSpec{Key: model.TaskKey(strings.ToUpper(string(key))), RunID: "run-1"}},
		{"empty run id", ports.CellSpec{Key: key}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := fake.NewHost(bigHost)
			if _, err := h.Sandbox("a").CreateCell(context.Background(), tt.spec); err == nil {
				t.Fatal("invalid spec accepted")
			}
			if st := h.Stats(); st.Created != 0 {
				t.Errorf("stats %+v after refusal", st)
			}
		})
	}
}

func TestSandbox_CancelledContext(t *testing.T) {
	h := fake.NewHost(bigHost)
	s := h.Sandbox("a")
	key := taskKey(t, "1")
	id := readyCell(t, s, key, "run-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		call func() error
	}{
		{"CreateCell", func() error { _, err := s.CreateCell(ctx, ports.CellSpec{Key: key, RunID: "run-2"}); return err }},
		{"PutContent", func() error { return s.PutContent(ctx, id, ports.CellContent{}) }},
		{"Run", func() error { _, err := s.Run(ctx, id, argv); return err }},
		{"GetFiles", func() error { _, err := s.GetFiles(ctx, id, nil); return err }},
		{"Kill", func() error { return s.Kill(ctx, id) }},
		{"ListResources", func() error { _, err := s.ListResources(ctx, ports.ResourceFilter{}); return err }},
		{"DestroyByTaskKey", func() error { return s.DestroyByTaskKey(ctx, key) }},
		{"HostCapacity", func() error { _, err := s.HostCapacity(ctx); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", err)
			}
		})
	}
	if st := h.Stats(); st != (fake.Stats{Created: 1, Live: 1}) {
		t.Errorf("stats %+v after cancelled calls", st)
	}
	if len(h.Runs()) != 0 {
		t.Errorf("cancelled run recorded")
	}
}
