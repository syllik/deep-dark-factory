// Package fake is an in-memory implementation of ports.Sandbox for tests.
//
// A Host holds every resource, scripted run step and counter, so that
// several Sandbox instances on one Host model a daemon restart: a later
// instance lists, kills and destroys the resources an earlier one created.
// Run never executes anything: it returns the outcome scripted with
// Host.ScriptRun for the cell's task key. The package imports no os, os/exec,
// syscall or net package; a test in this directory enforces that.
package fake
