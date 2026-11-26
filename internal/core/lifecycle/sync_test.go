package lifecycle

import (
	"errors"
	"reflect"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

func TestRouteSyncMerge(t *testing.T) {
	l := DefaultLimits()
	s := Status{State: model.TaskStateMerge}
	owner := func(reason string) Outcome {
		return Outcome{State: model.TaskStateMerge, Flag: model.TaskFlagNeedsOwner, Reason: reason}
	}
	cases := []struct {
		name string
		r    SyncResult
		runs []model.Run
		want Outcome
	}{
		{"up-to-date", SyncResultUpToDate, nil, Outcome{State: model.TaskStateMerge}},
		{"merged", SyncResultMerged, nil, Outcome{State: model.TaskStateCI, Reason: "merged"}},
		{"conflict within bound", SyncResultConflict, boundsRuns(1, model.RunPurposeSync), Outcome{State: model.TaskStateFixing, Action: ActionSyncBatch, Reason: "conflict"}},
		{"conflict over bound", SyncResultConflict, boundsRuns(2, model.RunPurposeSync), owner("bound exceeded: sync batches per change (2)")},
		{"conflict over run budget", SyncResultConflict, boundsRuns(20, model.RunPurposeReview), owner("bound exceeded: agent runs per task (20)")},
		{"workflow-changed", SyncResultWorkflowChanged, boundsRuns(2, model.RunPurposeSync), owner(ReasonWorkflowFiles)},
		{"forge-refused-workflows", SyncResultForgeRefusedWorkflows, boundsRuns(2, model.RunPurposeSync), owner(ReasonWorkflowFiles)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := append([]model.Run(nil), c.runs...)
			got, err := RouteSync(s, c.r, c.runs, l)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
			if !reflect.DeepEqual(c.runs, before) {
				t.Error("input runs mutated")
			}
		})
	}
}

// TestRouteSyncWorkflowConsumesNoBatch shows a workflow refusal leaves the
// sync budget untouched: with one batch left, a later conflict still gets it.
func TestRouteSyncWorkflowConsumesNoBatch(t *testing.T) {
	l := DefaultLimits()
	s := Status{State: model.TaskStateMerge}
	prior := boundsRuns(1, model.RunPurposeSync)
	if o, _ := RouteSync(s, SyncResultWorkflowChanged, prior, l); o.Action != ActionNone || o.Reason != ReasonWorkflowFiles {
		t.Fatalf("workflow-changed = %+v", o)
	}
	if o, err := RouteSync(s, SyncResultConflict, prior, l); err != nil || o.Action != ActionSyncBatch {
		t.Errorf("conflict after workflow refusal = %+v, %v, want ActionSyncBatch", o, err)
	}
}

func TestRouteSyncPublishing(t *testing.T) {
	l := DefaultLimits()
	s := Status{State: model.TaskStatePublishing}
	want := Outcome{State: model.TaskStatePublishing, Flag: model.TaskFlagNeedsOwner, Reason: ReasonWorkflowFiles}
	for _, r := range []SyncResult{SyncResultWorkflowChanged, SyncResultForgeRefusedWorkflows} {
		got, err := RouteSync(s, r, nil, l)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, %v, want %+v", r, got, err, want)
		}
	}
	for _, r := range []SyncResult{SyncResultUpToDate, SyncResultMerged, SyncResultConflict, "bogus"} {
		if _, err := RouteSync(s, r, nil, l); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", r, err)
		}
	}
}

func TestRouteSyncInvalid(t *testing.T) {
	l := DefaultLimits()
	if _, err := RouteSync(Status{State: model.TaskStateMerge}, "bogus", nil, l); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("unknown result err = %v, want ErrInvalidInput", err)
	}
	if _, err := RouteSync(Status{State: model.TaskStateReview}, SyncResultMerged, nil, l); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("review state err = %v, want ErrInvalidTransition", err)
	}
	flagged := Status{State: model.TaskStateMerge, Flags: []model.TaskFlag{model.TaskFlagHold}}
	if _, err := RouteSync(flagged, SyncResultMerged, nil, l); !errors.Is(err, ErrFlagged) {
		t.Errorf("flagged err = %v, want ErrFlagged", err)
	}
}
