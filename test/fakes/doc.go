// Package fakes holds the fake agent and the in-memory port fakes that DDF's
// tests wire in place of real adapters (M2 plan Task 9, OD-6): a scripted
// agent whose results are validated by the real role schemas, a patch
// builder for executor edits, and fakes for the notify, secrets, clock, ID
// source and instance lock ports.
//
// Every fake is safe for concurrent use. Nothing outside test/** may import
// this package (test/boundaries rule test-tree).
package fakes
