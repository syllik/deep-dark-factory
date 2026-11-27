package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// reviewerResult covers every field of ddf.reviewer.result/v1.
type reviewerResult struct {
	Schema    string              `json:"schema"`
	Role      model.Role          `json:"role"`
	Verdict   model.ReviewVerdict `json:"verdict"`
	RaiseRisk model.RiskClass     `json:"raiseRisk"`
	Findings  []struct {
		ID            string                `json:"id"`
		Severity      model.Severity        `json:"severity"`
		Category      model.FindingCategory `json:"category"`
		SecurityLevel model.SecurityLevel   `json:"securityLevel"`
		Evidence      string                `json:"evidence"`
		Location      string                `json:"location"`
		FixHint       string                `json:"fixHint"`
		PriorID       string                `json:"priorId"`
	} `json:"findings"`
	// HardStopChecklist is kept verbatim in the stored review.
	HardStopChecklist json.RawMessage `json:"hardStopChecklist"`
}

// reviewCheck is one check of the reviewed head in the reviewer brief.
type reviewCheck struct {
	Name      string `json:"name"`
	Completed bool   `json:"completed"`
	Passed    bool   `json:"passed"`
}

// taskChange returns the last stored change of kind change of key.
func (e *Engine) taskChange(ctx context.Context, key model.TaskKey) (model.Change, bool, error) {
	changes, err := e.deps.Store.ListChanges(ctx, key)
	if err != nil {
		return model.Change{}, false, fmt.Errorf("task change: %w", err)
	}
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i].Kind == model.ChangeKindChange {
			return changes[i], true, nil
		}
	}
	return model.Change{}, false, nil
}

// mergeBase returns the single merge base of a and b in the clean clone.
func (e *Engine) mergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := e.deps.Git.Run(ctx, e.settings.CloneDir, nil, "merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("merge base of %s and %s: %w", a, b, err)
	}
	mb := strings.TrimSpace(string(out))
	if mb == "" || strings.ContainsAny(mb, " \n") {
		return "", fmt.Errorf("merge base of %s and %s: unexpected output %q", a, b, out)
	}
	return mb, nil
}

// review runs the required reviewers of a task in review on its change head,
// one after another, stores each signed review, projects it to the forge and
// routes the results.
func (e *Engine) review(ctx context.Context, it tickTask) error {
	t := it.task
	forge := e.deps.Forge
	log := e.deps.Logger.With("key", string(t.Key))
	change, found, err := e.taskChange(ctx, t.Key)
	if err != nil {
		return err
	}
	if !found {
		log.Info("task in review has no stored change")
		return nil
	}
	info, found, err := forge.FindChangeByTaskKey(ctx, t.Key)
	if err != nil {
		return err
	}
	if !found {
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
	required, err := policy.Reviewers(t.Risk)
	if err != nil {
		return fmt.Errorf("review: %w", err)
	}
	checks, err := forge.ReadChecks(ctx, head)
	if err != nil {
		return err
	}
	base, err := e.integrationTip(ctx)
	if err != nil {
		return err
	}
	mb, err := e.mergeBase(ctx, base, head)
	if err != nil {
		return fmt.Errorf("review: %w", err)
	}
	diff, err := e.deps.Git.Run(ctx, e.settings.CloneDir, nil, "diff", "--no-ext-diff", "--no-textconv", "--no-color", mb, head)
	if err != nil {
		return fmt.Errorf("review: diff: %w", err)
	}
	br, err := reviewBrief(it, head, string(diff), checks, required)
	if err != nil {
		return err
	}

	w := e.intents.Forge(forge)
	var results []lifecycle.ReviewResult
	var last *runInfo
	for _, rv := range required {
		res, done, err := e.runReviewer(ctx, t, status, change, head, rv, br, w)
		if err != nil || done {
			return err
		}
		results = append(results, res.result)
		last = res.run
	}

	o, err := lifecycle.RouteReview(status, head, required, results)
	if errors.Is(err, lifecycle.ErrInvalidInput) {
		_, applyErr := e.apply(ctx, t, *ownerOutcome(status, "review results: "+err.Error()), last)
		return applyErr
	}
	if err != nil {
		return fmt.Errorf("route review: %w", err)
	}
	switch o.Action {
	case lifecycle.ActionRerunReviewer:
		log.Info("reviewers to rerun", "roles", fmt.Sprint(o.Roles), "reason", o.Reason)
		return nil
	case lifecycle.ActionAddReviewer:
		risk, err := policy.Raise(t.Risk, o.Risk)
		if err != nil {
			return fmt.Errorf("review: raise risk: %w", err)
		}
		log.Info("reviewer added", "roles", fmt.Sprint(o.Roles), "risk", string(risk))
		_, err = e.applyRisk(ctx, t, risk, last)
		return err
	case lifecycle.ActionNone:
		_, err := e.apply(ctx, t, o, last)
		return err
	case lifecycle.ActionBackoff, lifecycle.ActionNotify, lifecycle.ActionSync, lifecycle.ActionCreateChildren,
		lifecycle.ActionMerge, lifecycle.ActionLeave, lifecycle.ActionRetry, lifecycle.ActionSyncBatch, lifecycle.ActionRerunCI,
		lifecycle.ActionOpenRevert, lifecycle.ActionPause, lifecycle.ActionDiscardSyncRun:
		return fmt.Errorf("route review: unexpected action %q", o.Action)
	}
	return fmt.Errorf("route review: unknown action %q", o.Action)
}

// reviewed is one reviewer's routed result and the run behind it.
type reviewed struct {
	result lifecycle.ReviewResult
	run    *runInfo
}

// runReviewer runs reviewer rv on head in its own run copy, stores the
// signed review and posts its projection. done reports that the step ended
// here because an outcome was applied to t.
func (e *Engine) runReviewer(ctx context.Context, t model.Task, status lifecycle.Status, change model.Change, head string,
	rv policy.Reviewer, br []byte, w *ForgeWrites) (out reviewed, done bool, err error) {
	g := e.deps.Git
	dir := filepath.Join(e.settings.ScratchDir, "run-"+e.deps.IDs.NewID())
	if err := g.NewRunCopy(ctx, e.settings.CloneDir, head, dir); err != nil {
		return reviewed{}, false, err
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove run copy: %w", rmErr))
		}
	}()
	rev := change.Revision
	res, run, owner, err := e.runAgent(ctx, t, status, runSpec{
		Role: rv.Role, Purpose: model.RunPurposeReview, Brief: br, Workspace: os.DirFS(dir),
		Revision: &rev, HeadSHA: head, Deep: rv.Deep,
	})
	if err != nil {
		return reviewed{}, false, err
	}
	ri := &runInfo{role: rv.Role, usage: res.Usage}
	stop := func(o lifecycle.Outcome) (reviewed, bool, error) {
		_, err := e.apply(ctx, t, o, ri)
		return reviewed{}, true, err
	}
	if owner != nil {
		return stop(*owner)
	}
	var r reviewerResult
	if err := json.Unmarshal(res.Result, &r); err != nil {
		return reviewed{}, false, fmt.Errorf("decode reviewer result: %w", err)
	}
	if r.Role != rv.Role {
		return stop(*ownerOutcome(status, fmt.Sprintf("reviewer result role %q, want %q", r.Role, rv.Role)))
	}
	reviewID := e.deps.IDs.NewID()
	findings := make([]model.Finding, 0, len(r.Findings))
	blocking := 0
	for _, rf := range r.Findings {
		f := model.Finding{
			Key: t.Key, ReviewID: reviewID, Role: rv.Role, HeadSHA: head, ID: rf.ID, PriorID: rf.PriorID,
			Severity: rf.Severity, Category: rf.Category, SecurityLevel: rf.SecurityLevel,
			Evidence: rf.Evidence, Location: rf.Location, FixHint: rf.FixHint,
		}
		b, err := policy.IsBlocking(f)
		if err != nil {
			return stop(*ownerOutcome(status, fmt.Sprintf("review results: %s finding %q: %v", rv.Role, rf.ID, err)))
		}
		f.Blocking = b
		if b {
			blocking++
		}
		findings = append(findings, f)
	}
	digest, err := signature.FindingsDigest(findings)
	if err != nil {
		return stop(*ownerOutcome(status, fmt.Sprintf("review results: %s: %v", rv.Role, err)))
	}
	checklist, err := lifecycle.ParseChecklist(r.HardStopChecklist)
	if err != nil {
		return stop(*ownerOutcome(status, fmt.Sprintf("review results: %s: %v", rv.Role, err)))
	}
	round, err := e.deps.Store.NextReviewRound(ctx, t.Key, change.ID, rv.Role)
	if err != nil {
		return reviewed{}, false, err
	}
	keyID := e.deps.Signer.KeyID()
	sig, err := e.deps.Signer.Sign(signature.Payload{
		Version: signature.Version, KeyID: keyID, TaskKey: t.Key, Forge: t.Forge, Repository: t.Repository,
		ChangeID: change.ID, Head: head, Role: rv.Role, Round: round, Verdict: r.Verdict,
		BlockingCount: blocking, FindingsDigest: digest,
	})
	if err != nil {
		return reviewed{}, false, fmt.Errorf("sign review: %w", err)
	}
	if err := e.deps.Store.CreateReview(ctx, model.Review{
		Key: t.Key, ID: reviewID, ChangeID: change.ID, Role: rv.Role, HeadSHA: head, Round: round,
		Verdict: r.Verdict, BlockingCount: blocking, FindingsDigest: digest, RaiseRisk: r.RaiseRisk,
		SigVersion: 1, KeyID: keyID, Signature: sig, RunID: run.ID, HardStopChecklist: r.HardStopChecklist,
		CreatedAt: e.deps.Clock.Now(),
	}, findings); err != nil {
		return reviewed{}, false, err
	}
	if err := w.PostReview(ctx, t.Key, ports.ReviewPost{
		ChangeID: change.ID, HeadSHA: head,
		Reviewer: ports.Identity{ID: "ddf-" + string(rv.Role), Name: "DDF reviewer " + string(rv.Role)},
		Verdict:  r.Verdict, Body: reviewBody(t.Key, rv.Role, round, head, r.Verdict, blocking, len(findings)),
	}); err != nil {
		return reviewed{}, false, err
	}
	e.deps.Logger.Info("review stored", "key", string(t.Key), "role", string(rv.Role), "round", round,
		"verdict", string(r.Verdict), "blocking", blocking)
	return reviewed{
		result: lifecycle.ReviewResult{
			Role: rv.Role, Head: head, Verdict: r.Verdict, Findings: findings, RaiseRisk: r.RaiseRisk,
			Checklist: checklist, HasPrior: false,
		},
		run: ri,
	}, false, nil
}

// reviewBrief is the reviewer brief: the task brief plus the head, its diff
// from the merge base, its checks and the reviewer roles. Prior findings are
// empty in this slice.
func reviewBrief(it tickTask, head, diff string, checks []ports.Check, required []policy.Reviewer) ([]byte, error) {
	cs := make([]reviewCheck, 0, len(checks))
	for _, c := range checks {
		cs = append(cs, reviewCheck{Name: c.Name, Completed: c.Completed, Passed: c.Passed})
	}
	roles := make([]model.Role, 0, len(required))
	for _, rv := range required {
		roles = append(roles, rv.Role)
	}
	b, err := json.Marshal(struct {
		Title         string          `json:"title"`
		Body          string          `json:"body"`
		Spec          string          `json:"spec"`
		Acceptance    []string        `json:"acceptance"`
		Head          string          `json:"head"`
		Diff          string          `json:"diff"`
		Checks        []reviewCheck   `json:"checks"`
		Reviewers     []model.Role    `json:"reviewers"`
		PriorFindings []model.Finding `json:"priorFindings"`
	}{it.task.Title, it.cand.Body, it.task.Spec, it.task.Acceptance, head, diff, cs, roles, []model.Finding{}})
	if err != nil {
		return nil, fmt.Errorf("encode review brief: %w", err)
	}
	return b, nil
}

// reviewBody is the forge projection of a stored review: daemon text only,
// never agent text.
func reviewBody(key model.TaskKey, role model.Role, round int, head string, verdict model.ReviewVerdict, blocking, total int) string {
	return fmt.Sprintf("DDF review by %s, round %d, on head %s.\n\nVerdict: %s. Blocking findings: %d of %d.\n\n<!-- %s -->\n",
		role, round, head, verdict, blocking, total, Marker(key))
}
