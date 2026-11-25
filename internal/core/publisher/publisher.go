package publisher

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

// Publisher turns an Executor patch into one revision commit (spec §5.2,
// §8.3). Clone is the clean clone and Scratch a daemon-owned directory
// where Prepare creates per-call 'apply-<id>' and 'gate-<id>' paths; both
// are absolute.
type Publisher struct {
	Git     *git.Runner
	Clone   string
	Scratch string
	Sandbox ports.Sandbox
	Clock   ports.Clock
	IDs     ports.IDSource
}

// Input is one revision to prepare. BranchHead is the current change head
// (empty before the first revision), Base the commit the patch was exported
// against and ChangedFrom the merge base of the integration branch and the
// change. Profile, Template and Approved come from the integration branch
// and the answered hard-stop question; Loaded are the secrets loaded into
// the run's container. PlannerRisk is empty when the Planner set none.
type Input struct {
	Key           model.TaskKey
	RunID         string
	TaskRef       string
	Title         string
	Branch        string
	BaseBranch    string
	BranchHead    string
	Base          string
	ChangedFrom   string
	Patch         []byte
	Profile       policy.Profile
	Approved      []policy.HardStopEntry
	PlannerRisk   model.RiskClass
	Template      string
	Verify        []string
	Registries    []string
	Limits        ports.CellLimits
	AgentNotes    string
	Authorization string
	Identity      git.Identity
	Loaded        []ports.Secret
}

var branchChars = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

func (p *Publisher) validate() error {
	if p.Git == nil || p.Sandbox == nil || p.Clock == nil || p.IDs == nil {
		return errors.New("nil git runner, sandbox, clock or id source")
	}
	if !filepath.IsAbs(p.Clone) || !filepath.IsAbs(p.Scratch) {
		return fmt.Errorf("clone %q and scratch %q must be absolute", p.Clone, p.Scratch)
	}
	return nil
}

func (in Input) validate() error {
	if _, err := model.ParseTaskKey(string(in.Key)); err != nil {
		return err
	}
	if in.RunID == "" {
		return errors.New("empty run id")
	}
	for _, b := range []string{in.Branch, in.BaseBranch} {
		if !branchChars.MatchString(b) || strings.Contains(b, "..") || strings.HasPrefix(b, "-") ||
			strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".lock") {
			return fmt.Errorf("invalid branch name %q", b)
		}
	}
	if !isFullSHA(in.Base) || !isFullSHA(in.ChangedFrom) {
		return fmt.Errorf("base %q or changed-from %q: %w", in.Base, in.ChangedFrom, git.ErrInvalidSHA)
	}
	if in.BranchHead != "" && in.BranchHead != in.Base {
		return fmt.Errorf("branch head %q is not the base %q: a revision builds on the current head", in.BranchHead, in.Base)
	}
	if in.PlannerRisk != "" {
		if _, err := model.ParseRiskClass(string(in.PlannerRisk)); err != nil || in.PlannerRisk == model.RiskClassRevert {
			return fmt.Errorf("invalid planner risk %q", in.PlannerRisk)
		}
	}
	return nil
}

// Prepare applies in.Patch, scans it for new secrets, checks the path
// policy and hard stops, classifies it, writes exactly one commit on
// in.Base (objects only) and runs the local gate on it. Precedence: apply
// refusal (Failed) > secrets > path policy > unapproved hard stop
// (NeedsOwner) > gate (GateFailed); no cell is created and no commit is
// written for Failed or NeedsOwner. Every error returns a nil Outcome.
func (p *Publisher) Prepare(ctx context.Context, in Input) (Outcome, error) {
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	if err := in.validate(); err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	red := NewRedactor(in.Loaded)
	res, err := patch.Apply(ctx, p.Git, p.Clone, filepath.Join(p.Scratch, "apply-"+p.IDs.NewID()),
		patch.Input{Base: in.Base, ChangedFrom: in.ChangedFrom, Patch: in.Patch})
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	var applied patch.Applied
	switch o := res.(type) {
	case patch.Refused:
		reason, err := failReasonFromPatch(o.Reason)
		if err != nil {
			return nil, fmt.Errorf("prepare: %w", err)
		}
		return Failed{Reason: reason, Paths: o.Paths}, nil
	case patch.Applied:
		applied = o
	default:
		return nil, fmt.Errorf("prepare: unknown apply outcome %T", res)
	}
	stop, err := p.scan(ctx, red, applied.ChangedPaths)
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	if stop != nil {
		return stop, nil
	}
	pr, err := policy.CheckPaths(in.Profile, applied.ChangedPaths, in.Approved)
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	if len(pr.Forbidden) > 0 || len(pr.Symlinks) > 0 {
		paths := slices.Compact(slices.Sorted(slices.Values(slices.Concat(pr.Forbidden, pr.Symlinks))))
		q := fmt.Sprintf("Hard stop (forbidden path): the revision changes forbidden paths [%s] and symlinks [%s]. Nothing was published. Authorize these exact paths, or answer no.",
			quoteAll(pr.Forbidden), quoteAll(pr.Symlinks))
		return NeedsOwner{Stop: StopPathPolicy, Category: "forbidden path", Forbidden: pr.Forbidden, Symlinks: pr.Symlinks,
			Paths: paths, HitStop: true, Question: red.Redact(q)}, nil
	}
	if len(pr.Unapproved) > 0 {
		entries := make([]string, 0, len(pr.Unapproved))
		paths := make([]string, 0, len(pr.Unapproved))
		for _, e := range pr.Unapproved {
			paths = append(paths, e.Path)
			if e.Deleted {
				entries = append(entries, fmt.Sprintf("%q (deleted)", e.Path))
			} else {
				entries = append(entries, fmt.Sprintf("%q (mode %s, blob %s)", e.Path, e.Mode, e.BlobID))
			}
		}
		q := fmt.Sprintf("Hard stop (deploy and infrastructure): the revision changes %s. Nothing was published. Approve these exact entries, or answer no.",
			strings.Join(entries, ", "))
		return NeedsOwner{Stop: StopHardStop, Category: "deploy and infrastructure", HardStops: pr.Unapproved,
			Paths: paths, HitStop: true, Question: red.Redact(q)}, nil
	}
	cl, err := policy.Classify(in.Profile, applied.ChangedPaths)
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	class := cl.Class
	if in.PlannerRisk != "" {
		if class, err = policy.Raise(class, in.PlannerRisk); err != nil {
			return nil, fmt.Errorf("prepare: %w", err)
		}
	}
	var names []string
	for _, c := range applied.ChangedPaths {
		names = append(names, c.Path)
		if c.Status == policy.ChangeStatusRenamed {
			names = append(names, c.OldPath)
		}
	}
	text := ChangeText{TaskRef: in.TaskRef, Title: in.Title, Class: class, ChangedPaths: names,
		RequiredChecks: in.Profile.RequiredChecks, AgentNotes: in.AgentNotes, Authorization: in.Authorization}
	msg, err := SquashMessage(text, red)
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	commit, err := p.Git.CommitTree(ctx, p.Clone, git.CommitInput{Tree: applied.Tree, Parents: []string{in.Base},
		Identity: in.Identity, When: p.Clock.Now(), Message: msg})
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	id := p.IDs.NewID()
	gate, err := LocalGate(ctx, p.Git, p.Sandbox, p.Clone, GateInput{Key: in.Key, RunID: id, Commit: commit,
		CopyDir: filepath.Join(p.Scratch, "gate-"+id), Verify: in.Verify, Limits: in.Limits, Egress: in.Registries})
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	if !gate.Passed {
		return GateFailed{Command: red.Redact(SanitizeLine(in.Verify[gate.Failed])), Index: gate.Failed, Outcome: gate.Outcome}, nil
	}
	for _, v := range in.Verify {
		text.Verify = append(text.Verify, VerifyLine{Command: v, Passed: true})
	}
	body, err := RenderBody(in.Template, text, red)
	if err != nil {
		return nil, fmt.Errorf("prepare: %w", err)
	}
	return Ready{Commit: commit, Tree: applied.Tree, Class: class, ChangedPaths: applied.ChangedPaths, HardStops: pr.HardStops,
		Push: PushRequest{Clone: p.Clone, Branch: in.Branch, OldSHA: in.BranchHead, NewSHA: commit},
		Change: ports.PublishRequest{Key: in.Key, Branch: in.Branch, BaseBranch: in.BaseBranch, HeadSHA: commit,
			Title: red.Redact(SanitizeLine(in.Title)), Body: body},
		SquashMessage: msg}, nil
}
