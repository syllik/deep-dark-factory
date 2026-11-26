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
//     lifecycle.md#merge-refusals);
//   - fixing (lifecycle.md#fixing): finding identity by id and priorId only,
//     the loop guard that sends a blocking finding continuing a blocking
//     prior finding to needs-owner, and the route of every finding to
//     fixing, the change's follow-up task or the minors task (RouteFindings);
//   - bounds (lifecycle.md#bounds): Limits with DefaultLimits, counters
//     derived from a task's runs, and checks that send a task over any bound
//     to needs-owner;
//   - sync with base (lifecycle.md#sync-with-base): routing of a SyncResult,
//     where a conflict starts an Executor sync batch in fixing and changed
//     workflow files send the task to needs-owner;
//   - the revert rule (RouteMergeCI, lifecycle.md#revert): red CI on a DDF
//     merge commit after one rerun opens a revert and reopens the task to
//     ready, and a second DDF-attributed red within 24 h pauses the daemon;
//   - the global pause (lifecycle.md#global-pause): the set of PauseReason
//     values, how a reason is set (SetPause, IsolationPause), the automatic
//     lift of runtime only (LiftAutomatic) and Resume, which clears the set.
//
// RouteRefusals maps the gate decision of a flagged task too, so that a
// change closed or converted to draft by a human still sets hold; the gate
// itself reports the flag as a flag-* refusal, which maps to stay. The fixed
// precedence when a Decision holds several refusals is: errors > stay with
// backoff > hold > flag or pause stay > needs-owner (unasked head) > sync >
// ci > review > hit_stop or autonomy stay. A Decision with no refusals merges
// only an unflagged task without hit_stop: a flagged task is ErrFlagged and a
// task with hit_stop is ErrInvalidInput.
//
// RouteReview takes the required reviewer set as []policy.Reviewer and the
// hard-stop checklist as typed items; ParseChecklist decodes the verbatim JSON
// kept in model.Review.HardStopChecklist into them. A missing or empty
// checklist from a reviewer with HardStopChecklist is ErrInvalidInput, so it
// never reaches merge, fixing or needs-owner.
//
// The package keeps no state: the caller passes the heads already asked
// about for a hard stop or path policy, and an Outcome names the head to
// record. Time enters only as a now parameter that the daemon reads from its
// clock.
//
// The package is pure: no I/O, clock, git, store or forge calls, and it never
// mutates its input.
package lifecycle
