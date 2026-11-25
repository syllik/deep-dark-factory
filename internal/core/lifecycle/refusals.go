package lifecycle

import (
	"fmt"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy/gate"
)

// row is a merge-refusal row of lifecycle.md#merge-refusals; a lower value
// wins when a Decision holds several refusals.
type row int

// Rows in precedence order.
const (
	rowError row = iota
	rowBackoff
	rowHold
	rowStay
	rowNeedsOwner // downgraded to rowHitStop on a head already asked about
	rowSync
	rowCI
	rowReview
	rowHitStop
)

// rows maps every gate.Reason to its row.
var rows = map[gate.Reason]row{
	gate.ReasonInvalidInput:         rowError,
	gate.ReasonTaskState:            rowError,
	gate.ReasonReadError:            rowBackoff,
	gate.ReasonMergeableUnknown:     rowBackoff,
	gate.ReasonChangeClosed:         rowHold,
	gate.ReasonChangeDraft:          rowHold,
	gate.ReasonNeedsOwner:           rowStay,
	gate.ReasonHold:                 rowStay,
	gate.ReasonFailed:               rowStay,
	gate.ReasonPaused:               rowStay,
	gate.ReasonPathPolicy:           rowNeedsOwner,
	gate.ReasonHardStop:             rowNeedsOwner,
	gate.ReasonBehindBase:           rowSync,
	gate.ReasonNotMergeable:         rowSync,
	gate.ReasonChecksMissing:        rowCI,
	gate.ReasonChecksPending:        rowCI,
	gate.ReasonChecksFailed:         rowCI,
	gate.ReasonReviewMissing:        rowReview,
	gate.ReasonReviewUnknownVersion: rowReview,
	gate.ReasonReviewUnknownKey:     rowReview,
	gate.ReasonReviewBadSignature:   rowReview,
	gate.ReasonReviewDuplicateRound: rowReview,
	gate.ReasonReviewNotApproved:    rowReview,
	gate.ReasonReviewBlocking:       rowReview,
	gate.ReasonReviewStale:          rowReview,
	gate.ReasonHitStop:              rowHitStop,
	gate.ReasonAutonomy:             rowHitStop,
}

// RouteRefusals maps the gate decision for a task in merge to its outcome
// (lifecycle.md#merge-refusals). asked lists the heads already asked about
// for a hard stop or path policy. No refusal is ActionMerge. A flagged task
// is mapped too (see the package comment); invalid-input and task-not-merge
// are errors, never a transition.
func RouteRefusals(d gate.Decision, s Status, asked []string) (Outcome, error) {
	if err := validateState(s); err != nil {
		return Outcome{}, err
	}
	if len(d.Refusals) == 0 {
		if s.State != model.TaskStateMerge {
			return Outcome{}, fmt.Errorf("%w: merge allowed in state %s", ErrInvalidInput, s.State)
		}
		return Outcome{State: s.State, Action: ActionMerge}, nil
	}
	best, win := rowHitStop+1, gate.Refusal{}
	for _, r := range d.Refusals {
		rw, ok := rows[r.Reason]
		if !ok {
			return Outcome{}, fmt.Errorf("%w: unmapped refusal %q", ErrInvalidInput, r.Reason)
		}
		if rw == rowNeedsOwner {
			if d.Head == "" {
				return Outcome{}, fmt.Errorf("%w: %s without a head", ErrInvalidInput, r.Reason)
			}
			if slices.Contains(asked, d.Head) {
				rw = rowHitStop
			}
		}
		if rw < best {
			best, win = rw, r
		}
	}
	reason := string(win.Reason)
	if win.Detail != "" {
		reason += ": " + win.Detail
	}
	switch best {
	case rowError:
		if win.Reason == gate.ReasonTaskState {
			return Outcome{}, fmt.Errorf("%w: %s", ErrInvalidTransition, reason)
		}
		return Outcome{}, fmt.Errorf("%w: %s", ErrInvalidInput, reason)
	case rowBackoff:
		return Outcome{State: s.State, Action: ActionBackoff, Reason: reason}, nil
	case rowHold:
		return Outcome{State: s.State, Flag: model.TaskFlagHold, Action: ActionNotify, Reason: reason}, nil
	case rowNeedsOwner:
		return Outcome{State: s.State, Flag: model.TaskFlagNeedsOwner, SetHitStop: true, AskedHead: d.Head, Reason: reason}, nil
	case rowSync:
		return Outcome{State: s.State, Action: ActionSync, Reason: reason}, nil
	case rowCI:
		return Outcome{State: model.TaskStateCI, Reason: reason}, nil
	case rowReview:
		return Outcome{State: model.TaskStateReview, Reason: reason}, nil
	}
	// rowStay and rowHitStop: stay, no new needs-owner.
	o := stay(s)
	o.Reason = reason
	return o, nil
}
