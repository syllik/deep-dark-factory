package lifecycle

import (
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// SyncResult is the outcome of a sync with the base (lifecycle.md#sync-with-base).
// The daemon maps the workspace's sync outcome to it.
type SyncResult string

// SyncResult values.
const (
	SyncResultUpToDate              SyncResult = "up-to-date"              // the branch already contains the base
	SyncResultMerged                SyncResult = "merged"                  // daemon-authored base merge pushed
	SyncResultConflict              SyncResult = "conflict"                // the base merge conflicts
	SyncResultWorkflowChanged       SyncResult = "workflow-changed"        // the base changed CI configuration
	SyncResultForgeRefusedWorkflows SyncResult = "forge-refused-workflows" // the forge refused a workflow write
)

// ReasonWorkflowFiles is the needs-owner reason when the base changed CI
// configuration the worker identity may not write.
const ReasonWorkflowFiles = "base changed workflow files; update the branch manually"

// RouteSync routes a sync result. For a task in merge: up-to-date stays (the
// gate is re-evaluated); merged goes to ci for the new head; conflict checks
// the sync bound against runs and goes to fixing with ActionSyncBatch; a
// workflow result is needs-owner with ReasonWorkflowFiles and consumes no
// batch. For a task in publishing (push of an Executor sync batch) only the
// workflow results are accepted.
func RouteSync(s Status, r SyncResult, runs []model.Run, l Limits) (Outcome, error) {
	if err := validate(s); err != nil {
		return Outcome{}, err
	}
	switch s.State {
	case model.TaskStateMerge:
		switch r {
		case SyncResultUpToDate:
			return stay(s), nil
		case SyncResultMerged:
			return Outcome{State: model.TaskStateCI, Reason: string(r)}, nil
		case SyncResultConflict:
			o, err := CheckStart(s, runs, model.Run{Role: model.RoleExecutor, Purpose: model.RunPurposeSync, Attempt: 1}, l)
			if err != nil || o.Flag != "" {
				return o, err
			}
			return Outcome{State: model.TaskStateFixing, Action: ActionSyncBatch, Reason: string(r)}, nil
		case SyncResultWorkflowChanged, SyncResultForgeRefusedWorkflows:
			return ownerOutcome(s, ReasonWorkflowFiles), nil
		}
	case model.TaskStatePublishing:
		switch r {
		case SyncResultWorkflowChanged, SyncResultForgeRefusedWorkflows:
			return ownerOutcome(s, ReasonWorkflowFiles), nil
		case SyncResultUpToDate, SyncResultMerged, SyncResultConflict:
		}
	case model.TaskStateInbox, model.TaskStatePlanning, model.TaskStateReady, model.TaskStateWaiting,
		model.TaskStateRunning, model.TaskStateCI, model.TaskStateReview, model.TaskStateFixing, model.TaskStateDone:
		return Outcome{}, fmt.Errorf("%w: sync result in state %s", ErrInvalidTransition, s.State)
	}
	return Outcome{}, fmt.Errorf("%w: sync result %q in state %s", ErrInvalidInput, r, s.State)
}
