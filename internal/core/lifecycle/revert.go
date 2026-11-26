package lifecycle

import (
	"fmt"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// Action values of the revert rule and the global pause.
const (
	ActionRerunCI    Action = "rerun-ci"    // rerun the integration-branch CI once
	ActionOpenRevert Action = "open-revert" // open a revert change of the red merge commit
	ActionPause      Action = "pause"       // add RevertDecision.Pause to the global pause set
)

// RevertRedWindow is the window in which a prior DDF revert makes a new
// DDF-attributed red pause the daemon (lifecycle.md#revert).
const RevertRedWindow = 24 * time.Hour

// MergeCI is the integration-branch CI result on a merge commit.
type MergeCI struct {
	SHA   string // the merge commit
	Red   bool   // CI failed on SHA
	Rerun bool   // the one rerun already happened and is the result
}

// RevertDecision is what the caller applies for a MergeCI result.
type RevertDecision struct {
	// Actions in order: ActionRerunCI alone; or ActionOpenRevert, followed by
	// ActionPause and ActionNotify when the red is a revert-red. Empty for no
	// action.
	Actions  []Action
	Key      model.TaskKey   // the task to reopen with ActionOpenRevert
	ChangeID string          // the DDF change whose merge commit is red
	State    model.TaskState // the state the task reopens to: ready
	Risk     model.RiskClass // the risk class of the revert change
	Pause    PauseReason     // the reason of ActionPause
	Reason   string
}

// RouteMergeCI applies the revert rule (lifecycle.md#revert) to a CI result
// on the integration branch. changes are the project's DDF changes; now is
// the daemon's clock reading. Red before the rerun reruns CI. Red after the
// rerun on the merge commit of a DDF change of kind change opens a revert of
// risk class revert and reopens its task to ready; if a DDF revert change was
// created within RevertRedWindow before now (a creation time after now
// counts too), it also pauses with reason revert-red and notifies. A red
// commit that is no DDF change's merge commit is a human merge: no action.
func RouteMergeCI(ci MergeCI, changes []model.Change, now time.Time) (RevertDecision, error) {
	if ci.SHA == "" {
		return RevertDecision{}, fmt.Errorf("%w: CI result without a merge commit", ErrInvalidInput)
	}
	var target *model.Change
	recentRevert := false
	for i := range changes {
		c := &changes[i]
		switch c.Kind {
		case model.ChangeKindChange:
			if c.MergeSHA != ci.SHA {
				continue
			}
			if target != nil {
				return RevertDecision{}, fmt.Errorf("%w: changes %s and %s share merge commit %s", ErrInvalidInput, target.ID, c.ID, ci.SHA)
			}
			target = c
		case model.ChangeKindRevert:
			if c.CreatedAt.IsZero() {
				return RevertDecision{}, fmt.Errorf("%w: revert change %s without a creation time", ErrInvalidInput, c.ID)
			}
			if now.Sub(c.CreatedAt) <= RevertRedWindow {
				recentRevert = true
			}
		default:
			return RevertDecision{}, fmt.Errorf("%w: change %s kind %q", ErrInvalidInput, c.ID, c.Kind)
		}
	}
	if !ci.Red {
		return RevertDecision{}, nil
	}
	if !ci.Rerun {
		return RevertDecision{Actions: []Action{ActionRerunCI}, Reason: "red before rerun"}, nil
	}
	if target == nil {
		return RevertDecision{Reason: "red on a merge commit DDF did not make"}, nil
	}
	d := RevertDecision{
		Actions:  []Action{ActionOpenRevert},
		Key:      target.Key,
		ChangeID: target.ID,
		State:    model.TaskStateReady,
		Risk:     model.RiskClassRevert,
		Reason:   "red on DDF merge commit",
	}
	if recentRevert {
		d.Actions = append(d.Actions, ActionPause, ActionNotify)
		d.Pause = PauseRevertRed
		d.Reason = "second DDF-attributed red within 24 h"
	}
	return d, nil
}
