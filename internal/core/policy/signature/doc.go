// Package signature signs and verifies stored review verdicts with Ed25519
// (spec §6.6 Review signatures). It defines the canonical JSON of the signed
// payload, the findings digest over the reviewer-result findings (§6.3), the
// key ID and a Signer and Verifier.
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
// The package is pure: the private key only enters as a ports.Secret
// argument, and no function reads files, the environment, flags or the clock.
package signature
