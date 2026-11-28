package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
)

// batch is one Executor batch: its purpose, the stored change it revises
// (nil before the first revision), the commit its run copy is made of and
// its brief.
type batch struct {
	purpose model.RunPurpose
	change  *model.Change
	base    string
	brief   []byte
}

// batchFinding is one blocking finding in a correction brief.
type batchFinding struct {
	ID            string                `json:"id"`
	Role          model.Role            `json:"role"`
	Severity      model.Severity        `json:"severity"`
	Category      model.FindingCategory `json:"category"`
	SecurityLevel model.SecurityLevel   `json:"securityLevel,omitempty"`
	Evidence      string                `json:"evidence"`
	Location      string                `json:"location,omitempty"`
	FixHint       string                `json:"fixHint,omitempty"`
}

// gateFailure is the local gate failure in a correction brief.
type gateFailure struct {
	Command  string `json:"command"`
	Index    int    `json:"index"`
	ExitCode int    `json:"exitCode"`
	TimedOut bool   `json:"timedOut"`
}

// batchBrief is the brief of a correction or CI-fix batch: the task brief
// plus the purpose and the head, and either the failed check names (ci-fix),
// the blocking findings or the local gate failure (correction).
func batchBrief(it tickTask, purpose model.RunPurpose, head string, failed []string, findings []model.Finding, gate *publisher.GateFailed) ([]byte, error) {
	var fs []batchFinding
	for _, f := range findings {
		fs = append(fs, batchFinding{ID: f.ID, Role: f.Role, Severity: f.Severity, Category: f.Category,
			SecurityLevel: f.SecurityLevel, Evidence: f.Evidence, Location: f.Location, FixHint: f.FixHint})
	}
	var gf *gateFailure
	if gate != nil {
		gf = &gateFailure{Command: gate.Command, Index: gate.Index, ExitCode: gate.Outcome.ExitCode, TimedOut: gate.Outcome.TimedOut}
	}
	b, err := json.Marshal(struct {
		Title        string           `json:"title"`
		Body         string           `json:"body"`
		Spec         string           `json:"spec"`
		Acceptance   []string         `json:"acceptance"`
		Purpose      model.RunPurpose `json:"purpose"`
		Head         string           `json:"head"`
		FailedChecks []string         `json:"failedChecks,omitempty"`
		Findings     []batchFinding   `json:"findings,omitempty"`
		GateFailure  *gateFailure     `json:"gateFailure,omitempty"`
	}{it.task.Title, it.cand.Body, it.task.Spec, it.task.Acceptance, purpose, head, failed, fs, gf})
	if err != nil {
		return nil, fmt.Errorf("encode %s brief: %w", purpose, err)
	}
	return b, nil
}

// fix runs the next batch of a task in fixing on its stored change head: a
// CI-fix batch for a failed check, else a correction batch for the blocking
// findings of the latest review of each required role on the head; with
// neither the owner decides.
func (e *Engine) fix(ctx context.Context, it tickTask) error {
	t := it.task
	forge := e.deps.Forge
	status := statusOf(t)
	owner := func(reason string) error {
		_, err := e.apply(ctx, t, *ownerOutcome(status, reason), nil)
		return err
	}
	stored, found, err := e.taskChange(ctx, t.Key)
	if err != nil {
		return err
	}
	if !found {
		return owner("task in fixing has no stored change")
	}
	info, found, err := forge.FindChangeByTaskKey(ctx, t.Key)
	if err != nil {
		return err
	}
	if !found {
		return owner("stored change not found on the forge")
	}
	if !info.Open {
		_, err := e.apply(ctx, t, lifecycle.Outcome{State: t.State, Flag: model.TaskFlagHold, Reason: "change closed by a human"}, nil)
		return err
	}
	head := stored.HeadSHA
	if info.HeadSHA != head {
		return owner("change head moved outside DDF")
	}
	if err := e.deps.Git.Fetch(ctx, e.settings.CloneDir); err != nil {
		return err
	}
	change := &stored

	checks, err := forge.ReadChecks(ctx, head)
	if err != nil {
		return err
	}
	if failed := failedChecks(e.settings.Profile.RequiredChecks, head, checks); len(failed) > 0 {
		br, err := batchBrief(it, model.RunPurposeCIFix, head, failed, nil, nil)
		if err != nil {
			return err
		}
		return e.runBatch(ctx, it, t, batch{purpose: model.RunPurposeCIFix, change: change, base: head, brief: br})
	}

	findings, err := e.blockingFindings(ctx, t, stored, head)
	if err != nil {
		return err
	}
	if len(findings) > 0 {
		br, err := batchBrief(it, model.RunPurposeCorrection, head, nil, findings, nil)
		if err != nil {
			return err
		}
		return e.runBatch(ctx, it, t, batch{purpose: model.RunPurposeCorrection, change: change, base: head, brief: br})
	}

	// Part 3 (#162): the sync batch for a conflict with the base goes here,
	// before the owner fallback.
	return owner("fixing with nothing to fix")
}

// blockingFindings returns the blocking findings of the highest-round review
// of each required role (policy.Reviewers of the task risk) on head, in
// reviewer order.
func (e *Engine) blockingFindings(ctx context.Context, t model.Task, c model.Change, head string) ([]model.Finding, error) {
	required, err := policy.Reviewers(t.Risk)
	if err != nil {
		return nil, fmt.Errorf("fixing: %w", err)
	}
	reviews, err := e.deps.Store.ListReviews(ctx, t.Key, c.ID)
	if err != nil {
		return nil, err
	}
	var out []model.Finding
	for _, rv := range required {
		r, ok := latestReview(reviews, rv.Role, func(h string) bool { return h == head })
		if !ok {
			continue
		}
		fs, err := e.deps.Store.ListFindings(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			if f.Blocking {
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// latestReview returns the highest-round review of role whose head matches.
func latestReview(reviews []model.Review, role model.Role, head func(string) bool) (model.Review, bool) {
	var best model.Review
	found := false
	for _, r := range reviews {
		if r.Role == role && head(r.HeadSHA) && (!found || r.Round > best.Round) {
			best, found = r, true
		}
	}
	return best, found
}

// runBatch runs Executor batch b for t and publishes its patch in the same
// step, so the patch is never stored. A local gate failure moves the task to
// fixing and runs a correction batch on the same base with the failure in
// its brief; the failed patch is not reapplied. This loop ends at a
// published revision, an owner outcome, or the run bounds
// (lifecycle.CheckStart: correction batches per change, runs per task).
func (e *Engine) runBatch(ctx context.Context, it tickTask, t model.Task, b batch) error {
	for {
		runID, res, run, owner, err := e.runExecutor(ctx, t, b)
		if err != nil {
			return err
		}
		if owner != nil {
			_, err := e.apply(ctx, t, *owner, run)
			return err
		}
		var r executorResult
		if err := json.Unmarshal(res.Result, &r); err != nil {
			return fmt.Errorf("decode executor result: %w", err)
		}
		o, err := lifecycle.RouteExecutor(statusOf(t), r.Verdict)
		if err != nil {
			return fmt.Errorf("route executor verdict: %w", err)
		}
		if t, err = e.apply(ctx, t, o, run); err != nil || o.Flag != "" {
			return err
		}
		next, gf, err := e.publish(ctx, t, revision{runID: runID, kind: b.purpose, change: b.change, base: b.base,
			patch: res.Patch, summary: r.Summary, wontFix: r.WontFix})
		if err != nil {
			// The patch is never stored, so a task left in publishing cannot
			// resume (#146). The flag is written even when ctx is canceled.
			_, applyErr := e.apply(context.WithoutCancel(ctx), t, *ownerOutcome(statusOf(t), "publish failed"), nil)
			return errors.Join(err, applyErr)
		}
		if gf == nil {
			return nil
		}
		br, err := batchBrief(it, model.RunPurposeCorrection, b.base, nil, nil, gf)
		if err != nil {
			return err
		}
		t, b = next, batch{purpose: model.RunPurposeCorrection, change: b.change, base: b.base, brief: br}
	}
}

// runExecutor runs the Executor for batch b in a run copy of b.base, which
// is removed when the run ends. runID is the run copy's id, which the
// Publisher uses as the revision's run id.
func (e *Engine) runExecutor(ctx context.Context, t model.Task, b batch) (runID string, res ports.AgentRunResult, run *runInfo, owner *lifecycle.Outcome, err error) {
	runID = e.deps.IDs.NewID()
	dir := filepath.Join(e.settings.ScratchDir, "run-"+runID)
	if err := e.deps.Git.NewRunCopy(ctx, e.settings.CloneDir, b.base, dir); err != nil {
		return "", ports.AgentRunResult{}, nil, nil, err
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove run copy: %w", rmErr))
		}
	}()
	rs := runSpec{Role: model.RoleExecutor, Purpose: b.purpose, Brief: b.brief, Workspace: os.DirFS(dir)}
	if b.change != nil {
		rev := b.change.Revision
		rs.Revision, rs.HeadSHA = &rev, b.change.HeadSHA
	}
	res, _, owner, err = e.runAgent(ctx, t, statusOf(t), rs)
	if err != nil {
		return "", ports.AgentRunResult{}, nil, nil, err
	}
	return runID, res, &runInfo{role: model.RoleExecutor, usage: res.Usage}, owner, nil
}
