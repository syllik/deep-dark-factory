module example.com/boundaries/clean

go 1.27

require (
	github.com/bmatcuk/doublestar/v4 v4.0.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.0
	modernc.org/sqlite v0.0.0
)

replace (
	github.com/bmatcuk/doublestar/v4 => ../stubs/doublestar
	github.com/santhosh-tekuri/jsonschema/v6 => ../stubs/jsonschema
	modernc.org/sqlite => ../stubs/sqlite
)
