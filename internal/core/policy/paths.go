package policy

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrInvalidHardStopEntry reports an approved hard-stop entry policy refuses.
var ErrInvalidHardStopEntry = errors.New("invalid hard-stop entry")

// HardStopEntry is a changed path matching a default hard-stop glob, with its
// state in the tree: mode and blob ID, or deleted (spec §6.4).
type HardStopEntry struct {
	Path    string
	Deleted bool
	Mode    Mode   // empty when Deleted
	BlobID  string // empty when Deleted
}

// PathResult is the path policy and hard-stop outcome over a change's
// changed paths.
type PathResult struct {
	Forbidden  []string        // candidate paths matching a forbidden glob, sorted and unique
	Symlinks   []string        // added or changed symlinks, sorted and unique
	HardStops  []HardStopEntry // every hard-stop entry, sorted by Path
	Unapproved []HardStopEntry // HardStops not exactly equal to an approved entry
}

// Passes reports whether the change has no forbidden path, no added or
// changed symlink and no unapproved hard-stop entry (spec §6.6 condition 6).
func (r PathResult) Passes() bool {
	return len(r.Forbidden) == 0 && len(r.Symlinks) == 0 && len(r.Unapproved) == 0
}

func validateHardStopEntry(e HardStopEntry) error {
	if err := validatePath(e.Path); err != nil {
		return fmt.Errorf("path: %w", err)
	}
	if e.Deleted {
		if e.Mode != "" || e.BlobID != "" {
			return errors.New("deleted entry has a mode or blob")
		}
		return nil
	}
	m, err := ParseMode(string(e.Mode))
	if err != nil {
		return fmt.Errorf("mode: %w", err)
	}
	if m == ModeAbsent {
		return errors.New("mode: absent on a present entry")
	}
	if err := validateBlob(e.BlobID); err != nil {
		return fmt.Errorf("blobID: %w", err)
	}
	if isZeroBlob(e.BlobID) {
		return errors.New("blobID: all zeros on a present entry")
	}
	return nil
}

// CheckPaths evaluates path policy and hard-stop entries over a change's
// changed paths (spec §6.4, §6.5); approved lists the hard-stop entries the
// owner approved.
func CheckPaths(p Profile, paths []ChangedPath, approved []HardStopEntry) (PathResult, error) {
	if err := p.Validate(); err != nil {
		return PathResult{}, err
	}
	if err := validateChangedPaths(paths); err != nil {
		return PathResult{}, err
	}
	for i, e := range approved {
		if err := validateHardStopEntry(e); err != nil {
			return PathResult{}, fmt.Errorf("%w: approved[%d]: %w", ErrInvalidHardStopEntry, i, err)
		}
	}
	forbidden, stops := slices.Concat(forbiddenGlobs(), p.Forbidden), hardStopGlobs()
	live := make(map[string]ChangedPath, len(paths))
	for _, c := range paths {
		if c.Status != ChangeStatusDeleted {
			live[c.Path] = c
		}
	}
	var r PathResult
	seen := map[string]bool{}
	for i, c := range paths {
		// policy.md forbidden paths: only an added or changed symlink is refused.
		if c.Status != ChangeStatusDeleted && c.NewMode == ModeSymlink {
			r.Symlinks = append(r.Symlinks, c.Path)
		}
		for _, name := range c.candidates() {
			f, err := matchAny(forbidden, name, true)
			if err != nil {
				return PathResult{}, fmt.Errorf("paths[%d]: %w", i, err)
			}
			if f {
				r.Forbidden = append(r.Forbidden, name)
			}
			h, err := matchAny(stops, name, true)
			if err != nil {
				return PathResult{}, fmt.Errorf("paths[%d]: %w", i, err)
			}
			if !h || seen[name] {
				continue
			}
			seen[name] = true
			// spec §6.4: the state comes from the non-deleted entry whose Path
			// it is; otherwise the path is deleted in the tree.
			e := HardStopEntry{Path: name, Deleted: true}
			if l, ok := live[name]; ok {
				e = HardStopEntry{Path: name, Mode: l.NewMode, BlobID: l.NewBlob}
			}
			r.HardStops = append(r.HardStops, e)
		}
	}
	slices.Sort(r.Forbidden)
	r.Forbidden = slices.Compact(r.Forbidden)
	slices.Sort(r.Symlinks)
	r.Symlinks = slices.Compact(r.Symlinks)
	slices.SortFunc(r.HardStops, func(a, b HardStopEntry) int { return strings.Compare(a.Path, b.Path) })
	for _, e := range r.HardStops {
		if !slices.Contains(approved, e) {
			r.Unapproved = append(r.Unapproved, e)
		}
	}
	return r, nil
}
