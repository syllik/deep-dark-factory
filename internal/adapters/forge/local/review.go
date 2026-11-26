package local

import (
	"context"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// PostReview writes the review projection
// reviews/<change id>/<head sha>/<reviewer id>.json, replacing the same
// reviewer's projection for that head (docs/adapters/forge-local.md#other-writes).
// Any full lowercase SHA may be the head, since a projection belongs to the
// reviewed head; the change record must exist and the reviewer id must be
// path-safe (Task 10b agentDefault 6). The signed review stays in the DDF
// store.
func (f *Forge) PostReview(ctx context.Context, mode ports.WriteMode, r ports.ReviewPost) error {
	live, err := f.guard(mode, "PostReview",
		"changeId", r.ChangeID, "headSha", r.HeadSHA, "reviewerId", r.Reviewer.ID)
	if err != nil || !live {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch {
	case !safeID(r.ChangeID):
		return fmt.Errorf("post review: change id %q is not path-safe", r.ChangeID)
	case !validSHA(r.HeadSHA):
		return fmt.Errorf("post review: head sha %q is not a full lowercase sha", r.HeadSHA)
	case !safeID(r.Reviewer.ID):
		return fmt.Errorf("post review: reviewer id %q is not path-safe", r.Reviewer.ID)
	}
	verdict, err := model.ParseReviewVerdict(string(r.Verdict))
	if err != nil {
		return fmt.Errorf("post review: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.loadChange(r.ChangeID); err != nil {
		return fmt.Errorf("post review: %w", err)
	}
	rec := reviewRecord{
		Schema: reviewSchema, ChangeID: r.ChangeID, HeadSHA: r.HeadSHA,
		Reviewer: reviewer{ID: r.Reviewer.ID, Name: r.Reviewer.Name},
		Verdict:  string(verdict), Body: r.Body,
	}
	if err := f.storeReview(rec); err != nil {
		return fmt.Errorf("post review: %w", err)
	}
	return nil
}
