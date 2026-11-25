package local_test

import (
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

const (
	secretTitle   = "dry-title-7f3a"
	secretBody    = "dry-body-91c2"
	secretComment = "dry-comment-55de"
	secretLabel   = "dry-label-0b8e"
)

// dryRunCall runs one write with mode and reports whether its result is
// the zero value.
type dryRunCall struct {
	name string
	call func(f *local.Forge, src model.TaskSource, key model.TaskKey, mode ports.WriteMode) (zero bool, err error)
}

func dryRunCalls() []dryRunCall {
	return []dryRunCall{
		{"Ingest", func(f *local.Forge, _ model.TaskSource, _ model.TaskKey, mode ports.WriteMode) (bool, error) {
			rej, err := f.Ingest(ctx, mode)
			return rej == nil, err
		}},
		{"CreateTask", func(f *local.Forge, _ model.TaskSource, key model.TaskKey, mode ports.WriteMode) (bool, error) {
			src, err := f.CreateTask(ctx, mode, ports.NewTask{Key: key, Title: secretTitle, Body: secretBody, Labels: []string{secretLabel}})
			return src == (model.TaskSource{}), err
		}},
		{"CloseTask", func(f *local.Forge, src model.TaskSource, _ model.TaskKey, mode ports.WriteMode) (bool, error) {
			return true, f.CloseTask(ctx, mode, src, secretComment)
		}},
		{"ReopenTask", func(f *local.Forge, src model.TaskSource, _ model.TaskKey, mode ports.WriteMode) (bool, error) {
			return true, f.ReopenTask(ctx, mode, src, secretComment)
		}},
		{"SetProjectionLabels", func(f *local.Forge, src model.TaskSource, _ model.TaskKey, mode ports.WriteMode) (bool, error) {
			return true, f.SetProjectionLabels(ctx, mode, src, []string{secretLabel})
		}},
	}
}

// dryRunEnv opens a project holding task 1 and an un-ingested inbox file
// with the secret title and body.
func dryRunEnv(t *testing.T) (*env, *local.Forge, model.TaskSource) {
	t.Helper()
	e := newEnv(t)
	f := e.open(t)
	src := createTask(t, f, testKey(t, "existing"), "Existing")
	e.drop(t, "dry.task.md", taskText(secretTitle, secretBody))
	e.logs.Reset()
	return e, f, src
}

func TestDryRun_WritesNothing(t *testing.T) {
	for _, c := range dryRunCalls() {
		t.Run(c.name, func(t *testing.T) {
			e, f, src := dryRunEnv(t)
			before := snapshot(t, e.root())
			zero, err := c.call(f, src, testKey(t, "new"), ports.WriteModeDryRun)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if !zero {
				t.Error("dry run returned a non-zero result")
			}
			sameSnapshot(t, before, snapshot(t, e.root()))
			logs := e.logs.String()
			if !strings.Contains(logs, c.name) {
				t.Errorf("log %q lacks the method name", logs)
			}
			for _, s := range []string{secretTitle, secretBody, secretComment, secretLabel} {
				if strings.Contains(logs, s) {
					t.Errorf("log contains free text %q: %s", s, logs)
				}
			}
		})
	}
}

func TestDryRun_InvalidModeWritesNothing(t *testing.T) {
	for _, mode := range []ports.WriteMode{"", "bogus"} {
		for _, c := range dryRunCalls() {
			t.Run(c.name+"/"+string(mode), func(t *testing.T) {
				e, f, src := dryRunEnv(t)
				before := snapshot(t, e.root())
				if _, err := c.call(f, src, testKey(t, "new"), mode); err == nil {
					t.Fatal("invalid write mode accepted")
				}
				sameSnapshot(t, before, snapshot(t, e.root()))
			})
		}
	}
}
