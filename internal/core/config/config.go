// Package config loads the bytes of .ddf/project.yaml, projects.yaml and
// routing.yaml into typed values validated against the embedded schemas
// (docs/concepts/config-layers.md). It takes bytes, never paths, and does no
// I/O.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/syllik/deep-dark-factory/schemas"
)

// ErrInvalidConfig marks config bytes that fail to parse, validate or pass
// the semantic checks.
var ErrInvalidConfig = errors.New("invalid config")

const maxConfigBytes = 1 << 20

// Loader parses and validates config files against the embedded schemas.
type Loader struct {
	project, projects, routing *jsonschema.Schema
}

// newCompiler returns a compiler whose loader loads nothing, so a $ref to a
// file or a network URL fails; the draft 2020-12 meta-schema comes from the
// library's embedded copy.
func newCompiler() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.UseLoader(jsonschema.SchemeURLLoader{})
	return c
}

// NewLoader compiles project.json, projects.json and routing.json from
// schemas.FS, each under a fixed mem:///schemas/ URL. A compile failure is a
// plain error.
func NewLoader() (*Loader, error) {
	c := newCompiler()
	names := []string{"project.json", "projects.json", "routing.json"}
	for _, name := range names {
		raw, err := fs.ReadFile(schemas.FS(), name)
		if err != nil {
			return nil, fmt.Errorf("read schema %s: %w", name, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("parse schema %s: %w", name, err)
		}
		if err := c.AddResource("mem:///schemas/"+name, doc); err != nil {
			return nil, fmt.Errorf("add schema %s: %w", name, err)
		}
	}
	compiled := make([]*jsonschema.Schema, len(names))
	for i, name := range names {
		sch, err := c.Compile("mem:///schemas/" + name)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", name, err)
		}
		compiled[i] = sch
	}
	return &Loader{project: compiled[0], projects: compiled[1], routing: compiled[2]}, nil
}

// Project loads .ddf/project.yaml and validates its policy profile.
func (l *Loader) Project(data []byte) (Project, error) {
	p := defaultProject()
	if err := load(l.project, "project.yaml", data, &p); err != nil {
		return Project{}, err
	}
	if _, err := p.PolicyProfile(); err != nil {
		return Project{}, fmt.Errorf("project.yaml: %w: %w", ErrInvalidConfig, err)
	}
	return p, nil
}

// Projects loads <operator-config>/projects.yaml.
func (l *Loader) Projects(data []byte) (Projects, error) {
	p := defaultProjects()
	if err := load(l.projects, "projects.yaml", data, &p); err != nil {
		return Projects{}, err
	}
	if err := p.check(); err != nil {
		return Projects{}, fmt.Errorf("projects.yaml: %w: %w", ErrInvalidConfig, err)
	}
	return p, nil
}

// Routing loads <operator-config>/routing.yaml.
func (l *Loader) Routing(data []byte) (Routing, error) {
	r := defaultRouting()
	if err := load(l.routing, "routing.yaml", data, &r); err != nil {
		return Routing{}, err
	}
	return r, nil
}

// load runs the size, UTF-8, YAML, schema and typed-decode steps in that
// order; out holds the defaults on entry.
func load[T any](sch *jsonschema.Schema, name string, data []byte, out *T) error {
	if len(data) > maxConfigBytes {
		return fmt.Errorf("%s: %w: larger than %d bytes", name, ErrInvalidConfig, maxConfigBytes)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("%s: %w: not valid UTF-8", name, ErrInvalidConfig)
	}
	v, err := parseYAML(data)
	if err != nil {
		return fmt.Errorf("%s: %w: %w", name, ErrInvalidConfig, err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("%s: %w: %w", name, ErrInvalidConfig, err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s: %w: encode: %w", name, ErrInvalidConfig, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: %w: decode: %w", name, ErrInvalidConfig, err)
	}
	return nil
}
