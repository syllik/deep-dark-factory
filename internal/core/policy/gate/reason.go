package gate

import (
	"errors"
	"fmt"
	"slices"
)

// ErrInvalidReason reports an empty or unknown refusal reason.
var ErrInvalidReason = errors.New("invalid reason")

// ErrInvalidMergeability reports an empty or unknown mergeability value.
var ErrInvalidMergeability = errors.New("invalid mergeability")

// Reason is an enumerated merge refusal reason (spec §6.6).
type Reason string

// Reason values, in the order refusals are reported.
const (
	ReasonInvalidInput         Reason = "invalid-input"
	ReasonReadError            Reason = "read-error"
	ReasonAutonomy             Reason = "autonomy-propose"
	ReasonPaused               Reason = "global-pause"
	ReasonTaskState            Reason = "task-not-merge"
	ReasonNeedsOwner           Reason = "flag-needs-owner"
	ReasonHold                 Reason = "flag-hold"
	ReasonFailed               Reason = "flag-failed"
	ReasonHitStop              Reason = "hit-stop"
	ReasonChangeClosed         Reason = "change-closed"
	ReasonChangeDraft          Reason = "change-draft"
	ReasonReviewMissing        Reason = "review-missing"
	ReasonReviewUnknownVersion Reason = "review-unknown-version"
	ReasonReviewUnknownKey     Reason = "review-unknown-key"
	ReasonReviewBadSignature   Reason = "review-bad-signature"
	ReasonReviewDuplicateRound Reason = "review-duplicate-round"
	ReasonReviewNotApproved    Reason = "review-not-approved"
	ReasonReviewBlocking       Reason = "review-blocking"
	ReasonReviewStale          Reason = "review-stale"
	ReasonChecksMissing        Reason = "checks-missing"
	ReasonChecksPending        Reason = "checks-pending"
	ReasonChecksFailed         Reason = "checks-failed"
	ReasonPathPolicy           Reason = "path-policy"
	ReasonHardStop             Reason = "hard-stop-unapproved"
	ReasonBehindBase           Reason = "behind-base"
	ReasonNotMergeable         Reason = "not-mergeable"
	ReasonMergeableUnknown     Reason = "mergeable-unknown"
)

// Reasons returns every Reason in report order.
func Reasons() []Reason {
	return []Reason{
		ReasonInvalidInput, ReasonReadError,
		ReasonAutonomy, ReasonPaused,
		ReasonTaskState, ReasonNeedsOwner, ReasonHold, ReasonFailed, ReasonHitStop,
		ReasonChangeClosed, ReasonChangeDraft,
		ReasonReviewMissing, ReasonReviewUnknownVersion, ReasonReviewUnknownKey, ReasonReviewBadSignature,
		ReasonReviewDuplicateRound, ReasonReviewNotApproved, ReasonReviewBlocking, ReasonReviewStale,
		ReasonChecksMissing, ReasonChecksPending, ReasonChecksFailed,
		ReasonPathPolicy, ReasonHardStop,
		ReasonBehindBase, ReasonNotMergeable, ReasonMergeableUnknown,
	}
}

// ParseReason returns the Reason spelled s; an unknown or empty value is an
// error wrapping ErrInvalidReason.
func ParseReason(s string) (Reason, error) {
	if i := slices.Index(Reasons(), Reason(s)); i >= 0 {
		return Reasons()[i], nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidReason, s)
}

// Mergeability is the forge's mergeability of a change at its head.
type Mergeability string

// Mergeability values.
const (
	MergeabilityMergeable   Mergeability = "mergeable"
	MergeabilityConflicting Mergeability = "conflicting"
	MergeabilityUnknown     Mergeability = "unknown"
)

// ParseMergeability returns the Mergeability spelled s; an unknown or empty
// value is an error wrapping ErrInvalidMergeability.
func ParseMergeability(s string) (Mergeability, error) {
	switch m := Mergeability(s); m {
	case MergeabilityMergeable, MergeabilityConflicting, MergeabilityUnknown:
		return m, nil
	}
	return "", fmt.Errorf("%w: %q", ErrInvalidMergeability, s)
}
