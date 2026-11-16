package config

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestNewCompiler_BlocksExternalRefs proves SchemeURLLoader loads no file or
// network reference.
func TestNewCompiler_BlocksExternalRefs(t *testing.T) {
	for _, ref := range []string{"file:///etc/example.json", "https://example.invalid/example.json"} {
		t.Run(ref, func(t *testing.T) {
			c := newCompiler()
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(`{"$ref": "` + ref + `"}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := c.AddResource("mem:///test.json", doc); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Compile("mem:///test.json"); err == nil {
				t.Errorf("compile with $ref %s succeeded", ref)
			}
		})
	}
}
