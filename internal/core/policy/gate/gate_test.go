package gate_test

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"reflect"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/policy/gate"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

var (
	taskKey = model.TaskKey(strings.Repeat("a", 64))
	head    = strings.Repeat("c", 40)
	h0      = strings.Repeat("d", 40)
	h1      = strings.Repeat("e", 40)
)

const (
	forge = "local"
	repo  = "demo"
	chgID = "1"
)

func key(b byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed([]byte(strings.Repeat(string(rune(b)), ed25519.SeedSize)))
}

func signerOf(t *testing.T, priv ed25519.PrivateKey) *signature.Signer {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	s, err := signature.NewSigner(ports.NewSecret(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

// fixture holds the registered key, a second unregistered key and the
// verifier of the registered key.
type fixture struct {
	t     *testing.T
	main  *signature.Signer
	other *signature.Signer
	v     *signature.Verifier
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	main := signerOf(t, key(1))
	v, err := signature.NewVerifier(main.Public())
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, main: main, other: signerOf(t, key(2)), v: v}
}

func emptyDigest(t *testing.T) string {
	t.Helper()
	d, err := signature.FindingsDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// payloadOf returns the payload a row of this fixture's change signs.
func payloadOf(r gate.Review) signature.Payload {
	return signature.Payload{
		Version: r.Version, KeyID: r.KeyID, TaskKey: taskKey, Forge: forge, Repository: repo,
		ChangeID: chgID, Head: r.Head, Role: r.Role, Round: r.Round, Verdict: r.Verdict,
		BlockingCount: r.BlockingCount, FindingsDigest: r.FindingsDigest,
	}
}

// signWith signs r for payload p (built from r and adjusted by edit) with s.
func (f *fixture) signWith(s *signature.Signer, r gate.Review, edit func(*signature.Payload)) gate.Review {
	f.t.Helper()
	r.Version, r.KeyID = signature.Version, s.KeyID()
	p := payloadOf(r)
	if edit != nil {
		edit(&p)
	}
	sig, err := s.Sign(p)
	if err != nil {
		f.t.Fatalf("Sign: %v", err)
	}
	r.Signature = sig
	return r
}

// review returns a row signed by the registered key.
func (f *fixture) review(role model.Role, h string, round int, verdict model.ReviewVerdict, blocking int) gate.Review {
	f.t.Helper()
	return f.signWith(f.main, gate.Review{
		Role: role, Head: h, Round: round, Verdict: verdict, BlockingCount: blocking,
		FindingsDigest: emptyDigest(f.t),
	}, nil)
}

func blob(c string) string { return strings.Repeat(c, 40) }

func mod(p string) policy.ChangedPath {
	return policy.ChangedPath{
		Status: policy.ChangeStatusModified, Path: p, OldMode: policy.ModeRegular, NewMode: policy.ModeRegular,
		OldBlob: blob("1"), NewBlob: blob("2"), Added: 1, Deleted: 1,
	}
}

// baseline returns an Input that merges at head.
func (f *fixture) baseline() gate.Input {
	return gate.Input{
		OperatorMax: policy.AutonomyMergeIntegration,
		Profile: policy.Profile{
			Autonomy: policy.AutonomyMergeIntegration, LowMaxLines: policy.DefaultLowMaxLines,
			RequiredChecks: []string{"verify"},
		},
		Task:   gate.Task{Key: taskKey, Forge: forge, Repository: repo, State: model.TaskStateMerge, Risk: model.RiskClassStandard},
		Change: gate.Change{ID: chgID, HeadSHA: head, Open: true, Mergeable: gate.MergeabilityMergeable},
		Reviews: []gate.Review{
			f.review(model.RoleR1, head, 1, model.ReviewVerdictApprove, 0),
			f.review(model.RoleR2, head, 1, model.ReviewVerdictApprove, 0),
		},
		PatchIDs:     map[string]string{head: "patch-1"},
		Checks:       []ports.Check{{Name: "verify", HeadSHA: head, Completed: true, Passed: true}},
		ChangedPaths: []policy.ChangedPath{mod("internal/x.go")},
	}
}

func eval(f *fixture, in gate.Input) gate.Decision { return gate.EvaluateMerge(in, f.v) }

func want(t *testing.T, d gate.Decision, rs ...gate.Refusal) {
	t.Helper()
	if len(rs) == 0 {
		if !d.Merge() {
			t.Fatalf("Refusals = %v, want none", d.Refusals)
		}
		return
	}
	if d.Merge() {
		t.Fatalf("Merge() = true, want refusals %v", rs)
	}
	if !reflect.DeepEqual(d.Refusals, rs) {
		t.Fatalf("Refusals = %v, want %v", d.Refusals, rs)
	}
}

func TestBaselineMerges(t *testing.T) {
	f := newFixture(t)
	d := eval(f, f.baseline())
	want(t, d)
	if d.Head != head {
		t.Fatalf("Head = %q, want %q", d.Head, head)
	}
	if !d.Paths.Passes() {
		t.Fatalf("Paths = %+v", d.Paths)
	}
}

// reasonCases returns one minimal baseline change per Reason and the single
// refusal it must produce.
func reasonCases(f *fixture) map[gate.Reason]struct {
	edit   func(*gate.Input)
	detail string
} {
	r2 := func(in *gate.Input, r gate.Review) { in.Reviews[1] = r }
	type c = struct {
		edit   func(*gate.Input)
		detail string
	}
	return map[gate.Reason]c{
		gate.ReasonInvalidInput:  {func(in *gate.Input) { in.Task.State = "bogus" }, ""},
		gate.ReasonReadError:     {func(in *gate.Input) { in.ReadErrors = []string{"checks"} }, "checks"},
		gate.ReasonAutonomy:      {func(in *gate.Input) { in.OperatorMax = policy.AutonomyPropose }, ""},
		gate.ReasonPaused:        {func(in *gate.Input) { in.Paused = true }, ""},
		gate.ReasonTaskState:     {func(in *gate.Input) { in.Task.State = model.TaskStateReview }, ""},
		gate.ReasonNeedsOwner:    {func(in *gate.Input) { in.Task.Flags = []model.TaskFlag{model.TaskFlagNeedsOwner} }, ""},
		gate.ReasonHold:          {func(in *gate.Input) { in.Task.Flags = []model.TaskFlag{model.TaskFlagHold} }, ""},
		gate.ReasonFailed:        {func(in *gate.Input) { in.Task.Flags = []model.TaskFlag{model.TaskFlagFailed} }, ""},
		gate.ReasonHitStop:       {func(in *gate.Input) { in.Task.HitStop = true }, ""},
		gate.ReasonChangeClosed:  {func(in *gate.Input) { in.Change.Open = false }, ""},
		gate.ReasonChangeDraft:   {func(in *gate.Input) { in.Change.Draft = true }, ""},
		gate.ReasonReviewMissing: {func(in *gate.Input) { in.Reviews = in.Reviews[:1] }, "r2"},
		gate.ReasonReviewUnknownVersion: {func(in *gate.Input) {
			in.Reviews[1].Version = "ddf.review-signature/v2"
		}, "r2"},
		gate.ReasonReviewUnknownKey: {func(in *gate.Input) {
			r2(in, f.signWith(f.other, in.Reviews[1], nil))
		}, "r2"},
		gate.ReasonReviewBadSignature: {func(in *gate.Input) {
			s := []byte(in.Reviews[1].Signature)
			s[0] ^= 0x01
			in.Reviews[1].Signature = string(s)
		}, "r2"},
		gate.ReasonReviewDuplicateRound: {func(in *gate.Input) { in.Reviews = append(in.Reviews, in.Reviews[1]) }, "r2"},
		gate.ReasonReviewNotApproved: {func(in *gate.Input) {
			r2(in, f.review(model.RoleR2, head, 1, model.ReviewVerdictRequestChanges, 0))
		}, "r2"},
		gate.ReasonReviewBlocking: {func(in *gate.Input) {
			r2(in, f.review(model.RoleR2, head, 1, model.ReviewVerdictApprove, 1))
		}, "r2"},
		gate.ReasonReviewStale: {func(in *gate.Input) {
			r2(in, f.review(model.RoleR2, h0, 1, model.ReviewVerdictApprove, 0))
		}, "r2"},
		gate.ReasonChecksMissing: {func(in *gate.Input) { in.Checks = nil }, "verify"},
		gate.ReasonChecksPending: {func(in *gate.Input) { in.Checks[0].Completed, in.Checks[0].Passed = false, false }, "verify"},
		gate.ReasonChecksFailed:  {func(in *gate.Input) { in.Checks[0].Passed = false }, "verify"},
		gate.ReasonPathPolicy: {func(in *gate.Input) {
			in.ChangedPaths = append(in.ChangedPaths, mod(".github/workflows/ci.yml"))
		}, ".github/workflows/ci.yml"},
		gate.ReasonHardStop: {func(in *gate.Input) {
			in.ChangedPaths = append(in.ChangedPaths, mod("deploy/app.yaml"))
		}, "deploy/app.yaml"},
		gate.ReasonBehindBase:       {func(in *gate.Input) { in.Change.Behind = true }, ""},
		gate.ReasonNotMergeable:     {func(in *gate.Input) { in.Change.Mergeable = gate.MergeabilityConflicting }, ""},
		gate.ReasonMergeableUnknown: {func(in *gate.Input) { in.Change.Mergeable = gate.MergeabilityUnknown }, ""},
	}
}

func TestEachReason(t *testing.T) {
	f := newFixture(t)
	for reason, tc := range reasonCases(f) {
		t.Run(string(reason), func(t *testing.T) {
			in := f.baseline()
			tc.edit(&in)
			d := eval(f, in)
			want(t, d, gate.Refusal{Reason: reason, Detail: tc.detail})
			if d.Head != head {
				t.Fatalf("Head = %q", d.Head)
			}
		})
	}
}

func TestEveryReasonHasACase(t *testing.T) {
	cases := reasonCases(newFixture(t))
	all := gate.Reasons()
	if len(all) != 27 {
		t.Fatalf("len(Reasons()) = %d, want 27", len(all))
	}
	for _, r := range all {
		if _, ok := cases[r]; !ok {
			t.Errorf("no test case for reason %q", r)
		}
	}
	if len(cases) != len(all) {
		t.Errorf("%d cases for %d reasons", len(cases), len(all))
	}
}

func TestReasonStrings(t *testing.T) {
	want := []string{
		"invalid-input", "read-error", "autonomy-propose", "global-pause", "task-not-merge",
		"flag-needs-owner", "flag-hold", "flag-failed", "hit-stop", "change-closed", "change-draft",
		"review-missing", "review-unknown-version", "review-unknown-key", "review-bad-signature",
		"review-duplicate-round", "review-not-approved", "review-blocking", "review-stale",
		"checks-missing", "checks-pending", "checks-failed", "path-policy", "hard-stop-unapproved",
		"behind-base", "not-mergeable", "mergeable-unknown",
	}
	got := gate.Reasons()
	if len(got) != len(want) {
		t.Fatalf("len = %d", len(got))
	}
	for i, r := range got {
		if string(r) != want[i] {
			t.Errorf("Reasons()[%d] = %q, want %q", i, r, want[i])
		}
	}
}

func TestParseReason(t *testing.T) {
	for _, r := range gate.Reasons() {
		got, err := gate.ParseReason(string(r))
		if err != nil || got != r {
			t.Errorf("ParseReason(%q) = %q, %v", r, got, err)
		}
	}
	for _, s := range []string{"", "unknown", "Review-Missing"} {
		if _, err := gate.ParseReason(s); err == nil {
			t.Errorf("ParseReason(%q) succeeded", s)
		}
	}
}

func TestParseMergeability(t *testing.T) {
	for _, m := range []gate.Mergeability{gate.MergeabilityMergeable, gate.MergeabilityConflicting, gate.MergeabilityUnknown} {
		if got, err := gate.ParseMergeability(string(m)); err != nil || got != m {
			t.Errorf("ParseMergeability(%q) = %q, %v", m, got, err)
		}
	}
	for _, s := range []string{"", "clean"} {
		if _, err := gate.ParseMergeability(s); err == nil {
			t.Errorf("ParseMergeability(%q) succeeded", s)
		}
	}
}

// allFailing returns an input failing nearly every condition at once.
func allFailing(f *fixture) gate.Input {
	in := f.baseline()
	in.OperatorMax = policy.AutonomyPropose
	in.Paused = true
	in.Task.State = model.TaskStateReview
	in.Change.Open = false
	in.Reviews = nil
	in.Checks = nil
	in.Change.Behind = true
	return in
}

func TestReadErrorReportedAlone(t *testing.T) {
	f := newFixture(t)
	in := allFailing(f)
	in.Task.State = "bogus"
	in.ReadErrors = []string{"reviews", "checks"}
	d := gate.EvaluateMerge(in, nil)
	want(t, d, gate.Refusal{Reason: gate.ReasonReadError, Detail: "checks, reviews"})
}

func TestInvalidInputFailsClosed(t *testing.T) {
	f := newFixture(t)
	cases := map[string]struct {
		edit func(*gate.Input)
		v    *signature.Verifier
	}{
		"nil verifier":        {func(*gate.Input) {}, nil},
		"invalid profile":     {func(in *gate.Input) { in.Profile.LowMaxLines = -1 }, f.v},
		"empty required list": {func(in *gate.Input) { in.Profile.RequiredChecks = []string{} }, f.v},
		"unknown task state":  {func(in *gate.Input) { in.Task.State = "bogus" }, f.v},
		"missing head patch":  {func(in *gate.Input) { delete(in.PatchIDs, head) }, f.v},
		"empty head patch":    {func(in *gate.Input) { in.PatchIDs[head] = "" }, f.v},
		"operator max":        {func(in *gate.Input) { in.OperatorMax = "" }, f.v},
		"task key":            {func(in *gate.Input) { in.Task.Key = "abc" }, f.v},
		"forge":               {func(in *gate.Input) { in.Task.Forge = "" }, f.v},
		"repository":          {func(in *gate.Input) { in.Task.Repository = "" }, f.v},
		"change id":           {func(in *gate.Input) { in.Change.ID = "" }, f.v},
		"head upper":          {func(in *gate.Input) { in.Change.HeadSHA = strings.Repeat("C", 40) }, f.v},
		"head short":          {func(in *gate.Input) { in.Change.HeadSHA = "cc" }, f.v},
		"unknown flag":        {func(in *gate.Input) { in.Task.Flags = []model.TaskFlag{"paused"} }, f.v},
		"empty risk":          {func(in *gate.Input) { in.Task.Risk = "" }, f.v},
		"mergeable empty":     {func(in *gate.Input) { in.Change.Mergeable = "" }, f.v},
		"review role":         {func(in *gate.Input) { in.Reviews[0].Role = "r3" }, f.v},
		"review verdict":      {func(in *gate.Input) { in.Reviews[0].Verdict = "LGTM" }, f.v},
		"no changed paths":    {func(in *gate.Input) { in.ChangedPaths = nil }, f.v},
		"bad changed path":    {func(in *gate.Input) { in.ChangedPaths[0].Path = "/abs" }, f.v},
		"bad approved entry":  {func(in *gate.Input) { in.Approved = []policy.HardStopEntry{{Path: ""}} }, f.v},
		"check without name":  {func(in *gate.Input) { in.Checks = append(in.Checks, ports.Check{HeadSHA: head}) }, f.v},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := f.baseline()
			tc.edit(&in)
			d := gate.EvaluateMerge(in, tc.v)
			want(t, d, gate.Refusal{Reason: gate.ReasonInvalidInput})
		})
	}
}

func TestMultipleRefusalsInOrder(t *testing.T) {
	f := newFixture(t)
	in := f.baseline()
	in.Change.Mergeable = gate.MergeabilityUnknown // 7
	in.Checks[0].Passed = false                    // 5
	in.Change.Draft = true                         // 3
	in.Change.Open = false                         // 3
	in.Paused = true                               // 1
	in.OperatorMax = policy.AutonomyPropose        // 1
	in.Change.Behind = true                        // 7
	want(t, eval(f, in),
		gate.Refusal{Reason: gate.ReasonAutonomy},
		gate.Refusal{Reason: gate.ReasonPaused},
		gate.Refusal{Reason: gate.ReasonChangeClosed},
		gate.Refusal{Reason: gate.ReasonChangeDraft},
		gate.Refusal{Reason: gate.ReasonChecksFailed, Detail: "verify"},
		gate.Refusal{Reason: gate.ReasonBehindBase},
		gate.Refusal{Reason: gate.ReasonMergeableUnknown},
	)
}

func TestRefusalsDeduplicatedAndSortedByDetail(t *testing.T) {
	f := newFixture(t)
	in := f.baseline()
	in.Reviews = nil
	in.Task.Flags = []model.TaskFlag{model.TaskFlagHold, model.TaskFlagHold}
	want(t, eval(f, in),
		gate.Refusal{Reason: gate.ReasonHold},
		gate.Refusal{Reason: gate.ReasonReviewMissing, Detail: "r1"},
		gate.Refusal{Reason: gate.ReasonReviewMissing, Detail: "r2"},
	)
}
