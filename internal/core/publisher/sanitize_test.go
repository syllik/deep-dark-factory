package publisher_test

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/syllik/deep-dark-factory/internal/core/publisher"
)

// Detectors for the property test; independent of the implementation's
// patterns where the rule allows it.
var (
	closingRef = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b\s*:?\s*(?:#[0-9]|[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[0-9]|gh-[0-9]|https?://)`)
	ciSkip     = regexp.MustCompile(`(?i)\[\s*(?:skip|ci|no|actions)[\s_-]*(?:ci|skip|actions|checks)\s*\]|skip-checks\s*:\s*true|\*\*\*no_ci\*\*\*`)
	mention    = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.\-/@])@[A-Za-z0-9]`)
)

func sanitizeCases() []struct{ name, in, want string } {
	nested := "[skip ci]"
	for range 10 {
		nested = "[sk" + nested + "ip ci]"
	}
	return []struct{ name, in, want string }{
		{"b keyword before issue", "Fixes #12", "#12"},
		{"b keyword colon owner/repo", "closes: owner/repo#3", "owner/repo#3"},
		{"b keyword before URL", "Resolved https://x.example/o/r/issues/4", "https://x.example/o/r/issues/4"},
		{"b keyword before GH ref", "see fix GH-7 now", "see GH-7 now"},
		{"b keyword without reference stays", "fixes the bug", "fixes the bug"},
		{"a zero-width split keyword", "Clo\u200bses #5", "#5"},
		{"a bidi controls removed", "a\u202eb\u2066c", "abc"},
		{"a controls and CR", "a\x00b\r\nc\rd\u0085e\tf", "ab\nc\nde\tf"},
		{"a invalid UTF-8", "a\xffb", "a�b"},
		{"c nested skip ci", "[sk[skip ci]ip ci]", ""},
		{"c ci skip upper", "x [CI SKIP] y", "x  y"},
		{"c skip-actions", "[skip-actions]", ""},
		{"c skip_checks inner spacing", "[ skip _checks ]", ""},
		{"c no ci", "[no ci]", ""},
		{"c trailer", "Skip-Checks : true", ""},
		{"c NO_CI", "***NO_CI***", ""},
		{"d mention", "@alice hi", "alice hi"},
		{"d team mention", "(@org/team)", "(org/team)"},
		{"d e-mail kept", "a@b.example", "a@b.example"},
		{"d backtick gives no exemption", "`@x`", "`x`"},
		{"d after a closed code span", "`x`@alice", "`x`alice"},
		{"d after a quote", "a/'@org/team'.go", "a/'org/team'.go"},
		{"d after a non-ASCII letter", "\u00e9@alice", "\u00e9alice"},
		{"e comment escaped", "<!-- ddf:task-key x -->", "&lt;!-- ddf:task-key x --&gt;"},
		{"e nested comment marker", "<!-->", "&lt;!--&gt;"},
		{"nesting keyword in keyword", "Fixes Fixes #1", "#1"},
		{"nesting never converges", nested, publisher.Withheld},
	}
}

func TestSanitize_Rules(t *testing.T) {
	for _, tc := range sanitizeCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got := publisher.Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitize_Truncates(t *testing.T) {
	in := strings.Repeat("a", 20000)
	got := publisher.Sanitize(in)
	if want := in[:publisher.MaxSanitizedBytes] + "\n[truncated]"; got != want {
		t.Fatalf("Sanitize(20000 bytes) has %d bytes, want %d", len(got), len(want))
	}
	if again := publisher.Sanitize(got); again != got {
		t.Errorf("Sanitize is not idempotent on truncated text")
	}
	multi := strings.Repeat("é", 10000) // 2 bytes each; the cut falls inside no rune
	if got := publisher.Sanitize("a" + multi); !utf8.ValidString(got) || publisher.Sanitize(got) != got {
		t.Errorf("Sanitize cut a rune or is not idempotent: %q...", got[:8])
	}
}

func TestSanitize_Properties(t *testing.T) {
	inputs := []string{strings.Repeat("Fixes #1 @a [skip ci] <!-- ", 1000)}
	for _, tc := range sanitizeCases() {
		inputs = append(inputs, tc.in, "pre "+tc.in+" post")
	}
	for i, in := range inputs {
		for name, f := range map[string]func(string) string{"Sanitize": publisher.Sanitize, "SanitizeLine": publisher.SanitizeLine} {
			out := f(in)
			if again := f(out); again != out {
				t.Errorf("%s not idempotent on input %d: %q -> %q", name, i, out, again)
			}
			for rule, re := range map[string]*regexp.Regexp{"closing": closingRef, "ci-skip": ciSkip, "mention": mention} {
				if re.MatchString(out) {
					t.Errorf("%s output of input %d matches %s: %q", name, i, rule, out)
				}
			}
			if strings.Contains(out, "<!--") {
				t.Errorf("%s output of input %d contains a comment marker", name, i)
			}
		}
	}
}

func TestSanitizeLine_CollapsesAndCaps(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"whitespace runs", "  a\n\tb \u00a0 c  ", "a b c"},
		{"keyword split by newline", "fixes\n#4 done", "#4 done"},
		{"rune cap", strings.Repeat("ж", 130), strings.Repeat("ж", publisher.MaxLineRunes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := publisher.SanitizeLine(tc.in); got != tc.want {
				t.Errorf("SanitizeLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
