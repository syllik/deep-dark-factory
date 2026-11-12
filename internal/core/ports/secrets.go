package ports

import (
	"context"
	"errors"
	"fmt"
	"io"
)

const redacted = "[redacted]"

// Secret holds a secret value. It prints as [redacted] and refuses JSON and
// text marshalling, so it cannot leak through logs or encoders (spec §8.4).
type Secret struct {
	b []byte
}

// NewSecret returns a Secret holding a copy of b.
func NewSecret(b []byte) Secret {
	return Secret{b: append([]byte(nil), b...)}
}

// Bytes returns a copy of the secret value.
func (s Secret) Bytes() []byte {
	return append([]byte(nil), s.b...)
}

// IsEmpty reports whether the secret holds no bytes.
func (s Secret) IsEmpty() bool {
	return len(s.b) == 0
}

// String returns the fixed marker [redacted].
func (s Secret) String() string { return redacted }

// GoString returns the fixed marker [redacted].
func (s Secret) GoString() string { return redacted }

// Format writes the fixed marker [redacted] for every verb and flag.
func (s Secret) Format(f fmt.State, _ rune) {
	// A write error cannot be reported from Format.
	_, _ = io.WriteString(f, redacted)
}

// MarshalJSON always fails: a secret is never encoded.
func (s Secret) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal secret: refused")
}

// MarshalText always fails: a secret is never encoded.
func (s Secret) MarshalText() ([]byte, error) {
	return nil, errors.New("marshal secret: refused")
}

// Secrets reads named secrets.
type Secrets interface {
	Read(ctx context.Context, name string) (Secret, error)
}
