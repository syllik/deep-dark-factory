package publisher_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// gateCommit returns a clean clone and an unreferenced commit in it, made
// by CommitTree as Prepare makes the revision commit, whose tree holds
// README and src/a.go.
func gateCommit(t *testing.T) (*git.Runner, string, string) {
	t.Helper()
	r := newRunner(t)
	src, base := newSourceRepo(t, r)
	keep(t, r, src, base)
	applied := commitOn(t, r, src, base, map[string]string{"src/a.go": "package a\n"})
	clone := cleanClone(t, r, src)
	tree := gitOut(t, r, clone, nil, "rev-parse", applied+"^{tree}")
	commit, err := r.CommitTree(t.Context(), clone, git.CommitInput{Tree: tree, Parents: []string{base},
		Identity: git.Identity{Name: "DDF Test", Email: "ddf@example.invalid"},
		When:     time.Unix(1_800_000_000, 0), Message: "revision\n"})
	if err != nil {
		t.Fatalf("CommitTree: %v", err)
	}
	if refs := gitOut(t, r, clone, nil, "for-each-ref", "--points-at", commit); refs != "" {
		t.Fatalf("revision commit is referenced: %s", refs)
	}
	return r, clone, commit
}

func TestLocalGate(t *testing.T) {
	r, clone, commit := gateCommit(t)
	key := model.TaskKey(strings.Repeat("a", 64))
	verify := []string{"go vet ./...", "go test ./...", "echo done"}
	runErr := errors.New("sandbox lost")
	for _, tc := range []struct {
		name     string
		script   []fake.RunStep
		edit     func(in *publisher.GateInput)
		want     publisher.GateResult
		wantErr  error // nil: no error; errAny: any error
		wantRuns int
		created  int
	}{
		{name: "every command exits 0: passed",
			want: publisher.GateResult{Passed: true, Failed: -1}, wantRuns: 3, created: 1},
		{name: "second of three exits 1: failed at 1, third not run",
			script: []fake.RunStep{{}, {Outcome: ports.CellRunOutcome{ExitCode: 1}}},
			want:   publisher.GateResult{Failed: 1, Outcome: ports.CellRunOutcome{ExitCode: 1}}, wantRuns: 2, created: 1},
		{name: "first command times out: failed at 0",
			script: []fake.RunStep{{Outcome: ports.CellRunOutcome{TimedOut: true}}},
			want:   publisher.GateResult{Failed: 0, Outcome: ports.CellRunOutcome{TimedOut: true}}, wantRuns: 1, created: 1},
		{name: "sandbox run error: error, fail closed",
			script:  []fake.RunStep{{}, {Err: runErr}},
			wantErr: runErr, wantRuns: 2, created: 1},
		{name: "empty verify: ErrNoVerify, no cell",
			edit:    func(in *publisher.GateInput) { in.Verify = nil },
			wantErr: publisher.ErrNoVerify},
		{name: "empty verify command: error, no cell",
			edit:    func(in *publisher.GateInput) { in.Verify = []string{"true", ""} },
			wantErr: errAny},
		{name: "invalid task key: error, no cell",
			edit:    func(in *publisher.GateInput) { in.Key = "ABC" },
			wantErr: errAny},
		{name: "empty run id: error, no cell",
			edit:    func(in *publisher.GateInput) { in.RunID = "" },
			wantErr: errAny},
		{name: "commit not a full sha: ErrInvalidSHA, no cell",
			edit:    func(in *publisher.GateInput) { in.Commit = "HEAD" },
			wantErr: git.ErrInvalidSHA},
		{name: "relative copy dir: error, no cell",
			edit:    func(in *publisher.GateInput) { in.CopyDir = "copy" },
			wantErr: errAny},
		{name: "existing copy dir: error, no cell, dir kept",
			edit: func(in *publisher.GateInput) {
				writeFile(t, filepath.Join(in.CopyDir, "keep.txt"), "mine\n", 0o644)
			},
			wantErr: errAny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := fake.NewHost(ports.Capacity{CPUMillis: 4000, MemoryBytes: 1 << 32, DiskBytes: 1 << 34})
			host.ScriptRun(key, tc.script...)
			in := publisher.GateInput{Key: key, RunID: "run-1", Commit: commit,
				CopyDir: filepath.Join(t.TempDir(), "copy"), Verify: slices.Clone(verify),
				Limits: ports.CellLimits{CPUMillis: 1000, MemoryBytes: 1 << 30, DiskBytes: 1 << 30},
				Egress: []string{"proxy.golang.org"}}
			if tc.edit != nil {
				tc.edit(&in)
			}
			_, existed := os.Lstat(in.CopyDir)
			got, err := publisher.LocalGate(t.Context(), r, host.Sandbox("i1"), clone, in)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("LocalGate error: %v", err)
			case tc.wantErr == errAny && err == nil, tc.wantErr != nil && tc.wantErr != errAny && !errors.Is(err, tc.wantErr):
				t.Fatalf("LocalGate error %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got != tc.want {
				t.Errorf("result %+v, want %+v", got, tc.want)
			}
			runs := host.Runs()
			if len(runs) != tc.wantRuns {
				t.Fatalf("%d runs, want %d: %+v", len(runs), tc.wantRuns, runs)
			}
			for i, run := range runs {
				if want := []string{"sh", "-c", verify[i]}; !reflect.DeepEqual(run.Argv, want) {
					t.Errorf("run %d argv %q, want %q", i, run.Argv, want)
				}
			}
			if st := host.Stats(); st != (fake.Stats{Created: tc.created, Killed: tc.created}) {
				t.Errorf("stats %+v, want %d created and killed, none live", st, tc.created)
			}
			if tc.created > 0 {
				content, ok := host.Content(runs[0].CellID)
				if !ok {
					t.Fatal("no content put in the cell")
				}
				for _, f := range []string{"README", "src/a.go"} {
					if !slices.Contains(content.Workspace, f) {
						t.Errorf("workspace %q lacks %s", content.Workspace, f)
					}
				}
			}
			_, statErr := os.Lstat(in.CopyDir)
			if existed == nil {
				if statErr != nil {
					t.Errorf("pre-existing copy dir removed: %v", statErr)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("copy dir %s still exists: %v", in.CopyDir, statErr)
			}
		})
	}
}

// errAny marks a table row that expects some error, of no particular kind.
var errAny = errors.New("any error")
