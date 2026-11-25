package publisher

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/workspace/patch"
)

func TestParseFailReason(t *testing.T) {
	for _, r := range []FailReason{FailEmpty, FailTooLarge, FailDoesNotApply, FailOutsideRepository,
		FailGitDir, FailGitlink, FailNodeModules, FailScanTooLarge} {
		t.Run(string(r), func(t *testing.T) {
			got, err := ParseFailReason(string(r))
			if err != nil || got != r {
				t.Fatalf("ParseFailReason(%q) = %q, %v", r, got, err)
			}
		})
	}
	for _, s := range []string{"", "unknown", "Empty", " empty"} {
		t.Run("refuse "+strconv.Quote(s), func(t *testing.T) {
			if got, err := ParseFailReason(s); err == nil {
				t.Fatalf("ParseFailReason(%q) = %q, want error", s, got)
			}
		})
	}
}

func TestParseStop(t *testing.T) {
	for _, s := range []Stop{StopSecrets, StopPathPolicy, StopHardStop} {
		t.Run(string(s), func(t *testing.T) {
			got, err := ParseStop(string(s))
			if err != nil || got != s {
				t.Fatalf("ParseStop(%q) = %q, %v", s, got, err)
			}
		})
	}
	for _, s := range []string{"", "unknown", "Secrets", "hard_stop"} {
		t.Run("refuse "+strconv.Quote(s), func(t *testing.T) {
			if got, err := ParseStop(s); err == nil {
				t.Fatalf("ParseStop(%q) = %q, want error", s, got)
			}
		})
	}
}

// patchReasons enumerates every constant of type patch.Reason declared in
// the patch package's non-test sources, so a new Reason without a mapping
// fails this test.
func patchReasons(t *testing.T) []patch.Reason {
	t.Helper()
	dir := filepath.Join("..", "workspace", "patch")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []patch.Reason
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, s := range gd.Specs {
				vs := s.(*ast.ValueSpec)
				if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "Reason" {
					continue
				}
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("%s: Reason constant is not a string literal", name)
					}
					val, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, patch.Reason(val))
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no patch.Reason constants found")
	}
	return out
}

func TestFailReasonFromPatch(t *testing.T) {
	for _, r := range patchReasons(t) {
		t.Run(string(r), func(t *testing.T) {
			if _, err := patch.ParseReason(string(r)); err != nil {
				t.Fatalf("patch.ParseReason(%q): %v", r, err)
			}
			got, err := failReasonFromPatch(r)
			if err != nil {
				t.Fatalf("failReasonFromPatch(%q): %v", r, err)
			}
			if string(got) != string(r) {
				t.Fatalf("failReasonFromPatch(%q) = %q, want the same spelling", r, got)
			}
			if _, err := ParseFailReason(string(got)); err != nil {
				t.Fatalf("ParseFailReason(%q): %v", got, err)
			}
		})
	}
	for _, r := range []patch.Reason{"", "unknown", patch.Reason(FailScanTooLarge)} {
		t.Run("refuse "+strconv.Quote(string(r)), func(t *testing.T) {
			if got, err := failReasonFromPatch(r); err == nil {
				t.Fatalf("failReasonFromPatch(%q) = %q, want error", r, got)
			}
		})
	}
}
