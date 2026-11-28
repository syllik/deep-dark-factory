package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/adapters/sandbox/fake"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/daemon"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

// reviewerFinding is an r1 REQUEST_CHANGES with one blocking P1 finding id,
// continuing priorID when it is not empty.
func reviewerFinding(id, priorID string) string {
	prior := ""
	if priorID != "" {
		prior = fmt.Sprintf(`,"priorId":%q`, priorID)
	}
	return `{"schema":"ddf.reviewer.result/v1","role":"r1","verdict":"REQUEST_CHANGES",` +
		fmt.Sprintf(`"findings":[{"id":%q%s,"severity":"P1","category":"correctness","evidence":"greeting %s is wrong.",`, id, prior, id) +
		`"location":"hello.txt:1","fixHint":"say hello world"}],"hardStopChecklist":[{"item":"secrets","status":"clear"}]}`
}

// executorFix is a COMPLETE Executor step writing content to hello.txt,
// declining the findings in wontFix (id, reason pairs).
func executorFix(content string, wontFix ...string) fakes.AgentStep {
	var wf []string
	for i := 0; i+1 < len(wontFix); i += 2 {
		wf = append(wf, fmt.Sprintf(`{"findingId":%q,"reason":%q}`, wontFix[i], wontFix[i+1]))
	}
	extra := ""
	if len(wf) > 0 {
		extra = `,"wontFix":[` + strings.Join(wf, ",") + `]`
	}
	res := `{"schema":"ddf.executor.result/v1","verdict":"COMPLETE","summary":"Fixed the greeting.",` +
		`"verify":{"commands":["make verify"],"passed":true}` + extra + `}`
	return fakes.AgentStep{Result: []byte(res), Edits: []fakes.FileEdit{{Path: "hello.txt", Content: []byte(content)}}}
}

// executorCalls returns the agent calls of the executor in call order.
func executorCalls(e *engEnv) []ports.AgentRunRequest {
	var out []ports.AgentRunRequest
	for _, c := range e.agent.Calls() {
		if c.Role == model.RoleExecutor {
			out = append(out, c)
		}
	}
	return out
}

// batchBriefOf decodes the brief of a correction or CI-fix run.
type batchBriefOf struct {
	Purpose      model.RunPurpose `json:"purpose"`
	Head         string           `json:"head"`
	FailedChecks []string         `json:"failedChecks"`
	Findings     []struct {
		ID, Role, Severity, Category, Evidence, Location, FixHint string
	} `json:"findings"`
	GateFailure *struct {
		Command  string `json:"command"`
		Index    int    `json:"index"`
		ExitCode int    `json:"exitCode"`
		TimedOut bool   `json:"timedOut"`
	} `json:"gateFailure"`
}

func decodeBrief(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("decode brief %s: %v", b, err)
	}
}

// executorRuns returns the stored executor runs of key with purpose.
func executorRuns(t *testing.T, e *engEnv, key model.TaskKey, purpose model.RunPurpose) []model.Run {
	t.Helper()
	runs, err := e.store.ListRuns(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	var out []model.Run
	for _, r := range runs {
		if r.Role == model.RoleExecutor && r.Purpose == purpose {
			out = append(out, r)
		}
	}
	return out
}

func tickOnce(t *testing.T, e *engEnv) model.Task {
	t.Helper()
	if err := e.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	task, err := e.store.GetTask(t.Context(), e.key)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func withLogs(logs *syncBuffer) func(*daemon.Settings, *daemon.Deps) {
	return func(_ *daemon.Settings, d *daemon.Deps) { d.Logger = slog.New(slog.NewTextHandler(logs, nil)) }
}

func TestTick_CorrectionBatchToMerge(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	e.addInbox(t)
	e.scriptHappyPath()
	e.agent.Script(model.RoleR1, fakes.AgentStep{Result: []byte(reviewerFinding("f1", ""))},
		fakes.AgentStep{Result: []byte(reviewerApprove)})
	e.agent.Script(model.RoleExecutor, executorFix("hello world\n", "f1", "the greeting text is final"))
	task := e.tickUntil(t, 12, model.TaskStateFixing)
	rev1 := storedChange(t, e, task.Key)
	if rev1.Revision != 1 {
		t.Fatalf("stored change = %+v, want revision 1", rev1)
	}

	// The fixing tick runs the correction batch and publishes revision 2.
	if err := e.clock.Advance(time.Minute); err != nil {
		t.Fatal(err)
	}
	task = tickOnce(t, e)
	if task.State != model.TaskStateCI || len(task.Flags) != 0 {
		t.Fatalf("task = %s %v, want ci", task.State, task.Flags)
	}
	calls := executorCalls(e)
	if len(calls) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(calls))
	}
	var b batchBriefOf
	decodeBrief(t, calls[1].Brief, &b)
	if b.Purpose != model.RunPurposeCorrection || b.Head != rev1.HeadSHA || len(b.Findings) != 1 {
		t.Fatalf("correction brief = %+v, want correction on %s with one finding", b, rev1.HeadSHA)
	}
	if f := b.Findings[0]; f.ID != "f1" || f.Role != "r1" || f.Severity != "P1" || f.Category != "correctness" ||
		f.Evidence != "greeting f1 is wrong." || f.Location != "hello.txt:1" || f.FixHint != "say hello world" {
		t.Errorf("brief finding = %+v", f)
	}
	cr := executorRuns(t, e, task.Key, model.RunPurposeCorrection)
	if len(cr) != 1 || cr[0].Revision == nil || *cr[0].Revision != 1 || cr[0].HeadSHA != rev1.HeadSHA {
		t.Errorf("correction runs = %+v, want one on revision 1 at %s", cr, rev1.HeadSHA)
	}
	rev2 := storedChange(t, e, task.Key)
	if rev2.ID != rev1.ID || rev2.Revision != 2 || rev2.HeadSHA == rev1.HeadSHA || rev2.CISince == nil ||
		!rev2.CISince.Equal(e.clock.Now()) || rev2.PatchID == "" || rev2.PatchID == rev1.PatchID {
		t.Errorf("stored change = %+v, want revision 2 of %s at a new head with ci wait reset", rev2, rev1.ID)
	}
	branch := "ddf/task-" + string(task.Key)[:12]
	if tip := engGit(t, e.git, e.bare, "rev-parse", "refs/heads/"+branch); tip != rev2.HeadSHA {
		t.Errorf("remote branch tip = %s, want %s", tip, rev2.HeadSHA)
	}
	if parent := engGit(t, e.git, e.bare, "rev-parse", rev2.HeadSHA+"^"); parent != rev1.HeadSHA {
		t.Errorf("revision 2 parent = %s, want revision 1 head %s", parent, rev1.HeadSHA)
	}
	if got := engGit(t, e.git, e.bare, "show", rev2.HeadSHA+":hello.txt"); got != "hello world" {
		t.Errorf("revision 2 hello.txt = %q", got)
	}
	info, found, err := e.forge.FindChangeByTaskKey(t.Context(), task.Key)
	if err != nil || !found || info.HeadSHA != rev2.HeadSHA || info.ID != rev1.ID {
		t.Fatalf("forge change = %+v found %v err %v, want %s at %s", info, found, err, rev1.ID, rev2.HeadSHA)
	}
	if !strings.Contains(info.Body, "Won't fix:\n- f1: the greeting text is final") {
		t.Errorf("change body = %q, want the won't-fix list", info.Body)
	}
	// recForge records every PublishChange as open-change; here it runs
	// inside the update-change intent of revision 2.
	ev := e.rec.eventStrings()
	if i := slices.Index(ev, "create update-change dryRun=false"); i < 0 || i+2 >= len(ev) ||
		ev[i+1] != "write open-change live" || ev[i+2] != "resolve update-change succeeded" {
		t.Errorf("intent and write calls = %v, want PublishChange inside an update-change intent", ev)
	}

	// ci, then a re-review with the prior finding, then merge.
	task = e.tickUntil(t, 4, model.TaskStateMerge)
	var r1 []ports.AgentRunRequest
	for _, c := range e.agent.Calls() {
		if c.Role == model.RoleR1 {
			r1 = append(r1, c)
		}
	}
	if len(r1) != 2 {
		t.Fatalf("r1 calls = %d, want 2", len(r1))
	}
	var first, second struct {
		Head          string          `json:"head"`
		PriorFindings []model.Finding `json:"priorFindings"`
		ChangeBody    string          `json:"changeBody"`
	}
	decodeBrief(t, r1[0].Brief, &first)
	decodeBrief(t, r1[1].Brief, &second)
	if first.PriorFindings == nil || len(first.PriorFindings) != 0 {
		t.Errorf("first review prior findings = %+v, want an empty list", first.PriorFindings)
	}
	if second.Head != rev2.HeadSHA || len(second.PriorFindings) != 1 || second.PriorFindings[0].ID != "f1" ||
		!second.PriorFindings[0].Blocking || second.PriorFindings[0].HeadSHA != rev1.HeadSHA {
		t.Errorf("re-review brief head %s prior %+v, want f1 of %s", second.Head, second.PriorFindings, rev1.HeadSHA)
	}
	if !strings.Contains(second.ChangeBody, "- f1: the greeting text is final") {
		t.Errorf("re-review change body = %q, want the won't-fix line", second.ChangeBody)
	}
	rs := storedReviews(t, e, task, rev2)
	if len(rs) != 2 || rs[1].Round != 2 || rs[1].HeadSHA != rev2.HeadSHA || rs[1].Verdict != model.ReviewVerdictApprove {
		t.Errorf("stored reviews = %+v, want round 2 APPROVE on %s", rs, rev2.HeadSHA)
	}
}

func TestTick_ReReviewLoopGuardNeedsOwner(t *testing.T) {
	logs := &syncBuffer{}
	e := newEngEnv(t, ports.WriteModeLive, withLogs(logs))
	e.addInbox(t)
	e.scriptHappyPath()
	e.agent.Script(model.RoleR1, fakes.AgentStep{Result: []byte(reviewerFinding("f1", ""))},
		fakes.AgentStep{Result: []byte(reviewerFinding("f2", "f1"))})
	e.agent.Script(model.RoleExecutor, executorFix("hello world\n"))
	e.tickUntil(t, 12, model.TaskStateFixing)
	task := e.tickUntil(t, 4, model.TaskStateReview)
	task = tickOnce(t, e)
	if task.State != model.TaskStateReview || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) || task.HitStop {
		t.Errorf("task = %s %v hitStop %v, want review with needs-owner", task.State, task.Flags, task.HitStop)
	}
	if !strings.Contains(logs.String(), "fix loop") {
		t.Errorf("logs do not name the fix loop:\n%s", logs.String())
	}
}

func TestTick_CorrectionBatchBound(t *testing.T) {
	logs := &syncBuffer{}
	e := newEngEnv(t, ports.WriteModeLive, withLogs(logs))
	e.addInbox(t)
	e.scriptHappyPath()
	for i := 1; i <= 4; i++ {
		e.agent.Script(model.RoleR1, fakes.AgentStep{Result: []byte(reviewerFinding(fmt.Sprintf("f%d", i), ""))})
	}
	for i := 1; i <= 3; i++ {
		e.agent.Script(model.RoleExecutor, executorFix(fmt.Sprintf("hello %d\n", i)))
	}
	e.tickUntil(t, 12, model.TaskStateFixing)
	for i := 1; i <= 3; i++ {
		e.tickUntil(t, 6, model.TaskStateCI)
		e.tickUntil(t, 6, model.TaskStateFixing)
	}
	task := tickOnce(t, e)
	if task.State != model.TaskStateFixing || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
		t.Errorf("task = %s %v, want fixing with needs-owner", task.State, task.Flags)
	}
	if !strings.Contains(logs.String(), "correction batches per change") {
		t.Errorf("logs do not name the bound:\n%s", logs.String())
	}
	if n := len(executorRuns(t, e, task.Key, model.RunPurposeCorrection)); n != 3 {
		t.Errorf("correction runs = %d, want 3", n)
	}
	if c := storedChange(t, e, task.Key); c.Revision != 4 {
		t.Errorf("stored revision = %d, want 4", c.Revision)
	}
}

// failChecksForge reports the completed checks of the first n distinct heads
// it reads as failed.
type failChecksForge struct {
	ports.Forge
	mu    *sync.Mutex
	n     int
	heads map[string]bool
}

func newFailChecksForge(inner ports.Forge, n int) failChecksForge {
	return failChecksForge{Forge: inner, mu: &sync.Mutex{}, n: n, heads: map[string]bool{}}
}

func (f failChecksForge) ReadChecks(ctx context.Context, head string) ([]ports.Check, error) {
	cs, err := f.Forge.ReadChecks(ctx, head)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(cs) > 0 && cs[0].Completed {
		if _, seen := f.heads[head]; !seen {
			f.heads[head] = len(f.heads) < f.n
		}
	}
	if !f.heads[head] {
		return cs, nil
	}
	out := slices.Clone(cs)
	for i := range out {
		out[i].Passed = false
	}
	return out, nil
}

func TestTick_CIFixBatch(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, func(_ *daemon.Settings, d *daemon.Deps) {
		d.Forge = newFailChecksForge(d.Forge, 1)
	})
	e.addInbox(t)
	e.scriptToMerge()
	e.agent.Script(model.RoleExecutor, executorFix("hello fixed\n"))
	task := e.tickUntil(t, 12, model.TaskStateFixing)
	rev1 := storedChange(t, e, task.Key)

	task = tickOnce(t, e)
	if task.State != model.TaskStateCI || len(task.Flags) != 0 {
		t.Fatalf("task = %s %v, want ci after the ci-fix", task.State, task.Flags)
	}
	calls := executorCalls(e)
	var b batchBriefOf
	decodeBrief(t, calls[len(calls)-1].Brief, &b)
	if b.Purpose != model.RunPurposeCIFix || b.Head != rev1.HeadSHA || !slices.Equal(b.FailedChecks, []string{"verify"}) {
		t.Errorf("ci-fix brief = %+v, want ci-fix on %s with failed check verify", b, rev1.HeadSHA)
	}
	runs := executorRuns(t, e, task.Key, model.RunPurposeCIFix)
	if len(runs) != 1 || runs[0].Revision == nil || *runs[0].Revision != 1 || runs[0].HeadSHA != rev1.HeadSHA {
		t.Errorf("ci-fix runs = %+v, want one on revision 1 at %s", runs, rev1.HeadSHA)
	}
	c := storedChange(t, e, task.Key)
	if c.Revision != 1 || c.HeadSHA == rev1.HeadSHA {
		t.Errorf("stored change = %+v, want revision 1 at a new head", c)
	}
	e.tickUntil(t, 4, model.TaskStateMerge)
}

func TestTick_CIFixBound(t *testing.T) {
	logs := &syncBuffer{}
	e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, d *daemon.Deps) {
		withLogs(logs)(s, d)
		d.Forge = newFailChecksForge(d.Forge, 3)
	})
	e.addInbox(t)
	e.scriptHappyPath()
	e.agent.Script(model.RoleExecutor, executorFix("hello 1\n"), executorFix("hello 2\n"))
	e.tickUntil(t, 12, model.TaskStateFixing)
	for range 2 {
		e.tickUntil(t, 2, model.TaskStateCI)
		e.tickUntil(t, 2, model.TaskStateFixing)
	}
	task := tickOnce(t, e)
	if task.State != model.TaskStateFixing || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
		t.Errorf("task = %s %v, want fixing with needs-owner", task.State, task.Flags)
	}
	if !strings.Contains(logs.String(), "ci-fix attempts per revision") {
		t.Errorf("logs do not name the bound:\n%s", logs.String())
	}
	if n := len(executorRuns(t, e, task.Key, model.RunPurposeCIFix)); n != 2 {
		t.Errorf("ci-fix runs = %d, want 2", n)
	}
	if c := storedChange(t, e, task.Key); c.Revision != 1 {
		t.Errorf("stored revision = %d, want 1", c.Revision)
	}
}

func TestTick_CIWaitBound(t *testing.T) {
	logs := &syncBuffer{}
	e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, d *daemon.Deps) {
		withLogs(logs)(s, d)
		// No check runner: the check attempt stays pending.
		d.Checks = nil
	})
	e.addInbox(t)
	e.scriptHappyPath()
	task := e.tickUntil(t, 10, model.TaskStateCI)
	since := storedChange(t, e, task.Key).CISince
	if since == nil {
		t.Fatal("stored change has no ci wait start")
	}
	e.clock.Set(since.Add(e.settings.Limits.CIWait))
	task = tickOnce(t, e)
	if task.State != model.TaskStateCI || len(task.Flags) != 0 {
		t.Fatalf("at exactly CIWait task = %s %v, want ci without flags", task.State, task.Flags)
	}
	if err := e.clock.Advance(1); err != nil {
		t.Fatal(err)
	}
	task = tickOnce(t, e)
	if task.State != model.TaskStateCI || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
		t.Errorf("past CIWait task = %s %v, want ci with needs-owner", task.State, task.Flags)
	}
	if !strings.Contains(logs.String(), "ci wait for one head") {
		t.Errorf("logs do not name the bound:\n%s", logs.String())
	}
}

// gateHost replaces the Publisher's sandbox with one on a new fake host, so
// the test scripts the local gate runs.
func gateHost(host *fake.Host) func(*daemon.Settings, *daemon.Deps) {
	return func(_ *daemon.Settings, d *daemon.Deps) {
		p := *d.Publisher
		p.Sandbox = host.Sandbox("ddfd")
		d.Publisher = &p
	}
}

func newGateHost() *fake.Host {
	return fake.NewHost(ports.Capacity{CPUMillis: 1 << 40, MemoryBytes: 1 << 50, DiskBytes: 1 << 50})
}

func TestTick_GateFailureCorrectionSameTick(t *testing.T) {
	host := newGateHost()
	e := newEngEnv(t, ports.WriteModeLive, gateHost(host))
	e.addInbox(t)
	e.scriptHappyPath()
	e.agent.Script(model.RoleExecutor, executorFix("hello gate\n"))
	e.tickUntil(t, 6, model.TaskStateRunning)
	host.ScriptRun(e.key, fake.RunStep{Outcome: ports.CellRunOutcome{ExitCode: 2}})
	base := engGit(t, e.git, e.bare, "rev-parse", "refs/heads/"+engBranch)

	task := tickOnce(t, e)
	if task.State != model.TaskStateCI || len(task.Flags) != 0 {
		t.Fatalf("task = %s %v, want ci in the same tick", task.State, task.Flags)
	}
	calls := executorCalls(e)
	if len(calls) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(calls))
	}
	var b batchBriefOf
	decodeBrief(t, calls[1].Brief, &b)
	if b.Purpose != model.RunPurposeCorrection || b.Head != base || b.GateFailure == nil ||
		b.GateFailure.Command != "make verify" || b.GateFailure.Index != 0 || b.GateFailure.ExitCode != 2 || b.GateFailure.TimedOut {
		t.Errorf("correction brief = %+v gate %+v, want the gate failure on %s", b, b.GateFailure, base)
	}
	cr := executorRuns(t, e, task.Key, model.RunPurposeCorrection)
	if len(cr) != 1 || cr[0].Revision != nil {
		t.Errorf("correction runs = %+v, want one without a revision", cr)
	}
	c := storedChange(t, e, task.Key)
	if c.Revision != 1 {
		t.Errorf("stored revision = %d, want 1", c.Revision)
	}
	if got := engGit(t, e.git, e.bare, "show", c.HeadSHA+":hello.txt"); got != "hello gate" {
		t.Errorf("published hello.txt = %q, want the correction's", got)
	}
	if parent := engGit(t, e.git, e.bare, "rev-parse", c.HeadSHA+"^"); parent != base {
		t.Errorf("revision parent = %s, want %s", parent, base)
	}
	if !slices.Contains(e.rec.states, model.TaskStateFixing) {
		t.Errorf("stored states = %v, want fixing", e.rec.states)
	}
}

func TestTick_GateFailureBound(t *testing.T) {
	logs := &syncBuffer{}
	host := newGateHost()
	e := newEngEnv(t, ports.WriteModeLive, func(s *daemon.Settings, d *daemon.Deps) {
		withLogs(logs)(s, d)
		gateHost(host)(s, d)
	})
	e.addInbox(t)
	e.scriptHappyPath()
	for i := 1; i <= 3; i++ {
		e.agent.Script(model.RoleExecutor, executorFix(fmt.Sprintf("hello %d\n", i)))
	}
	e.tickUntil(t, 6, model.TaskStateRunning)
	for range 5 {
		host.ScriptRun(e.key, fake.RunStep{Outcome: ports.CellRunOutcome{TimedOut: true, ExitCode: -1}})
	}

	task := tickOnce(t, e)
	if task.State != model.TaskStateFixing || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) {
		t.Errorf("task = %s %v, want fixing with needs-owner", task.State, task.Flags)
	}
	if !strings.Contains(logs.String(), "correction batches per change") {
		t.Errorf("logs do not name the bound:\n%s", logs.String())
	}
	if n := len(executorRuns(t, e, task.Key, model.RunPurposeCorrection)); n != 3 {
		t.Errorf("correction runs = %d, want 3", n)
	}
	if n := len(host.Runs()); n != 4 {
		t.Errorf("gate runs = %d, want 4", n)
	}
	if cs, err := e.store.ListChanges(t.Context(), task.Key); err != nil || len(cs) != 0 {
		t.Errorf("stored changes = %v, %v; want none", cs, err)
	}
	if got := e.rec.eventStrings(); len(got) != 0 {
		t.Errorf("intent and write calls = %v, want none", got)
	}
}
