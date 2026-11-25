package lifecycle

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

// RoutePlanner routes the Planner verdict of a task in planning. children is
// the number of children of a DECOMPOSE (at least 1); maxAutoChildren is
// planning.maxAutoChildren; reason is the verdict's reason, required for
// REJECT.
func RoutePlanner(s Status, v model.PlannerVerdict, children, maxAutoChildren int, reason string) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	if s.State != model.TaskStatePlanning {
		return Outcome{}, fmt.Errorf("%w: planner verdict in state %s", ErrInvalidTransition, s.State)
	}
	switch v {
	case model.PlannerVerdictReady:
		return Outcome{State: model.TaskStateReady}, nil
	case model.PlannerVerdictDecompose:
		if children < 1 || maxAutoChildren < 0 {
			return Outcome{}, fmt.Errorf("%w: decompose with %d children, max %d", ErrInvalidInput, children, maxAutoChildren)
		}
		if children > maxAutoChildren {
			return ownerOutcome(s, fmt.Sprintf("decompose into %d children exceeds maxAutoChildren %d", children, maxAutoChildren)), nil
		}
		return Outcome{State: s.State, Action: ActionCreateChildren}, nil
	case model.PlannerVerdictAsk:
		return ownerOutcome(s, reason), nil
	case model.PlannerVerdictReject:
		if reason == "" {
			return Outcome{}, fmt.Errorf("%w: reject without reason", ErrInvalidInput)
		}
		return ownerOutcome(s, reason), nil
	}
	return Outcome{}, fmt.Errorf("%w: planner verdict %q", ErrInvalidInput, v)
}

// RouteExecutor routes the Executor verdict of a task in running or fixing
// (an initial or correction batch). Both batches go to publishing.
func RouteExecutor(s Status, v model.ExecutorVerdict) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	if s.State != model.TaskStateRunning && s.State != model.TaskStateFixing {
		return Outcome{}, fmt.Errorf("%w: executor verdict in state %s", ErrInvalidTransition, s.State)
	}
	switch v {
	case model.ExecutorVerdictComplete:
		return Outcome{State: model.TaskStatePublishing}, nil
	case model.ExecutorVerdictBlocked:
		return ownerOutcome(s, "executor blocked"), nil
	}
	return Outcome{}, fmt.Errorf("%w: executor verdict %q", ErrInvalidInput, v)
}

// ChecklistStatus is the status of one hard-stop checklist item.
type ChecklistStatus string

// ChecklistStatus values.
const (
	ChecklistClear      ChecklistStatus = "clear"
	ChecklistHit        ChecklistStatus = "hit"
	ChecklistUnassessed ChecklistStatus = "unassessed"
)

// ChecklistItem is one hard-stop checklist item of a reviewer result.
type ChecklistItem struct {
	Item   string          `json:"item"`
	Status ChecklistStatus `json:"status"`
}

// ParseChecklist decodes a hard-stop checklist as kept verbatim in
// model.Review.HardStopChecklist; empty input is an empty checklist.
func ParseChecklist(raw json.RawMessage) ([]ChecklistItem, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var items []ChecklistItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%w: hard-stop checklist: %w", ErrInvalidInput, err)
	}
	for i, it := range items {
		if err := validateItem(it); err != nil {
			return nil, fmt.Errorf("hard-stop checklist[%d]: %w", i, err)
		}
	}
	return items, nil
}

func validateItem(it ChecklistItem) error {
	if it.Item == "" {
		return fmt.Errorf("%w: empty checklist item", ErrInvalidInput)
	}
	switch it.Status {
	case ChecklistClear, ChecklistHit, ChecklistUnassessed:
		return nil
	}
	return fmt.Errorf("%w: checklist status %q", ErrInvalidInput, it.Status)
}

// ReviewResult is one reviewer's result on one head.
type ReviewResult struct {
	Role      model.Role
	Head      string
	Verdict   model.ReviewVerdict
	Findings  []model.Finding
	RaiseRisk model.RiskClass // empty, standard or sensitive; honoured from R1
	Checklist []ChecklistItem
}

// reviewSummary is what routing needs from one valid result.
type reviewSummary struct {
	blocking   int
	hardStop   bool // a hard-stop finding or a checklist hit
	unassessed bool
}

func summarize(r ReviewResult, head string) (reviewSummary, error) {
	var sum reviewSummary
	if r.Head != head {
		return sum, fmt.Errorf("%w: %s result for head %q, want %q", ErrInvalidInput, r.Role, r.Head, head)
	}
	if _, err := model.ParseReviewVerdict(string(r.Verdict)); err != nil {
		return sum, fmt.Errorf("%w: %s: %w", ErrInvalidInput, r.Role, err)
	}
	switch r.RaiseRisk {
	case "", model.RiskClassStandard, model.RiskClassSensitive:
	case model.RiskClassDocs, model.RiskClassLow, model.RiskClassRevert:
		return sum, fmt.Errorf("%w: %s: raiseRisk %q", ErrInvalidInput, r.Role, r.RaiseRisk)
	default:
		return sum, fmt.Errorf("%w: %s: raiseRisk %q", ErrInvalidInput, r.Role, r.RaiseRisk)
	}
	for i, f := range r.Findings {
		b, err := policy.IsBlocking(f)
		if err != nil {
			return sum, fmt.Errorf("%w: %s findings[%d]: %w", ErrInvalidInput, r.Role, i, err)
		}
		if b {
			sum.blocking++
		}
		if f.Category == model.FindingCategoryHardStop {
			sum.hardStop = true
		}
	}
	for i, it := range r.Checklist {
		if err := validateItem(it); err != nil {
			return sum, fmt.Errorf("%s checklist[%d]: %w", r.Role, i, err)
		}
		switch it.Status {
		case ChecklistHit:
			sum.hardStop = true
		case ChecklistUnassessed:
			sum.unassessed = true
		case ChecklistClear:
		}
	}
	if r.Verdict == model.ReviewVerdictRequestChanges && sum.blocking == 0 {
		return sum, fmt.Errorf("%w: %s REQUEST_CHANGES without a blocking finding", ErrInvalidInput, r.Role)
	}
	return sum, nil
}

// RouteReview routes the results of the required reviewers on head for a
// task in review. Exactly one result per required role, all for head, is
// expected; anything else is ErrInvalidInput. A reviewer with
// HardStopChecklist must send a non-empty checklist, else ErrInvalidInput;
// another reviewer may send one and its items count the same. In order: an
// unassessed checklist item reruns that reviewer; a hard-stop finding or
// checklist hit raises needs-owner with hit_stop; a blocking finding goes to
// fixing; an R1 raiseRisk without R2 in the set adds R2; else every result
// approves and the task goes to merge.
func RouteReview(s Status, head string, required []policy.Reviewer, results []ReviewResult) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	if s.State != model.TaskStateReview {
		return Outcome{}, fmt.Errorf("%w: review results in state %s", ErrInvalidTransition, s.State)
	}
	if head == "" || len(required) == 0 {
		return Outcome{}, fmt.Errorf("%w: empty head or reviewer set", ErrInvalidInput)
	}
	roles := make([]model.Role, len(required))
	for i, rv := range required {
		roles[i] = rv.Role
	}
	byRole := map[model.Role]ReviewResult{}
	for _, r := range results {
		if !slices.Contains(roles, r.Role) {
			return Outcome{}, fmt.Errorf("%w: result for role %q not in the reviewer set", ErrInvalidInput, r.Role)
		}
		if _, dup := byRole[r.Role]; dup {
			return Outcome{}, fmt.Errorf("%w: duplicate result for %s", ErrInvalidInput, r.Role)
		}
		byRole[r.Role] = r
	}
	sums := make([]reviewSummary, len(required))
	for i, rv := range required {
		role := rv.Role
		if role != model.RoleR1 && role != model.RoleR2 {
			return Outcome{}, fmt.Errorf("%w: reviewer role %q", ErrInvalidInput, role)
		}
		if slices.Index(roles, role) != i {
			return Outcome{}, fmt.Errorf("%w: duplicate required role %s", ErrInvalidInput, role)
		}
		r, ok := byRole[role]
		if !ok {
			return Outcome{}, fmt.Errorf("%w: missing result for %s", ErrInvalidInput, role)
		}
		if rv.HardStopChecklist && len(r.Checklist) == 0 {
			return Outcome{}, fmt.Errorf("%w: %s result without the hard-stop checklist", ErrInvalidInput, role)
		}
		sum, err := summarize(r, head)
		if err != nil {
			return Outcome{}, err
		}
		sums[i] = sum
	}

	var rerun []model.Role
	hardStop, blocking := false, false
	for i, sum := range sums {
		if sum.unassessed {
			rerun = append(rerun, roles[i])
		}
		hardStop = hardStop || sum.hardStop
		blocking = blocking || sum.blocking > 0
	}
	switch {
	case len(rerun) > 0:
		return Outcome{State: s.State, Action: ActionRerunReviewer, Roles: rerun, Reason: "hard-stop checklist item unassessed"}, nil
	case hardStop:
		o := ownerOutcome(s, "hard stop found in review")
		o.SetHitStop = true
		return o, nil
	case blocking:
		return Outcome{State: model.TaskStateFixing}, nil
	}
	if r1, ok := byRole[model.RoleR1]; ok && r1.RaiseRisk != "" && !slices.Contains(roles, model.RoleR2) {
		return Outcome{State: s.State, Action: ActionAddReviewer, Roles: []model.Role{model.RoleR2}, Risk: r1.RaiseRisk, Reason: "r1 raised risk to " + string(r1.RaiseRisk)}, nil
	}
	// Every result is APPROVE here: a REQUEST_CHANGES has a blocking finding.
	return Outcome{State: model.TaskStateMerge}, nil
}
