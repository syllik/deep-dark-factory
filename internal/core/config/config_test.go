package config_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/syllik/deep-dark-factory/internal/core/config"
	"github.com/syllik/deep-dark-factory/internal/core/policy"
	"github.com/syllik/deep-dark-factory/schemas"
)

const minProject = "version: ddf.project/v1\nintegrationBranch: main\nverify: [make]\n"

func newLoader(t *testing.T) *config.Loader {
	t.Helper()
	l, err := config.NewLoader()
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	return l
}

// loadAny runs the loader for file ("project", "projects", "routing").
func loadAny(l *config.Loader, file string, data []byte) error {
	var err error
	switch file {
	case "project":
		_, err = l.Project(data)
	case "projects":
		_, err = l.Projects(data)
	case "routing":
		_, err = l.Routing(data)
	}
	return err
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoader_RealAndFullDocuments(t *testing.T) {
	l := newLoader(t)
	for _, tt := range []struct{ file, path string }{
		{"project", "../../../schemas/examples/project.yaml"},
		{"projects", "../../../schemas/examples/projects.yaml"},
		{"routing", "../../../schemas/examples/routing.yaml"},
		{"project", "../../../templates/project/.ddf/project.yaml"},
		{"project", "testdata/project-full.yaml"},
		{"projects", "testdata/projects-full.yaml"},
		{"routing", "testdata/routing-full.yaml"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			if err := loadAny(l, tt.file, readFile(t, tt.path)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProject_PolicyProfile_Example(t *testing.T) {
	p, err := newLoader(t).Project(readFile(t, "../../../schemas/examples/project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.PolicyProfile()
	if err != nil {
		t.Fatal(err)
	}
	want := policy.Profile{
		Autonomy:       policy.AutonomyMergeIntegration,
		LowMaxLines:    200,
		Docs:           []string{"docs/**", "**/*.md"},
		Sensitive:      []string{"src/payments/**", "src/auth/**", "migrations/**"},
		CIEntrypoints:  []string{"package.json", "scripts/ci/**"},
		Forbidden:      []string{"ops/**"},
		RequiredChecks: []string{"verify"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PolicyProfile = %+v, want %+v", got, want)
	}
	got.Docs[0], got.Forbidden[0], got.RequiredChecks[0] = "x", "x", "x"
	if p.Risk.Docs[0] != "docs/**" || p.Forbidden[0] != "ops/**" || p.Checks.Required[0] != "verify" {
		t.Error("changing the profile slices changed the project")
	}
}

func TestProject_PolicyProfile_RequiredChecks(t *testing.T) {
	l := newLoader(t)
	for _, tt := range []struct {
		name, extra string
		wantNil     bool
	}{
		{"checks absent", "", true},
		{"checks empty", "checks: {}\n", true},
		{"checks.required present", "checks: {required: [ci]}\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := l.Project([]byte(minProject + tt.extra))
			if err != nil {
				t.Fatal(err)
			}
			prof, err := p.PolicyProfile()
			if err != nil {
				t.Fatal(err)
			}
			if (prof.RequiredChecks == nil) != tt.wantNil {
				t.Errorf("RequiredChecks = %#v, want nil %v", prof.RequiredChecks, tt.wantNil)
			}
		})
	}
}

func TestLoader_Project_InvalidProfileFailsClosed(t *testing.T) {
	l := newLoader(t)
	for _, extra := range []string{`forbidden: ["/ops/**"]`, `risk: {docs: ["a["]}`} {
		t.Run(extra, func(t *testing.T) {
			_, err := l.Project([]byte(minProject + extra + "\n"))
			if !errors.Is(err, config.ErrInvalidConfig) || !errors.Is(err, policy.ErrInvalidProfile) {
				t.Errorf("err = %v, want ErrInvalidConfig and ErrInvalidProfile", err)
			}
		})
	}
}

// TestLoader_SchemaBeforeDecode proves schema validation runs before the
// typed decode: each failure carries a *jsonschema.ValidationError.
func TestLoader_SchemaBeforeDecode(t *testing.T) {
	l := newLoader(t)
	const routingMin = "tiers: {deep: [{adapter: a, provider: p, model: m}], standard: [{adapter: a, provider: p, model: m}]}\n"
	for _, tt := range []struct{ name, file, doc string }{
		{"project unknown key", "project", minProject + "bogus: 1\n"},
		{"project wrong type", "project", minProject + "planning: {maxAutoChildren: \"3\"}\n"},
		{"project missing required", "project", "version: ddf.project/v1\nverify: [make]\n"},
		{"project autonomy bogus", "project", minProject + "autonomy: bogus\n"},
		{"project yes stays a string", "project", minProject + "reviewers: {r2DifferentProvider: yes}\n"},
		{"projects unknown key", "projects", "projects: []\nbogus: 1\n"},
		{"projects wrong type", "projects", "projects: {}\n"},
		{"projects missing required", "projects", "limits: {}\n"},
		{"routing unknown key", "routing", routingMin + "bogus: 1\n"},
		{"routing wrong type", "routing", routingMin + "limits: {maxSubagentsPerRun: x}\n"},
		{"routing missing required", "routing", "tiers: {deep: [{adapter: a, provider: p, model: m}]}\n"},
		{"github without allowedActors", "projects", "projects: [{id: a, forge: github, repository: o/r, maxAutonomy: propose}]\n"},
		{"github with remote", "projects", "projects: [{id: a, forge: github, repository: o/r, remote: file:///x, maxAutonomy: propose, allowedActors: [m]}]\n"},
		{"local without remote", "projects", "projects: [{id: a, forge: local, maxAutonomy: propose}]\n"},
		{"local with repository", "projects", "projects: [{id: a, forge: local, remote: file:///x, repository: o/r, maxAutonomy: propose}]\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := loadAny(l, tt.file, []byte(tt.doc))
			var ve *jsonschema.ValidationError
			if !errors.Is(err, config.ErrInvalidConfig) || !errors.As(err, &ve) {
				t.Errorf("err = %v, want ErrInvalidConfig with a ValidationError", err)
			}
		})
	}
}

func TestLoader_Projects_Duplicates(t *testing.T) {
	l := newLoader(t)
	gh := func(id string) string {
		return "{id: " + id + ", forge: github, repository: o/r, maxAutonomy: propose, allowedActors: [m]}"
	}
	lo := func(id string) string {
		return "{id: " + id + ", forge: local, remote: file:///x, maxAutonomy: propose}"
	}
	for _, tt := range []struct{ name, a, b string }{
		{"duplicate id", gh("a"), lo("a")},
		{"duplicate github repository", gh("a"), gh("b")},
		{"duplicate local remote", lo("a"), lo("b")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := l.Projects([]byte("projects: [" + tt.a + ", " + tt.b + "]\n"))
			if !errors.Is(err, config.ErrInvalidConfig) {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
	if _, err := l.Projects([]byte("projects: [" + gh("a") + ", " + lo("b") + "]\n")); err != nil {
		t.Errorf("distinct projects: %v", err)
	}
}

func TestLoader_YAMLSafety(t *testing.T) {
	l := newLoader(t)
	big := minProject + "image: \"" + strings.Repeat("x", 1<<20) + "\"\n"
	deep := minProject + "zones: " + strings.Repeat("[", 40) + strings.Repeat("]", 40) + "\n"
	laughs := "a: &a [x, x]\nb: &b [*a, *a]\nc: &c [*b, *b]\nd: [*c, *c]\n"
	for _, tt := range []struct{ name, doc string }{
		{"over 1 MiB", big},
		{"invalid UTF-8", minProject + "image: \"\xff\"\n"},
		{"empty", ""},
		{"comment only", "# nothing\n"},
		{"two documents", minProject + "---\n" + minProject},
		{"anchor", minProject + "forbidden: &a [\"x/**\"]\n"},
		{"alias", "base: &a main\nversion: ddf.project/v1\nintegrationBranch: *a\nverify: [make]\n"},
		{"merge key", "x: &a {lowMaxLines: 1}\nrisk: {<<: *a}\n"},
		{"merge key without alias", minProject + "risk: {<<: {lowMaxLines: 1}}\n"},
		{"billion laughs", laughs},
		{"duplicate top-level key", minProject + "verify: [x]\n"},
		{"duplicate nested key", minProject + "risk: {lowMaxLines: 1, lowMaxLines: 2}\n"},
		{"int key", minProject + "1: x\n"},
		{"bool key", minProject + "true: x\n"},
		{"null key", minProject + "null: x\n"},
		{"custom tag", minProject + "image: !foo x\n"},
		{"binary tag", minProject + "image: !!binary aGVsbG8=\n"},
		{"float", minProject + "risk: {lowMaxLines: 200.0}\n"},
		{"timestamp", "version: ddf.project/v1\nintegrationBranch: 2026-01-01\nverify: [make]\n"},
		{"hex int", minProject + "risk: {lowMaxLines: 0x10}\n"},
		{"leading zero int", minProject + "risk: {lowMaxLines: 010}\n"},
		{"underscore int", minProject + "risk: {lowMaxLines: 1_0}\n"},
		{"plus int", minProject + "risk: {lowMaxLines: +5}\n"},
		{"int overflow", minProject + "risk: {lowMaxLines: 9223372036854775808}\n"},
		{"nesting deeper than 32", deep},
		{"NUL in string", minProject + "image: \"a\\0b\"\n"},
		{"yes is not a bool", minProject + "reviewers: {r2DifferentProvider: yes}\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := l.Project([]byte(tt.doc))
			var ve *jsonschema.ValidationError
			// Only "yes" reaches the schema; every other row stops before it.
			if !errors.Is(err, config.ErrInvalidConfig) || errors.As(err, &ve) != (tt.name == "yes is not a bool") {
				t.Errorf("err = %v, want ErrInvalidConfig from the YAML step", err)
			}
		})
	}
}

// schemaDefault reads the default keyword at path (property names, with
// "properties" steps implied) from the embedded schema file.
func schemaDefault(t *testing.T, file string, path ...string) any {
	t.Helper()
	raw, err := fs.ReadFile(schemas.FS(), file)
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	for _, p := range path {
		node = node["properties"].(map[string]any)[p].(map[string]any)
	}
	d, ok := node["default"]
	if !ok {
		t.Fatalf("%s %v has no default", file, path)
	}
	return d
}

func TestLoader_Defaults(t *testing.T) {
	l := newLoader(t)
	p, err := l.Project([]byte(minProject))
	if err != nil {
		t.Fatal(err)
	}
	ps, err := l.Projects([]byte("projects: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := l.Routing([]byte("tiers: {deep: [{adapter: a, provider: p, model: m}], standard: [{adapter: a, provider: p, model: m}]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Risk.LowMaxLines != policy.DefaultLowMaxLines {
		t.Errorf("lowMaxLines = %d, want policy.DefaultLowMaxLines", p.Risk.LowMaxLines)
	}
	for _, tt := range []struct {
		name string
		got  any
		want any
	}{
		{"autonomy", string(p.Autonomy), schemaDefault(t, "project.json", "autonomy")},
		{"risk.lowMaxLines", float64(p.Risk.LowMaxLines), schemaDefault(t, "project.json", "risk", "lowMaxLines")},
		{"planning.maxAutoChildren", float64(p.Planning.MaxAutoChildren), schemaDefault(t, "project.json", "planning", "maxAutoChildren")},
		{"change.merge", string(p.Change.Merge), schemaDefault(t, "project.json", "change", "merge")},
		{"reviewers.r2DifferentProvider", p.Reviewers.R2DifferentProvider, schemaDefault(t, "project.json", "reviewers", "r2DifferentProvider")},
		{"limits.minFreeDiskGb", float64(ps.Limits.MinFreeDiskGb), schemaDefault(t, "projects.json", "limits", "minFreeDiskGb")},
		{"limits.maxRunsPerTask", float64(ps.Limits.MaxRunsPerTask), schemaDefault(t, "projects.json", "limits", "maxRunsPerTask")},
		{"limits.maxSubagentsPerRun", float64(r.Limits.MaxSubagentsPerRun), schemaDefault(t, "routing.json", "limits", "maxSubagentsPerRun")},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want schema default %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestParse_Enums(t *testing.T) {
	for _, tt := range []struct {
		name  string
		parse func(string) (string, error)
		good  []string
	}{
		{"Forge", func(s string) (string, error) { v, err := config.ParseForge(s); return string(v), err }, []string{"github", "local"}},
		{"Effort", func(s string) (string, error) { v, err := config.ParseEffort(s); return string(v), err }, []string{"low", "medium", "high"}},
		{"MergeMethod", func(s string) (string, error) { v, err := config.ParseMergeMethod(s); return string(v), err }, []string{"squash"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, s := range tt.good {
				if v, err := tt.parse(s); err != nil || v != s {
					t.Errorf("parse %q = %q, %v", s, v, err)
				}
			}
			for _, s := range []string{"", "bogus"} {
				if _, err := tt.parse(s); !errors.Is(err, config.ErrInvalidConfig) {
					t.Errorf("parse %q: err = %v, want ErrInvalidConfig", s, err)
				}
			}
		})
	}
	var f config.Forge
	var e config.Effort
	var m config.MergeMethod
	if f.UnmarshalText([]byte("local")) != nil || e.UnmarshalText([]byte("high")) != nil || m.UnmarshalText([]byte("squash")) != nil ||
		f != config.ForgeLocal || e != config.EffortHigh || m != config.MergeMethodSquash {
		t.Errorf("UnmarshalText = %q %q %q", f, e, m)
	}
	if f.UnmarshalText([]byte("x")) == nil || e.UnmarshalText([]byte("x")) == nil || m.UnmarshalText([]byte("x")) == nil {
		t.Error("UnmarshalText accepted an unknown value")
	}
}
