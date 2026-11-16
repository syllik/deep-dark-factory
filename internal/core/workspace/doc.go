// Package workspace is the daemon-owned workspace module: clean clones,
// per-run copies handed to the Sandbox port as content (never as a host
// path), patch export and apply, and patch-id. The host never executes
// repository content (spec §8.3,
// docs/specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication).
//
// Subpackage git is the only place that runs the git CLI: the runner with its
// fixed environment and options, the clean clone and the per-run copy.
// Subpackage patch exports and applies patches through it.
package workspace
