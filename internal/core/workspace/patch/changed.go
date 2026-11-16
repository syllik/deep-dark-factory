package patch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/internal/core/workspace/git"
)

// ChangedPaths returns the changed paths (policy.md#changed-paths) between
// commit from and commit or tree to in the repository dir (the clean clone).
// Equal trees give an empty list. A record DDF cannot map (a copy, an
// unmerged entry, an unknown mode, a malformed record) is an error, never a
// partial list.
func ChangedPaths(ctx context.Context, r *git.Runner, dir, from, to string) ([]policy.ChangedPath, error) {
	if !validSHA(from) || !validSHA(to) {
		return nil, fmt.Errorf("changed paths of %q..%q: %w", from, to, git.ErrInvalidSHA)
	}
	for _, sha := range []string{from, to} {
		if _, err := r.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", sha+"^{tree}"); err != nil {
			return nil, fmt.Errorf("changed paths: tree of %s: %w", sha, err)
		}
	}
	var out [2][]byte
	for i, args := range [][]string{{"--raw", "-M", "--no-abbrev"}, {"--numstat", "-M"}} {
		var err error
		if out[i], err = r.Run(ctx, dir, nil, append(append([]string{"diff-tree", "-r", "-z"}, args...), from, to)...); err != nil {
			return nil, fmt.Errorf("changed paths: %w", err)
		}
	}
	paths, err := changedFromOutput(out[0], out[1])
	if err != nil {
		return nil, fmt.Errorf("changed paths of %s..%s: %w", from, to, err)
	}
	return paths, nil
}

// changedFromOutput zips raw and numstat records by position; each pair's
// paths must be equal.
func changedFromOutput(raw, num []byte) ([]policy.ChangedPath, error) {
	paths, err := parseRaw(raw)
	if err != nil {
		return nil, err
	}
	stats, err := parseNumstat(num)
	if err != nil {
		return nil, err
	}
	if len(paths) != len(stats) {
		return nil, fmt.Errorf("%d raw records, %d numstat records", len(paths), len(stats))
	}
	for i, s := range stats {
		c := &paths[i]
		if s.path != c.Path || s.oldPath != c.OldPath {
			return nil, fmt.Errorf("record %d: raw path %q (from %q), numstat path %q (from %q)", i, c.Path, c.OldPath, s.path, s.oldPath)
		}
		c.Added, c.Deleted, c.Binary = s.added, s.deleted, s.binary
	}
	return paths, nil
}

// numstat is one 'git diff --numstat -z' record; 'git apply --numstat -z'
// prints the same format.
type numstat struct {
	path, oldPath  string // oldPath: renames only
	added, deleted int
	binary         bool
}

// parseRaw parses 'git diff --raw -z --no-abbrev' records: ':<oldmode>
// <newmode> <oldoid> <newoid> <status>' NUL <path> NUL, for R<score> NUL
// <old> NUL <new> NUL. Counts are left zero.
func parseRaw(out []byte) ([]policy.ChangedPath, error) {
	fields, err := nulRecords(out)
	if err != nil {
		return nil, err
	}
	paths := []policy.ChangedPath{}
	for i := 0; i < len(fields); {
		head, ok := strings.CutPrefix(fields[i], ":")
		meta := strings.Split(head, " ")
		if !ok || len(meta) != 5 || !validSHA(meta[2]) || !validSHA(meta[3]) {
			return nil, fmt.Errorf("malformed raw record %q", fields[i])
		}
		c := policy.ChangedPath{OldBlob: meta[2], NewBlob: meta[3]}
		if c.OldMode, err = policy.ParseMode(meta[0]); err == nil {
			c.NewMode, err = policy.ParseMode(meta[1])
		}
		if err != nil {
			return nil, fmt.Errorf("raw record %q: %w", fields[i], err)
		}
		n := 1
		switch s := meta[4]; {
		case s == "A":
			c.Status = policy.ChangeStatusAdded
		case s == "M":
			c.Status = policy.ChangeStatusModified
		case s == "D":
			c.Status = policy.ChangeStatusDeleted
		case s == "T":
			c.Status = policy.ChangeStatusTypeChanged
		case len(s) > 1 && s[0] == 'R' && digits(s[1:]):
			c.Status, n = policy.ChangeStatusRenamed, 2
		default:
			return nil, fmt.Errorf("raw record %q: unsupported status %q", fields[i], s)
		}
		names, err := pathFields(fields[i+1:], n)
		if err != nil {
			return nil, fmt.Errorf("raw record %q: %w", fields[i], err)
		}
		c.Path = names[n-1]
		if n == 2 {
			c.OldPath = names[0]
		}
		paths = append(paths, c)
		i += 1 + n
	}
	return paths, nil
}

// parseNumstat parses 'git diff --numstat -z' records: '<added>TAB<deleted>
// TAB<path>' NUL, for a rename '<added>TAB<deleted>TAB' NUL <old> NUL <new>
// NUL; '-' '-' is binary.
func parseNumstat(out []byte) ([]numstat, error) {
	fields, err := nulRecords(out)
	if err != nil {
		return nil, err
	}
	stats := []numstat{}
	for i := 0; i < len(fields); i++ {
		added, rest, ok1 := strings.Cut(fields[i], "\t")
		deleted, p, ok2 := strings.Cut(rest, "\t")
		s := numstat{path: p, binary: added == "-" && deleted == "-"}
		if !ok1 || !ok2 || !s.binary && (!digits(added) || !digits(deleted)) {
			return nil, fmt.Errorf("malformed numstat record %q", fields[i])
		}
		if !s.binary {
			if s.added, err = strconv.Atoi(added); err == nil {
				s.deleted, err = strconv.Atoi(deleted)
			}
			if err != nil {
				return nil, fmt.Errorf("numstat record %q: %w", fields[i], err)
			}
		}
		if p == "" {
			names, err := pathFields(fields[i+1:], 2)
			if err != nil {
				return nil, fmt.Errorf("numstat rename record: %w", err)
			}
			s.oldPath, s.path = names[0], names[1]
			i += 2
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// nulRecords splits NUL-terminated output into its fields; output that does
// not end in NUL is truncated.
func nulRecords(out []byte) ([]string, error) {
	if len(out) == 0 {
		return nil, nil
	}
	s, ok := strings.CutSuffix(string(out), "\x00")
	if !ok {
		return nil, errors.New("output is not NUL-terminated")
	}
	return strings.Split(s, "\x00"), nil
}

// pathFields returns the first n fields, each a non-empty path.
func pathFields(fields []string, n int) ([]string, error) {
	if len(fields) < n {
		return nil, errors.New("missing path field")
	}
	for _, f := range fields[:n] {
		if f == "" {
			return nil, errors.New("empty path field")
		}
	}
	return fields[:n], nil
}

func digits(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

// validSHA accepts only a full lowercase hex SHA-1 or SHA-256, so an argument
// is never a ref or an option. git's own check is unexported.
func validSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
