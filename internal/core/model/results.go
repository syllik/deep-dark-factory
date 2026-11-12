package model

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/syllik/deep-dark-factory/schemas"
)

// ErrInvalidResult marks a role result that fails to parse or to validate
// against its schema.
var ErrInvalidResult = errors.New("invalid role result")

// ResultSchemaID returns the schema ID of the role result of kind.
func ResultSchemaID(kind ResultKind) (SchemaID, error) {
	switch kind {
	case ResultKindPlanner:
		return SchemaIDPlannerResult, nil
	case ResultKindExecutor:
		return SchemaIDExecutorResult, nil
	case ResultKindReviewer:
		return SchemaIDReviewerResult, nil
	}
	return "", fmt.Errorf("result schema id: unknown result kind %q", kind)
}

func resultSchemaFile(kind ResultKind) (string, error) {
	switch kind {
	case ResultKindPlanner:
		return "planner.result.json", nil
	case ResultKindExecutor:
		return "executor.result.json", nil
	case ResultKindReviewer:
		return "reviewer.result.json", nil
	}
	return "", fmt.Errorf("result schema file: unknown result kind %q", kind)
}

// ResultValidator validates role results against the embedded schemas.
type ResultValidator struct {
	schemas map[ResultKind]*jsonschema.Schema
}

// NewResultValidator compiles the role-result schemas from schemas.FS. Each
// schema is registered under a fixed mem:///schemas/ URL and the compiler's
// loader loads nothing, so no reference is read from disk or network; the
// draft 2020-12 meta-schema comes from the library's embedded copy.
func NewResultValidator() (*ResultValidator, error) {
	kinds := []ResultKind{ResultKindPlanner, ResultKindExecutor, ResultKindReviewer}
	c := jsonschema.NewCompiler()
	c.UseLoader(jsonschema.SchemeURLLoader{})
	urls := make(map[ResultKind]string, len(kinds))
	for _, kind := range kinds {
		name, err := resultSchemaFile(kind)
		if err != nil {
			return nil, err
		}
		raw, err := fs.ReadFile(schemas.FS(), name)
		if err != nil {
			return nil, fmt.Errorf("read schema %s: %w", name, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("parse schema %s: %w", name, err)
		}
		url := "mem:///schemas/" + name
		if err := c.AddResource(url, doc); err != nil {
			return nil, fmt.Errorf("add schema %s: %w", name, err)
		}
		urls[kind] = url
	}
	v := &ResultValidator{schemas: make(map[ResultKind]*jsonschema.Schema, len(kinds))}
	for kind, url := range urls {
		sch, err := c.Compile(url)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", url, err)
		}
		v.schemas[kind] = sch
	}
	return v, nil
}

// Validate checks that data is exactly one JSON value valid for kind. A parse
// or validation failure wraps ErrInvalidResult; an unknown kind is an error.
func (v *ResultValidator) Validate(kind ResultKind, data []byte) error {
	sch, ok := v.schemas[kind]
	if !ok {
		return fmt.Errorf("validate result: unknown result kind %q", kind)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parse %s result: %w: %w", kind, ErrInvalidResult, err)
	}
	if err := sch.Validate(doc); err != nil {
		return fmt.Errorf("validate %s result: %w: %w", kind, ErrInvalidResult, err)
	}
	return nil
}
