package publisher

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxSanitizedBytes caps Sanitize's output before the truncation line.
	MaxSanitizedBytes = 16384
	// MaxLineRunes caps SanitizeLine's output.
	MaxLineRunes = 120
	// Withheld replaces agent text that does not reach a fixed point.
	Withheld = "[agent text withheld]"

	sanitizeRounds = 8
	truncatedLine  = "\n[truncated]"
)

// sanitizeRules holds the compiled patterns of Sanitize rules b and c. ws
// includes \p{Z} so that collapsing whitespace in SanitizeLine cannot join a
// keyword the rules did not already see.
type sanitizeRules struct {
	closing *regexp.Regexp
	ciSkip  *regexp.Regexp
}

func newSanitizeRules() sanitizeRules {
	const ws = `[\s\p{Z}]`
	ref := `(?P<ref>#[0-9]+|[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+#[0-9]+|(?i:GH)-[0-9]+|(?i:https?)://[^\s\p{Z}]*/(?:issues|pull)/[0-9]+)`
	return sanitizeRules{
		closing: regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b` + ws + `*:?` + ws + `*` + ref),
		ciSkip: regexp.MustCompile(`(?i)\[` + ws + `*(?:skip[\s\p{Z}_-]*ci|ci[\s\p{Z}_-]*skip|no[\s\p{Z}_-]*ci|skip[\s\p{Z}_-]*actions|actions[\s\p{Z}_-]*skip|skip[\s\p{Z}_-]*checks)` + ws + `*\]` +
			`|skip-checks` + ws + `*:` + ws + `*true|\*\*\*NO_CI\*\*\*`),
	}
}

// Sanitize makes agent-written text safe for the change's sanitized section:
// it removes control, zero-width and bidi characters, closing keywords before
// a task reference (the reference stays), CI-skip directives and the '@' of
// mentions, and escapes HTML comment markers. The rules repeat until the text
// stops changing; text that does not settle within the round limit becomes
// Withheld (fail closed). The result is capped at MaxSanitizedBytes.
func Sanitize(s string) string {
	return sanitize(s, sanitizeRounds)
}

func sanitize(s string, rounds int) string {
	rules := newSanitizeRules()
	for range rounds {
		next := rules.once(s)
		if next == s {
			return capBytes(s)
		}
		s = next
	}
	return Withheld
}

func (r sanitizeRules) once(s string) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	s = strings.Map(func(c rune) rune {
		if (unicode.IsControl(c) && c != '\n' && c != '\t') || hiddenRune(c) {
			return -1
		}
		return c
	}, s)
	s = r.closing.ReplaceAllString(s, "${ref}")
	s = r.ciSkip.ReplaceAllString(s, "")
	s = stripMentions(s)
	return strings.NewReplacer("<!--", "&lt;!--", "-->", "--&gt;").Replace(s)
}

// hiddenRune reports zero-width and bidi control characters, which can split
// or hide a keyword.
func hiddenRune(c rune) bool {
	return (c >= 0x200B && c <= 0x200D) || c == 0x2060 || c == 0xFEFF ||
		(c >= 0x202A && c <= 0x202E) || (c >= 0x2066 && c <= 0x2069)
}

// stripMentions drops the '@' of a mention: an '@' followed by an ASCII letter
// or digit, at the start or after a rune that cannot precede a mention. An
// '@' inside an e-mail address stays. A backtick gives no exemption: it may
// close a code span right before the '@' (fail closed).
func stripMentions(s string) string {
	var b strings.Builder
	prev := rune(-1)
	for i, c := range s {
		if c == '@' && i+1 < len(s) && isASCIIAlnum(s[i+1]) && mentionMayFollow(prev) {
			prev = c
			continue
		}
		b.WriteRune(c)
		prev = c
	}
	return b.String()
}

func mentionMayFollow(prev rune) bool {
	if prev < 0 {
		return true
	}
	return prev >= utf8.RuneSelf || !isASCIIAlnum(byte(prev)) && !strings.ContainsRune("_.-/@", prev)
}

func isASCIIAlnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// capBytes cuts s to MaxSanitizedBytes at a rune boundary and appends the
// truncation line; text already capped this way is returned unchanged, so
// Sanitize stays idempotent.
func capBytes(s string) string {
	if len(s) <= MaxSanitizedBytes {
		return s
	}
	if strings.HasSuffix(s, truncatedLine) && len(s)-len(truncatedLine) <= MaxSanitizedBytes {
		return s
	}
	cut := MaxSanitizedBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncatedLine
}

// SanitizeLine sanitizes s for a single line (titles, path names): Sanitize,
// then every whitespace run becomes one space, trimmed, capped at
// MaxLineRunes.
func SanitizeLine(s string) string {
	line := strings.Join(strings.Fields(Sanitize(s)), " ")
	if utf8.RuneCountInString(line) <= MaxLineRunes {
		return line
	}
	return strings.TrimSpace(string([]rune(line)[:MaxLineRunes]))
}
