// Package local is the forge-local adapter: DDF's forge for a plain Git
// remote with no forge (docs/adapters/forge-local.md, spec §10.1).
//
// It keeps its own storage of plain files under
// <data>/forge-local/<project id>/, never the DDF store. This package holds
// the storage, the task, answer, change, check-attempt and review records,
// inbox ingestion, the task-side forge methods, the verify check of each
// head, the update of a change branch from its base, the compare-and-swap
// merge pinned to the change head (spec §6.6) and revert. It runs git only
// through the core's git.Runner, in the daemon's clean clone of the remote,
// pushes only through one lease helper, and runs verify only in a sandbox
// cell through the core's local gate. It starts no goroutine; every write
// goes through one write-mode guard first.
package local
