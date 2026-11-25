// Package lifecycle is DDF's conveyor (docs/concepts/lifecycle.md#conveyor,
// spec §5.2): deterministic functions that take a task's Status and return
// the Outcome the daemon applies.
//
// It covers:
//
//   - the happy-path transitions (Advance): inbox → planning, ready →
//     running, publishing → ci, ci → review, merge → done; planning → ready,
//     running → publishing and review → merge happen only through verdict
//     routing;
//   - Planner, Executor and reviewer verdict routing (RoutePlanner,
//     RouteExecutor, RouteReview);
//   - the task flags needs-owner, hold and failed (SetFlag, ClearFlag): a
//     flagged task is off the conveyor, and every transition and verdict
//     routing function refuses it with ErrFlagged and moves nothing;
//   - the outcome of a merge attempt (RouteMerge) and the mapping of
//     gate.Decision refusals to transitions (RouteRefusals,
//     lifecycle.md#merge-refusals).
//
// RouteRefusals maps the gate decision of a flagged task too, so that a
// change closed or converted to draft by a human still sets hold; the gate
// itself reports the flag as a flag-* refusal, which maps to stay. The fixed
// precedence when a Decision holds several refusals is: errors > stay with
// backoff > hold > flag or pause stay > needs-owner (unasked head) > sync >
// ci > review > hit_stop or autonomy stay.
//
// RouteReview takes the hard-stop checklist as typed items; ParseChecklist
// decodes the verbatim JSON kept in model.Review.HardStopChecklist into them.
//
// The package keeps no state: the caller passes the heads already asked
// about for a hard stop or path policy, and an Outcome names the head to
// record. Out of scope (Task 6b): correction-batch and sync bounds, P2/P3
// follow-up and minors routing, the priorId repeat rule and the loop guard.
//
// The package is pure: no I/O, clock, git, store or forge calls, and it never
// mutates its input.
package lifecycle
