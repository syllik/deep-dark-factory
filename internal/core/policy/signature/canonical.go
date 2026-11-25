package signature

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// Version is the only signature version this package signs and verifies.
const Version = "ddf.review-signature/v1"

// Sentinel errors; returned errors wrap them.
var (
	ErrInvalidPayload = errors.New("invalid payload")
	ErrUnknownVersion = errors.New("unknown signature version")
	ErrUnknownKeyID   = errors.New("unknown key id")
	ErrBadSignature   = errors.New("bad signature")
	ErrInvalidKey     = errors.New("invalid key")
)

// maxInt is the largest integer the canonical JSON accepts (2^53-1).
const maxInt = 1<<53 - 1

// Payload is the signed review payload of spec §6.6. The JSON names are the
// member names of its canonical form.
type Payload struct {
	Version        string              `json:"version"`
	KeyID          string              `json:"keyId"`
	TaskKey        model.TaskKey       `json:"taskKey"`
	Forge          string              `json:"forge"`
	Repository     string              `json:"repository"`
	ChangeID       string              `json:"changeId"`
	Head           string              `json:"head"`
	Role           model.Role          `json:"role"`
	Round          int                 `json:"round"`
	Verdict        model.ReviewVerdict `json:"verdict"`
	BlockingCount  int                 `json:"blockingCount"`
	FindingsDigest string              `json:"findingsDigest"`
}

// Canonical validates p and returns its canonical JSON: RFC 8785 restricted
// to objects, arrays, strings and non-negative integers, keys sorted, no
// whitespace. Every error wraps ErrInvalidPayload.
func Canonical(p Payload) ([]byte, error) {
	if err := validatePayload(p); err != nil {
		return nil, err
	}
	b, err := encodeObject([]member{
		{"version", str(p.Version)},
		{"keyId", str(p.KeyID)},
		{"taskKey", str(string(p.TaskKey))},
		{"forge", str(p.Forge)},
		{"repository", str(p.Repository)},
		{"changeId", str(p.ChangeID)},
		{"head", str(p.Head)},
		{"role", str(string(p.Role))},
		{"round", integer(p.Round)},
		{"verdict", str(string(p.Verdict))},
		{"blockingCount", integer(p.BlockingCount)},
		{"findingsDigest", str(p.FindingsDigest)},
	})
	if err != nil {
		return nil, fmt.Errorf("canonical payload: %w: %w", ErrInvalidPayload, err)
	}
	return b, nil
}

func validatePayload(p Payload) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("canonical payload: %w: %s", ErrInvalidPayload, fmt.Sprintf(format, args...))
	}
	if p.Version == "" {
		return fail("empty version")
	}
	if !isLowerHex(p.KeyID, 32) {
		return fail("key id is not 32 lowercase hex characters")
	}
	if _, err := model.ParseTaskKey(string(p.TaskKey)); err != nil {
		return fail("task key: %v", err)
	}
	for _, f := range [...]struct{ name, value string }{
		{"forge", p.Forge},
		{"repository", p.Repository},
		{"change id", p.ChangeID},
	} {
		if f.value == "" {
			return fail("empty %s", f.name)
		}
		if !utf8.ValidString(f.value) {
			return fail("%s is not valid UTF-8", f.name)
		}
	}
	if !isLowerHex(p.Head, 40) && !isLowerHex(p.Head, 64) {
		return fail("head is not 40 or 64 lowercase hex characters")
	}
	if p.Role != model.RoleR1 && p.Role != model.RoleR2 {
		return fail("role %q is not r1 or r2", p.Role)
	}
	if p.Round < 1 {
		return fail("round %d is below 1", p.Round)
	}
	if p.Verdict != model.ReviewVerdictApprove && p.Verdict != model.ReviewVerdictRequestChanges {
		return fail("verdict %q is not APPROVE or REQUEST_CHANGES", p.Verdict)
	}
	if p.BlockingCount < 0 {
		return fail("blocking count %d is negative", p.BlockingCount)
	}
	if !isLowerHex(p.FindingsDigest, 64) {
		return fail("findings digest is not 64 lowercase hex characters")
	}
	return nil
}

// FindingsDigest returns the lowercase hex SHA-256 of the canonical JSON
// array of fs in the reviewer-result shape (§6.3), sorted by ID. Only the
// keys id, priorId, severity, category, securityLevel, evidence, location and
// fixHint enter; empty optional keys are omitted. An empty or nil list is the
// digest of "[]". Every error wraps ErrInvalidPayload.
func FindingsDigest(fs []model.Finding) (string, error) {
	sorted := make([]model.Finding, len(fs))
	copy(sorted, fs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	items := make([][]byte, 0, len(sorted))
	for i, f := range sorted {
		if i > 0 && sorted[i-1].ID == f.ID {
			return "", fmt.Errorf("findings digest: %w: duplicate finding id %q", ErrInvalidPayload, f.ID)
		}
		b, err := encodeFinding(f)
		if err != nil {
			return "", fmt.Errorf("findings digest: %w: finding %q: %w", ErrInvalidPayload, f.ID, err)
		}
		items = append(items, b)
	}
	sum := sha256.Sum256(encodeArray(items))
	return hex.EncodeToString(sum[:]), nil
}

func encodeFinding(f model.Finding) ([]byte, error) {
	if f.ID == "" {
		return nil, errors.New("empty id")
	}
	if f.Evidence == "" {
		return nil, errors.New("empty evidence")
	}
	if _, err := model.ParseSeverity(string(f.Severity)); err != nil {
		return nil, err
	}
	if _, err := model.ParseFindingCategory(string(f.Category)); err != nil {
		return nil, err
	}
	if f.SecurityLevel != "" {
		if _, err := model.ParseSecurityLevel(string(f.SecurityLevel)); err != nil {
			return nil, err
		}
	}
	ms := []member{
		{"id", str(f.ID)},
		{"severity", str(string(f.Severity))},
		{"category", str(string(f.Category))},
		{"evidence", str(f.Evidence)},
	}
	for _, opt := range [...]member{
		{"priorId", str(f.PriorID)},
		{"securityLevel", str(string(f.SecurityLevel))},
		{"location", str(f.Location)},
		{"fixHint", str(f.FixHint)},
	} {
		if opt.value.s != "" {
			ms = append(ms, opt)
		}
	}
	return encodeObject(ms)
}

// value is a canonical JSON scalar: a string, or an integer when isInt.
type value struct {
	s     string
	n     int
	isInt bool
}

func str(s string) value  { return value{s: s} }
func integer(n int) value { return value{n: n, isInt: true} }
func (v value) encode() ([]byte, error) {
	if v.isInt {
		return encodeInt(v.n)
	}
	return encodeString(v.s)
}

type member struct {
	key   string
	value value
}

// encodeObject writes the members sorted by key in byte order. Keys are
// ASCII and written without escapes.
func encodeObject(ms []member) ([]byte, error) {
	sorted := make([]member, len(ms))
	copy(sorted, ms)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
	b := []byte{'{'}
	for i, m := range sorted {
		if i > 0 && sorted[i-1].key == m.key {
			return nil, fmt.Errorf("duplicate key %q", m.key)
		}
		if i > 0 {
			b = append(b, ',')
		}
		k, err := encodeString(m.key)
		if err != nil {
			return nil, err
		}
		v, err := m.value.encode()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.key, err)
		}
		b = append(b, k...)
		b = append(b, ':')
		b = append(b, v...)
	}
	return append(b, '}'), nil
}

func encodeArray(items [][]byte) []byte {
	b := []byte{'['}
	for i, it := range items {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, it...)
	}
	return append(b, ']')
}

// encodeInt writes n in decimal; it accepts 0..2^53-1 only.
func encodeInt(n int) ([]byte, error) {
	if n < 0 || n > maxInt {
		return nil, fmt.Errorf("integer %d out of range 0..2^53-1", n)
	}
	return strconv.AppendInt(nil, int64(n), 10), nil
}

// encodeString writes s with the minimal escaping of RFC 8785: quotation mark
// and reverse solidus escaped, U+0008, U+0009, U+000A, U+000C and U+000D as
// short escapes, other control characters below U+0020 as \u00xx with
// lowercase hex, every other code point as literal UTF-8.
func encodeString(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, errors.New("string is not valid UTF-8")
	}
	const hexDigits = "0123456789abcdef"
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\':
			b = append(b, '\\', c)
		case '\b':
			b = append(b, '\\', 'b')
		case '\t':
			b = append(b, '\\', 't')
		case '\n':
			b = append(b, '\\', 'n')
		case '\f':
			b = append(b, '\\', 'f')
		case '\r':
			b = append(b, '\\', 'r')
		default:
			if c < 0x20 {
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			} else {
				b = append(b, c)
			}
		}
	}
	return append(b, '"'), nil
}

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
