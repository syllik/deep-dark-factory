package gate

import (
	"errors"
	"slices"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// Task is the task the change belongs to, as stored.
type Task struct {
	Key        model.TaskKey
	Forge      string
	Repository string
	State      model.TaskState
	Flags      []model.TaskFlag
	HitStop    bool            // the change hit a hard stop or path policy
	Risk       model.RiskClass // the stored effective class (tasks.risk)
}

// Change is the change under evaluation at its current head.
type Change struct {
	ID        string
	HeadSHA   string
	Open      bool
	Draft     bool
	Behind    bool
	Mergeable Mergeability
}

// Review is one stored reviews row of the change. The task key, forge,
// repository and change ID are deliberately not fields: the gate takes them
// from Task and Change, so a row moved from another task or change fails its
// signature.
type Review struct {
	Role           model.Role
	Head           string
	Round          int
	Verdict        model.ReviewVerdict
	BlockingCount  int
	FindingsDigest string
	Version        string
	KeyID          string
	Signature      string
}

// BaseMerge is a base_merges row of this change: a base merge the daemon
// itself created, from its own intent.
type BaseMerge struct {
	MergeSHA      string
	ParentHeadSHA string
	BaseSHA       string
}

// Input is everything the gate reads; the caller computes every value.
type Input struct {
	OperatorMax policy.Autonomy
	Profile     policy.Profile
	Paused      bool
	Task        Task
	Change      Change
	Reviews     []Review
	BaseMerges  []BaseMerge
	// PatchIDs maps a head SHA to git patch-id --verbatim of the change at
	// that head; it must hold the current head.
	PatchIDs map[string]string
	// Checks are the checks as read for the current head.
	Checks []ports.Check
	// ChangedPaths are the current head's changed paths since the merge base.
	ChangedPaths []policy.ChangedPath
	// Approved are the hard-stop entries approved by owner answers.
	Approved []policy.HardStopEntry
	// ReadErrors names the inputs whose read failed.
	ReadErrors []string
}

// Refusal is one failed gate condition. Detail is deterministic: the role,
// the check name or the sorted paths joined by ", ", else empty.
type Refusal struct {
	Reason Reason
	Detail string
}

// Decision is the gate outcome for Head.
type Decision struct {
	Head     string // always Input.Change.HeadSHA; the merge pins to it
	Refusals []Refusal
	Paths    policy.PathResult
}

// Merge reports whether the change may merge at Head.
func (d Decision) Merge() bool { return len(d.Refusals) == 0 }

// EvaluateMerge evaluates the merge gate (spec §6.6). Every failure is a
// refusal; a read error or invalid input is reported alone.
func EvaluateMerge(in Input, v *signature.Verifier) Decision {
	d := Decision{Head: in.Change.HeadSHA}
	if len(in.ReadErrors) > 0 {
		names := slices.Clone(in.ReadErrors)
		slices.Sort(names)
		d.Refusals = []Refusal{{ReasonReadError, strings.Join(names, ", ")}}
		return d
	}
	ev, ok := validate(in, v)
	if !ok {
		d.Refusals = []Refusal{{Reason: ReasonInvalidInput}}
		return d
	}
	var rs []Refusal
	add := func(r Reason, detail string) { rs = append(rs, Refusal{r, detail}) }

	// Condition 1.
	if ev.autonomy != policy.AutonomyMergeIntegration {
		add(ReasonAutonomy, "")
	}
	if in.Paused {
		add(ReasonPaused, "")
	}

	// Condition 2.
	if in.Task.State != model.TaskStateMerge {
		add(ReasonTaskState, "")
	}
	for _, f := range in.Task.Flags {
		switch f {
		case model.TaskFlagNeedsOwner:
			add(ReasonNeedsOwner, "")
		case model.TaskFlagHold:
			add(ReasonHold, "")
		case model.TaskFlagFailed:
			add(ReasonFailed, "")
		}
	}
	if in.Task.HitStop {
		add(ReasonHitStop, "")
	}

	// Condition 3.
	if !in.Change.Open {
		add(ReasonChangeClosed, "")
	}
	if in.Change.Draft {
		add(ReasonChangeDraft, "")
	}

	// Condition 4.
	for _, rv := range ev.reviewers {
		evaluateRole(in, v, rv.Role, add)
	}

	// Condition 5.
	evaluateChecks(in, add)

	// Condition 6.
	d.Paths = ev.paths
	if bad := slices.Concat(ev.paths.Forbidden, ev.paths.Symlinks); len(bad) > 0 {
		add(ReasonPathPolicy, joinSorted(bad))
	}
	if len(ev.paths.Unapproved) > 0 {
		names := make([]string, 0, len(ev.paths.Unapproved))
		for _, e := range ev.paths.Unapproved {
			names = append(names, e.Path)
		}
		add(ReasonHardStop, joinSorted(names))
	}

	// Condition 7.
	if in.Change.Behind {
		add(ReasonBehindBase, "")
	}
	switch in.Change.Mergeable {
	case MergeabilityMergeable:
		// No refusal.
	case MergeabilityConflicting:
		add(ReasonNotMergeable, "")
	case MergeabilityUnknown:
		add(ReasonMergeableUnknown, "")
	}

	d.Refusals = order(rs)
	return d
}

// evaluated holds the values validation already computed.
type evaluated struct {
	autonomy  policy.Autonomy
	reviewers []policy.Reviewer
	paths     policy.PathResult
}

// validate checks every input the gate cannot evaluate (fail-closed) and
// returns the derived values.
func validate(in Input, v *signature.Verifier) (evaluated, bool) {
	var ev evaluated
	if v == nil || in.Profile.Validate() != nil {
		return ev, false
	}
	if _, err := policy.ParseAutonomy(string(in.OperatorMax)); err != nil {
		return ev, false
	}
	a, err := policy.EffectiveAutonomy(in.OperatorMax, in.Profile.Autonomy)
	if err != nil {
		return ev, false
	}
	ev.autonomy = a
	if _, err := model.ParseTaskKey(string(in.Task.Key)); err != nil {
		return ev, false
	}
	if in.Task.Forge == "" || in.Task.Repository == "" || in.Change.ID == "" || !isObjectID(in.Change.HeadSHA) {
		return ev, false
	}
	if _, err := model.ParseTaskState(string(in.Task.State)); err != nil {
		return ev, false
	}
	for _, f := range in.Task.Flags {
		if _, err := model.ParseTaskFlag(string(f)); err != nil {
			return ev, false
		}
	}
	if _, err := model.ParseRiskClass(string(in.Task.Risk)); err != nil {
		return ev, false
	}
	if _, err := ParseMergeability(string(in.Change.Mergeable)); err != nil {
		return ev, false
	}
	for _, r := range in.Reviews {
		if _, err := model.ParseRole(string(r.Role)); err != nil {
			return ev, false
		}
		if _, err := model.ParseReviewVerdict(string(r.Verdict)); err != nil {
			return ev, false
		}
	}
	if in.PatchIDs[in.Change.HeadSHA] == "" {
		return ev, false
	}
	paths, err := policy.CheckPaths(in.Profile, in.ChangedPaths, in.Approved)
	if err != nil {
		return ev, false
	}
	ev.paths = paths
	c, err := policy.Classify(in.Profile, in.ChangedPaths)
	if err != nil {
		return ev, false
	}
	// A stale or lowered stored class never shrinks the reviewer set below
	// the head's own class; revert keeps its own set.
	class := in.Task.Risk
	if class != model.RiskClassRevert {
		if class, err = policy.Raise(c.Class, in.Task.Risk); err != nil {
			return ev, false
		}
	}
	if ev.reviewers, err = policy.Reviewers(class); err != nil {
		return ev, false
	}
	for _, ch := range in.Checks {
		if ch.Name == "" {
			return ev, false
		}
	}
	return ev, true
}

// evaluateRole applies condition 4 to one required role.
func evaluateRole(in Input, v *signature.Verifier, role model.Role, add func(Reason, string)) {
	detail := string(role)
	var rows []Review
	for _, r := range in.Reviews {
		if r.Role == role {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		add(ReasonReviewMissing, detail)
		return
	}
	failed := false
	rounds := map[int]bool{}
	for _, r := range rows {
		err := v.Verify(signature.Payload{
			Version:        r.Version,
			KeyID:          r.KeyID,
			TaskKey:        in.Task.Key,
			Forge:          in.Task.Forge,
			Repository:     in.Task.Repository,
			ChangeID:       in.Change.ID,
			Head:           r.Head,
			Role:           role,
			Round:          r.Round,
			Verdict:        r.Verdict,
			BlockingCount:  r.BlockingCount,
			FindingsDigest: r.FindingsDigest,
		}, r.Signature)
		switch {
		case err == nil:
		case errors.Is(err, signature.ErrUnknownVersion):
			add(ReasonReviewUnknownVersion, detail)
			failed = true
		case errors.Is(err, signature.ErrUnknownKeyID):
			add(ReasonReviewUnknownKey, detail)
			failed = true
		default: // ErrBadSignature, ErrInvalidPayload or anything else: fail closed
			add(ReasonReviewBadSignature, detail)
			failed = true
		}
		if rounds[r.Round] {
			add(ReasonReviewDuplicateRound, detail)
			failed = true
		}
		rounds[r.Round] = true
	}
	if failed {
		return
	}
	latest := rows[0]
	for _, r := range rows[1:] {
		if r.Round > latest.Round {
			latest = r
		}
	}
	if latest.Verdict != model.ReviewVerdictApprove {
		add(ReasonReviewNotApproved, detail)
	}
	if latest.BlockingCount != 0 {
		add(ReasonReviewBlocking, detail)
	}
	if latest.Head != in.Change.HeadSHA && !ancestorQualifies(in, latest.Head) {
		add(ReasonReviewStale, detail)
	}
}

// ancestorQualifies reports whether every commit from reviewed to the
// current head is a base merge the daemon recorded and the change's patch-id
// is unchanged (spec §6.6 condition 4).
func ancestorQualifies(in Input, reviewed string) bool {
	if p := in.PatchIDs[reviewed]; p == "" || p != in.PatchIDs[in.Change.HeadSHA] {
		return false
	}
	h := in.Change.HeadSHA
	seen := map[string]bool{h: true}
	for steps := 0; steps < len(in.BaseMerges); steps++ {
		parent, ok := parentOf(in.BaseMerges, h)
		if !ok || seen[parent] {
			return false
		}
		if parent == reviewed {
			return true
		}
		seen[parent] = true
		h = parent
	}
	return false
}

// parentOf returns the parent head of the base merge recorded for mergeSHA.
// No record, or records naming different parents, fails.
func parentOf(bms []BaseMerge, mergeSHA string) (string, bool) {
	parent, found := "", false
	for _, b := range bms {
		if b.MergeSHA != mergeSHA {
			continue
		}
		if found && b.ParentHeadSHA != parent {
			return "", false
		}
		parent, found = b.ParentHeadSHA, true
	}
	return parent, found && parent != ""
}

// evaluateChecks applies condition 5 to the checks of the current head.
func evaluateChecks(in Input, add func(Reason, string)) {
	var cur []ports.Check
	for _, c := range in.Checks {
		if c.HeadSHA == in.Change.HeadSHA {
			cur = append(cur, c)
		}
	}
	judge := func(cs []ports.Check) {
		for _, c := range cs {
			switch {
			case !c.Completed:
				add(ReasonChecksPending, c.Name)
			case !c.Passed:
				add(ReasonChecksFailed, c.Name)
			}
		}
	}
	if in.Profile.RequiredChecks == nil {
		if len(cur) == 0 {
			add(ReasonChecksMissing, "")
		}
		judge(cur)
		return
	}
	for _, name := range in.Profile.RequiredChecks {
		var named []ports.Check
		for _, c := range cur {
			if c.Name == name {
				named = append(named, c)
			}
		}
		if len(named) == 0 {
			add(ReasonChecksMissing, name)
		}
		judge(named)
	}
}

// order sorts refusals by Reasons() order then Detail and drops duplicates.
func order(rs []Refusal) []Refusal {
	rank := map[Reason]int{}
	for i, r := range Reasons() {
		rank[r] = i
	}
	slices.SortFunc(rs, func(a, b Refusal) int {
		if a.Reason != b.Reason {
			return rank[a.Reason] - rank[b.Reason]
		}
		return strings.Compare(a.Detail, b.Detail)
	})
	return slices.Compact(rs)
}

func joinSorted(names []string) string {
	s := slices.Clone(names)
	slices.Sort(s)
	return strings.Join(slices.Compact(s), ", ")
}

// isObjectID reports whether s is a full lowercase hex SHA-1 or SHA-256.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
