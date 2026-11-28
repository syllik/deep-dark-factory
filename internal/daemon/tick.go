package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
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
		// Correction batches are Task 12b.
		return nil
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

// execute runs the Executor on a task in running and publishes its patch
// in the same step, so the patch is never stored.
func (e *Engine) execute(ctx context.Context, it tickTask) (err error) {
	t := it.task
	g := e.deps.Git
	clone := e.settings.CloneDir
	if err := g.Fetch(ctx, clone); err != nil {
		return err
	}
	base, err := e.integrationTip(ctx)
	if err != nil {
		return err
	}
	copyID := e.deps.IDs.NewID()
	dir := filepath.Join(e.settings.ScratchDir, "run-"+copyID)
	if err := g.NewRunCopy(ctx, clone, base, dir); err != nil {
		return err
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove run copy: %w", rmErr))
		}
	}()
	br, err := brief(it)
	if err != nil {
		return err
	}
	status := statusOf(t)
	res, _, owner, err := e.runAgent(ctx, t, status, runSpec{
		Role: model.RoleExecutor, Purpose: model.RunPurposeInitial, Brief: br, Workspace: os.DirFS(dir),
	})
	if err != nil {
		return err
	}
	run := &runInfo{role: model.RoleExecutor, usage: res.Usage}
	if owner != nil {
		_, err := e.apply(ctx, t, *owner, run)
		return err
	}
	var r executorResult
	if err := json.Unmarshal(res.Result, &r); err != nil {
		return fmt.Errorf("decode executor result: %w", err)
	}
	o, err := lifecycle.RouteExecutor(status, r.Verdict)
	if err != nil {
		return fmt.Errorf("route executor verdict: %w", err)
	}
	if t, err = e.apply(ctx, t, o, run); err != nil || o.Flag != "" {
		return err
	}
	if err := e.publish(ctx, t, copyID, base, res.Patch, r.Summary); err != nil {
		// The patch is never stored, so a task left in publishing cannot
		// resume (#146). The flag is written even when ctx is canceled.
		_, applyErr := e.apply(context.WithoutCancel(ctx), t, *ownerOutcome(statusOf(t), "publish failed"), nil)
		return errors.Join(err, applyErr)
	}
	return nil
}

// publish prepares the revision of a task in publishing and, when it is
// Ready, publishes it through the intents and moves the task to ci.
func (e *Engine) publish(ctx context.Context, t model.Task, runID, base string, patch []byte, notes string) error {
	forge := e.deps.Forge
	s := e.settings
	key := string(t.Key)
	if len(key) < 12 {
		return fmt.Errorf("publish: task key %q too short", key)
	}
	branch := changeBranchPrefix + key[:12]
	out, err := e.deps.Publisher.Prepare(ctx, publisher.Input{
		Key: t.Key, RunID: runID, TaskRef: t.SourceID, Title: t.Title,
		Branch: branch, BaseBranch: s.Project.IntegrationBranch, BranchHead: "",
		Base: base, ChangedFrom: base, Patch: patch, Profile: s.Profile, PlannerRisk: t.PlannerRisk,
		Template: s.ChangeTemplate, Verify: s.Project.Verify, Registries: s.Project.Registries,
		AgentNotes: notes, Identity: s.Identity,
	})
	if err != nil {
		return err
	}
	status := statusOf(t)
	var o lifecycle.Outcome
	switch r := out.(type) {
	case publisher.Ready:
		t.Risk = r.Class
		info, err := publisher.Publish(ctx, s.Mode, e.intents.Pusher(t.Key, e.deps.Pusher), e.intents.ChangeWriter(t.Key, e.intents.Forge(forge), false), r)
		if err != nil {
			return err
		}
		e.deps.Logger.Info("revision published", "key", key, "changeId", info.ID, "head", r.Commit, "mode", string(s.Mode))
		if s.Mode == ports.WriteModeLive {
			if err := e.recordChange(ctx, t, info.ID, branch, base, r.Commit); err != nil {
				return err
			}
		}
		if o, err = lifecycle.Advance(status); err != nil {
			return err
		}
	case publisher.NeedsOwner:
		o = lifecycle.Outcome{State: t.State, Flag: model.TaskFlagNeedsOwner, SetHitStop: r.HitStop, Reason: string(r.Stop)}
	case publisher.Failed:
		o = lifecycle.Outcome{State: t.State, Flag: model.TaskFlagFailed, Reason: string(r.Reason)}
	case publisher.GateFailed:
		// Correction batches are Task 12b.
		o = lifecycle.Outcome{State: t.State, Flag: model.TaskFlagNeedsOwner, Reason: fmt.Sprintf("local gate failed at verify command %d", r.Index)}
	default:
		return fmt.Errorf("publish: unknown publisher outcome %T", out)
	}
	_, err = e.apply(ctx, t, o, nil)
	return err
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

// checkCI moves a task in ci to review once the checks of its change head
// pass; otherwise it stays.
func (e *Engine) checkCI(ctx context.Context, t model.Task) error {
	forge := e.deps.Forge
	change, found, err := forge.FindChangeByTaskKey(ctx, t.Key)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if e.deps.Checks != nil && e.settings.Mode == ports.WriteModeLive {
		if err := e.deps.Checks.RunChecks(ctx); err != nil {
			return err
		}
	}
	checks, err := forge.ReadChecks(ctx, change.HeadSHA)
	if err != nil {
		return err
	}
	if !checksPass(e.settings.Profile.RequiredChecks, change.HeadSHA, checks) {
		return nil
	}
	o, err := lifecycle.Advance(statusOf(t))
	if err != nil {
		return err
	}
	_, err = e.apply(ctx, t, o, nil)
	return err
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
