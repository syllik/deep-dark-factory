package publisher_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
)

// fakeTokens returns one obviously fake, pattern-shaped token per pattern
// name, built at run time so no source file matches a secret pattern.
func fakeTokens() []struct{ name, token, want string } {
	r := strings.Repeat
	return []struct{ name, token, want string }{
		{"private-key", "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIfake\nfake\n-----END " + "RSA PRIVATE KEY-----", "[redacted]"},
		{"github-token", "gh" + "p_" + r("A1", 18), "[redacted]"},
		{"github-pat", "github" + "_pat_" + r("a", 22), "[redacted]"},
		{"aws-access-key-id", "AK" + "IA" + r("F", 16), "[redacted]"},
		{"slack-token", "xo" + "xb-" + r("1", 10), "[redacted]"},
		{"api-key-sk", "s" + "k-" + r("f", 20), "[redacted]"},
		{"telegram-bot-token", "12345678" + ":" + r("T", 35), "[redacted]"},
		{"google-api-key", "AI" + "za" + r("G", 35), "[redacted]"},
		{"jwt", "ey" + "J" + r("a", 7) + "." + r("b", 10) + "." + r("c", 10), "[redacted]"},
		{"url-credentials", "https://" + "fakeuser" + ":" + "fakepass" + "@host.example/x", "https://[redacted]@host.example/x"},
		{"url-credentials", "https://" + "fakeuser" + ":" + "fake@pass" + "@host.example/x", "https://[redacted]@host.example/x"},
		{"private-key", "-----BEGIN " + "PGP PRIVATE KEY BLOCK-----\n\nlQfake\n-----END " + "PGP PRIVATE KEY BLOCK-----", "[redacted]"},
	}
}

func secrets(values ...string) []ports.Secret {
	out := make([]ports.Secret, 0, len(values))
	for _, v := range values {
		out = append(out, ports.NewSecret([]byte(v)))
	}
	return out
}

func TestRedact_Patterns(t *testing.T) {
	r := publisher.NewRedactor(nil)
	for _, tc := range fakeTokens() {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Redact("before " + tc.token + " after")
			if want := "before " + tc.want + " after"; got != want {
				t.Errorf("Redact = %q, want %q", got, want)
			}
			if again := r.Redact(got); again != got {
				t.Errorf("Redact not idempotent: %q", again)
			}
			if h := r.NewHits(nil, []byte(tc.token)); !slices.Equal(h.Patterns, []string{tc.name}) || h.Loaded {
				t.Errorf("NewHits = %+v, want only %s", h, tc.name)
			}
		})
	}
	t.Run("unterminated private key keeps no BEGIN line", func(t *testing.T) {
		got := r.Redact("-----BEGIN " + "PRIVATE KEY-----\nbody")
		if got != "[redacted]\nbody" {
			t.Errorf("Redact = %q", got)
		}
	})
}

func TestRedact_LoadedValues(t *testing.T) {
	cases := []struct {
		name   string
		loaded []string
		in     string
		want   string
	}{
		{"value inside a longer word", []string{"test-secret-alpha"}, "xxtest-secret-alphayy", "xx[redacted]yy"},
		{"prefix overlap leaves no fragment", []string{"test-secret-a", "test-secret-abc"}, "k=test-secret-abc;", "k=[redacted];"},
		{"inner overlap leaves no fragment", []string{"xab", "abc-secret"}, "k=xabc-secret;", "k=x[redacted];"},
		{"empty secret ignored", []string{""}, "abc", "abc"},
		{"value inside the marker stays idempotent", []string{"act"}, "act [redacted]", "[redacted] [redacted]"},
		{"values before patterns", []string{"test-secret-beta"}, "https://u" + ":test-secret-beta@h", "https://[redacted]@h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := publisher.NewRedactor(secrets(tc.loaded...))
			got := r.Redact(tc.in)
			if got != tc.want {
				t.Errorf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := r.Redact(got); again != got {
				t.Errorf("Redact not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestRedactor_PrintsNoValue(t *testing.T) {
	r := publisher.NewRedactor(secrets("test-secret-print"))
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(verb, r); strings.Contains(got, "test-secret") || got != "[redactor]" {
			t.Errorf("Sprintf(%s) = %q", verb, got)
		}
	}
}

func TestRedactor_NewHits(t *testing.T) {
	tok := fakeTokens()[1].token
	r := publisher.NewRedactor(secrets("test-secret-hit", ""))
	cases := []struct {
		name     string
		old, new string
		want     publisher.Hit
	}{
		{"loaded value only in new", "a", "a test-secret-hit", publisher.Hit{Loaded: true}},
		{"loaded value in both", "test-secret-hit", "x test-secret-hit", publisher.Hit{}},
		{"pattern only in new", "a", "a " + tok, publisher.Hit{Patterns: []string{"github-token"}}},
		{"same token in old and new", tok, "b " + tok, publisher.Hit{}},
		{"binary content scanned", "\x00", "\x00\x01" + tok + "\x00test-secret-hit\xff", publisher.Hit{Loaded: true, Patterns: []string{"github-token"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.NewHits([]byte(tc.old), []byte(tc.new))
			if got.Loaded != tc.want.Loaded || !slices.Equal(got.Patterns, tc.want.Patterns) {
				t.Errorf("NewHits = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSources_HoldNoSecretPattern keeps every test token built at run time.
func TestSources_HoldNoSecretPattern(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob sources: %v", err)
	}
	r := publisher.NewRedactor(nil)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if h := r.NewHits(nil, b); len(h.Patterns) > 0 {
			t.Errorf("%s matches secret patterns %v", f, h.Patterns)
		}
	}
}
