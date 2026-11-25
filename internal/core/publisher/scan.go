package publisher

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

// blobPair is the old and new blob of one changed path; old is "" when the
// path has no old side.
type blobPair struct{ path, old, new string }

// scan reads the blobs of every changed path that is not deleted (for a
// rename the old blob is the source blob) and looks for secrets that are
// new in the revision (spec §8.4). It returns Failed when a blob is too
// large to scan, NeedsOwner on a hit and a nil Outcome when clean.
func (p *Publisher) scan(ctx context.Context, red Redactor, paths []policy.ChangedPath) (Outcome, error) {
	var pairs []blobPair
	var tooLarge []string
	for _, c := range paths {
		if c.Status == policy.ChangeStatusDeleted {
			continue
		}
		bp := blobPair{path: c.Path, new: c.NewBlob}
		if strings.Trim(c.OldBlob, "0") != "" {
			bp.old = c.OldBlob
		}
		for _, sha := range []string{bp.old, bp.new} {
			if sha == "" {
				continue
			}
			n, err := p.blobSize(ctx, sha)
			if err != nil {
				return nil, err
			}
			if n > patch.MaxPatchBytes {
				tooLarge = append(tooLarge, c.Path)
				break
			}
		}
		pairs = append(pairs, bp)
	}
	if len(tooLarge) > 0 {
		return Failed{Reason: FailScanTooLarge, Paths: slices.Compact(slices.Sorted(slices.Values(tooLarge)))}, nil
	}
	var hitPaths, patterns []string
	loaded := false
	for _, bp := range pairs {
		var old []byte
		if bp.old != "" {
			b, err := p.blob(ctx, bp.old)
			if err != nil {
				return nil, err
			}
			old = b
		}
		nw, err := p.blob(ctx, bp.new)
		if err != nil {
			return nil, err
		}
		h := red.NewHits(old, nw)
		if h.Loaded || len(h.Patterns) > 0 {
			hitPaths = append(hitPaths, bp.path)
			patterns = append(patterns, h.Patterns...)
			loaded = loaded || h.Loaded
		}
	}
	if len(hitPaths) == 0 {
		return nil, nil
	}
	hitPaths = redactAll(red, slices.Compact(slices.Sorted(slices.Values(hitPaths))))
	patterns = slices.Compact(slices.Sorted(slices.Values(patterns)))
	var what []string
	if loaded {
		what = append(what, "a loaded secret value")
	}
	if len(patterns) > 0 {
		what = append(what, "secret patterns "+strings.Join(patterns, ", "))
	}
	q := fmt.Sprintf("Hard stop (secrets and credentials): the revision adds %s in %s. Nothing was published and the run failed. How should DDF proceed?",
		strings.Join(what, " and "), quoteAll(hitPaths))
	return NeedsOwner{Stop: StopSecrets, Category: "secrets and credentials", SecretPatterns: patterns,
		Paths: hitPaths, HitStop: true, FailedRun: true, Question: red.Redact(q)}, nil
}

// blobSize returns the size of blob sha in the clone.
func (p *Publisher) blobSize(ctx context.Context, sha string) (int64, error) {
	if !isFullSHA(sha) {
		return 0, fmt.Errorf("scan: blob %q is not a full sha", sha)
	}
	out, err := p.Git.Run(ctx, p.Clone, nil, "cat-file", "-s", sha)
	if err != nil {
		return 0, fmt.Errorf("scan: size of blob %s: %w", sha, err)
	}
	n, err := strconv.ParseInt(strings.TrimSuffix(string(out), "\n"), 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("scan: size of blob %s: unexpected output %q", sha, out)
	}
	return n, nil
}

// blob returns the content of blob sha in the clone.
func (p *Publisher) blob(ctx context.Context, sha string) ([]byte, error) {
	out, err := p.Git.Run(ctx, p.Clone, nil, "cat-file", "blob", sha)
	if err != nil {
		return nil, fmt.Errorf("scan: read blob %s: %w", sha, err)
	}
	return out, nil
}

// quoteAll quotes each path Go-style and joins them with ", ".
func quoteAll(paths []string) string {
	q := make([]string, 0, len(paths))
	for _, p := range paths {
		q = append(q, strconv.Quote(p))
	}
	return strings.Join(q, ", ")
}
