package fakes

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// BuildPatch returns a git-format patch that applies edits to workspace:
// whole-file hunks, text files only, mode 100644, files in path order. It
// refuses an invalid path, binary (NUL) content, a delete of a missing file,
// an edit that leaves a file unchanged and two edits of one path.
func BuildPatch(workspace fs.FS, edits []FileEdit) ([]byte, error) {
	if workspace == nil {
		return nil, errors.New("build patch: nil workspace")
	}
	sorted := slices.Clone(edits)
	slices.SortStableFunc(sorted, func(a, b FileEdit) int { return strings.Compare(a.Path, b.Path) })
	var out bytes.Buffer
	for i, e := range sorted {
		if i > 0 && sorted[i-1].Path == e.Path {
			return nil, fmt.Errorf("build patch: duplicate path %q", e.Path)
		}
		if err := writeFilePatch(&out, workspace, e); err != nil {
			return nil, fmt.Errorf("build patch %q: %w", e.Path, err)
		}
	}
	return out.Bytes(), nil
}

func writeFilePatch(out *bytes.Buffer, workspace fs.FS, e FileEdit) error {
	if err := checkPatchPath(e.Path); err != nil {
		return err
	}
	old, err := fs.ReadFile(workspace, e.Path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read workspace file: %w", err)
	}
	if exists && bytes.IndexByte(old, 0) >= 0 {
		return errors.New("workspace file is binary")
	}
	p := e.Path
	fmt.Fprintf(out, "diff --git a/%s b/%s\n", p, p)
	switch {
	case e.Delete:
		if !exists {
			return errors.New("delete of a missing file")
		}
		if len(e.Content) > 0 {
			return errors.New("delete with content")
		}
		out.WriteString("deleted file mode 100644\n")
		if len(old) == 0 {
			return nil
		}
		fmt.Fprintf(out, "--- a/%s\n+++ /dev/null\n", p)
	case !exists:
		if bytes.IndexByte(e.Content, 0) >= 0 {
			return errors.New("content is binary")
		}
		out.WriteString("new file mode 100644\n")
		if len(e.Content) == 0 {
			return nil
		}
		fmt.Fprintf(out, "--- /dev/null\n+++ b/%s\n", p)
	default:
		if bytes.IndexByte(e.Content, 0) >= 0 {
			return errors.New("content is binary")
		}
		if bytes.Equal(old, e.Content) {
			return errors.New("edit leaves the file unchanged")
		}
		fmt.Fprintf(out, "--- a/%s\n+++ b/%s\n", p, p)
	}
	writeHunk(out, old, e.Content)
	return nil
}

// checkPatchPath refuses paths that are not slash-separated relative paths
// inside the workspace, and paths git would quote in a diff header.
func checkPatchPath(p string) error {
	if !fs.ValidPath(p) || p == "." {
		return errors.New("invalid path")
	}
	for _, c := range []byte(p) {
		if c <= ' ' || c == 0x7f || c == '"' || c == '\\' {
			return errors.New("invalid path: needs quoting")
		}
	}
	return nil
}

// writeHunk writes one hunk replacing every line of old with every line of
// next; an empty side has the range 0,0.
func writeHunk(out *bytes.Buffer, old, next []byte) {
	ol, nl := splitLines(old), splitLines(next)
	fmt.Fprintf(out, "@@ -%s +%s @@\n", hunkRange(len(ol)), hunkRange(len(nl)))
	writeLines(out, '-', ol)
	writeLines(out, '+', nl)
}

func hunkRange(n int) string {
	if n == 0 {
		return "0,0"
	}
	return fmt.Sprintf("1,%d", n)
}

func splitLines(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(b, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func writeLines(out *bytes.Buffer, prefix byte, lines [][]byte) {
	for _, l := range lines {
		out.WriteByte(prefix)
		out.Write(l)
		if !bytes.HasSuffix(l, []byte("\n")) {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
}
