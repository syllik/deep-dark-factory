package scheduler

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// Priority is a task's priority from the forge label priority:high|normal|low;
// the empty value means PriorityNormal.
type Priority string

// Priority values.
const (
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

// Kind tells a follow-up task from a normal one; the empty value means
// KindNormal.
type Kind string

// Kind values.
const (
	KindNormal   Kind = "normal"
	KindFollowup Kind = "followup"
)

// Task is one task of the snapshot.
type Task struct {
	// Key identifies the task; keys are unique in one Input.
	Key model.TaskKey
	// State is the task's conveyor state.
	State model.TaskState
	// Flags lists the task's flags; any flag takes it off the conveyor.
	Flags []model.TaskFlag
	// Zones are the zones the task declares; they match by exact string
	// equality.
	Zones []string
	// Dependencies are the keys of the tasks this task depends on.
	Dependencies []model.TaskKey
	// Priority orders ready work; empty means PriorityNormal.
	Priority Priority
	// Kind marks follow-ups; empty means KindNormal.
	Kind Kind
	// CreatedAt is the task's creation time, used for oldest-first order.
	CreatedAt time.Time
	// WantsRun reports whether the task wants a new run now.
	WantsRun bool
	// Containers is the number of containers the next run needs; at least
	// 1 when WantsRun is set.
	Containers int
}

// Input is one scheduling snapshot.
type Input struct {
	// Tasks are the tasks of the snapshot, in any order.
	Tasks []Task
	// Pause is the global pause set as reason strings; any non-empty set
	// admits nothing.
	Pause []string
	// MaxConcurrentContainers is limits.maxConcurrentContainers; nil means
	// no cap.
	MaxConcurrentContainers *int
	// RunningContainers is the number of containers already running.
	RunningContainers int
	// HostCapacity is carried but not used for admission until #34.
	HostCapacity ports.Capacity
}

// Admission is a task admitted to start a new run.
type Admission struct {
	// Key is the admitted task.
	Key model.TaskKey
	// State is the task's state at decision time.
	State model.TaskState
	// Containers is the number of containers the run needs.
	Containers int
}

// ReasonKind is the kind of a blocking reason.
type ReasonKind string

// ReasonKind values.
const (
	// ReasonDependency: a dependency is not a task in state done.
	ReasonDependency ReasonKind = "dependency"
	// ReasonZone: a declared zone is held by another task.
	ReasonZone ReasonKind = "zone"
)

// Reason is one reason a task is blocked.
type Reason struct {
	// Kind is the kind of reason.
	Kind ReasonKind
	// Dependency is the unsatisfied dependency key when Kind is
	// ReasonDependency.
	Dependency model.TaskKey
	// Zone is the held zone when Kind is ReasonZone.
	Zone string
}

// Transition is a task that must change between ready and waiting.
type Transition struct {
	// Key is the task.
	Key model.TaskKey
	// Reasons lists why the task is blocked: unsatisfied dependencies by
	// key, then held zones by name. It is empty for a move back to ready.
	Reasons []Reason
}

// Decision is the result of Decide.
type Decision struct {
	// Admit lists the tasks that may start a new run, in admission order.
	Admit []Admission
	// ToWaiting lists the ready tasks that are blocked, ordered by key.
	ToWaiting []Transition
	// ToReady lists the waiting tasks whose blockers are cleared, ordered
	// by key; a waiting task that is admitted is listed too.
	ToReady []Transition
}

// band is a candidate's priority band; lower goes first.
type band int

const (
	bandNone band = iota
	bandFixingMerge
	bandCIReview
	bandReady
)

// classify returns the admission band of state and whether a task in state
// holds its zones.
func classify(state model.TaskState) (b band, holds bool) {
	switch state {
	case model.TaskStateFixing, model.TaskStateMerge:
		return bandFixingMerge, true
	case model.TaskStateCI, model.TaskStateReview:
		return bandCIReview, true
	case model.TaskStateRunning, model.TaskStatePublishing:
		return bandNone, true
	case model.TaskStateReady, model.TaskStateWaiting, model.TaskStatePlanning:
		return bandReady, false
	case model.TaskStateInbox, model.TaskStateDone:
		return bandNone, false
	}
	return bandNone, false
}

// priorityRank orders priorities high, normal, low.
func priorityRank(p Priority) int {
	switch p {
	case PriorityHigh:
		return 0
	case PriorityNormal:
		return 1
	case PriorityLow:
		return 2
	}
	return 1 // empty: normal
}

// validate checks in for input errors.
func validate(in Input) error {
	if in.MaxConcurrentContainers != nil && *in.MaxConcurrentContainers < 0 {
		return fmt.Errorf("scheduler: negative cap %d", *in.MaxConcurrentContainers)
	}
	if in.RunningContainers < 0 {
		return fmt.Errorf("scheduler: negative running container count %d", in.RunningContainers)
	}
	seen := make(map[model.TaskKey]bool, len(in.Tasks))
	for _, t := range in.Tasks {
		if seen[t.Key] {
			return fmt.Errorf("scheduler: duplicate task key %q", t.Key)
		}
		seen[t.Key] = true
		if _, err := model.ParseTaskState(string(t.State)); err != nil {
			return fmt.Errorf("scheduler: task %q: %w", t.Key, err)
		}
		for _, f := range t.Flags {
			if _, err := model.ParseTaskFlag(string(f)); err != nil {
				return fmt.Errorf("scheduler: task %q: %w", t.Key, err)
			}
		}
		switch t.Priority {
		case "", PriorityHigh, PriorityNormal, PriorityLow:
		default:
			return fmt.Errorf("scheduler: task %q: unknown priority %q", t.Key, t.Priority)
		}
		switch t.Kind {
		case "", KindNormal, KindFollowup:
		default:
			return fmt.Errorf("scheduler: task %q: unknown kind %q", t.Key, t.Kind)
		}
		if t.WantsRun && t.Containers < 1 {
			return fmt.Errorf("scheduler: task %q: wants a run with %d containers, want at least 1", t.Key, t.Containers)
		}
	}
	return nil
}

// candidate is a task that may be admitted, with its sort keys.
type candidate struct {
	task      *Task
	band      band
	followup  bool
	priority  int
	satisfied int
}

// Decide returns the scheduling decision for in, or an error for invalid
// input. It never mutates in.
func Decide(in Input) (Decision, error) {
	if err := validate(in); err != nil {
		return Decision{}, err
	}

	done := make(map[model.TaskKey]bool)
	for i := range in.Tasks {
		if in.Tasks[i].State == model.TaskStateDone {
			done[in.Tasks[i].Key] = true
		}
	}

	// held maps a zone to the keys of the tasks holding it.
	held := make(map[string]map[model.TaskKey]bool)
	hold := func(t *Task) {
		for _, z := range t.Zones {
			if held[z] == nil {
				held[z] = make(map[model.TaskKey]bool)
			}
			held[z][t.Key] = true
		}
	}

	var cands []candidate
	for i := range in.Tasks {
		t := &in.Tasks[i]
		if len(t.Flags) > 0 {
			continue
		}
		b, holds := classify(t.State)
		if holds {
			hold(t)
		}
		if b == bandNone || !t.WantsRun {
			continue
		}
		cands = append(cands, candidate{
			task:      t,
			band:      b,
			followup:  t.Kind == KindFollowup,
			priority:  priorityRank(t.Priority),
			satisfied: satisfiedCount(t, done),
		})
	}

	slices.SortFunc(cands, func(a, b candidate) int {
		if c := cmp.Compare(a.band, b.band); c != 0 {
			return c
		}
		if a.band == bandReady {
			if a.followup != b.followup {
				if a.followup {
					return 1
				}
				return -1
			}
			if c := cmp.Compare(a.priority, b.priority); c != 0 {
				return c
			}
			if c := cmp.Compare(b.satisfied, a.satisfied); c != 0 {
				return c
			}
		}
		if c := a.task.CreatedAt.Compare(b.task.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.task.Key, b.task.Key)
	})

	var d Decision
	if len(in.Pause) > 0 {
		cands = nil // paused: no new runs
	}
	used := in.RunningContainers
	for _, c := range cands {
		t := c.task
		if t.State == model.TaskStateReady || t.State == model.TaskStateWaiting {
			if len(blockers(t, done, held)) > 0 {
				continue
			}
		}
		if in.MaxConcurrentContainers != nil && used+t.Containers > *in.MaxConcurrentContainers {
			break // strict order: nothing overtakes a candidate that does not fit
		}
		used += t.Containers
		d.Admit = append(d.Admit, Admission{Key: t.Key, State: t.State, Containers: t.Containers})
		hold(t)
	}

	for i := range in.Tasks {
		t := &in.Tasks[i]
		if len(t.Flags) > 0 {
			continue
		}
		switch t.State {
		case model.TaskStateReady:
			if r := blockers(t, done, held); len(r) > 0 {
				d.ToWaiting = append(d.ToWaiting, Transition{Key: t.Key, Reasons: r})
			}
		case model.TaskStateWaiting:
			if len(blockers(t, done, held)) == 0 {
				d.ToReady = append(d.ToReady, Transition{Key: t.Key})
			}
		case model.TaskStateInbox, model.TaskStatePlanning, model.TaskStateRunning,
			model.TaskStatePublishing, model.TaskStateCI, model.TaskStateReview,
			model.TaskStateFixing, model.TaskStateMerge, model.TaskStateDone:
		}
	}
	byKey := func(a, b Transition) int { return cmp.Compare(a.Key, b.Key) }
	slices.SortFunc(d.ToWaiting, byKey)
	slices.SortFunc(d.ToReady, byKey)
	return d, nil
}

// satisfiedCount returns the number of distinct dependencies of t that are
// tasks in state done.
func satisfiedCount(t *Task, done map[model.TaskKey]bool) int {
	seen := make(map[model.TaskKey]bool, len(t.Dependencies))
	n := 0
	for _, dep := range t.Dependencies {
		if !seen[dep] && done[dep] {
			n++
		}
		seen[dep] = true
	}
	return n
}

// blockers returns why t is blocked: unsatisfied dependencies sorted by key,
// then zones held by another task sorted by name, each listed once.
func blockers(t *Task, done map[model.TaskKey]bool, held map[string]map[model.TaskKey]bool) []Reason {
	var deps []model.TaskKey
	for _, dep := range t.Dependencies {
		if !done[dep] && !slices.Contains(deps, dep) {
			deps = append(deps, dep)
		}
	}
	slices.Sort(deps)
	var zones []string
	for _, z := range t.Zones {
		if slices.Contains(zones, z) {
			continue
		}
		for k := range held[z] {
			if k != t.Key {
				zones = append(zones, z)
				break
			}
		}
	}
	slices.Sort(zones)
	var r []Reason
	for _, dep := range deps {
		r = append(r, Reason{Kind: ReasonDependency, Dependency: dep})
	}
	for _, z := range zones {
		r = append(r, Reason{Kind: ReasonZone, Zone: z})
	}
	return r
}
