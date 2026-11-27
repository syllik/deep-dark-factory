// Package daemon is the DDF engine (spec §4.4, §4.5): it wires the core
// modules to the adapters and advances tasks through the lifecycle one tick
// at a time.
//
// Open loads the project, projects and routing config, opens the store, the
// git runner, forge-local and the Publisher, and builds the Engine. Every
// forge write and the daemon push run inside an intent (spec §4.5): the
// intent is stored before the write and resolved after it, so a crash
// between the two is found by recovery and never retried blindly.
//
// Every agent run is stored, and the run budget is checked before each
// attempt. In review the required reviewers run on the change head; each
// review is signed with the review-signing key from the Secrets port, stored,
// and projected to the forge. In merge the gate is fed only from the store and
// git, and a passing gate merges the pinned head and closes the task.
// Correction and sync batches, pause and recovery of pending intents come
// with later tasks.
package daemon
