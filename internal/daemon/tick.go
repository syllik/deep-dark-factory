package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
	"github.com/syllik/deep-dark-factory/internal/core/scheduler"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// Event types the engine journals with a task change.
const (
	eventIntake     = "intake"
	eventTransition = "transition"
	eventFlag       = "flag"
	eventRisk       = "risk"
)

// changeBranchPrefix names the change branch until change.branch pattern
// expansion exists: ddf/task-<first 12 hex of the key>.
const changeBranchPrefix = "ddf/task-"

// tickTask is one stored task of this tick with the forge candidate it
// came from.
type tickTask struct {
	task model.Task
	cand ports.CandidateTask
}

// runInfo is what an event records about the run behind a change.
type runInfo struct {
	role  model.Role
	usage ports.AgentUsage
}

// plannerResult covers every field of ddf.planner.result/v1.
type plannerResult struct {
	Schema       string               `json:"schema"`
	Verdict      model.PlannerVerdict `json:"verdict"`
	Spec         string               `json:"spec"`
	Acceptance   []string             `json:"acceptance"`
	RiskProposal string               `json:"riskProposal"`
	Zones        []string             `json:"zones"`
	DependsOn    []string             `json:"dependsOn"`
	Children     []struct {
		Title string `json:"title"`
		Spec  string `json:"spec"`
	} `json:"children"`
	Question      string `json:"question"`
	Reason        string `json:"reason"`
	AgentDefaults []struct {
		Question string `json:"question"`
		Choice   string `json:"choice"`
	} `json:"agentDefaults"`
}

// executorResult is the part of ddf.executor.result/v1 the engine reads.
type executorResult struct {
	Verdict model.ExecutorVerdict `json:"verdict"`
	Summary string                `json:"summary"`
	WontFix []wontFix             `json:"wontFix"`
}

// wontFix is one finding the Executor declined, with its reason.
type wontFix struct {
	FindingID string `json:"findingId"`
	Reason    string `json:"reason"`
}

// Tick runs one pass: ingest, intake of new candidates, then at most one
// step for every listed task without flags and without a pending intent.
// Any error ends the tick (fail closed); the next tick starts from the
// stored state. With a Lock set, Tick refuses to run until Recover has
// succeeded on this engine.
func (e *Engine) Tick(ctx context.Context) error {
	if e.deps.Lock != nil && !e.recovered.Load() {
		return errors.New("tick: recovery has not succeeded on this engine")
	}
	forge := e.deps.Forge
	if e.deps.Ingest != nil {
		if err := e.deps.Ingest(ctx, e.settings.Mode); err != nil {
			return fmt.Errorf("tick: ingest: %w", err)
		}
	}
	cands, err := forge.ListCandidateTasks(ctx)
	if err != nil {
		return fmt.Errorf("tick: list candidate tasks: %w", err)
	}
	var items []tickTask
	for _, c := range cands {
		key, err := model.NewTaskKey(c.Source)
		if err != nil {
			return fmt.Errorf("tick: %w", err)
		}
		t, err := e.deps.Store.GetTask(ctx, key)
		switch {
		case err == nil:
			items = append(items, tickTask{task: t, cand: c})
		case errors.Is(err, store.ErrNotFound):
			// Intake is the new task's step for this tick.
			if err := e.intake(ctx, key, c); err != nil {
				return fmt.Errorf("tick: %w", err)
			}
		default:
			return fmt.Errorf("tick: %w", err)
		}
	}
	dec, err := e.schedule(items)
	if err != nil {
		return fmt.Errorf("tick: %w", err)
	}
	pending, err := e.deps.Store.PendingIntents(ctx)
	if err != nil {
		return fmt.Errorf("tick: %w", err)
	}
	held := map[model.TaskKey]string{}
	for _, in := range pending {
		if _, ok := held[in.Key]; !ok {
			held[in.Key] = in.ID
		}
	}
	for _, it := range items {
		if len(it.task.Flags) > 0 {
			continue
		}
		if id, ok := held[it.task.Key]; ok {
			// A pending intent is recovery's to reconcile (spec §9).
			e.deps.Logger.Info("task step held by a pending intent", "key", string(it.task.Key), "intentId", id)
			continue
		}
		if err := e.step(ctx, it, dec); err != nil {
			return fmt.Errorf("tick: task %s: %w", it.task.Key, err)
		}
	}
	return nil
}

// intake stores candidate c as a new task in inbox when its intake actor is
// allowed; any other candidate is logged and skipped.
func (e *Engine) intake(ctx context.Context, key model.TaskKey, c ports.CandidateTask) error {
	forge := e.deps.Forge
	actor, err := forge.ReadIntakeActor(ctx, c.Source)
	if err != nil {
		return fmt.Errorf("intake %s: %w", key, err)
	}
	if !slices.Contains(e.settings.AllowedActors, actor.ID) {
		e.deps.Logger.Info("candidate skipped: intake actor not allowed",
			"key", string(key), "sourceId", c.Source.SourceID, "actor", actor.ID)
		return nil
	}
	now := e.deps.Clock.Now()
	t := model.Task{
		Key: key, Forge: c.Source.Forge, Repository: c.Source.Repository,
		SourceKind: c.Source.SourceKind, SourceID: c.Source.SourceID,
		Kind: model.TaskKindTask, State: model.TaskStateInbox, Priority: model.PriorityNormal,
		Title: c.Title, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.deps.Store.CreateTask(ctx, t, model.Event{Key: key, Type: eventIntake, At: now}); err != nil {
		return fmt.Errorf("intake %s: %w", key, err)
	}
	e.deps.Logger.Info("task taken in", "key", string(key), "sourceId", c.Source.SourceID)
	return nil
}

// schedule decides admission over every task of the tick.
func (e *Engine) schedule(items []tickTask) (scheduler.Decision, error) {
	in := scheduler.Input{}
	if n := e.settings.Routing.Limits.MaxConcurrentContainers; n > 0 {
		in.MaxConcurrentContainers = &n
	}
	for _, it := range items {
		t := it.task
		in.Tasks = append(in.Tasks, scheduler.Task{
			Key: t.Key, State: t.State, Flags: t.Flags, Zones: t.Zones, Priority: t.Priority, Kind: t.Kind,
			CreatedAt: t.CreatedAt, WantsRun: t.State == model.TaskStateReady, Containers: 1,
		})
	}
	d, err := scheduler.Decide(in)
	if err != nil {
		return scheduler.Decision{}, fmt.Errorf("schedule: %w", err)
	}
	return d, nil
}

func statusOf(t model.Task) lifecycle.Status {
	return lifecycle.Status{State: t.State, Flags: t.Flags, HitStop: t.HitStop}
}

// step advances one task without flags by at most one step.
func (e *Engine) step(ctx context.Context, it tickTask, dec scheduler.Decision) error {
	t := it.task
	switch t.State {
	case model.TaskStateInbox:
		o, err := lifecycle.Advance(statusOf(t))
		if err != nil {
			return err
		}
		_, err = e.apply(ctx, t, o, nil)
		return err
	case model.TaskStatePlanning:
		return e.plan(ctx, it)
	case model.TaskStateReady, model.TaskStateWaiting:
		return e.admit(ctx, t, dec)
	case model.TaskStateRunning:
		return e.execute(ctx, it)
	case model.TaskStateCI:
		return e.checkCI(ctx, t)
	case model.TaskStatePublishing:
		// A task left in publishing lost its in-memory patch, which is
		// never stored; only the owner can decide (#146).
		_, err := e.apply(ctx, t, *ownerOutcome(statusOf(t), "task in publishing without its patch"), nil)
		return err
	case model.TaskStateReview:
		return e.review(ctx, it)
	case model.TaskStateMerge:
		return e.merge(ctx, it)
	case model.TaskStateFixing:
		return e.fix(ctx, it)
	case model.TaskStateDone:
		return e.closeDone(ctx, t)
	}
	return fmt.Errorf("step: unknown state %q", t.State)
}

// apply writes outcome o to t with one UpdateTask and its event; an outcome
// that changes nothing writes nothing. It returns the stored task.
func (e *Engine) apply(ctx context.Context, t model.Task, o lifecycle.Outcome, run *runInfo) (model.Task, error) {
	next := t
	evType := ""
	if o.State != "" && o.State != t.State {
		next.State = o.State
		evType = eventTransition
	}
	if o.Flag != "" {
		st, err := lifecycle.SetFlag(statusOf(t), o.Flag)
		if err != nil {
			return model.Task{}, fmt.Errorf("apply: %w", err)
		}
		next.Flags = st.Flags
		if evType == "" {
			evType = eventFlag
		}
		e.deps.Logger.Warn("task flagged", "key", string(t.Key), "flag", string(o.Flag), "reason", o.Reason)
	}
	if o.SetHitStop {
		next.HitStop = true // sticky: only ever set
		if evType == "" {
			evType = eventFlag
		}
	}
	if evType == "" {
		return t, nil
	}
	now := e.deps.Clock.Now()
	next.UpdatedAt = now
	ev := model.Event{Key: t.Key, Type: evType, At: now}
	if run != nil {
		ev.Role = run.role
		ev.Model = run.usage.ModelID
		ev.Usage = &model.Usage{InputTokens: run.usage.InputTokens, OutputTokens: run.usage.OutputTokens}
	}
	if err := e.deps.Store.UpdateTask(ctx, next, ev); err != nil {
		return model.Task{}, fmt.Errorf("apply: %w", err)
	}
	return next, nil
}

// applyRisk stores risk as the task's risk class with one UpdateTask and a
// risk event. It returns the stored task.
func (e *Engine) applyRisk(ctx context.Context, t model.Task, risk model.RiskClass, run *runInfo) (model.Task, error) {
	now := e.deps.Clock.Now()
	next := t
	next.Risk = risk
	next.UpdatedAt = now
	ev := model.Event{Key: t.Key, Type: eventRisk, At: now}
	if run != nil {
		ev.Role = run.role
		ev.Model = run.usage.ModelID
		ev.Usage = &model.Usage{InputTokens: run.usage.InputTokens, OutputTokens: run.usage.OutputTokens}
	}
	if err := e.deps.Store.UpdateTask(ctx, next, ev); err != nil {
		return model.Task{}, fmt.Errorf("apply risk: %w", err)
	}
	return next, nil
}

// brief is the run brief of a task: its title, body, spec and acceptance.
func brief(it tickTask) ([]byte, error) {
	b, err := json.Marshal(struct {
		Title      string   `json:"title"`
		Body       string   `json:"body"`
		Spec       string   `json:"spec"`
		Acceptance []string `json:"acceptance"`
	}{it.task.Title, it.cand.Body, it.task.Spec, it.task.Acceptance})
	if err != nil {
		return nil, fmt.Errorf("encode brief: %w", err)
	}
	return b, nil
}

// plan runs the Planner on a task in planning and routes its verdict.
func (e *Engine) plan(ctx context.Context, it tickTask) error {
	t := it.task
	status := statusOf(t)
	br, err := brief(it)
	if err != nil {
		return err
	}
	res, _, owner, err := e.runAgent(ctx, t, status, runSpec{Role: model.RolePlanner, Purpose: model.RunPurposePlanning, Brief: br})
	if err != nil {
		return err
	}
	run := &runInfo{role: model.RolePlanner, usage: res.Usage}
	if owner != nil {
		_, err := e.apply(ctx, t, *owner, run)
		return err
	}
	var r plannerResult
	if err := json.Unmarshal(res.Result, &r); err != nil {
		return fmt.Errorf("decode planner result: %w", err)
	}
	reason := r.Reason
	if r.Verdict == model.PlannerVerdictAsk {
		reason = r.Question
	}
	o, err := lifecycle.RoutePlanner(status, r.Verdict, len(r.Children), e.settings.Project.Planning.MaxAutoChildren, reason)
	if err != nil {
		return fmt.Errorf("route planner verdict: %w", err)
	}
	if o.Action == lifecycle.ActionCreateChildren {
		// Child creation is a later task; the owner decides meanwhile.
		o.Flag, o.Reason = model.TaskFlagNeedsOwner, fmt.Sprintf("decompose into %d children", len(r.Children))
	}
	t.Spec, t.Acceptance, t.Zones, t.DependsOn = r.Spec, r.Acceptance, r.Zones, r.DependsOn
	if r.RiskProposal != "" {
		risk, err := model.ParseRiskClass(r.RiskProposal)
		if err != nil {
			return fmt.Errorf("planner risk proposal: %w", err)
		}
		t.PlannerRisk = risk
	}
	now := e.deps.Clock.Now()
	t.AgentDefaults = nil
	for _, d := range r.AgentDefaults {
		t.AgentDefaults = append(t.AgentDefaults, model.Decision{Key: t.Key, Question: d.Question, Choice: d.Choice, CreatedAt: now})
	}
	_, err = e.apply(ctx, t, o, run)
	return err
}

// admit applies the scheduling decision to a task in ready or waiting.
func (e *Engine) admit(ctx context.Context, t model.Task, dec scheduler.Decision) error {
	has := func(keys []model.TaskKey) bool { return slices.Contains(keys, t.Key) }
	var admitted, toWaiting, toReady []model.TaskKey
	for _, a := range dec.Admit {
		admitted = append(admitted, a.Key)
	}
	for _, tr := range dec.ToWaiting {
		toWaiting = append(toWaiting, tr.Key)
	}
	for _, tr := range dec.ToReady {
		toReady = append(toReady, tr.Key)
	}
	var o lifecycle.Outcome
	switch {
	case t.State == model.TaskStateReady && has(admitted):
		var err error
		if o, err = lifecycle.Advance(statusOf(t)); err != nil {
			return err
		}
	case t.State == model.TaskStateReady && has(toWaiting):
		o = lifecycle.Outcome{State: model.TaskStateWaiting}
	case t.State == model.TaskStateWaiting && has(toReady):
		o = lifecycle.Outcome{State: model.TaskStateReady}
	default:
		return nil
	}
	_, err := e.apply(ctx, t, o, nil)
	return err
}

// integrationTip returns the commit at the integration branch tip of the
// clean clone.
func (e *Engine) integrationTip(ctx context.Context) (string, error) {
	branch := e.settings.Project.IntegrationBranch
	out, err := e.deps.Git.Run(ctx, e.settings.CloneDir, nil, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("read integration branch tip: %w", err)
	}
	return string(bytes.TrimSpace(out)), nil
}

// execute runs the Executor on a task in running as the initial batch on
// the integration tip and publishes its patch in the same step, so the patch
// is never stored.
func (e *Engine) execute(ctx context.Context, it tickTask) error {
	if err := e.deps.Git.Fetch(ctx, e.settings.CloneDir); err != nil {
		return err
	}
	base, err := e.integrationTip(ctx)
	if err != nil {
		return err
	}
	br, err := brief(it)
	if err != nil {
		return err
	}
	return e.runBatch(ctx, it, it.task, batch{purpose: model.RunPurposeInitial, base: base, brief: br})
}

// revision is one Executor patch to publish: kind is the batch purpose
// (initial, correction or ci-fix) and change the stored change it revises,
// nil for the first revision. base is the commit the patch was exported
// against: the integration tip for the first revision, the change head
// later.
type revision struct {
	runID   string
	kind    model.RunPurpose
	change  *model.Change
	base    string
	patch   []byte
	summary string
	wontFix []wontFix
}

// agentNotes is the summary plus, when wontFix is non-empty, a daemon-written
// 'Won't fix:' list with one line per entry. The Publisher sanitizes and
// redacts it with the rest of the change body.
func agentNotes(summary string, wontFix []wontFix) string {
	if len(wontFix) == 0 {
		return summary
	}
	line := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var b strings.Builder
	b.WriteString(summary)
	b.WriteString("\n\nWon't fix:\n")
	for _, w := range wontFix {
		fmt.Fprintf(&b, "- %s: %s\n", line(w.FindingID), line(w.Reason))
	}
	return b.String()
}

// publish prepares the revision rv of a task in publishing and, when it is
// Ready, publishes it through the intents (open-change for the first
// revision, update-change later) and moves the task to ci. On a local gate
// failure the task moves to fixing and the stored task and the failure are
// returned, so the caller runs a correction batch.
func (e *Engine) publish(ctx context.Context, t model.Task, rv revision) (model.Task, *publisher.GateFailed, error) {
	forge := e.deps.Forge
	s := e.settings
	key := string(t.Key)
	if len(key) < 12 {
		return t, nil, fmt.Errorf("publish: task key %q too short", key)
	}
	branch := changeBranchPrefix + key[:12]
	branchHead, changedFrom, tip := "", rv.base, rv.base
	if c := rv.change; c != nil {
		if rv.base != c.HeadSHA {
			return t, nil, fmt.Errorf("publish: base %s is not the change head %s", rv.base, c.HeadSHA)
		}
		var err error
		if tip, err = e.integrationTip(ctx); err != nil {
			return t, nil, err
		}
		if changedFrom, err = e.mergeBase(ctx, tip, c.HeadSHA); err != nil {
			return t, nil, fmt.Errorf("publish: %w", err)
		}
		branch, branchHead = c.Branch, c.HeadSHA
	}
	out, err := e.deps.Publisher.Prepare(ctx, publisher.Input{
		Key: t.Key, RunID: rv.runID, TaskRef: t.SourceID, Title: t.Title,
		Branch: branch, BaseBranch: s.Project.IntegrationBranch, BranchHead: branchHead,
		Base: rv.base, ChangedFrom: changedFrom, Patch: rv.patch, Profile: s.Profile, PlannerRisk: t.PlannerRisk,
		Template: s.ChangeTemplate, Verify: s.Project.Verify, Registries: s.Project.Registries,
		AgentNotes: agentNotes(rv.summary, rv.wontFix), Identity: s.Identity,
	})
	if err != nil {
		return t, nil, err
	}
	status := statusOf(t)
	var o lifecycle.Outcome
	switch r := out.(type) {
	case publisher.Ready:
		// The risk class is never lowered by a later revision.
		risk := r.Class
		if t.Risk != "" {
			if risk, err = policy.Raise(t.Risk, r.Class); err != nil {
				return t, nil, fmt.Errorf("publish: raise risk: %w", err)
			}
		}
		t.Risk = risk
		cw := e.intents.ChangeWriter(t.Key, e.intents.Forge(forge), rv.change != nil)
		info, err := publisher.Publish(ctx, s.Mode, e.intents.Pusher(t.Key, e.deps.Pusher), cw, r)
		if err != nil {
			return t, nil, err
		}
		e.deps.Logger.Info("revision published", "key", key, "changeId", info.ID, "head", r.Commit,
			"kind", string(rv.kind), "mode", string(s.Mode))
		if s.Mode == ports.WriteModeLive {
			if rv.change == nil {
				err = e.recordChange(ctx, t, info.ID, branch, rv.base, r.Commit)
			} else {
				err = e.updateChange(ctx, *rv.change, rv.kind, tip, r.Commit)
			}
			if err != nil {
				return t, nil, err
			}
		}
		if o, err = lifecycle.Advance(status); err != nil {
			return t, nil, err
		}
	case publisher.NeedsOwner:
		o = lifecycle.Outcome{State: t.State, Flag: model.TaskFlagNeedsOwner, SetHitStop: r.HitStop, Reason: string(r.Stop)}
	case publisher.Failed:
		o = lifecycle.Outcome{State: t.State, Flag: model.TaskFlagFailed, Reason: string(r.Reason)}
	case publisher.GateFailed:
		e.deps.Logger.Info("local gate failed; correction batch next", "key", key, "index", r.Index,
			"exitCode", r.Outcome.ExitCode, "timedOut", r.Outcome.TimedOut)
		next, err := e.apply(ctx, t, lifecycle.Outcome{State: model.TaskStateFixing,
			Reason: fmt.Sprintf("local gate failed at verify command %d", r.Index)}, nil)
		if err != nil {
			return t, nil, err
		}
		return next, &r, nil
	default:
		return t, nil, fmt.Errorf("publish: unknown publisher outcome %T", out)
	}
	next, err := e.apply(ctx, t, o, nil)
	return next, nil, err
}

// updateChange stores head as the new head of change c: revision + 1 for a
// correction batch and unchanged for a CI-fix, the patch-id of head against
// tip and its CI wait starting now.
func (e *Engine) updateChange(ctx context.Context, c model.Change, kind model.RunPurpose, tip, head string) error {
	switch kind {
	case model.RunPurposeCorrection:
		c.Revision++
	case model.RunPurposeCIFix:
	case model.RunPurposePlanning, model.RunPurposeInitial, model.RunPurposeSync, model.RunPurposeReview:
		// A sync batch revision is part 3 (#162).
		return fmt.Errorf("update change: revision kind %q on an existing change", kind)
	default:
		return fmt.Errorf("update change: unknown revision kind %q", kind)
	}
	pid, err := e.deps.Git.PatchID(ctx, e.settings.CloneDir, tip, head)
	if err != nil {
		return fmt.Errorf("update change: %w", err)
	}
	now := e.deps.Clock.Now()
	c.HeadSHA, c.PatchID, c.CISince, c.UpdatedAt = head, pid, &now, now
	if err := e.deps.Store.UpdateChange(ctx, c); err != nil {
		return fmt.Errorf("update change: %w", err)
	}
	return nil
}

// recordChange stores the published change of t as revision 1, open, with
// the patch-id of head against base and its CI wait starting now.
func (e *Engine) recordChange(ctx context.Context, t model.Task, id, branch, base, head string) error {
	pid, err := e.deps.Git.PatchID(ctx, e.settings.CloneDir, base, head)
	if err != nil {
		return fmt.Errorf("record change: %w", err)
	}
	now := e.deps.Clock.Now()
	c := model.Change{
		Key: t.Key, ID: id, Kind: model.ChangeKindChange, Branch: branch,
		BaseBranch: e.settings.Project.IntegrationBranch, HeadSHA: head, Revision: 1, Open: true,
		PatchID: pid, CISince: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.deps.Store.CreateChange(ctx, c); err != nil {
		return fmt.Errorf("record change: %w", err)
	}
	return nil
}

// checkCI routes a task in ci by the checks of its stored change head:
// passing checks move it to review, a failed check moves it to fixing (the
// CI-fix batch runs on the next tick), otherwise it stays until the CI wait
// bound. Without a stored change (dry-run) it stays. There is no automatic
// check rerun: RerunChecks has no intent operation.
func (e *Engine) checkCI(ctx context.Context, t model.Task) error {
	forge := e.deps.Forge
	change, found, err := e.taskChange(ctx, t.Key)
	if err != nil || !found {
		return err
	}
	info, found, err := forge.FindChangeByTaskKey(ctx, t.Key)
	if err != nil {
		return err
	}
	if !found {
		e.deps.Logger.Info("task in ci: stored change not found on the forge", "key", string(t.Key), "changeId", change.ID)
		return nil
	}
	status := statusOf(t)
	if !info.Open {
		_, err := e.apply(ctx, t, lifecycle.Outcome{State: t.State, Flag: model.TaskFlagHold, Reason: "change closed by a human"}, nil)
		return err
	}
	head := change.HeadSHA
	if info.HeadSHA != head {
		_, err := e.apply(ctx, t, *ownerOutcome(status, "change head moved outside DDF"), nil)
		return err
	}
	if e.deps.Checks != nil && e.settings.Mode == ports.WriteModeLive {
		if err := e.deps.Checks.RunChecks(ctx); err != nil {
			return err
		}
	}
	checks, err := forge.ReadChecks(ctx, head)
	if err != nil {
		return err
	}
	required := e.settings.Profile.RequiredChecks
	var o lifecycle.Outcome
	switch {
	case checksPass(required, head, checks):
		if o, err = lifecycle.Advance(status); err != nil {
			return err
		}
	case len(failedChecks(required, head, checks)) > 0:
		o = lifecycle.Outcome{State: model.TaskStateFixing, Reason: "checks failed"}
	case change.CISince == nil:
		o = *ownerOutcome(status, "change has no ci wait start")
	default:
		if o, err = lifecycle.CheckCIWait(status, change, e.settings.Limits, e.deps.Clock.Now()); err != nil {
			return fmt.Errorf("check ci wait: %w", err)
		}
	}
	_, err = e.apply(ctx, t, o, nil)
	return err
}

// failedChecks returns the names of the required checks (every check when
// none is required) that completed and failed on head.
func failedChecks(required []string, head string, checks []ports.Check) []string {
	var out []string
	for _, c := range checks {
		if c.HeadSHA != head || !c.Completed || c.Passed {
			continue
		}
		if required != nil && !slices.Contains(required, c.Name) {
			continue
		}
		out = append(out, c.Name)
	}
	return out
}

// checksPass reports whether every required check passed on head; with no
// required list, at least one check must exist and all must pass. A check
// for another head never counts (fail closed).
func checksPass(required []string, head string, checks []ports.Check) bool {
	ok := func(c ports.Check) bool { return c.HeadSHA == head && c.Completed && c.Passed }
	if required == nil {
		return len(checks) > 0 && !slices.ContainsFunc(checks, func(c ports.Check) bool { return !ok(c) })
	}
	for _, name := range required {
		if !slices.ContainsFunc(checks, func(c ports.Check) bool { return c.Name == name && ok(c) }) {
			return false
		}
	}
	return true
}
