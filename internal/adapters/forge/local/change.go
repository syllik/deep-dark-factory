package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// FindChangeByTaskKey returns the change of key from the change records
// only (docs/adapters/forge-local.md#task-key-marker): the open change,
// else the most recent by createdAt, ties going to the greatest id (Task
// 10b agentDefault 3). It does no Git read. An unknown key gives
// found=false and a nil error; two open changes for one key are an error.
func (f *Forge) FindChangeByTaskKey(ctx context.Context, key model.TaskKey) (ports.ChangeInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.ChangeInfo{}, false, err
	}
	k, err := model.ParseTaskKey(string(key))
	if err != nil {
		return ports.ChangeInfo{}, false, fmt.Errorf("find change: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, found, err := f.changeForKeyLocked(ctx, k)
	if err != nil || !found {
		return ports.ChangeInfo{}, false, err
	}
	return r.info(), true, nil
}

// changeForKeyLocked picks the change of key as FindChangeByTaskKey does.
// The caller holds f.mu.
func (f *Forge) changeForKeyLocked(ctx context.Context, key model.TaskKey) (changeRecord, bool, error) {
	all, err := f.listChanges(ctx)
	if err != nil {
		return changeRecord{}, false, err
	}
	marker := markerPrefix + string(key)
	var open, latest *changeRecord
	for i := range all {
		r := &all[i]
		if r.Marker != marker {
			continue
		}
		if r.State == changeOpen {
			if open != nil {
				return changeRecord{}, false, fmt.Errorf("find change: changes %s and %s are both open for key %s", open.ID, r.ID, key)
			}
			open = r
		}
		// listChanges is ordered by id, so a later record with an equal
		// createdAt has the greater id and wins the tie.
		if latest == nil || !r.CreatedAt.Before(latest.CreatedAt) {
			latest = r
		}
	}
	switch {
	case open != nil:
		return *open, true, nil
	case latest != nil:
		return *latest, true, nil
	}
	return changeRecord{}, false, nil
}

// info is the ports view of a change record.
func (r changeRecord) info() ports.ChangeInfo {
	return ports.ChangeInfo{ID: r.ID, Branch: r.Branch, HeadSHA: r.HeadSHA, Body: r.Body, Open: r.State == changeOpen}
}

// PublishChange creates or updates the change record of r.Key for the
// pushed head and starts a check attempt for a head that has none
// (docs/adapters/forge-local.md#other-writes, Task 10b agentDefault 3). It
// is idempotent per key: an identical second call writes nothing. The tip
// of r.Branch in the clean clone, after a fetch, must equal r.HeadSHA.
func (f *Forge) PublishChange(ctx context.Context, mode ports.WriteMode, r ports.PublishRequest) (ports.ChangeInfo, error) {
	live, err := f.guard(mode, "PublishChange",
		"key", string(r.Key), "branch", r.Branch, "baseBranch", r.BaseBranch, "headSha", r.HeadSHA)
	if err != nil || !live {
		return ports.ChangeInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.ChangeInfo{}, err
	}
	key, err := f.validatePublish(ctx, r)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish change: %w", err)
	}
	// f.mu covers the fetch and tip read too, so the record matches the tip
	// it was checked against (Task 10b agentDefault 9).
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.cfg.Git.Fetch(ctx, f.cfg.CloneDir); err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish change: %w", err)
	}
	tip, err := f.branchTipLocked(ctx, r.Branch)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish change: %w", err)
	}
	if tip != r.HeadSHA {
		return ports.ChangeInfo{}, fmt.Errorf("publish change: branch %q tip is %s, not head %s", r.Branch, tip, r.HeadSHA)
	}
	rec, err := f.upsertChangeLocked(ctx, key, r)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish change: %w", err)
	}
	attempts, err := f.listAttempts(ctx, rec.HeadSHA)
	if err != nil {
		return ports.ChangeInfo{}, fmt.Errorf("publish change: %w", err)
	}
	if len(attempts) == 0 {
		a, err := f.startAttemptLocked(ctx, rec.HeadSHA, rec.Marker)
		if err != nil {
			return ports.ChangeInfo{}, fmt.Errorf("publish change: %w", err)
		}
		f.cfg.Logger.Info("forge-local check attempt started",
			"changeId", rec.ID, "head", a.HeadSHA, "attempt", a.Attempt, "runId", a.RunID)
	}
	return rec.info(), nil
}

// validatePublish checks a publish request before any storage access and
// returns its key.
func (f *Forge) validatePublish(ctx context.Context, r ports.PublishRequest) (model.TaskKey, error) {
	key, err := model.ParseTaskKey(string(r.Key))
	if err != nil {
		return "", err
	}
	switch {
	case r.BaseBranch != f.cfg.IntegrationBranch:
		return "", fmt.Errorf("base branch %q is not the integration branch %q", r.BaseBranch, f.cfg.IntegrationBranch)
	case r.Branch == f.cfg.IntegrationBranch:
		return "", fmt.Errorf("branch %q is the integration branch", r.Branch)
	case !validSHA(r.HeadSHA):
		return "", fmt.Errorf("head sha %q is not a full lowercase sha", r.HeadSHA)
	case strings.TrimSpace(r.Title) == "":
		return "", errors.New("empty title")
	}
	if err := refFormatOK(ctx, f.cfg.Git, f.cfg.CloneDir, r.Branch); err != nil {
		return "", err
	}
	return key, nil
}

// branchTipLocked returns the commit refs/heads/<branch> names in the
// clean clone; a missing branch is an error. The caller holds f.mu.
func (f *Forge) branchTipLocked(ctx context.Context, branch string) (string, error) {
	out, err := f.cfg.Git.Run(ctx, f.cfg.CloneDir, nil,
		"rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		var ce *git.CommandError
		if errors.As(err, &ce) {
			return "", fmt.Errorf("branch %q is missing on the remote", branch)
		}
		return "", fmt.Errorf("read tip of branch %q: %w", branch, err)
	}
	tip := string(bytes.TrimSpace(out))
	if !validSHA(tip) {
		return "", fmt.Errorf("tip of branch %q: unexpected output %q", branch, tip)
	}
	return tip, nil
}

// upsertChangeLocked applies a validated publish request to the change
// records of key: an open change on the same branch is rewritten only when
// head, title or body differ; an open change on another branch is an
// error; with none open a new open change is written. The caller holds
// f.mu.
func (f *Forge) upsertChangeLocked(ctx context.Context, key model.TaskKey, r ports.PublishRequest) (changeRecord, error) {
	cur, found, err := f.changeForKeyLocked(ctx, key)
	if err != nil {
		return changeRecord{}, err
	}
	if found && cur.State == changeOpen {
		if cur.Branch != r.Branch {
			return changeRecord{}, fmt.Errorf("open change %s of key %s is on branch %q, not %q", cur.ID, key, cur.Branch, r.Branch)
		}
		if cur.HeadSHA == r.HeadSHA && cur.Title == r.Title && cur.Body == r.Body {
			return cur, nil
		}
		cur.HeadSHA, cur.Title, cur.Body = r.HeadSHA, r.Title, r.Body
		if err := f.storeChange(cur); err != nil {
			return changeRecord{}, err
		}
		return cur, nil
	}
	id := f.cfg.IDs.NewID()
	if !safeID(id) {
		return changeRecord{}, fmt.Errorf("change id %q is not path-safe", id)
	}
	all, err := f.listChanges(ctx)
	if err != nil {
		return changeRecord{}, err
	}
	for _, c := range all {
		if c.ID == id {
			return changeRecord{}, fmt.Errorf("change id %q is already used", id)
		}
	}
	rec := changeRecord{
		Schema: changeSchema, ID: id, Kind: kindChange, Branch: r.Branch, BaseBranch: r.BaseBranch,
		HeadSHA: r.HeadSHA, Title: r.Title, Body: r.Body, Marker: markerPrefix + string(key),
		State: changeOpen, CreatedAt: f.cfg.Clock.Now(),
	}
	if err := f.storeChange(rec); err != nil {
		return changeRecord{}, err
	}
	return rec, nil
}

// UpdateChangeBody rewrites the body of an existing change record
// (docs/adapters/forge-local.md#other-writes). An unknown or path-unsafe
// id is an error.
func (f *Forge) UpdateChangeBody(ctx context.Context, mode ports.WriteMode, changeID, body string) error {
	live, err := f.guard(mode, "UpdateChangeBody", "changeId", changeID)
	if err != nil || !live {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !safeID(changeID) {
		return fmt.Errorf("update change body: change id %q is not path-safe", changeID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.loadChange(changeID)
	if err != nil {
		return fmt.Errorf("update change body: %w", err)
	}
	if r.Body == body {
		return nil
	}
	r.Body = body
	if err := f.storeChange(r); err != nil {
		return fmt.Errorf("update change body: %w", err)
	}
	return nil
}
