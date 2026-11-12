// Package smoke checks that the pinned runtime dependencies build and work
// together with CGO_ENABLED=0.
package smoke

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
	_ "modernc.org/sqlite"
)

func TestRuntimeDependencies(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "smoke.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("sqlite query: got %d, err %v", one, err)
	}

	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(`{"type":"object","required":["name"]}`))
	if err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("smoke.json", doc); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	sch, err := c.Compile("smoke.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	var v map[string]any
	if err := yaml.Unmarshal([]byte("name: ddf\n"), &v); err != nil {
		t.Fatalf("unmarshal yaml: %v", err)
	}
	if err := sch.Validate(v); err != nil {
		t.Fatalf("validate yaml against schema: %v", err)
	}
	if err := sch.Validate(map[string]any{}); err == nil {
		t.Fatal("validate: want error for a document without name")
	}

	ok, err := doublestar.Match(".github/**", ".github/workflows/ci.yml")
	if err != nil || !ok {
		t.Fatalf("doublestar match: got %v, err %v", ok, err)
	}
}
