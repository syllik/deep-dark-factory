package store_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/store"
)

// reviewSetup stores task 1 with change c1 and the review runs run-r1 (role
// r1) and run-r2 (role r2).
func reviewSetup(t *testing.T) (*store.Store, string, model.TaskKey) {
	t.Helper()
	s, path := newTestStore(t)
	task := newTask(t, "1")
	mustCreate(t, s, task)
	mustCreateChange(t, s, newChange(task.Key, "c1"))
	for id, role := range map[string]model.Role{"run-r1": model.RoleR1, "run-r2": model.RoleR2} {
		r := newRun(task.Key, id)
		r.Role, r.Purpose = role, model.RunPurposeReview
		mustCreateRun(t, s, r)
	}
	return s, path, task.Key
}

// newReview is an approving review of change c1 by role with no findings.
func newReview(key model.TaskKey, id string, role model.Role, round int) model.Review {
	return model.Review{
		Key: key, ID: id, ChangeID: "c1", Role: role, HeadSHA: "head1", Round: round,
		Verdict: model.ReviewVerdictApprove, FindingsDigest: "sha256:f", SigVersion: 1, KeyID: "k1",
		Signature: "sig", RunID: "run-" + string(role), CreatedAt: t0,
	}
}

// newFinding is a P1 correctness finding of review r.
func newFinding(r model.Review, id string, blocking bool) model.Finding {
	return model.Finding{
		Key: r.Key, ReviewID: r.ID, Role: r.Role, HeadSHA: r.HeadSHA, ID: id, Severity: model.SeverityP1,
		Category: model.FindingCategoryCorrectness, Blocking: blocking, Evidence: "evidence " + id,
	}
}

func utcReview(r model.Review) model.Review {
	r.CreatedAt = r.CreatedAt.UTC()
	return r
}

func mustCreateReview(t *testing.T, s *store.Store, r model.Review, findings ...model.Finding) {
	t.Helper()
	if err := s.CreateReview(t.Context(), r, findings); err != nil {
		t.Fatalf("CreateReview %s: %v", r.ID, err)
	}
}

func countRows(t *testing.T, path, table string) int {
	t.Helper()
	var n int
	if err := rawDB(t, path).QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReview_RoundTrip(t *testing.T) {
	s, path, key := reviewSetup(t)
	r := newReview(key, "rv1", model.RoleR1, 1)
	r.Verdict, r.BlockingCount, r.RaiseRisk = model.ReviewVerdictRequestChanges, 1, model.RiskClassSensitive
	r.HardStopChecklist, r.Summary = json.RawMessage(`{"secrets":false,"items":[1,2]}`), "one blocker"
	f2 := newFinding(r, "f2", true)
	f2.Severity, f2.Category, f2.SecurityLevel = model.SeverityP0, model.FindingCategorySecurity, model.SecurityLevelHigh
	f2.PriorID, f2.Routed = "old-f", model.FindingRouteFixing
	f2.Location, f2.FixHint, f2.WontFixReason = "a.go:1", "check it", "not now"
	f1 := newFinding(r, "f1", false)
	mustCreateReview(t, s, r, f2, f1)

	got, err := s.GetReview(t.Context(), "rv1")
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if !reflect.DeepEqual(got, utcReview(r)) {
		t.Fatalf("GetReview =\n%+v\nwant\n%+v", got, utcReview(r))
	}
	reviews, err := s.ListReviews(t.Context(), key, "c1")
	if err != nil || !reflect.DeepEqual(reviews, []model.Review{utcReview(r)}) {
		t.Fatalf("ListReviews = %+v, %v", reviews, err)
	}
	findings, err := s.ListFindings(t.Context(), "rv1")
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if want := []model.Finding{f1, f2}; !reflect.DeepEqual(findings, want) {
		t.Fatalf("ListFindings =\n%+v\nwant\n%+v", findings, want)
	}
	var sigVersion string
	if err := rawDB(t, path).QueryRowContext(t.Context(), "SELECT sig_version FROM reviews WHERE review_id = 'rv1'").
		Scan(&sigVersion); err != nil {
		t.Fatal(err)
	}
	if sigVersion != signature.Version {
		t.Errorf("stored sig_version = %q, want %q", sigVersion, signature.Version)
	}
	if _, err := s.GetReview(t.Context(), "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetReview missing = %v, want ErrNotFound", err)
	}
	if f, err := s.ListFindings(t.Context(), "missing"); err != nil || f != nil {
		t.Errorf("ListFindings missing = %+v, %v", f, err)
	}
}

func TestReview_NextRoundAndListOrder(t *testing.T) {
	s, _, key := reviewSetup(t)
	mustCreateChange(t, s, newChange(key, "c2"))
	next := func(changeID string, role model.Role) int {
		t.Helper()
		n, err := s.NextReviewRound(t.Context(), key, changeID, role)
		if err != nil {
			t.Fatalf("NextReviewRound: %v", err)
		}
		return n
	}
	if n := next("c1", model.RoleR1); n != 1 {
		t.Fatalf("NextReviewRound before any review = %d, want 1", n)
	}
	r1a := newReview(key, "b", model.RoleR1, 1)
	mustCreateReview(t, s, r1a)
	if n := next("c1", model.RoleR1); n != 2 {
		t.Fatalf("NextReviewRound r1 after one review = %d, want 2", n)
	}
	if n := next("c1", model.RoleR2); n != 1 {
		t.Fatalf("NextReviewRound r2 = %d, want 1", n)
	}
	if n := next("c2", model.RoleR1); n != 1 {
		t.Fatalf("NextReviewRound of another change = %d, want 1", n)
	}
	r2 := newReview(key, "a", model.RoleR2, 1)
	r1b := newReview(key, "c", model.RoleR1, 2)
	mustCreateReview(t, s, r2)
	mustCreateReview(t, s, r1b)
	reviews, err := s.ListReviews(t.Context(), key, "c1")
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if want := []model.Review{utcReview(r1a), utcReview(r1b), utcReview(r2)}; !reflect.DeepEqual(reviews, want) {
		t.Fatalf("ListReviews =\n%+v\nwant\n%+v", reviews, want)
	}
	if got, err := s.ListReviews(t.Context(), key, "c2"); err != nil || got != nil {
		t.Fatalf("ListReviews of a change without reviews = %+v, %v", got, err)
	}
}

func TestReview_RefusedWritesNothing(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(r *model.Review, f []model.Finding) []model.Finding
		is     error
	}{
		{"round not next", func(r *model.Review, f []model.Finding) []model.Finding { r.Round = 2; return f }, store.ErrReviewRound},
		{"finding head", func(_ *model.Review, f []model.Finding) []model.Finding { f[1].HeadSHA = "other"; return f }, nil},
		{"finding role", func(_ *model.Review, f []model.Finding) []model.Finding { f[1].Role = model.RoleR2; return f }, nil},
		{"finding review id", func(_ *model.Review, f []model.Finding) []model.Finding { f[1].ReviewID = "rv2"; return f }, nil},
		{"finding empty id", func(_ *model.Review, f []model.Finding) []model.Finding { f[1].ID = ""; return f }, nil},
		{"blocking count", func(r *model.Review, f []model.Finding) []model.Finding { r.BlockingCount = 2; return f }, nil},
		{"missing change", func(r *model.Review, f []model.Finding) []model.Finding { r.ChangeID = "nope"; return f }, store.ErrNotFound},
		{"run of another task", func(r *model.Review, f []model.Finding) []model.Finding { r.RunID = "run-o"; return f }, nil},
		{"run of another role", func(r *model.Review, f []model.Finding) []model.Finding { r.RunID = "run-r2"; return f }, nil},
		{"missing run", func(r *model.Review, f []model.Finding) []model.Finding { r.RunID = "nope"; return f }, store.ErrNotFound},
		{"duplicate finding id", func(_ *model.Review, f []model.Finding) []model.Finding { f[1].ID = f[0].ID; return f }, nil},
		{"sig version 0", func(r *model.Review, f []model.Finding) []model.Finding { r.SigVersion = 0; return f }, nil},
		{"round 0", func(r *model.Review, f []model.Finding) []model.Finding { r.Round = 0; return f }, nil},
		{"empty signature", func(r *model.Review, f []model.Finding) []model.Finding { r.Signature = ""; return f }, nil},
		{"unknown verdict", func(r *model.Review, f []model.Finding) []model.Finding { r.Verdict = "MAYBE"; return f }, nil},
		{"unknown severity", func(_ *model.Review, f []model.Finding) []model.Finding { f[0].Severity = "P9"; return f }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path, key := reviewSetup(t)
			other := newTask(t, "2")
			mustCreate(t, s, other)
			o := newRun(other.Key, "run-o")
			o.Role, o.Purpose = model.RoleR1, model.RunPurposeReview
			mustCreateRun(t, s, o)
			r := newReview(key, "rv1", model.RoleR1, 1)
			r.BlockingCount = 1
			findings := tc.mutate(&r, []model.Finding{newFinding(r, "f1", true), newFinding(r, "f2", false)})
			err := s.CreateReview(t.Context(), r, findings)
			if err == nil {
				t.Fatal("CreateReview succeeded")
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("CreateReview error = %v, want %v", err, tc.is)
			}
			for _, table := range []string{"reviews", "findings"} {
				if n := countRows(t, path, table); n != 0 {
					t.Errorf("%s rows = %d, want 0", table, n)
				}
			}
		})
	}
}

// #73: every bad stored reviews or findings value fails the read with
// ErrDecode, and a list read returns no partial list.
func TestReview_DecodeFailsClosed(t *testing.T) {
	cases := []struct {
		name, stmt string
		finding    bool
	}{
		{"reviews.role", "UPDATE reviews SET role = 'boss'", false},
		{"reviews.role empty", "UPDATE reviews SET role = ''", false},
		{"reviews.verdict", "UPDATE reviews SET verdict = 'MAYBE'", false},
		{"reviews.verdict empty", "UPDATE reviews SET verdict = ''", false},
		{"reviews.raise_risk", "UPDATE reviews SET raise_risk = 'huge'", false},
		{"reviews.raise_risk empty", "UPDATE reviews SET raise_risk = ''", false},
		{"reviews.sig_version v1", "UPDATE reviews SET sig_version = 'v1'", false},
		{"reviews.sig_version v0", "UPDATE reviews SET sig_version = 'ddf.review-signature/v0'", false},
		{"reviews.sig_version v01", "UPDATE reviews SET sig_version = 'ddf.review-signature/v01'", false},
		{"reviews.sig_version v+1", "UPDATE reviews SET sig_version = 'ddf.review-signature/v+1'", false},
		{"reviews.sig_version no number", "UPDATE reviews SET sig_version = 'ddf.review-signature/v'", false},
		{"reviews.round", "UPDATE reviews SET round = 0", false},
		{"reviews.blocking_count", "UPDATE reviews SET blocking_count = -1", false},
		{"reviews payload schema", `UPDATE reviews SET payload = '{"schema":"ddf.review/v2"}'`, false},
		{"reviews payload unknown field", `UPDATE reviews SET payload = '{"schema":"ddf.review/v1","x":1}'`, false},
		{"findings.severity", "UPDATE findings SET severity = 'P9'", true},
		{"findings.severity empty", "UPDATE findings SET severity = ''", true},
		{"findings.category", "UPDATE findings SET category = 'style'", true},
		{"findings.category empty", "UPDATE findings SET category = ''", true},
		{"findings.security_level", "UPDATE findings SET security_level = 'Extreme'", true},
		{"findings.security_level empty", "UPDATE findings SET security_level = ''", true},
		{"findings.routed", "UPDATE findings SET routed = 'nowhere'", true},
		{"findings.routed empty", "UPDATE findings SET routed = ''", true},
		{"findings.role", "UPDATE findings SET role = 'boss'", true},
		{"findings.blocking", "UPDATE findings SET blocking = 2", true},
		{"findings payload schema", `UPDATE findings SET payload = '{"schema":"ddf.finding/v2","evidence":"e"}'`, true},
		{"findings payload unknown field", `UPDATE findings SET payload = '{"schema":"ddf.finding/v1","evidence":"e","x":1}'`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, path, key := reviewSetup(t)
			r := newReview(key, "rv1", model.RoleR1, 1)
			r.RaiseRisk = model.RiskClassLow
			f := newFinding(r, "f1", false)
			f.SecurityLevel, f.Routed = model.SecurityLevelLow, model.FindingRouteMinors
			mustCreateReview(t, s, r, f)
			if _, err := rawDB(t, path).ExecContext(t.Context(), tc.stmt); err != nil {
				t.Fatalf("raw exec: %v", err)
			}
			if tc.finding {
				got, err := s.ListFindings(t.Context(), "rv1")
				if got != nil {
					t.Errorf("partial list %+v", got)
				}
				if !errors.Is(err, store.ErrDecode) {
					t.Errorf("ListFindings error = %v, want ErrDecode", err)
				}
				return
			}
			if _, err := s.GetReview(t.Context(), "rv1"); !errors.Is(err, store.ErrDecode) {
				t.Errorf("GetReview error = %v, want ErrDecode", err)
			}
			got, err := s.ListReviews(t.Context(), key, "c1")
			if got != nil {
				t.Errorf("partial list %+v", got)
			}
			if !errors.Is(err, store.ErrDecode) {
				t.Errorf("ListReviews error = %v, want ErrDecode", err)
			}
		})
	}
}
