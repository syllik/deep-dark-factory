module example.com/boundaries/violations

go 1.27

require (
	github.com/bmatcuk/doublestar/v4 v4.0.0
	github.com/google/uuid v0.0.0
	go.yaml.in/yaml/v3 v3.0.0
	modernc.org/sqlite v0.0.0
)

replace (
	github.com/bmatcuk/doublestar/v4 => ../stubs/doublestar
	github.com/google/uuid => ../stubs/uuid
	go.yaml.in/yaml/v3 => ../stubs/yaml
	modernc.org/sqlite => ../stubs/sqlite
)
