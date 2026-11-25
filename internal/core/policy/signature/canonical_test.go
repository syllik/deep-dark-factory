package signature_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/policy/signature"
)

const emptyDigest = "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"

func goldenPayload() signature.Payload {
	return signature.Payload{
		Version:        signature.Version,
		KeyID:          "21fe31dfa154a261626bf854046fd227",
		TaskKey:        model.TaskKey(strings.Repeat("a", 64)),
		Forge:          "local",
		Repository:     "demo",
		ChangeID:       "1",
		Head:           strings.Repeat("b", 40),
		Role:           model.RoleR1,
		Round:          2,
		Verdict:        model.ReviewVerdictApprove,
		BlockingCount:  0,
		FindingsDigest: emptyDigest,
	}
}

func TestCanonicalGolden(t *testing.T) {
	got, err := signature.Canonical(goldenPayload())
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	want := `{"blockingCount":0,"changeId":"1","findingsDigest":"4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945","forge":"local","head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","keyId":"21fe31dfa154a261626bf854046fd227","repository":"demo","role":"r1","round":2,"taskKey":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","verdict":"APPROVE","version":"ddf.review-signature/v1"}`
	if string(got) != want {
		t.Fatalf("Canonical:\n got %s\nwant %s", got, want)
	}
}

func TestCanonicalStringEscaping(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"quotation mark", `a"b`, `a\"b`},
		{"reverse solidus", `a\b`, `a\\b`},
		{"U+0008", "\b", `\b`},
		{"U+0009", "\t", `\t`},
		{"U+000A", "\n", `\n`},
		{"U+000C", "\f", `\f`},
		{"U+000D", "\r", `\r`},
		{"U+0001", "\x01", `\u0001`},
		{"U+001F", "\x1f", `\u001f`},
		{"U+0000", "\x00", `\u0000`},
		{"U+001B", "\x1b", `\u001b`},
		{"solidus literal", "/", "/"},
		{"less-than literal", "<", "<"},
		{"greater-than literal", ">", ">"},
		{"ampersand literal", "&", "&"},
		{"U+007F literal", "\x7f", "\x7f"},
		{"U+2028 literal", " ", " "},
		{"U+2029 literal", " ", " "},
		{"non-ASCII letter literal", "ж", "ж"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := goldenPayload()
			p.Forge = "x" + tt.in
			got, err := signature.Canonical(p)
			if err != nil {
				t.Fatalf("Canonical: %v", err)
			}
			member := `"forge":"x` + tt.want + `"`
			if !strings.Contains(string(got), member) {
				t.Fatalf("Canonical = %q, want member %q", got, member)
			}
		})
	}
}

func TestCanonicalValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*signature.Payload)
	}{
		{"empty version", func(p *signature.Payload) { p.Version = "" }},
		{"invalid UTF-8 version", func(p *signature.Payload) { p.Version = "v\xff" }},
		{"empty key id", func(p *signature.Payload) { p.KeyID = "" }},
		{"key id 31 characters", func(p *signature.Payload) { p.KeyID = strings.Repeat("a", 31) }},
		{"key id 33 characters", func(p *signature.Payload) { p.KeyID = strings.Repeat("a", 33) }},
		{"key id uppercase", func(p *signature.Payload) { p.KeyID = strings.Repeat("A", 32) }},
		{"key id non-hex", func(p *signature.Payload) { p.KeyID = strings.Repeat("g", 32) }},
		{"empty task key", func(p *signature.Payload) { p.TaskKey = "" }},
		{"task key uppercase", func(p *signature.Payload) { p.TaskKey = model.TaskKey(strings.Repeat("A", 64)) }},
		{"task key short", func(p *signature.Payload) { p.TaskKey = model.TaskKey(strings.Repeat("a", 63)) }},
		{"empty forge", func(p *signature.Payload) { p.Forge = "" }},
		{"invalid UTF-8 forge", func(p *signature.Payload) { p.Forge = "\xff" }},
		{"empty repository", func(p *signature.Payload) { p.Repository = "" }},
		{"invalid UTF-8 repository", func(p *signature.Payload) { p.Repository = "a\xc3" }},
		{"empty change id", func(p *signature.Payload) { p.ChangeID = "" }},
		{"invalid UTF-8 change id", func(p *signature.Payload) { p.ChangeID = "\xed\xa0\x80" }},
		{"empty head", func(p *signature.Payload) { p.Head = "" }},
		{"head 39 characters", func(p *signature.Payload) { p.Head = strings.Repeat("b", 39) }},
		{"head 41 characters", func(p *signature.Payload) { p.Head = strings.Repeat("b", 41) }},
		{"head 63 characters", func(p *signature.Payload) { p.Head = strings.Repeat("b", 63) }},
		{"head uppercase", func(p *signature.Payload) { p.Head = strings.Repeat("B", 40) }},
		{"empty role", func(p *signature.Payload) { p.Role = "" }},
		{"role planner", func(p *signature.Payload) { p.Role = model.RolePlanner }},
		{"role executor", func(p *signature.Payload) { p.Role = model.RoleExecutor }},
		{"round 0", func(p *signature.Payload) { p.Round = 0 }},
		{"round negative", func(p *signature.Payload) { p.Round = -1 }},
		{"round above 2^53-1", func(p *signature.Payload) { p.Round = 1 << 53 }},
		{"empty verdict", func(p *signature.Payload) { p.Verdict = "" }},
		{"verdict lowercase", func(p *signature.Payload) { p.Verdict = "approve" }},
		{"negative blocking count", func(p *signature.Payload) { p.BlockingCount = -1 }},
		{"blocking count above 2^53-1", func(p *signature.Payload) { p.BlockingCount = 1 << 53 }},
		{"empty findings digest", func(p *signature.Payload) { p.FindingsDigest = "" }},
		{"findings digest 63 characters", func(p *signature.Payload) { p.FindingsDigest = emptyDigest[:63] }},
		{"findings digest uppercase", func(p *signature.Payload) { p.FindingsDigest = strings.ToUpper(emptyDigest) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := goldenPayload()
			tt.mutate(&p)
			got, err := signature.Canonical(p)
			if !errors.Is(err, signature.ErrInvalidPayload) {
				t.Fatalf("Canonical = %q, %v; want ErrInvalidPayload", got, err)
			}
		})
	}
}

func TestCanonicalAcceptsValidVariants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*signature.Payload)
	}{
		{"64-character head", func(p *signature.Payload) { p.Head = strings.Repeat("c", 64) }},
		{"role r2", func(p *signature.Payload) { p.Role = model.RoleR2 }},
		{"REQUEST_CHANGES", func(p *signature.Payload) { p.Verdict = model.ReviewVerdictRequestChanges }},
		{"round 2^53-1", func(p *signature.Payload) { p.Round = 1<<53 - 1 }},
		{"blocking count 7", func(p *signature.Payload) { p.BlockingCount = 7 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := goldenPayload()
			tt.mutate(&p)
			if _, err := signature.Canonical(p); err != nil {
				t.Fatalf("Canonical: %v", err)
			}
		})
	}
	p := goldenPayload()
	p.Round = 1<<53 - 1
	got, _ := signature.Canonical(p)
	if !strings.Contains(string(got), `"round":9007199254740991,`) {
		t.Fatalf("Canonical = %s, want round 9007199254740991", got)
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestFindingsDigestEmpty(t *testing.T) {
	if sha256Hex("[]") != emptyDigest {
		t.Fatalf("emptyDigest constant is not sha256(\"[]\")")
	}
	for name, fs := range map[string][]model.Finding{"nil": nil, "empty": {}} {
		got, err := signature.FindingsDigest(fs)
		if err != nil || got != emptyDigest {
			t.Fatalf("FindingsDigest(%s) = %q, %v; want %q", name, got, err, emptyDigest)
		}
	}
}

func TestFindingsDigestGolden(t *testing.T) {
	f := model.Finding{
		Key:           model.TaskKey(strings.Repeat("d", 64)),
		ReviewID:      "review-1",
		Role:          model.RoleR2,
		HeadSHA:       strings.Repeat("e", 40),
		ID:            "f1",
		Severity:      model.SeverityP2,
		Category:      model.FindingCategoryCorrectness,
		Blocking:      true,
		Routed:        model.FindingRouteFixing,
		Evidence:      "x",
		WontFixReason: "because",
	}
	got, err := signature.FindingsDigest([]model.Finding{f})
	if err != nil {
		t.Fatalf("FindingsDigest: %v", err)
	}
	const want = "c3ee3aeae12745fbf1ed0801df06f672fbd7e6f0bc92f3e72cd8d966926ada3a"
	if got != want {
		t.Fatalf("FindingsDigest = %q, want %q", got, want)
	}
	if sha256Hex(`[{"category":"correctness","evidence":"x","id":"f1","severity":"P2"}]`) != want {
		t.Fatalf("golden digest is not the hash of the expected canonical array")
	}
}

func TestFindingsDigestOptionalKeys(t *testing.T) {
	f := model.Finding{
		ID:            "f1",
		PriorID:       "p0",
		Severity:      model.SeverityP1,
		Category:      model.FindingCategorySecurity,
		SecurityLevel: model.SecurityLevelHigh,
		Evidence:      "e</>&",
		Location:      "a/b.go:1",
		FixHint:       "h\n",
	}
	got, err := signature.FindingsDigest([]model.Finding{f})
	if err != nil {
		t.Fatalf("FindingsDigest: %v", err)
	}
	want := sha256Hex(`[{"category":"security","evidence":"e</>&","fixHint":"h\n","id":"f1","location":"a/b.go:1","priorId":"p0","securityLevel":"High","severity":"P1"}]`)
	if got != want {
		t.Fatalf("FindingsDigest = %q, want %q", got, want)
	}
}

func TestFindingsDigestOrderIndependent(t *testing.T) {
	a := model.Finding{ID: "a", Severity: model.SeverityP3, Category: model.FindingCategoryDocs, Evidence: "1"}
	b := model.Finding{ID: "b", Severity: model.SeverityP0, Category: model.FindingCategoryData, Evidence: "2"}
	ab, err := signature.FindingsDigest([]model.Finding{a, b})
	if err != nil {
		t.Fatalf("FindingsDigest: %v", err)
	}
	ba, err := signature.FindingsDigest([]model.Finding{b, a})
	if err != nil {
		t.Fatalf("FindingsDigest: %v", err)
	}
	if ab != ba {
		t.Fatalf("digest depends on order: %q vs %q", ab, ba)
	}
	want := sha256Hex(`[{"category":"docs","evidence":"1","id":"a","severity":"P3"},{"category":"data","evidence":"2","id":"b","severity":"P0"}]`)
	if ab != want {
		t.Fatalf("FindingsDigest = %q, want %q", ab, want)
	}
}

func TestFindingsDigestErrors(t *testing.T) {
	valid := model.Finding{ID: "f1", Severity: model.SeverityP2, Category: model.FindingCategoryCorrectness, Evidence: "x"}
	tests := []struct {
		name string
		fs   []model.Finding
	}{
		{"duplicate id", []model.Finding{valid, {ID: "f1", Severity: model.SeverityP3, Category: model.FindingCategoryDocs, Evidence: "y"}}},
		{"empty id", []model.Finding{{Severity: model.SeverityP2, Category: model.FindingCategoryCorrectness, Evidence: "x"}}},
		{"empty evidence", []model.Finding{{ID: "f1", Severity: model.SeverityP2, Category: model.FindingCategoryCorrectness}}},
		{"unknown severity", []model.Finding{{ID: "f1", Severity: "P9", Category: model.FindingCategoryCorrectness, Evidence: "x"}}},
		{"empty severity", []model.Finding{{ID: "f1", Category: model.FindingCategoryCorrectness, Evidence: "x"}}},
		{"unknown category", []model.Finding{{ID: "f1", Severity: model.SeverityP2, Category: "style", Evidence: "x"}}},
		{"unknown security level", []model.Finding{{ID: "f1", Severity: model.SeverityP2, Category: model.FindingCategorySecurity, SecurityLevel: "high", Evidence: "x"}}},
		{"invalid UTF-8 evidence", []model.Finding{{ID: "f1", Severity: model.SeverityP2, Category: model.FindingCategoryCorrectness, Evidence: "\xff"}}},
		{"invalid UTF-8 location", []model.Finding{{ID: "f1", Severity: model.SeverityP2, Category: model.FindingCategoryCorrectness, Evidence: "x", Location: "\xff"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := signature.FindingsDigest(tt.fs)
			if !errors.Is(err, signature.ErrInvalidPayload) {
				t.Fatalf("FindingsDigest = %q, %v; want ErrInvalidPayload", got, err)
			}
		})
	}
}

func TestKeyID(t *testing.T) {
	// RFC 8032 section 7.1, TEST 1 public key.
	pub, err := hex.DecodeString("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	if err != nil {
		t.Fatal(err)
	}
	got, err := signature.KeyID(ed25519.PublicKey(pub))
	if err != nil || got != "21fe31dfa154a261626bf854046fd227" {
		t.Fatalf("KeyID = %q, %v; want 21fe31dfa154a261626bf854046fd227", got, err)
	}
	for _, n := range []int{0, 31, 33, 64} {
		if _, err := signature.KeyID(make(ed25519.PublicKey, n)); !errors.Is(err, signature.ErrInvalidKey) {
			t.Fatalf("KeyID(%d bytes) error = %v, want ErrInvalidKey", n, err)
		}
	}
}
