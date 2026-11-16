package fake_test

import (
	"go/build"
	"testing"
)

// TestImports_OnlyAllowedPackages guarantees the fake never runs workspace
// files: its non-test files import no os, os/exec, syscall or net package.
func TestImports_OnlyAllowedPackages(t *testing.T) {
	allowed := map[string]bool{
		"context": true, "errors": true, "fmt": true, "io/fs": true, "slices": true,
		"sort": true, "strconv": true, "strings": true, "sync": true, "maps": true,
		"github.com/syllik/deep-dark-factory/internal/core/ports": true,
		"github.com/syllik/deep-dark-factory/internal/core/model": true,
	}
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("import dir: %v", err)
	}
	if len(pkg.GoFiles) == 0 {
		t.Fatal("no non-test go files found")
	}
	for _, imp := range pkg.Imports {
		if !allowed[imp] {
			t.Errorf("non-test file imports %q, which is not allowed", imp)
		}
	}
}
