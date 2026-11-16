package policy

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// ErrInvalidChangedPath reports a changed-path entry that policy refuses.
var ErrInvalidChangedPath = errors.New("invalid changed path")

// ErrNoChangedPaths reports an empty change, which is never classified or
// passed.
var ErrNoChangedPaths = errors.New("no changed paths")

// ChangeStatus is how a changed path differs (policy.md#changed-paths).
type ChangeStatus string

// ChangeStatus values.
const (
	ChangeStatusAdded       ChangeStatus = "added"
	ChangeStatusModified    ChangeStatus = "modified"
	ChangeStatusDeleted     ChangeStatus = "deleted"
	ChangeStatusTypeChanged ChangeStatus = "type-changed"
	ChangeStatusRenamed     ChangeStatus = "renamed"
)

// ParseChangeStatus returns the ChangeStatus spelled s; an unknown or empty
// value is an error wrapping ErrInvalidChangedPath.
func ParseChangeStatus(s string) (ChangeStatus, error) {
	return parseValue(ErrInvalidChangedPath, "change status", s, ChangeStatusAdded, ChangeStatusModified, ChangeStatusDeleted, ChangeStatusTypeChanged, ChangeStatusRenamed)
}

// Mode is a git tree entry mode.
type Mode string

// Mode values.
const (
	ModeAbsent     Mode = "000000"
	ModeRegular    Mode = "100644"
	ModeExecutable Mode = "100755"
	ModeSymlink    Mode = "120000"
	ModeGitlink    Mode = "160000"
)

// ParseMode returns the Mode spelled s; an unknown or empty value is an error
// wrapping ErrInvalidChangedPath.
func ParseMode(s string) (Mode, error) {
	return parseValue(ErrInvalidChangedPath, "mode", s, ModeAbsent, ModeRegular, ModeExecutable, ModeSymlink, ModeGitlink)
}

// ChangedPath is one entry of a change's changed paths, as git diff --raw -M
// --no-abbrev and --numstat -M report it (policy.md#changed-paths).
type ChangedPath struct {
	Status  ChangeStatus
	Path    string // the new path; for deleted, the deleted path
	OldPath string // renamed only
	OldMode Mode
	NewMode Mode
	OldBlob string // full lowercase hex; all zeros on the absent side
	NewBlob string
	Added   int
	Deleted int
	Binary  bool // numstat reported '-'
}

// candidates returns the paths every path rule matches for c: Path and, for
// a rename, OldPath (spec §6.2 rule 7).
func (c ChangedPath) candidates() []string {
	if c.Status == ChangeStatusRenamed {
		return []string{c.Path, c.OldPath}
	}
	return []string{c.Path}
}

// validatePath checks a repository-relative path.
func validatePath(p string) error {
	switch {
	case p == "":
		return errors.New("empty path")
	case strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/"):
		return fmt.Errorf("path %q has a leading or trailing '/'", p)
	case strings.ContainsRune(p, 0):
		return fmt.Errorf("path %q contains NUL", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." || strings.EqualFold(seg, ".git") {
			return fmt.Errorf("path %q has a %q segment", p, seg)
		}
	}
	if path.Clean(p) != p {
		return fmt.Errorf("path %q is not clean", p)
	}
	return nil
}

// validateBlob checks a full lowercase hex object ID.
func validateBlob(b string) error {
	if len(b) != 40 && len(b) != 64 {
		return fmt.Errorf("blob %q is not 40 or 64 characters", b)
	}
	for _, r := range b {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("blob %q is not lowercase hex", b)
		}
	}
	return nil
}

func isZeroBlob(b string) bool { return strings.Trim(b, "0") == "" }

func validateChangedPath(c ChangedPath) error {
	if _, err := ParseChangeStatus(string(c.Status)); err != nil {
		return fmt.Errorf("status: %w", err)
	}
	if _, err := ParseMode(string(c.OldMode)); err != nil {
		return fmt.Errorf("oldMode: %w", err)
	}
	if _, err := ParseMode(string(c.NewMode)); err != nil {
		return fmt.Errorf("newMode: %w", err)
	}
	if err := validatePath(c.Path); err != nil {
		return fmt.Errorf("path: %w", err)
	}
	renamed := c.Status == ChangeStatusRenamed
	if renamed != (c.OldPath != "") {
		return errors.New("oldPath: set if and only if renamed")
	}
	if renamed {
		if err := validatePath(c.OldPath); err != nil {
			return fmt.Errorf("oldPath: %w", err)
		}
		if c.OldPath == c.Path {
			return errors.New("oldPath: equals path")
		}
	}
	oldAbsent, newAbsent := c.OldMode == ModeAbsent, c.NewMode == ModeAbsent
	var ok bool
	switch c.Status {
	case ChangeStatusAdded:
		ok = oldAbsent && !newAbsent
	case ChangeStatusDeleted:
		ok = !oldAbsent && newAbsent
	case ChangeStatusModified, ChangeStatusTypeChanged, ChangeStatusRenamed:
		ok = !oldAbsent && !newAbsent
	}
	if !ok {
		return fmt.Errorf("modes %s -> %s do not fit status %s", c.OldMode, c.NewMode, c.Status)
	}
	for _, b := range []struct {
		field, blob string
		absent      bool
	}{{"oldBlob", c.OldBlob, oldAbsent}, {"newBlob", c.NewBlob, newAbsent}} {
		if err := validateBlob(b.blob); err != nil {
			return fmt.Errorf("%s: %w", b.field, err)
		}
		if isZeroBlob(b.blob) != b.absent {
			return fmt.Errorf("%s: all zeros if and only if its mode is absent", b.field)
		}
	}
	if len(c.OldBlob) != len(c.NewBlob) {
		return errors.New("newBlob: length differs from oldBlob")
	}
	if c.Added < 0 || c.Deleted < 0 {
		return fmt.Errorf("negative line count %d/%d", c.Added, c.Deleted)
	}
	return nil
}

// validateChangedPaths checks every entry and that no path has two
// non-deleted entries.
func validateChangedPaths(paths []ChangedPath) error {
	if len(paths) == 0 {
		return ErrNoChangedPaths
	}
	live := make(map[string]bool, len(paths))
	for i, c := range paths {
		if err := validateChangedPath(c); err != nil {
			return fmt.Errorf("%w: paths[%d]: %w", ErrInvalidChangedPath, i, err)
		}
		if c.Status == ChangeStatusDeleted {
			continue
		}
		if live[c.Path] {
			return fmt.Errorf("%w: paths[%d]: second non-deleted entry for %q", ErrInvalidChangedPath, i, c.Path)
		}
		live[c.Path] = true
	}
	return nil
}
