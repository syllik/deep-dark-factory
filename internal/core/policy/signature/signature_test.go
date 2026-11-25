package signature_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

// rfc8032Seed is the RFC 8032 section 7.1 TEST 1 secret key; its public key
// has the key ID 21fe31dfa154a261626bf854046fd227.
const rfc8032Seed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"

func testKey(t *testing.T, seedHex string) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func otherKey(t *testing.T) ed25519.PrivateKey {
	return testKey(t, strings.Repeat("02", ed25519.SeedSize))
}

func pemOf(t *testing.T, blockType string, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

func newSigner(t *testing.T, priv ed25519.PrivateKey) *signature.Signer {
	t.Helper()
	s, err := signature.NewSigner(ports.NewSecret(pemOf(t, "PRIVATE KEY", priv)))
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestSignerKeyIDAndPublic(t *testing.T) {
	priv := testKey(t, rfc8032Seed)
	s := newSigner(t, priv)
	if s.KeyID() != "21fe31dfa154a261626bf854046fd227" {
		t.Fatalf("KeyID = %q", s.KeyID())
	}
	pub := s.Public()
	if !pub.Equal(priv.Public()) {
		t.Fatalf("Public = %x, want %x", pub, priv.Public())
	}
	pub[0] ^= 0xff
	if !s.Public().Equal(priv.Public()) {
		t.Fatalf("Public does not return a copy")
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	s := newSigner(t, testKey(t, rfc8032Seed))
	p := goldenPayload()
	sig, err := s.Sign(p)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 88 || !strings.HasSuffix(sig, "==") {
		t.Fatalf("signature %q is not 88 characters of padded base64", sig)
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(sig)
	if err != nil || len(raw) != ed25519.SignatureSize {
		t.Fatalf("signature does not decode to 64 bytes: %v", err)
	}
	v, err := signature.NewVerifier(otherKey(t).Public().(ed25519.PublicKey), s.Public())
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify(p, sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// Ed25519 is deterministic: the same payload gives the same signature.
	again, err := s.Sign(p)
	if err != nil || again != sig {
		t.Fatalf("Sign is not deterministic: %q vs %q (%v)", again, sig, err)
	}
}

func TestVerifyRejections(t *testing.T) {
	s := newSigner(t, testKey(t, rfc8032Seed))
	v, err := signature.NewVerifier(s.Public())
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	p := goldenPayload()
	sig, err := s.Sign(p)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(sig)

	flipped := append([]byte(nil), raw...)
	flipped[10] ^= 0x01
	// The second-to-last base64 character carries 2 data bits and 4 zero
	// padding bits; setting a padding bit is non-strict base64.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	idx := strings.IndexByte(alphabet, sig[85])
	nonStrict := sig[:85] + string(alphabet[idx|1]) + sig[86:]

	forgedMsg, err := signature.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	forged := base64.StdEncoding.EncodeToString(ed25519.Sign(otherKey(t), forgedMsg))

	sigTests := []struct {
		name, sig string
	}{
		{"flipped bit", base64.StdEncoding.EncodeToString(flipped)},
		{"63-byte signature", base64.StdEncoding.EncodeToString(raw[:63])},
		{"65-byte signature", base64.StdEncoding.EncodeToString(append(append([]byte(nil), raw...), 0))},
		{"non-strict base64", nonStrict},
		{"unpadded base64", strings.TrimRight(sig, "=")},
		{"embedded newline", sig[:40] + "\n" + sig[40:]},
		{"empty signature", ""},
		{"forged with another key under the registered key id", forged},
	}
	for _, tt := range sigTests {
		t.Run(tt.name, func(t *testing.T) {
			if err := v.Verify(p, tt.sig); !errors.Is(err, signature.ErrBadSignature) {
				t.Fatalf("Verify error = %v, want ErrBadSignature", err)
			}
		})
	}

	fieldTests := []struct {
		field  string
		mutate func(*signature.Payload)
	}{
		{"taskKey", func(p *signature.Payload) { p.TaskKey = model.TaskKey(strings.Repeat("c", 64)) }},
		{"forge", func(p *signature.Payload) { p.Forge = "github" }},
		{"repository", func(p *signature.Payload) { p.Repository = "other" }},
		{"changeId", func(p *signature.Payload) { p.ChangeID = "2" }},
		{"head", func(p *signature.Payload) { p.Head = strings.Repeat("c", 40) }},
		{"role", func(p *signature.Payload) { p.Role = model.RoleR2 }},
		{"round", func(p *signature.Payload) { p.Round = 3 }},
		{"verdict", func(p *signature.Payload) { p.Verdict = model.ReviewVerdictRequestChanges }},
		{"blockingCount", func(p *signature.Payload) { p.BlockingCount = 1 }},
		{"findingsDigest", func(p *signature.Payload) { p.FindingsDigest = strings.Repeat("0", 64) }},
	}
	for _, tt := range fieldTests {
		t.Run("changed "+tt.field, func(t *testing.T) {
			q := p
			tt.mutate(&q)
			if q == p {
				t.Fatalf("mutation did not change the payload")
			}
			if err := v.Verify(q, sig); !errors.Is(err, signature.ErrBadSignature) {
				t.Fatalf("Verify error = %v, want ErrBadSignature", err)
			}
		})
	}

	t.Run("unknown version", func(t *testing.T) {
		q := p
		q.Version = "ddf.review-signature/v2"
		if err := v.Verify(q, sig); !errors.Is(err, signature.ErrUnknownVersion) {
			t.Fatalf("Verify error = %v, want ErrUnknownVersion", err)
		}
	})
	t.Run("unknown key id", func(t *testing.T) {
		q := p
		q.KeyID = strings.Repeat("0", 32)
		if err := v.Verify(q, sig); !errors.Is(err, signature.ErrUnknownKeyID) {
			t.Fatalf("Verify error = %v, want ErrUnknownKeyID", err)
		}
	})
	t.Run("version checked before key id", func(t *testing.T) {
		q := p
		q.Version = ""
		q.KeyID = "nope"
		if err := v.Verify(q, sig); !errors.Is(err, signature.ErrUnknownVersion) {
			t.Fatalf("Verify error = %v, want ErrUnknownVersion", err)
		}
	})
	t.Run("invalid payload", func(t *testing.T) {
		q := p
		q.Round = 0
		if err := v.Verify(q, sig); !errors.Is(err, signature.ErrInvalidPayload) {
			t.Fatalf("Verify error = %v, want ErrInvalidPayload", err)
		}
	})
	t.Run("nil verifier", func(t *testing.T) {
		var nv *signature.Verifier
		if err := nv.Verify(p, sig); !errors.Is(err, signature.ErrInvalidKey) {
			t.Fatalf("Verify error = %v, want ErrInvalidKey", err)
		}
	})
}

func TestNewSignerAcceptsTrailingWhitespace(t *testing.T) {
	b := append([]byte("\n  "), pemOf(t, "PRIVATE KEY", testKey(t, rfc8032Seed))...)
	b = append(b, " \r\n\t\n"...)
	if _, err := signature.NewSigner(ports.NewSecret(b)); err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
}

func TestNewSignerRejections(t *testing.T) {
	priv := testKey(t, rfc8032Seed)
	good := pemOf(t, "PRIVATE KEY", priv)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	withHeaders := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"}, Bytes: der})
	tests := []struct {
		name string
		key  []byte
	}{
		{"empty secret", nil},
		{"whitespace only", []byte(" \n")},
		{"raw seed bytes", priv.Seed()},
		{"raw private key bytes", []byte(priv)},
		{"other PEM block type", pemOf(t, "EC PRIVATE KEY", priv)},
		{"PKCS#8 ECDSA P-256 key", pemOf(t, "PRIVATE KEY", ecKey)},
		{"two PEM blocks", append(append([]byte(nil), good...), good...)},
		{"trailing data", append(append([]byte(nil), good...), "junk"...)},
		{"leading data", append([]byte("junk\n"), good...)},
		{"PEM headers", withHeaders},
		{"garbage DER", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := signature.NewSigner(ports.NewSecret(tt.key))
			if !errors.Is(err, signature.ErrInvalidKey) || s != nil {
				t.Fatalf("NewSigner = %v, %v; want nil, ErrInvalidKey", s, err)
			}
		})
	}
}

func TestSignRefusals(t *testing.T) {
	s := newSigner(t, testKey(t, rfc8032Seed))
	tests := []struct {
		name   string
		mutate func(*signature.Payload)
	}{
		{"other version", func(p *signature.Payload) { p.Version = "ddf.review-signature/v2" }},
		{"other key id", func(p *signature.Payload) { p.KeyID = strings.Repeat("0", 32) }},
		{"invalid payload", func(p *signature.Payload) { p.Head = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := goldenPayload()
			tt.mutate(&p)
			sig, err := s.Sign(p)
			if !errors.Is(err, signature.ErrInvalidPayload) || sig != "" {
				t.Fatalf("Sign = %q, %v; want ErrInvalidPayload", sig, err)
			}
		})
	}
	var nilSigner *signature.Signer
	if _, err := nilSigner.Sign(goldenPayload()); !errors.Is(err, signature.ErrInvalidKey) {
		t.Fatalf("nil Signer Sign error = %v, want ErrInvalidKey", err)
	}
}

func TestSignerRedaction(t *testing.T) {
	s := newSigner(t, testKey(t, rfc8032Seed))
	for _, verb := range []string{"%v", "%s", "%+v", "%#v", "%q", "%x"} {
		for name, arg := range map[string]any{"pointer": s, "value": *s} {
			if got := fmt.Sprintf(verb, arg); got != "[redacted]" {
				t.Fatalf("Sprintf(%s, %s) = %q, want [redacted]", verb, name, got)
			}
		}
	}
	if got := fmt.Sprint(struct{ S *signature.Signer }{s}); strings.Contains(got, "9d61") || !strings.Contains(got, "[redacted]") {
		t.Fatalf("nested Sprint = %q", got)
	}
	for name, arg := range map[string]any{"pointer": s, "value": *s} {
		if b, err := json.Marshal(arg); err == nil {
			t.Fatalf("json.Marshal(%s) = %s, want error", name, b)
		}
	}
	if b, err := s.MarshalText(); err == nil {
		t.Fatalf("MarshalText = %s, want error", b)
	}
}

func TestNewVerifierRejections(t *testing.T) {
	if v, err := signature.NewVerifier(); !errors.Is(err, signature.ErrInvalidKey) || v != nil {
		t.Fatalf("NewVerifier() = %v, %v; want nil, ErrInvalidKey", v, err)
	}
	good := testKey(t, rfc8032Seed).Public().(ed25519.PublicKey)
	if v, err := signature.NewVerifier(good, make(ed25519.PublicKey, 31)); !errors.Is(err, signature.ErrInvalidKey) || v != nil {
		t.Fatalf("NewVerifier(short key) = %v, %v; want nil, ErrInvalidKey", v, err)
	}
}
