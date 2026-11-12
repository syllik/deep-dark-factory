// Package schemas embeds DDF's top-level JSON Schema files so the binary
// carries them. The JSON files remain the single normative source; Go code
// reads them through FS and never restates their rules.
package schemas

import (
	"embed"
	"io/fs"
)

//go:embed *.json
var files embed.FS

// FS returns the embedded top-level *.json schema files, read-only.
// schemas/examples is not embedded.
func FS() fs.FS {
	return files
}
