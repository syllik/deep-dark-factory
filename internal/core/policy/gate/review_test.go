package gate_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/policy/gate"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// ancestorInput returns the baseline with both reviews on h0 and the
// current head reached from h0 by two daemon base merges h1 and head.
func ancestorInput(f *fixture) gate.Input {
	in := f.baseline()
	in.Reviews = []gate.Review{
		f.review(model.RoleR1, h0, 1, model.ReviewVerdictApprove, 0),
		f.review(model.RoleR2, h0, 1, model.ReviewVerdictApprove, 0),
	}
	in.BaseMerges = []gate.BaseMerge{
		{MergeSHA: h1, ParentHeadSHA: h0, BaseSHA: blob("8")},
		{MergeSHA: head, ParentHeadSHA: h1, BaseSHA: blob("9")},
	}
	in.PatchIDs = map[string]string{h0: "patch-1", h1: "patch-1", head: "patch-1"}
	return in
}

func TestAncestorExceptionQualifies(t *testing.T) {
	f := newFixture(t)
	want(t, eval(f, ancestorInput(f)))
}

func TestAncestorExceptionFails(t *testing.T) {
	f := newFixture(t)
	stale := []gate.Refusal{
		{Reason: gate.ReasonReviewStale, Detail: "r1"},
		{Reason: gate.ReasonReviewStale, Detail: "r2"},
	}
	cases := map[string]func(*gate.Input){
		"unrecorded merge (human update branch)": func(in *gate.Input) { in.BaseMerges = in.BaseMerges[:1] },
		"changed patch-id":                       func(in *gate.Input) { in.PatchIDs[h0] = "patch-2" },
		"missing patch-id":                       func(in *gate.Input) { delete(in.PatchIDs, h0) },
		"empty patch-id":                         func(in *gate.Input) { in.PatchIDs[h0] = "" },
		"cycle": func(in *gate.Input) {
			in.BaseMerges = []gate.BaseMerge{{MergeSHA: head, ParentHeadSHA: h1}, {MergeSHA: h1, ParentHeadSHA: head}}
		},
		"ambiguous parent": func(in *gate.Input) {
			in.BaseMerges = append(in.BaseMerges, gate.BaseMerge{MergeSHA: head, ParentHeadSHA: blob("7")})
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			in := ancestorInput(f)
			edit(&in)
			want(t, eval(f, in), stale...)
		})
	}
}

// flipBit returns sig with one bit of the decoded signature flipped.
func flipBit(t *testing.T, sig string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatal(err)
	}
	b[10] ^= 0x04
	return base64.StdEncoding.EncodeToString(b)
}

// TestProbe1 is spec §14.1 probe 1: the gate accepts no forged, unknown-key,
// unknown-version, copied or superseded review.
func TestProbe1(t *testing.T) {
	f := newFixture(t)
	r2 := func(in *gate.Input) *gate.Review { return &in.Reviews[1] }
	cases := map[string]struct {
		edit   func(*gate.Input)
		reason gate.Reason
	}{
		"unregistered key": {func(in *gate.Input) { *r2(in) = f.signWith(f.other, *r2(in), nil) }, gate.ReasonReviewUnknownKey},
		"unknown version":  {func(in *gate.Input) { r2(in).Version = "ddf.review-signature/v2" }, gate.ReasonReviewUnknownVersion},
		"flipped bit":      {func(in *gate.Input) { r2(in).Signature = flipBit(t, r2(in).Signature) }, gate.ReasonReviewBadSignature},
		"forged under registered key id": {func(in *gate.Input) {
			p := payloadOf(*r2(in))
			c, err := signature.Canonical(p)
			if err != nil {
				t.Fatal(err)
			}
			r2(in).Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key(2), c))
		}, gate.ReasonReviewBadSignature},
		"signed for another task key": {func(in *gate.Input) {
			*r2(in) = f.signWith(f.main, *r2(in), func(p *signature.Payload) { p.TaskKey = model.TaskKey(strings.Repeat("b", 64)) })
		}, gate.ReasonReviewBadSignature},
		"signed for another change": {func(in *gate.Input) {
			*r2(in) = f.signWith(f.main, *r2(in), func(p *signature.Payload) { p.ChangeID = "2" })
		}, gate.ReasonReviewBadSignature},
		"signed for another forge": {func(in *gate.Input) {
			*r2(in) = f.signWith(f.main, *r2(in), func(p *signature.Payload) { p.Forge = "github" })
		}, gate.ReasonReviewBadSignature},
		"signed for another repository": {func(in *gate.Input) {
			*r2(in) = f.signWith(f.main, *r2(in), func(p *signature.Payload) { p.Repository = "other" })
		}, gate.ReasonReviewBadSignature},
		"head rewritten": {func(in *gate.Input) {
			*r2(in) = f.review(model.RoleR2, h0, 1, model.ReviewVerdictApprove, 0)
			r2(in).Head = head
		}, gate.ReasonReviewBadSignature},
		"row moved to another role": {func(in *gate.Input) {
			moved := in.Reviews[0]
			moved.Role = model.RoleR2
			*r2(in) = moved
		}, gate.ReasonReviewBadSignature},
		"round raised after signing": {func(in *gate.Input) { r2(in).Round = 2 }, gate.ReasonReviewBadSignature},
		"verdict changed after signing": {func(in *gate.Input) {
			*r2(in) = f.review(model.RoleR2, head, 1, model.ReviewVerdictRequestChanges, 0)
			r2(in).Verdict = model.ReviewVerdictApprove
		}, gate.ReasonReviewBadSignature},
		"invalid payload round 0": {func(in *gate.Input) {
			r2(in).Round = 0
		}, gate.ReasonReviewBadSignature},
		"verbatim copy": {func(in *gate.Input) { in.Reviews = append(in.Reviews, *r2(in)) }, gate.ReasonReviewDuplicateRound},
		"older approve superseded": {func(in *gate.Input) {
			in.Reviews = append(in.Reviews, f.review(model.RoleR2, head, 2, model.ReviewVerdictRequestChanges, 0))
		}, gate.ReasonReviewNotApproved},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := f.baseline()
			tc.edit(&in)
			want(t, eval(f, in), gate.Refusal{Reason: tc.reason, Detail: "r2"})
		})
	}
}

func TestLatestRoundCounts(t *testing.T) {
	f := newFixture(t)
	in := f.baseline()
	in.Reviews = append(in.Reviews,
		f.review(model.RoleR2, h0, 3, model.ReviewVerdictRequestChanges, 2),
		f.review(model.RoleR2, head, 4, model.ReviewVerdictApprove, 0),
	)
	want(t, eval(f, in))
}

func TestOtherRolesIgnored(t *testing.T) {
	f := newFixture(t)
	in := f.baseline()
	planner := f.review(model.RoleR1, head, 1, model.ReviewVerdictRequestChanges, 1)
	planner.Role = model.RolePlanner
	in.Reviews = append(in.Reviews, planner)
	want(t, eval(f, in))
}

func TestStoredRiskNeverShrinksReviewers(t *testing.T) {
	f := newFixture(t)
	in := f.baseline()
	in.Task.Risk = model.RiskClassLow
	in.Profile.Sensitive = []string{"internal/**"}
	in.Reviews = in.Reviews[:1]
	want(t, eval(f, in), gate.Refusal{Reason: gate.ReasonReviewMissing, Detail: "r2"})

	in.Profile.Sensitive = nil
	want(t, eval(f, in))
}

func TestRevertRequiresOnlyR1(t *testing.T) {
	f := newFixture(t)
	in := f.baseline()
	in.Task.Risk = model.RiskClassRevert
	in.Profile.Sensitive = []string{"internal/**"}
	in.Reviews = in.Reviews[:1]
	want(t, eval(f, in))
}

func TestChecks(t *testing.T) {
	f := newFixture(t)
	other := strings.Repeat("f", 40)
	cases := map[string]struct {
		edit func(*gate.Input)
		want []gate.Refusal
	}{
		"required on another head only": {func(in *gate.Input) { in.Checks[0].HeadSHA = other },
			[]gate.Refusal{{Reason: gate.ReasonChecksMissing, Detail: "verify"}}},
		"no list and no entry": {func(in *gate.Input) { in.Profile.RequiredChecks, in.Checks = nil, nil },
			[]gate.Refusal{{Reason: gate.ReasonChecksMissing}}},
		"no list and entries on another head only": {func(in *gate.Input) {
			in.Profile.RequiredChecks = nil
			in.Checks[0].HeadSHA = other
		}, []gate.Refusal{{Reason: gate.ReasonChecksMissing}}},
		"no list, all pass": {func(in *gate.Input) {
			in.Profile.RequiredChecks = nil
			in.Checks = append(in.Checks, ports.Check{Name: "lint", HeadSHA: head, Completed: true, Passed: true})
		}, nil},
		"no list, one failing": {func(in *gate.Input) {
			in.Profile.RequiredChecks = nil
			in.Checks = append(in.Checks, ports.Check{Name: "lint", HeadSHA: head, Completed: true})
		}, []gate.Refusal{{Reason: gate.ReasonChecksFailed, Detail: "lint"}}},
		"no list, one pending": {func(in *gate.Input) {
			in.Profile.RequiredChecks = nil
			in.Checks = append(in.Checks, ports.Check{Name: "lint", HeadSHA: head})
		}, []gate.Refusal{{Reason: gate.ReasonChecksPending, Detail: "lint"}}},
		"extra non-required failing": {func(in *gate.Input) {
			in.Checks = append(in.Checks, ports.Check{Name: "lint", HeadSHA: head, Completed: true})
		}, nil},
		"duplicate required entry failing": {func(in *gate.Input) {
			in.Checks = append(in.Checks, ports.Check{Name: "verify", HeadSHA: head, Completed: true})
		}, []gate.Refusal{{Reason: gate.ReasonChecksFailed, Detail: "verify"}}},
		"two required, one missing": {func(in *gate.Input) {
			in.Profile.RequiredChecks = []string{"verify", "build"}
		}, []gate.Refusal{{Reason: gate.ReasonChecksMissing, Detail: "build"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := f.baseline()
			tc.edit(&in)
			want(t, eval(f, in), tc.want...)
		})
	}
}

func TestPathPolicyDetailAndPaths(t *testing.T) {
	f := newFixture(t)
	in := f.baseline()
	link := mod("internal/link")
	link.OldMode, link.NewMode = policy.ModeSymlink, policy.ModeSymlink
	in.ChangedPaths = append(in.ChangedPaths, mod(".github/ci.yml"), link, mod("deploy/b.yaml"), mod("deploy/a.yaml"))
	d := eval(f, in)
	want(t, d,
		gate.Refusal{Reason: gate.ReasonPathPolicy, Detail: ".github/ci.yml, internal/link"},
		gate.Refusal{Reason: gate.ReasonHardStop, Detail: "deploy/a.yaml, deploy/b.yaml"},
	)
	if len(d.Paths.HardStops) != 2 || len(d.Paths.Unapproved) != 2 {
		t.Fatalf("Paths = %+v", d.Paths)
	}

	in.Approved = d.Paths.HardStops
	in.ChangedPaths = []policy.ChangedPath{mod("internal/x.go"), mod("deploy/b.yaml"), mod("deploy/a.yaml")}
	want(t, eval(f, in))
}
