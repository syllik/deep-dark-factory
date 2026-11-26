package lifecycle_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/lifecycle"
	"github.com/syllik/deep-dark-factory/internal/core/model"
)

var revertNow = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

const (
	revertSHA = "1111111111111111111111111111111111111111"
	otherSHA  = "2222222222222222222222222222222222222222"
)

func ddfChange() model.Change {
	return model.Change{
		Key:       "k1",
		ID:        "c1",
		Kind:      model.ChangeKindChange,
		MergeSHA:  revertSHA,
		CreatedAt: revertNow.Add(-48 * time.Hour),
	}
}

func revertChange(created time.Time) model.Change {
	return model.Change{Key: "k0", ID: "r0", Kind: model.ChangeKindRevert, CreatedAt: created}
}

func openRevert() lifecycle.RevertDecision {
	return lifecycle.RevertDecision{
		Actions:  []lifecycle.Action{lifecycle.ActionOpenRevert},
		Key:      "k1",
		ChangeID: "c1",
		State:    model.TaskStateReady,
		Risk:     model.RiskClassRevert,
		Reason:   "red on DDF merge commit",
	}
}

func revertRed() lifecycle.RevertDecision {
	d := openRevert()
	d.Actions = []lifecycle.Action{lifecycle.ActionOpenRevert, lifecycle.ActionPause, lifecycle.ActionNotify}
	d.Pause = lifecycle.PauseRevertRed
	d.Reason = "second DDF-attributed red within 24 h"
	return d
}

func TestRouteMergeCI(t *testing.T) {
	redRerun := lifecycle.MergeCI{SHA: revertSHA, Red: true, Rerun: true}
	tests := []struct {
		name    string
		ci      lifecycle.MergeCI
		changes []model.Change
		want    lifecycle.RevertDecision
	}{
		{
			name:    "green: no action",
			ci:      lifecycle.MergeCI{SHA: revertSHA},
			changes: []model.Change{ddfChange()},
		},
		{
			name:    "red before rerun: rerun CI",
			ci:      lifecycle.MergeCI{SHA: revertSHA, Red: true},
			changes: []model.Change{ddfChange()},
			want:    lifecycle.RevertDecision{Actions: []lifecycle.Action{lifecycle.ActionRerunCI}, Reason: "red before rerun"},
		},
		{
			name:    "red after rerun on DDF merge commit: open revert, reopen to ready",
			ci:      redRerun,
			changes: []model.Change{ddfChange()},
			want:    openRevert(),
		},
		{
			name:    "red on a human merge: no action",
			ci:      lifecycle.MergeCI{SHA: otherSHA, Red: true, Rerun: true},
			changes: []model.Change{ddfChange()},
			want:    lifecycle.RevertDecision{Reason: "red on a merge commit DDF did not make"},
		},
		{
			name:    "red on a human merge with a recent revert: no action",
			ci:      lifecycle.MergeCI{SHA: otherSHA, Red: true, Rerun: true},
			changes: []model.Change{ddfChange(), revertChange(revertNow.Add(-time.Hour))},
			want:    lifecycle.RevertDecision{Reason: "red on a merge commit DDF did not make"},
		},
		{
			name:    "prior revert exactly 24h before now: revert-red pause and notify",
			ci:      redRerun,
			changes: []model.Change{revertChange(revertNow.Add(-24 * time.Hour)), ddfChange()},
			want:    revertRed(),
		},
		{
			name:    "prior revert 24h+1ms before now: no pause",
			ci:      redRerun,
			changes: []model.Change{revertChange(revertNow.Add(-24*time.Hour - time.Millisecond)), ddfChange()},
			want:    openRevert(),
		},
		{
			name:    "prior revert created after now: within the window",
			ci:      redRerun,
			changes: []model.Change{revertChange(revertNow.Add(time.Minute)), ddfChange()},
			want:    revertRed(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.changes)
			got, err := lifecycle.RouteMergeCI(tt.ci, tt.changes, revertNow)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("decision = %+v, want %+v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.changes, before) {
				t.Fatalf("input changes mutated")
			}
		})
	}
}

func TestRouteMergeCIInvalid(t *testing.T) {
	dup := ddfChange()
	dup.ID = "c2"
	badKind := ddfChange()
	badKind.Kind = "bogus"
	tests := []struct {
		name    string
		ci      lifecycle.MergeCI
		changes []model.Change
	}{
		{name: "empty SHA", ci: lifecycle.MergeCI{Red: true, Rerun: true}},
		{name: "two changes share the merge commit", ci: lifecycle.MergeCI{SHA: revertSHA, Red: true, Rerun: true}, changes: []model.Change{ddfChange(), dup}},
		{name: "unknown change kind", ci: lifecycle.MergeCI{SHA: revertSHA, Red: true, Rerun: true}, changes: []model.Change{badKind}},
		{name: "revert without creation time", ci: lifecycle.MergeCI{SHA: revertSHA, Red: true, Rerun: true}, changes: []model.Change{ddfChange(), revertChange(time.Time{})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lifecycle.RouteMergeCI(tt.ci, tt.changes, revertNow)
			if !errors.Is(err, lifecycle.ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
