package daemon_test

import (
	"slices"
	"strconv"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/test/fakes"
)

const (
	reviewerApproveRaise = `{"schema":"ddf.reviewer.result/v1","role":"r1","verdict":"APPROVE","raiseRisk":"standard",` +
		`"findings":[],"hardStopChecklist":[{"item":"secrets","status":"clear"}]}`
	reviewerApproveR2 = `{"schema":"ddf.reviewer.result/v1","role":"r2","verdict":"APPROVE","findings":[],` +
		`"hardStopChecklist":[{"item":"secrets","status":"clear"}]}`
	reviewerHardStop = `{"schema":"ddf.reviewer.result/v1","role":"r1","verdict":"REQUEST_CHANGES",` +
		`"findings":[{"id":"f1","severity":"P1","category":"hard-stop","evidence":"hello.txt touches a hard-stop zone."}],` +
		`"hardStopChecklist":[{"item":"secrets","status":"hit"}]}`
)

// storedChange returns the only stored change of key.
func storedChange(t *testing.T, e *engEnv, key model.TaskKey) model.Change {
	t.Helper()
	cs, err := e.store.ListChanges(t.Context(), key)
	if err != nil || len(cs) != 1 {
		t.Fatalf("stored changes = %v, %v; want one", cs, err)
	}
	return cs[0]
}

// storedReviews returns the stored reviews of the change of task.
func storedReviews(t *testing.T, e *engEnv, task model.Task, c model.Change) []model.Review {
	t.Helper()
	rs, err := e.store.ListReviews(t.Context(), task.Key, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// verifyReview checks the signature of the stored row r against the payload
// rebuilt from that row with the engine's verifier.
func verifyReview(t *testing.T, e *engEnv, task model.Task, c model.Change, r model.Review) {
	t.Helper()
	p := signature.Payload{
		Version: "ddf.review-signature/v" + strconv.Itoa(r.SigVersion), KeyID: r.KeyID, TaskKey: task.Key,
		Forge: task.Forge, Repository: task.Repository, ChangeID: c.ID, Head: r.HeadSHA, Role: r.Role,
		Round: r.Round, Verdict: r.Verdict, BlockingCount: r.BlockingCount, FindingsDigest: r.FindingsDigest,
	}
	if err := e.verifier.Verify(p, r.Signature); err != nil {
		t.Errorf("verify stored %s review round %d: %v", r.Role, r.Round, err)
	}
}

func TestTick_ReviewRequestChangesToFixing(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	e.addInbox(t)
	e.scriptHappyPath()
	e.agent.Script(model.RoleR1, fakes.AgentStep{Result: []byte(reviewerRequestChanges)})
	task := e.tickUntil(t, 12, model.TaskStateFixing)

	c := storedChange(t, e, task.Key)
	rs := storedReviews(t, e, task, c)
	if len(rs) != 1 {
		t.Fatalf("stored reviews = %+v, want one", rs)
	}
	r := rs[0]
	if r.Role != model.RoleR1 || r.Round != 1 || r.Verdict != model.ReviewVerdictRequestChanges || r.BlockingCount != 1 ||
		r.HeadSHA != c.HeadSHA || r.KeyID != e.signer.KeyID() || r.SigVersion != 1 {
		t.Errorf("stored review = %+v, want r1 round 1 REQUEST_CHANGES with 1 blocking on %s", r, c.HeadSHA)
	}
	fs, err := e.store.ListFindings(t.Context(), r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].ID != "f1" || !fs[0].Blocking || fs[0].Severity != model.SeverityP1 || fs[0].Routed != "" {
		t.Errorf("stored findings = %+v, want blocking f1", fs)
	}
	verifyReview(t, e, task, c, r)
	if len(task.Flags) != 0 {
		t.Errorf("task flags = %v, want none", task.Flags)
	}
	if !slices.Contains(e.rec.eventStrings(), "resolve post-review succeeded") {
		t.Errorf("intent and write calls = %v, want a post-review", e.rec.eventStrings())
	}
}

func TestTick_ReviewRaiseRiskAddsR2(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	e.addInbox(t)
	e.scriptHappyPath()
	// The raise reruns the whole standard set: r1 again, then r2.
	e.agent.Script(model.RoleR1, fakes.AgentStep{Result: []byte(reviewerApproveRaise)},
		fakes.AgentStep{Result: []byte(reviewerApprove)})
	e.agent.Script(model.RoleR2, fakes.AgentStep{Result: []byte(reviewerApproveR2)})
	task := e.tickUntil(t, 12, model.TaskStateReview)

	// The first review tick raises the risk and stays in review.
	if err := e.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	task, err := e.store.GetTask(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateReview || task.Risk != model.RiskClassStandard || len(task.Flags) != 0 {
		t.Fatalf("task = %s risk %s %v, want review with risk standard", task.State, task.Risk, task.Flags)
	}
	roles := func() []model.Role {
		var out []model.Role
		for _, c := range e.agent.Calls() {
			out = append(out, c.Role)
		}
		return out
	}
	if got := roles(); slices.Contains(got, model.RoleR2) {
		t.Fatalf("agent roles = %v, want no r2 yet", got)
	}

	// The next tick runs r2 as well and the task reaches merge.
	if err := e.engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if task, err = e.store.GetTask(t.Context(), task.Key); err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateMerge || len(task.Flags) != 0 {
		t.Errorf("task = %s %v, want merge", task.State, task.Flags)
	}
	if got := roles(); !slices.Contains(got, model.RoleR2) {
		t.Errorf("agent roles = %v, want an r2 run", got)
	}
	c := storedChange(t, e, task.Key)
	for _, r := range storedReviews(t, e, task, c) {
		verifyReview(t, e, task, c, r)
		if r.Role == model.RoleR2 && (r.Round != 1 || r.Verdict != model.ReviewVerdictApprove) {
			t.Errorf("stored r2 review = %+v, want round 1 APPROVE", r)
		}
	}
}

func TestTick_ReviewHardStopNeedsOwner(t *testing.T) {
	e := newEngEnv(t, ports.WriteModeLive, nil)
	e.addInbox(t)
	e.scriptHappyPath()
	e.agent.Script(model.RoleR1, fakes.AgentStep{Result: []byte(reviewerHardStop)})
	task := e.tickUntil(t, 12, model.TaskStateReview)
	for range 2 {
		if err := e.engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	task, err := e.store.GetTask(t.Context(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != model.TaskStateReview || !slices.Equal(task.Flags, []model.TaskFlag{model.TaskFlagNeedsOwner}) || !task.HitStop {
		t.Errorf("task = %s %v hitStop %v, want review with needs-owner and hit_stop", task.State, task.Flags, task.HitStop)
	}
	c := storedChange(t, e, task.Key)
	rs := storedReviews(t, e, task, c)
	if len(rs) != 1 || rs[0].BlockingCount != 1 {
		t.Errorf("stored reviews = %+v, want one with 1 blocking finding", rs)
	}
}
