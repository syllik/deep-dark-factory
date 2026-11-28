// Package e2e holds the end-to-end tests of the engine: each test opens the
// daemon through daemon.Open from YAML config bytes, on forge-local over a
// bare repository, sandbox-fake, the scripted fake agent and a file-backed
// SQLite store, ticks it, and observes the result only from outside the
// engine: git reads of the bare remote, a second handle on the store file
// and forge-local reopened on the same data directory.
//
// Nothing here uses the network, Docker or a real agent CLI.
package e2e
