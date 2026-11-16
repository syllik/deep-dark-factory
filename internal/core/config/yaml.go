package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const maxDepth = 32

// parseYAML parses exactly one YAML document into a JSON-compatible value.
// Anchors, aliases, merge keys, non-string keys, duplicate keys and every
// scalar type but str, bool, null and plain decimal int are refused, so no
// alias is ever expanded and no YAML 1.1 coercion reaches the schema.
func parseYAML(data []byte) (any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
		return nil, errors.New("empty document")
	} else if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("more than one document or trailing content")
	}
	return toValue(&doc, 0, "")
}

// yamlErr names the position and, when known, the key; never the value.
func yamlErr(n *yaml.Node, key, format string, args ...any) error {
	where := fmt.Sprintf("line %d column %d", n.Line, n.Column)
	if key != "" {
		where += fmt.Sprintf(" (key %q)", key)
	}
	return fmt.Errorf("%s: %s", where, fmt.Sprintf(format, args...))
}

func toValue(n *yaml.Node, depth int, key string) (any, error) {
	if n.Anchor != "" {
		return nil, yamlErr(n, key, "anchors are not allowed")
	}
	if depth > maxDepth {
		return nil, yamlErr(n, key, "nesting deeper than %d", maxDepth)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if depth != 0 || len(n.Content) != 1 {
			return nil, yamlErr(n, key, "empty or nested document")
		}
		return toValue(n.Content[0], 1, "")
	case yaml.AliasNode:
		return nil, yamlErr(n, key, "aliases are not allowed")
	case yaml.SequenceNode:
		if n.ShortTag() != "!!seq" {
			return nil, yamlErr(n, key, "tags are not allowed")
		}
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := toValue(c, depth+1, key)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.MappingNode:
		if n.ShortTag() != "!!map" {
			return nil, yamlErr(n, key, "tags are not allowed")
		}
		out := make(map[string]any, len(n.Content)/2)
		lines := make(map[string]int, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.ShortTag() != "!!str" || k.Anchor != "" {
				return nil, yamlErr(k, key, "mapping keys must be plain strings (no merge keys); quote the key")
			}
			if first, ok := lines[k.Value]; ok {
				return nil, yamlErr(k, k.Value, "duplicate key, first at line %d", first)
			}
			lines[k.Value] = k.Line
			v, err := toValue(n.Content[i+1], depth+1, k.Value)
			if err != nil {
				return nil, err
			}
			out[k.Value] = v
		}
		return out, nil
	case yaml.ScalarNode:
		return scalar(n, key)
	default:
		return nil, yamlErr(n, key, "unknown node kind %d", n.Kind)
	}
}

func scalar(n *yaml.Node, key string) (any, error) {
	switch tag := n.ShortTag(); tag {
	case "!!str":
		if !utf8.ValidString(n.Value) || strings.ContainsRune(n.Value, 0) {
			return nil, yamlErr(n, key, "string is not valid UTF-8 or contains NUL")
		}
		return n.Value, nil
	case "!!bool":
		switch n.Value {
		case "true", "True", "TRUE":
			return true, nil
		case "false", "False", "FALSE":
			return false, nil
		}
		return nil, yamlErr(n, key, "unsupported boolean spelling; use true or false")
	case "!!null":
		switch n.Value {
		case "", "~", "null", "Null", "NULL":
			return nil, nil
		}
		return nil, yamlErr(n, key, "unsupported null spelling")
	case "!!int":
		if !isDecimal(n.Value) {
			return nil, yamlErr(n, key, "only plain decimal integers are allowed; quote strings")
		}
		if _, err := strconv.ParseInt(n.Value, 10, 64); err != nil {
			return nil, yamlErr(n, key, "integer out of range")
		}
		return json.Number(n.Value), nil
	case "!!float", "!!timestamp", "!!binary":
		return nil, yamlErr(n, key, "%s values are not allowed; quote strings", strings.TrimPrefix(tag, "!!"))
	}
	return nil, yamlErr(n, key, "unsupported tag; quote strings")
}

// isDecimal reports whether s matches ^-?(0|[1-9][0-9]*)$.
func isDecimal(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" || (s[0] == '0' && len(s) > 1) {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
