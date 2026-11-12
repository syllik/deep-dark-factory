package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
)

// TaskKey is the lowercase hex SHA-256 digest that scopes every store row,
// lease and signature of one task (spec §4.5).
type TaskKey string

// TaskSource is the forge object a task comes from; NewTaskKey digests it.
type TaskSource struct {
	Forge      string
	Repository string
	SourceKind string
	SourceID   string
}

// NewTaskKey returns the task key of src: the SHA-256 over forge,
// repository, source kind and source id, in that order, each encoded as its
// decimal byte length, ':' and its bytes. An empty field is an error.
func NewTaskKey(src TaskSource) (TaskKey, error) {
	fields := [...]struct{ name, value string }{
		{"forge", src.Forge},
		{"repository", src.Repository},
		{"source kind", src.SourceKind},
		{"source id", src.SourceID},
	}
	h := sha256.New()
	for _, f := range fields {
		if f.value == "" {
			return "", fmt.Errorf("new task key: empty %s", f.name)
		}
		// hash.Hash.Write never returns an error.
		_, _ = h.Write([]byte(strconv.Itoa(len(f.value)) + ":" + f.value))
	}
	return TaskKey(hex.EncodeToString(h.Sum(nil))), nil
}

// ParseTaskKey accepts exactly 64 lowercase hex characters.
func ParseTaskKey(s string) (TaskKey, error) {
	if len(s) != sha256.Size*2 {
		return "", fmt.Errorf("parse task key: length %d, want %d", len(s), sha256.Size*2)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("parse task key: invalid character %q at %d", c, i)
		}
	}
	return TaskKey(s), nil
}
