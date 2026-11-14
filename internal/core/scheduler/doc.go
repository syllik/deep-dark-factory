// Package scheduler decides, for one snapshot of tasks, which tasks may
// start a new run now, which ready tasks must wait and which waiting tasks
// may become ready again (spec §4.1, M2 plan Task 7).
//
// The rules follow docs/concepts/lifecycle.md#ready-and-waiting and the
// global pause of docs/concepts/lifecycle.md#global-pause: flagged tasks are
// off the conveyor, zones are held from running to merge, open dependencies
// and held zones make a ready task wait, and candidates are admitted in the
// lifecycle's priority order under the optional count cap
// limits.maxConcurrentContainers.
//
// The package is pure: it reads no clock, randomness, environment, file,
// network or process, and never mutates its input. Host capacity is carried
// in the input but not used for admission; capacity admission and the spec
// §9 disk floor land with #34.
package scheduler
