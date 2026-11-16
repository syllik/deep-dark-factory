package patch

import (
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// ExportArgs returns the trusted-wrapper export of spec §8.3, 'git add -A &&
// git diff --cached --binary <base>', as two git argument lists without the
// program name or global options. The extra diff options keep copy-local diff
// config from changing the patch.
//
// The adapter's wrapper runs these steps in the container, in the copy's
// root, and writes the second step's stdout to /out/change.patch. The host
// must never run them in an agent-written copy: a filter, fsmonitor or hook
// defined in that copy's .git/config would then run on the host, because the
// runner's fixed options cannot override arbitrary filter.<name> keys. Tests
// run them through Runner.Run only in a copy the test itself wrote.
func ExportArgs(base string) ([][]string, error) {
	if !validSHA(base) {
		return nil, fmt.Errorf("export against %q: %w", base, git.ErrInvalidSHA)
	}
	return [][]string{
		{"add", "-A"},
		{"diff", "--cached", "--binary", "--full-index", "--no-renames", "--no-ext-diff", "--no-textconv",
			"--no-color", "--no-relative", "--src-prefix=a/", "--dst-prefix=b/", base, "--"},
	}, nil
}
