// Package local is the forge-local adapter: DDF's forge for a plain Git
// remote with no forge (docs/adapters/forge-local.md, spec §10.1).
//
// It keeps its own storage of plain files under
// <data>/forge-local/<project id>/, never the DDF store. This package holds
// the storage, the task and answer records, inbox ingestion and the
// task-side forge methods. It runs no git and starts no goroutine; every
// write goes through one write-mode guard first.
package local
