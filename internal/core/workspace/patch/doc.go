// Package patch holds the patch export arguments, the changed paths of a
// change and the patch apply of the clean clone. It runs git only through
// internal/core/workspace/git, never on its own
// (spec §8.3: docs/specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication).
package patch
