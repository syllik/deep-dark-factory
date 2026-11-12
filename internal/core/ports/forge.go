package ports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// parseEnum returns the member of values spelled s, or an error naming what.
func parseEnum[T ~string](what, s string, values []T) (T, error) {
	for _, v := range values {
		if string(v) == s {
			return v, nil
		}
	}
	return "", fmt.Errorf("parse %s: unknown value %q", what, s)
}

// unmarshalEnum parses b with parse and stores the result in dst.
func unmarshalEnum[T ~string](dst *T, b []byte, parse func(string) (T, error)) error {
	v, err := parse(string(b))
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// WriteMode selects whether a forge write is performed.
type WriteMode string

// WriteMode values.
const (
	WriteModeLive   WriteMode = "live"
	WriteModeDryRun WriteMode = "dry-run"
)

// ParseWriteMode returns the WriteMode spelled s; an unknown or empty value is an error.
func ParseWriteMode(s string) (WriteMode, error) {
	return parseEnum("write mode", s, []WriteMode{WriteModeLive, WriteModeDryRun})
}

// UnmarshalText implements encoding.TextUnmarshaler through ParseWriteMode.
func (wm *WriteMode) UnmarshalText(b []byte) error {
	return unmarshalEnum(wm, b, ParseWriteMode)
}

// ErrHeadMoved is returned by Forge.Merge when the change head no longer
// equals the pinned SHA (spec §5.2, head moved during merge).
var ErrHeadMoved = errors.New("change head moved")

// Identity is a forge or notify account: an intake actor, an answer author,
// a reviewer or a message sender.
type Identity struct {
	ID, Name string
}

// CandidateTask is a forge object that may become a task.
type CandidateTask struct {
	Source      model.TaskSource
	Title, Body string
	Labels      []string
}

// NewTask is a task DDF creates on the forge. Key goes into the hidden
// ddf:task-key marker (spec §4.5).
type NewTask struct {
	Key         model.TaskKey
	Title, Body string
	Labels      []string
}

// ChangeInfo is the forge's view of a change.
type ChangeInfo struct {
	ID, Branch, HeadSHA, Body string
	Open                      bool
}

// PublishRequest opens or updates the change of a task for a pushed head.
type PublishRequest struct {
	Key                         model.TaskKey
	Branch, BaseBranch, HeadSHA string
	Title, Body                 string
}

// Check is one CI check result for a head.
type Check struct {
	Name, HeadSHA     string
	Completed, Passed bool
}

// Answer is one answer on a task, with its author.
type Answer struct {
	ID, Body string
	Author   Identity
	At       time.Time
}

// ReviewPost is a review posted on one head of a change under Reviewer's
// identity; the port fixes no number of reviewer identities.
type ReviewPost struct {
	ChangeID, HeadSHA string
	Reviewer          Identity
	Verdict           model.ReviewVerdict
	Body              string
}

// Forge is the forge port (spec §4.2). Every write takes a WriteMode
// second. In WriteModeDryRun the adapter logs the write, performs no forge
// mutation and returns the zero result and a nil error. For any other
// value than WriteModeLive, including the zero value, it returns an error
// and writes nothing.
type Forge interface {
	ListCandidateTasks(ctx context.Context) ([]CandidateTask, error)
	ReadIntakeActor(ctx context.Context, source model.TaskSource) (Identity, error)
	// FindChangeByTaskKey reports found=false when no change carries key.
	FindChangeByTaskKey(ctx context.Context, key model.TaskKey) (change ChangeInfo, found bool, err error)
	ReadChecks(ctx context.Context, headSHA string) ([]Check, error)
	ReadAnswers(ctx context.Context, source model.TaskSource) ([]Answer, error)

	CreateTask(ctx context.Context, mode WriteMode, t NewTask) (model.TaskSource, error)
	PublishChange(ctx context.Context, mode WriteMode, r PublishRequest) (ChangeInfo, error)
	UpdateChangeBody(ctx context.Context, mode WriteMode, changeID, body string) error
	// UpdateBranchFromBase returns the new head SHA.
	UpdateBranchFromBase(ctx context.Context, mode WriteMode, changeID string) (string, error)
	RerunChecks(ctx context.Context, mode WriteMode, headSHA string) error
	PostReview(ctx context.Context, mode WriteMode, r ReviewPost) error
	// Merge merges changeID only if its head equals headSHA, else returns
	// ErrHeadMoved; it returns the merge commit SHA.
	Merge(ctx context.Context, mode WriteMode, changeID, headSHA string) (string, error)
	// Revert opens a change that reverts mergeSHA for the task key.
	Revert(ctx context.Context, mode WriteMode, key model.TaskKey, mergeSHA string) (ChangeInfo, error)
	CloseTask(ctx context.Context, mode WriteMode, source model.TaskSource, comment string) error
	ReopenTask(ctx context.Context, mode WriteMode, source model.TaskSource, comment string) error
	SetProjectionLabels(ctx context.Context, mode WriteMode, source model.TaskSource, labels []string) error
}
