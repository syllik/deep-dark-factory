package lifecycle

import (
	"errors"
	"reflect"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy/gate"
)

func TestEveryReasonMapped(t *testing.T) {
	for _, r := range gate.Reasons() {
		if _, ok := rows[r]; !ok {
			t.Errorf("gate reason %q has no mapping", r)
		}
	}
	if len(rows) != len(gate.Reasons()) {
		t.Errorf("rows has %d entries, gate.Reasons() %d", len(rows), len(gate.Reasons()))
	}
}

func decision(head string, rs ...gate.Reason) gate.Decision {
	d := gate.Decision{Head: head}
	for _, r := range rs {
		d.Refusals = append(d.Refusals, gate.Refusal{Reason: r})
	}
	return d
}

var merge = Status{State: model.TaskStateMerge}

func TestRouteRefusals(t *testing.T) {
	stayO := func(reason string) Outcome { return Outcome{State: model.TaskStateMerge, Reason: reason} }
	cases := []struct {
		name  string
		d     gate.Decision
		s     Status
		asked []string
		want  Outcome
		err   error
	}{
		{name: "no refusals merges", d: decision("h1"), s: merge, want: Outcome{State: model.TaskStateMerge, Action: ActionMerge}},
		{name: "no refusals outside merge", d: decision("h1"), s: Status{State: model.TaskStateCI}, err: ErrInvalidInput},
		{name: "no refusals with a flag", d: decision("h1"), s: Status{State: model.TaskStateMerge, Flags: []model.TaskFlag{model.TaskFlagHold}}, err: ErrFlagged},
		{name: "no refusals with hit_stop", d: decision("h1"), s: Status{State: model.TaskStateMerge, HitStop: true}, err: ErrInvalidInput},
		{name: "invalid-input is an error", d: decision("h1", gate.ReasonInvalidInput), s: merge, err: ErrInvalidInput},
		{name: "task-not-merge is an error", d: decision("h1", gate.ReasonTaskState), s: Status{State: model.TaskStateCI}, err: ErrInvalidTransition},
		{name: "read-error backoff", d: decision("h1", gate.ReasonReadError), s: merge, want: Outcome{State: model.TaskStateMerge, Action: ActionBackoff, Reason: "read-error"}},
		{name: "mergeable-unknown backoff", d: decision("h1", gate.ReasonMergeableUnknown), s: merge, want: Outcome{State: model.TaskStateMerge, Action: ActionBackoff, Reason: "mergeable-unknown"}},
		{name: "change-closed hold", d: decision("h1", gate.ReasonChangeClosed), s: merge, want: Outcome{State: model.TaskStateMerge, Flag: model.TaskFlagHold, Action: ActionNotify, Reason: "change-closed"}},
		{name: "change-draft hold", d: decision("h1", gate.ReasonChangeDraft), s: merge, want: Outcome{State: model.TaskStateMerge, Flag: model.TaskFlagHold, Action: ActionNotify, Reason: "change-draft"}},
		{name: "flag-needs-owner stay", d: decision("h1", gate.ReasonNeedsOwner), s: Status{State: model.TaskStateMerge, Flags: []model.TaskFlag{model.TaskFlagNeedsOwner}}, want: stayO("flag-needs-owner")},
		{name: "flag-hold stay", d: decision("h1", gate.ReasonHold), s: Status{State: model.TaskStateMerge, Flags: []model.TaskFlag{model.TaskFlagHold}}, want: stayO("flag-hold")},
		{name: "flag-failed stay", d: decision("h1", gate.ReasonFailed), s: Status{State: model.TaskStateMerge, Flags: []model.TaskFlag{model.TaskFlagFailed}}, want: stayO("flag-failed")},
		{name: "global-pause stay", d: decision("h1", gate.ReasonPaused), s: merge, want: stayO("global-pause")},
		{name: "path-policy unasked needs owner", d: decision("h1", gate.ReasonPathPolicy), s: merge, asked: []string{"h0"},
			want: Outcome{State: model.TaskStateMerge, Flag: model.TaskFlagNeedsOwner, SetHitStop: true, AskedHead: "h1", Reason: "path-policy"}},
		{name: "hard-stop-unapproved unasked needs owner", d: decision("h1", gate.ReasonHardStop), s: merge,
			want: Outcome{State: model.TaskStateMerge, Flag: model.TaskFlagNeedsOwner, SetHitStop: true, AskedHead: "h1", Reason: "hard-stop-unapproved"}},
		{name: "path-policy asked takes hit_stop row", d: decision("h1", gate.ReasonPathPolicy, gate.ReasonHitStop), s: Status{State: model.TaskStateMerge, HitStop: true}, asked: []string{"h1"}, want: stayO("path-policy")},
		{name: "hard-stop-unapproved asked takes hit_stop row", d: decision("h1", gate.ReasonHardStop), s: merge, asked: []string{"h1"}, want: stayO("hard-stop-unapproved")},
		{name: "path-policy without head", d: decision("", gate.ReasonPathPolicy), s: merge, err: ErrInvalidInput},
		{name: "behind-base sync", d: decision("h1", gate.ReasonBehindBase), s: merge, want: Outcome{State: model.TaskStateMerge, Action: ActionSync, Reason: "behind-base"}},
		{name: "not-mergeable sync", d: decision("h1", gate.ReasonNotMergeable), s: merge, want: Outcome{State: model.TaskStateMerge, Action: ActionSync, Reason: "not-mergeable"}},
		{name: "checks-missing ci", d: decision("h1", gate.ReasonChecksMissing), s: merge, want: Outcome{State: model.TaskStateCI, Reason: "checks-missing"}},
		{name: "checks-pending ci", d: decision("h1", gate.ReasonChecksPending), s: merge, want: Outcome{State: model.TaskStateCI, Reason: "checks-pending"}},
		{name: "checks-failed ci", d: decision("h1", gate.ReasonChecksFailed), s: merge, want: Outcome{State: model.TaskStateCI, Reason: "checks-failed"}},
		{name: "review-missing review", d: decision("h1", gate.ReasonReviewMissing), s: merge, want: Outcome{State: model.TaskStateReview, Reason: "review-missing"}},
		{name: "review-unknown-version review", d: decision("h1", gate.ReasonReviewUnknownVersion), s: merge, want: Outcome{State: model.TaskStateReview, Reason: "review-unknown-version"}},
		{name: "review-unknown-key review", d: decision("h1", gate.ReasonReviewUnknownKey), s: merge, want: Outcome{State: model.TaskStateReview, Reason: "review-unknown-key"}},
		{name: "review-bad-signature review", d: decision("h1", gate.ReasonReviewBadSignature), s: merge, want: Outcome{State: model.TaskStateReview, Reason: "review-bad-signature"}},
		{name: "review-duplicate-round review", d: decision("h1", gate.ReasonReviewDuplicateRound), s: merge, want: Outcome{State: model.TaskStateReview, Reason: "review-duplicate-round"}},
		{name: "review-not-approved review", d: decision("h1", gate.ReasonReviewNotApproved), s: merge, want: Outcome{State: model.TaskStateReview, Reason: "review-not-approved"}},
		{name: "review-blocking review", d: decision("h1", gate.ReasonReviewBlocking), s: merge, want: Outcome{State: model.TaskStateReview, Reason: "review-blocking"}},
		{name: "review-stale review", d: decision("h1", gate.ReasonReviewStale), s: merge, want: Outcome{State: model.TaskStateReview, Reason: "review-stale"}},
		{name: "hit-stop alone stays", d: decision("h1", gate.ReasonHitStop), s: Status{State: model.TaskStateMerge, HitStop: true}, want: stayO("hit-stop")},
		{name: "autonomy-propose alone stays", d: decision("h1", gate.ReasonAutonomy), s: merge, want: stayO("autonomy-propose")},
		{name: "unknown reason", d: decision("h1", "bogus"), s: merge, err: ErrInvalidInput},
		{name: "detail joins reason", d: gate.Decision{Head: "h1", Refusals: []gate.Refusal{{Reason: gate.ReasonChecksFailed, Detail: "build"}}}, s: merge,
			want: Outcome{State: model.TaskStateCI, Reason: "checks-failed: build"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RouteRefusals(c.d, c.s, c.asked)
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("error = %v, want %v", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("outcome = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestRefusalPrecedence pairs each row with the next lower one, in the
// fixed order errors > backoff > hold > flag or pause stay > needs-owner
// (unasked head) > sync > ci > review > hit_stop or autonomy stay.
func TestRefusalPrecedence(t *testing.T) {
	order := []struct {
		name   string
		reason gate.Reason
		want   Outcome
		err    error
	}{
		{"error", gate.ReasonInvalidInput, Outcome{}, ErrInvalidInput},
		{"backoff", gate.ReasonReadError, Outcome{State: model.TaskStateMerge, Action: ActionBackoff, Reason: "read-error"}, nil},
		{"hold", gate.ReasonChangeClosed, Outcome{State: model.TaskStateMerge, Flag: model.TaskFlagHold, Action: ActionNotify, Reason: "change-closed"}, nil},
		{"flag or pause stay", gate.ReasonPaused, Outcome{State: model.TaskStateMerge, Reason: "global-pause"}, nil},
		{"needs-owner", gate.ReasonPathPolicy, Outcome{State: model.TaskStateMerge, Flag: model.TaskFlagNeedsOwner, SetHitStop: true, AskedHead: "h1", Reason: "path-policy"}, nil},
		{"sync", gate.ReasonBehindBase, Outcome{State: model.TaskStateMerge, Action: ActionSync, Reason: "behind-base"}, nil},
		{"ci", gate.ReasonChecksFailed, Outcome{State: model.TaskStateCI, Reason: "checks-failed"}, nil},
		{"review", gate.ReasonReviewStale, Outcome{State: model.TaskStateReview, Reason: "review-stale"}, nil},
		{"hit_stop stay", gate.ReasonHitStop, Outcome{State: model.TaskStateMerge, Reason: "hit-stop"}, nil},
	}
	for i := 0; i+1 < len(order); i++ {
		hi, lo := order[i], order[i+1]
		t.Run(hi.name+" over "+lo.name, func(t *testing.T) {
			// The lower row is listed first, so order of report does not decide.
			got, err := RouteRefusals(decision("h1", lo.reason, hi.reason), merge, nil)
			if hi.err != nil {
				if !errors.Is(err, hi.err) {
					t.Fatalf("error = %v, want %v", err, hi.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, hi.want) {
				t.Fatalf("outcome = %+v, want %+v", got, hi.want)
			}
		})
	}
	t.Run("asked head yields to review", func(t *testing.T) {
		got, err := RouteRefusals(decision("h1", gate.ReasonReviewStale, gate.ReasonPathPolicy, gate.ReasonHitStop), merge, []string{"h1"})
		if err != nil {
			t.Fatal(err)
		}
		if want := (Outcome{State: model.TaskStateReview, Reason: "review-stale"}); !reflect.DeepEqual(got, want) {
			t.Fatalf("outcome = %+v, want %+v", got, want)
		}
	})
	t.Run("hold maps for a flagged task", func(t *testing.T) {
		s := Status{State: model.TaskStateMerge, Flags: []model.TaskFlag{model.TaskFlagNeedsOwner}}
		got, err := RouteRefusals(decision("h1", gate.ReasonNeedsOwner, gate.ReasonChangeClosed), s, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := (Outcome{State: model.TaskStateMerge, Flag: model.TaskFlagHold, Action: ActionNotify, Reason: "change-closed"}); !reflect.DeepEqual(got, want) {
			t.Fatalf("outcome = %+v, want %+v", got, want)
		}
	})
}
