package lifecycle

import (
	"errors"
	"fmt"
	"slices"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// Sentinel errors; returned errors wrap one of them.
var (
	// ErrFlagged reports a task carrying a flag: it is off the conveyor.
	ErrFlagged = errors.New("task is flagged")
	// ErrInvalidTransition reports a transition from a state that has none.
	ErrInvalidTransition = errors.New("invalid transition")
	// ErrInvalidInput reports an unusable input, such as an unknown state or
	// an inconsistent role result.
	ErrInvalidInput = errors.New("invalid input")
)

// Status is a task's conveyor position as stored.
type Status struct {
	State   model.TaskState
	Flags   []model.TaskFlag
	HitStop bool // sticky: the change hit a hard stop or path policy
}

// Action is a side effect the caller performs with an Outcome.
type Action string

// Action values.
const (
	ActionNone           Action = ""
	ActionBackoff        Action = "backoff"         // retry later with backoff
	ActionNotify         Action = "notify"          // notify the owner
	ActionSync           Action = "sync"            // sync the branch with the base
	ActionCreateChildren Action = "create-children" // create the Planner's children
	ActionRerunReviewer  Action = "rerun-reviewer"  // rerun Outcome.Roles with complete inputs
	ActionAddReviewer    Action = "add-reviewer"    // add Outcome.Roles to the reviewer set
	ActionMerge          Action = "merge"           // squash merge pinned to the head
	ActionLeave          Action = "leave"           // leave the conveyor; open change left alone
)

// Outcome is what the caller applies to the task.
type Outcome struct {
	State      model.TaskState // the next state; equal to the input state to stay
	Flag       model.TaskFlag  // the flag to add; empty for none
	SetHitStop bool            // set the sticky hit_stop bit
	Action     Action
	Roles      []model.Role    // the reviewers of ActionRerunReviewer or ActionAddReviewer
	Risk       model.RiskClass // the raised risk class of ActionAddReviewer
	AskedHead  string          // the head to record as asked about
	Reason     string
}

// validate checks the state and flags of s; a flagged task is ErrFlagged.
func validate(s Status) error {
	if err := validateState(s); err != nil {
		return err
	}
	if len(s.Flags) > 0 {
		return fmt.Errorf("%w: %v", ErrFlagged, s.Flags)
	}
	return nil
}

// validateState checks the state and flags of s without refusing flags.
func validateState(s Status) error {
	if _, err := model.ParseTaskState(string(s.State)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	for _, f := range s.Flags {
		if _, err := model.ParseTaskFlag(string(f)); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	return nil
}

func stay(s Status) Outcome { return Outcome{State: s.State} }

func ownerOutcome(s Status, reason string) Outcome {
	return Outcome{State: s.State, Flag: model.TaskFlagNeedsOwner, Reason: reason}
}

// happy maps each happy-path from-state to its to-state.
var happy = map[model.TaskState]model.TaskState{
	model.TaskStateInbox:      model.TaskStatePlanning,
	model.TaskStateReady:      model.TaskStateRunning,
	model.TaskStatePublishing: model.TaskStateCI,
	model.TaskStateCI:         model.TaskStateReview,
	model.TaskStateMerge:      model.TaskStateDone,
}

// Advance returns the happy-path transition from s.State. Every other state
// is ErrInvalidTransition; planning, running and review move only through
// verdict routing.
func Advance(s Status) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	to, ok := happy[s.State]
	if !ok {
		return Outcome{}, fmt.Errorf("%w: no happy-path transition from %s", ErrInvalidTransition, s.State)
	}
	return Outcome{State: to}, nil
}

// SetFlag returns s with f added; it is idempotent and keeps flags sorted.
func SetFlag(s Status, f model.TaskFlag) (Status, error) {
	if _, err := model.ParseTaskFlag(string(f)); err != nil {
		return Status{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	out := s
	out.Flags = slices.Clone(s.Flags)
	if !slices.Contains(out.Flags, f) {
		out.Flags = append(out.Flags, f)
	}
	slices.Sort(out.Flags)
	return out, nil
}

// ClearFlag returns s without f; the state is unchanged, so a cleared task
// resumes where it stood.
func ClearFlag(s Status, f model.TaskFlag) Status {
	out := s
	out.Flags = slices.DeleteFunc(slices.Clone(s.Flags), func(g model.TaskFlag) bool { return g == f })
	if len(out.Flags) == 0 {
		out.Flags = nil
	}
	return out
}

// MergeResult is the outcome of a merge attempt.
type MergeResult string

// MergeResult values.
const (
	MergeResultMerged     MergeResult = "merged"      // squash merge succeeded
	MergeResultHeadMoved  MergeResult = "head-moved"  // the head moved during the merge
	MergeResultTaskClosed MergeResult = "task-closed" // a human closed the task
)

// RouteMerge routes the result of a merge attempt of a task in merge.
func RouteMerge(s Status, r MergeResult) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	if s.State != model.TaskStateMerge {
		return Outcome{}, fmt.Errorf("%w: merge result in state %s", ErrInvalidTransition, s.State)
	}
	switch r {
	case MergeResultMerged:
		return Outcome{State: model.TaskStateDone}, nil
	case MergeResultHeadMoved:
		return Outcome{State: model.TaskStateCI, Reason: string(r)}, nil
	case MergeResultTaskClosed:
		return Outcome{State: s.State, Action: ActionLeave, Reason: string(r)}, nil
	}
	return Outcome{}, fmt.Errorf("%w: merge result %q", ErrInvalidInput, r)
}
