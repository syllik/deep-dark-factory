// Package policy holds DDF's deterministic policy functions: effective
// autonomy, the risk class and reviewer set, blocking findings, path policy
// and hard-stop entries over a change's changed paths (spec §6,
// docs/concepts/policy.md). Every function takes plain data and returns a
// value or an error; invalid input fails closed.
package policy
