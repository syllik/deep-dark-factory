// Package gate is DDF's merge gate: EvaluateMerge decides whether a change
// may merge at its current head (spec §6.6 Merge gate) and reports every
// failed condition as an enumerated Reason.
//
// Reasons and the §6.6 condition each one refuses:
//
//   - invalid-input: the input itself is unusable (fail-closed, reported
//     alone);
//   - read-error: condition 8, any forge or store read failed (reported
//     alone, before input validation);
//   - autonomy-propose, global-pause: condition 1;
//   - task-not-merge, flag-needs-owner, flag-hold, flag-failed, hit-stop:
//     condition 2;
//   - change-closed, change-draft: condition 3;
//   - review-missing, review-unknown-version, review-unknown-key,
//     review-bad-signature, review-duplicate-round, review-not-approved,
//     review-blocking, review-stale: condition 4 and the Review signatures
//     rules;
//   - checks-missing, checks-pending, checks-failed: condition 5;
//   - path-policy, hard-stop-unapproved: condition 6;
//   - behind-base, not-mergeable, mergeable-unknown: condition 7.
//
// Threat model: the signature binds a verdict to (task, change, head, role,
// round), so review evidence cannot be created, altered or moved to another
// task, change, head, role or round by anything without the private key:
// agents and reviewer runs (which never hold it, §4.4), forge comments, and
// any writer of the store file other than the daemon. It does not protect
// against the daemon process itself or a holder of the key, and it does not
// detect a deleted or rolled-back review row (removing the latest round lets
// the previous one count) or a change to other store state; those rely on the
// ownership and modes of the data and secrets locations (§8.1).
//
// The gate reads reviews from the store only; forge comments, task-key
// markers and native forge reviews are projections and never gate evidence.
// The package is pure: no git, no I/O, no clock and no logging. The caller
// supplies every input, including patch IDs and changed paths.
package gate
