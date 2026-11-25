package signature

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

const redacted = "[redacted]"

// sigLen is the length of a signature in padded standard base64.
var sigLen = base64.StdEncoding.EncodedLen(ed25519.SignatureSize)

// KeyID returns the lowercase hex of the first 16 bytes of the SHA-256 over
// the raw 32-byte public key. Any other length returns ErrInvalidKey.
func KeyID(pub ed25519.PublicKey) (string, error) {
	if len(pub) != ed25519.PublicKeySize {
		return "", fmt.Errorf("key id: %w: public key length %d, want %d", ErrInvalidKey, len(pub), ed25519.PublicKeySize)
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:16]), nil
}

// Signer signs payloads with the daemon's review-signing key. It prints as
// [redacted] and refuses JSON and text marshalling, like ports.Secret.
type Signer struct {
	priv  ed25519.PrivateKey
	keyID string
}

// NewSigner parses key: exactly one PEM block of type "PRIVATE KEY" holding
// a PKCS#8 Ed25519 key (as made by "openssl genpkey -algorithm ed25519"),
// optionally surrounded by ASCII whitespace. Every other form returns
// ErrInvalidKey.
func NewSigner(key ports.Secret) (*Signer, error) {
	trimmed := trimLeftSpace(key.Bytes())
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("new signer: %w: empty secret", ErrInvalidKey)
	}
	if !hasPrefix(trimmed, "-----BEGIN ") {
		return nil, fmt.Errorf("new signer: %w: not a PEM block", ErrInvalidKey)
	}
	block, rest := pem.Decode(trimmed)
	if block == nil {
		return nil, fmt.Errorf("new signer: %w: not a PEM block", ErrInvalidKey)
	}
	if len(trimLeftSpace(rest)) != 0 {
		return nil, fmt.Errorf("new signer: %w: data after the PEM block", ErrInvalidKey)
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("new signer: %w: PEM block type %q, want \"PRIVATE KEY\"", ErrInvalidKey, block.Type)
	}
	if len(block.Headers) != 0 {
		return nil, fmt.Errorf("new signer: %w: PEM block has headers", ErrInvalidKey)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("new signer: %w: not a PKCS#8 key", ErrInvalidKey)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("new signer: %w: not an Ed25519 key", ErrInvalidKey)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("new signer: %w: not an Ed25519 key", ErrInvalidKey)
	}
	id, err := KeyID(pub)
	if err != nil {
		return nil, fmt.Errorf("new signer: %w", err)
	}
	return &Signer{priv: append(ed25519.PrivateKey(nil), priv...), keyID: id}, nil
}

// KeyID returns the key ID of the signer's public key.
func (s *Signer) KeyID() string {
	if s == nil {
		return ""
	}
	return s.keyID
}

// Public returns a copy of the signer's public key.
func (s *Signer) Public() ed25519.PublicKey {
	if s == nil || len(s.priv) != ed25519.PrivateKeySize {
		return nil
	}
	return append(ed25519.PublicKey(nil), s.priv[ed25519.SeedSize:]...)
}

// Sign returns the padded standard base64 (88 characters) of the Ed25519
// signature over Canonical(p). p.Version must be Version and p.KeyID the
// signer's key ID, else the error wraps ErrInvalidPayload.
func (s *Signer) Sign(p Payload) (string, error) {
	if s == nil || len(s.priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("sign: %w: nil signer", ErrInvalidKey)
	}
	if p.Version != Version {
		return "", fmt.Errorf("sign: %w: version %q, want %q", ErrInvalidPayload, p.Version, Version)
	}
	if p.KeyID != s.keyID {
		return "", fmt.Errorf("sign: %w: key id %q is not the signer's", ErrInvalidPayload, p.KeyID)
	}
	msg, err := Canonical(p)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, msg)), nil
}

// String returns the fixed marker [redacted].
func (s Signer) String() string { return redacted }

// GoString returns the fixed marker [redacted].
func (s Signer) GoString() string { return redacted }

// Format writes the fixed marker [redacted] for every verb and flag.
func (s Signer) Format(f fmt.State, _ rune) {
	// A write error cannot be reported from Format.
	_, _ = f.Write([]byte(redacted))
}

// MarshalJSON always fails: a signer is never encoded.
func (s Signer) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal signer: refused")
}

// MarshalText always fails: a signer is never encoded.
func (s Signer) MarshalText() ([]byte, error) {
	return nil, errors.New("marshal signer: refused")
}

// Verifier verifies review signatures against a fixed set of public keys.
type Verifier struct {
	keys map[string]ed25519.PublicKey
}

// NewVerifier returns a Verifier for keys. Zero keys or a key of the wrong
// length returns ErrInvalidKey.
func NewVerifier(keys ...ed25519.PublicKey) (*Verifier, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("new verifier: %w: no keys", ErrInvalidKey)
	}
	v := &Verifier{keys: make(map[string]ed25519.PublicKey, len(keys))}
	for _, k := range keys {
		id, err := KeyID(k)
		if err != nil {
			return nil, fmt.Errorf("new verifier: %w", err)
		}
		v.keys[id] = append(ed25519.PublicKey(nil), k...)
	}
	return v, nil
}

// Verify checks sig over p, in this order: an unknown version returns
// ErrUnknownVersion, an unknown key ID ErrUnknownKeyID, an invalid payload
// ErrInvalidPayload, and a signature that is not strict padded standard
// base64 of 64 bytes or does not verify ErrBadSignature. A nil Verifier
// returns ErrInvalidKey.
func (v *Verifier) Verify(p Payload, sig string) error {
	if v == nil || len(v.keys) == 0 {
		return fmt.Errorf("verify: %w: nil verifier", ErrInvalidKey)
	}
	if p.Version != Version {
		return fmt.Errorf("verify: %w: %q", ErrUnknownVersion, p.Version)
	}
	pub, ok := v.keys[p.KeyID]
	if !ok {
		return fmt.Errorf("verify: %w: %q", ErrUnknownKeyID, p.KeyID)
	}
	msg, err := Canonical(p)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if len(sig) != sigLen {
		return fmt.Errorf("verify: %w: signature length %d, want %d", ErrBadSignature, len(sig), sigLen)
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(sig)
	// Strict decoding still skips CR and LF; re-encoding rejects them.
	if err != nil || len(raw) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(raw) != sig {
		return fmt.Errorf("verify: %w: not strict padded base64 of %d bytes", ErrBadSignature, ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, msg, raw) {
		return fmt.Errorf("verify: %w", ErrBadSignature)
	}
	return nil
}

// trimLeftSpace drops leading ASCII whitespace.
func trimLeftSpace(b []byte) []byte {
	for len(b) > 0 {
		switch b[0] {
		case ' ', '\t', '\r', '\n', '\v', '\f':
			b = b[1:]
		default:
			return b
		}
	}
	return b
}

func hasPrefix(b []byte, prefix string) bool {
	return len(b) >= len(prefix) && string(b[:len(prefix)]) == prefix
}
