package local_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/adapters/forge/local"
	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// readChecks returns the checks of head, failing the test on error.
func readChecks(t *testing.T, f *local.Forge, head string) []ports.Check {
	t.Helper()
	cs, err := f.ReadChecks(ctx, head)
	if err != nil {
		t.Fatalf("read checks: %v", err)
	}
	return cs
}

// wantCheck asserts that checks is the single verify check of head with
// the given flags.
func wantCheck(t *testing.T, checks []ports.Check, head string, completed, passed bool) {
	t.Helper()
	want := []ports.Check{{Name: "verify", HeadSHA: head, Completed: completed, Passed: passed}}
	if !slices.Equal(checks, want) {
		t.Fatalf("checks = %+v, want %+v", checks, want)
	}
}

func runChecks(t *testing.T, f *local.Forge) {
	t.Helper()
	if err := f.RunChecks(ctx); err != nil {
		t.Fatalf("run checks: %v", err)
	}
}

// publishedHead opens a project and publishes a change for key on feat/a
// whose head adds only-head.txt; it returns the forge and the head.
func publishedHead(t *testing.T, e *env, key model.TaskKey) (*local.Forge, string) {
	t.Helper()
	f := e.open(t)
	head := e.commitOn(t, "feat/a", "only-head.txt", "head only\n")
	publish(t, f, publishReq(key, "feat/a", head))
	return f, head
}

func TestReadChecks_NoAttempt(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	cs := readChecks(t, f, e.mainSHA)
	if cs == nil || len(cs) != 0 {
		t.Fatalf("checks = %#v, want an empty, non-nil list", cs)
	}
	if _, err := f.ReadChecks(ctx, "not-a-sha"); err == nil {
		t.Error("invalid head sha accepted")
	}
}

func TestRunChecks_NothingPending(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	runChecks(t, f)
	if got := e.host.Stats().Created; got != 0 {
		t.Errorf("created %d cells, want 0", got)
	}
}

func TestRunChecks_Passed(t *testing.T) {
	e := newEnv(t)
	e.cfg.Verify = []string{"make verify", "go test ./..."}
	f, head := publishedHead(t, e, testKey(t, "1"))
	wantCheck(t, readChecks(t, f, head), head, false, false)
	runChecks(t, f)
	wantCheck(t, readChecks(t, f, head), head, true, true)

	a := e.readAttempt(t, head, 1)
	if a["status"] != "passed" || a["finishedAt"] != testStart.Format("2006-01-02T15:04:05Z07:00") {
		t.Errorf("attempt 1 = %v, want passed at the clock time", a)
	}
	runs := e.host.Runs()
	if len(runs) != len(e.cfg.Verify) {
		t.Fatalf("runs = %+v, want one per verify command", runs)
	}
	for i, cmd := range e.cfg.Verify {
		if want := []string{"sh", "-c", cmd}; !slices.Equal(runs[i].Argv, want) {
			t.Errorf("run %d argv = %q, want %q", i, runs[i].Argv, want)
		}
	}
	content, ok := e.host.Content(runs[0].CellID)
	if !ok {
		t.Fatal("run cell has no content")
	}
	if !slices.Contains(content.Workspace, "only-head.txt") || !slices.Contains(content.Workspace, "README") {
		t.Errorf("workspace = %q, want the head's tree", content.Workspace)
	}
	if st := e.host.Stats(); st.Live != 0 || st.Created != 1 {
		t.Errorf("stats = %+v, want one cell, killed", st)
	}
	if _, err := os.Lstat(filepath.Join(e.cfg.RunDir, field(t, a["runId"]))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("run copy left behind: %v", err)
	}
	// A second call has nothing pending.
	runChecks(t, f)
	if got := len(e.host.Runs()); got != len(e.cfg.Verify) {
		t.Errorf("second RunChecks ran again: %d runs", got)
	}
}

func TestRunChecks_Failed(t *testing.T) {
	e := newEnv(t)
	key := testKey(t, "1")
	e.host.ScriptRun(key, fake.RunStep{Outcome: ports.CellRunOutcome{ExitCode: 3}})
	f, head := publishedHead(t, e, key)
	runChecks(t, f)
	wantCheck(t, readChecks(t, f, head), head, true, false)
	if got := e.readAttempt(t, head, 1)["status"]; got != "failed" {
		t.Errorf("attempt 1 status = %v, want failed", got)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "exitCode=3") || !strings.Contains(logs, "status=failed") {
		t.Errorf("log %q lacks the outcome", logs)
	}
}

func TestRunChecks_SandboxErrorLeavesAttemptRunning(t *testing.T) {
	e := newEnv(t)
	key := testKey(t, "1")
	boom := errors.New("sandbox down")
	e.host.ScriptRun(key, fake.RunStep{Err: boom})
	f, head := publishedHead(t, e, key)
	if err := f.RunChecks(ctx); !errors.Is(err, boom) {
		t.Fatalf("run checks = %v, want the sandbox error", err)
	}
	a := e.readAttempt(t, head, 1)
	if a["status"] != "running" || a["finishedAt"] != nil {
		t.Fatalf("attempt 1 = %v, want still running", a)
	}
	wantCheck(t, readChecks(t, f, head), head, false, false)

	// The attempt is still pending: a later call with a passing script
	// records it passed, with no new attempt.
	runChecks(t, f)
	wantCheck(t, readChecks(t, f, head), head, true, true)
	if n := e.attemptCount(t, head); n != 1 {
		t.Errorf("%d attempts, want 1", n)
	}
	if got := e.readAttempt(t, head, 1)["runId"]; got != a["runId"] {
		t.Errorf("run id changed from %v to %v", a["runId"], got)
	}
}

func TestRunChecks_RemovesLeftoverRunCopy(t *testing.T) {
	e := newEnv(t)
	f, head := publishedHead(t, e, testKey(t, "1"))
	copyDir := filepath.Join(e.cfg.RunDir, field(t, e.readAttempt(t, head, 1)["runId"]))
	if err := os.MkdirAll(filepath.Join(copyDir, "stale"), 0o700); err != nil {
		t.Fatal(err)
	}
	runChecks(t, f)
	wantCheck(t, readChecks(t, f, head), head, true, true)
	if _, err := os.Lstat(copyDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("run copy left behind: %v", err)
	}
}

func TestRerunChecks_KeepsOldAttempts(t *testing.T) {
	e := newEnv(t)
	key := testKey(t, "1")
	e.host.ScriptRun(key, fake.RunStep{Outcome: ports.CellRunOutcome{ExitCode: 1}})
	f, head := publishedHead(t, e, key)
	runChecks(t, f)
	wantCheck(t, readChecks(t, f, head), head, true, false)

	if err := f.RerunChecks(ctx, ports.WriteModeLive, head); err != nil {
		t.Fatalf("rerun checks: %v", err)
	}
	first, second := e.readAttempt(t, head, 1), e.readAttempt(t, head, 2)
	if first["status"] != "failed" {
		t.Errorf("attempt 1 = %v, want kept failed", first)
	}
	if second["status"] != "running" || second["runId"] == first["runId"] || second["marker"] != first["marker"] {
		t.Errorf("attempt 2 = %v, want a new running attempt with attempt 1's marker", second)
	}
	wantCheck(t, readChecks(t, f, head), head, false, false)
	runChecks(t, f)
	wantCheck(t, readChecks(t, f, head), head, true, true)
	if got := e.readAttempt(t, head, 1)["status"]; got != "failed" {
		t.Errorf("attempt 1 status = %v after the rerun", got)
	}
}

func TestRerunChecks_MergeSHA(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	key := testKey(t, "1")
	rec := changeJSON("c-1", key, "merged", "2026-11-25T09:00:00Z")
	e.writeFile(t, "changes/c-1.json", mustJSON(t, rec))
	merge := field(t, rec["mergeSha"])
	if err := f.RerunChecks(ctx, ports.WriteModeLive, merge); err != nil {
		t.Fatalf("rerun checks of the merge sha: %v", err)
	}
	if got := e.readAttempt(t, merge, 1)["marker"]; got != "ddf:task-key="+string(key) {
		t.Errorf("attempt marker = %v", got)
	}
}

func TestRerunChecks_Refused(t *testing.T) {
	e := newEnv(t)
	f := e.open(t)
	head := "0123456789abcdef0123456789abcdef01234567" // changeJSON's headSha
	e.writeFile(t, "changes/c-1.json", mustJSON(t, changeJSON("c-1", testKey(t, "1"), "open", "2026-11-25T09:00:00Z")))
	before := snapshot(t, e.root())
	for name, sha := range map[string]string{
		"unknown head": "fedcba9876543210fedcba9876543210fedcba98",
		"invalid sha":  "not-a-sha",
	} {
		if err := f.RerunChecks(ctx, ports.WriteModeLive, sha); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	sameSnapshot(t, before, snapshot(t, e.root()))

	// Two change records of different keys name one head.
	e.writeFile(t, "changes/c-2.json", mustJSON(t, changeJSON("c-2", testKey(t, "2"), "open", "2026-11-25T09:00:00Z")))
	before = snapshot(t, e.root())
	if err := f.RerunChecks(ctx, ports.WriteModeLive, head); err == nil {
		t.Error("head of two task keys accepted")
	}
	sameSnapshot(t, before, snapshot(t, e.root()))
}

// TestRunChecks_RestartAfterPublish leaves the attempt PublishChange
// started running with a live cell of its run, then opens a new instance on
// the same storage and sandbox host.
func TestRunChecks_RestartAfterPublish(t *testing.T) {
	e := newEnv(t)
	key := testKey(t, "1")
	f1, head := publishedHead(t, e, key)
	oldRun := field(t, e.readAttempt(t, head, 1)["runId"])
	if _, err := e.sandbox.CreateCell(ctx, ports.CellSpec{Key: key, RunID: oldRun}); err != nil {
		t.Fatal(err)
	}
	res, err := e.sandbox.ListResources(ctx, ports.ResourceFilter{Key: key, RunID: oldRun})
	if err != nil {
		t.Fatal(err)
	}
	containers := 0
	for _, r := range res {
		if r.Kind == fake.KindContainer {
			containers++
		}
	}
	before := e.host.Stats()

	e.cfg.Sandbox = e.host.Sandbox("b")
	f2 := e.open(t)

	if got := e.host.Stats().Killed - before.Killed; got != containers || containers == 0 {
		t.Fatalf("killed %d cells, want %d (> 0)", got, containers)
	}
	lost := e.readAttempt(t, head, 1)
	if lost["status"] != "lost" || lost["finishedAt"] == nil || lost["runId"] != oldRun {
		t.Fatalf("attempt 1 = %v, want lost with finishedAt", lost)
	}
	next := e.readAttempt(t, head, 2)
	if next["status"] != "running" || next["runId"] == oldRun || next["runId"] == "" {
		t.Fatalf("attempt 2 = %v, want running with a new run id", next)
	}
	wantCheck(t, readChecks(t, f2, head), head, false, false)

	// The new instance runs the new attempt; the old one's pending attempt
	// is no longer running and is refused.
	runChecks(t, f2)
	wantCheck(t, readChecks(t, f2, head), head, true, true)
	if err := f1.RunChecks(ctx); err == nil {
		t.Error("old instance ran an attempt marked lost")
	}
	if got := e.readAttempt(t, head, 1)["status"]; got != "lost" {
		t.Errorf("attempt 1 status = %v, want lost", got)
	}
}

// field returns a decoded JSON string field.
func field(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok || s == "" {
		t.Fatalf("field %v is not a non-empty string", v)
	}
	return s
}
