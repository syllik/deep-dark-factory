package git

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is a git release number.
type Version struct {
	Major, Minor, Patch int
}

// MinVersion is the oldest supported git: 2.39.0 is the first release with
// 'git patch-id --verbatim' (spec §6.6 rule 4).
func MinVersion() Version { return Version{Major: 2, Minor: 39, Patch: 0} }

// ErrGitTooOld matches a *VersionError.
var ErrGitTooOld = errors.New("git is older than the minimum version")

// VersionError reports a git older than MinVersion.
type VersionError struct {
	Found, Min Version
}

// Error names the found and the minimum version.
func (e *VersionError) Error() string {
	return fmt.Sprintf("git %s is older than the minimum %s", e.Found, e.Min)
}

// Is reports true for ErrGitTooOld.
func (e *VersionError) Is(target error) bool { return target == ErrGitTooOld }

// ParseVersion parses the output of 'git version': the prefix 'git version '
// and three dot-separated integers. Anything after the third integer
// ('.rc0', '.windows.1', ' (Apple Git-154)') is ignored.
func ParseVersion(out string) (Version, error) {
	rest, ok := strings.CutPrefix(out, "git version ")
	if !ok {
		return Version{}, fmt.Errorf("parse git version %q: missing prefix", out)
	}
	var nums [3]int
	for i := range nums {
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		n, err := strconv.Atoi(rest[:end])
		if err != nil {
			return Version{}, fmt.Errorf("parse git version %q: %w", out, err)
		}
		nums[i], rest = n, rest[end:]
		if i < 2 {
			if rest, ok = strings.CutPrefix(rest, "."); !ok {
				return Version{}, fmt.Errorf("parse git version %q: want three numbers", out)
			}
		}
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
}

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool {
	return cmp.Or(cmp.Compare(v.Major, o.Major), cmp.Compare(v.Minor, o.Minor), cmp.Compare(v.Patch, o.Patch)) < 0
}

// String formats v as '2.39.0'.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}
