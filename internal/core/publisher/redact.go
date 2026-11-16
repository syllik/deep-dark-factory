package publisher

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/ports"
)

const (
	redactedMark = "[redacted]"
	redactorMark = "[redactor]"
)

// secretPattern is one entry of the redaction pattern table (spec §8.4).
type secretPattern struct {
	name string
	re   *regexp.Regexp
	// repl is the replacement template; it keeps the non-secret parts of the
	// match.
	repl string
}

// secretPatterns returns the fixed pattern table. It is built per call so the
// package holds no mutable state.
func secretPatterns() []secretPattern {
	p := func(name, expr string) secretPattern {
		return secretPattern{name: name, re: regexp.MustCompile(expr), repl: redactedMark}
	}
	url := p("url-credentials", `\b([A-Za-z][A-Za-z0-9+.-]*://)[^\s/@:]+:[^\s/]*@`)
	url.repl = "${1}" + redactedMark + "@"
	return []secretPattern{
		p("private-key", `-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----(?:[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----)?`),
		p("github-token", `\bgh[pousr]_[A-Za-z0-9]{36,}\b`),
		p("github-pat", `\bgithub_pat_[A-Za-z0-9_]{22,}\b`),
		p("aws-access-key-id", `\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		p("slack-token", `\bxox[abposr]-[A-Za-z0-9-]{10,}`),
		p("api-key-sk", `\bsk-[A-Za-z0-9_-]{20,}`),
		p("telegram-bot-token", `\b[0-9]{8,10}:[A-Za-z0-9_-]{35}`),
		p("google-api-key", `\bAIza[0-9A-Za-z_-]{35}`),
		p("jwt", `\beyJ[A-Za-z0-9_-]{7,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
		url,
	}
}

// Redactor removes secrets from text: the exact values loaded into a run's
// container and the matches of the pattern table (spec §8.4). It prints as
// [redactor], never as a value.
type Redactor struct {
	values []ports.Secret
}

// NewRedactor returns a Redactor for the loaded secrets; empty secrets are
// skipped.
func NewRedactor(loaded []ports.Secret) Redactor {
	var values []ports.Secret
	for _, s := range loaded {
		if !s.IsEmpty() {
			values = append(values, s)
		}
	}
	return Redactor{values: values}
}

// String returns the fixed marker [redactor].
func (r Redactor) String() string { return redactorMark }

// GoString returns the fixed marker [redactor].
func (r Redactor) GoString() string { return redactorMark }

// Format writes the fixed marker [redactor] for every verb and flag.
func (r Redactor) Format(f fmt.State, _ rune) {
	// A write error cannot be reported from Format.
	_, _ = io.WriteString(f, redactorMark)
}

// loaded returns the loaded values, longest first, so a value that overlaps
// another leaves no fragment.
func (r Redactor) loaded() []string {
	out := make([]string, 0, len(r.values))
	for _, s := range r.values {
		out = append(out, string(s.Bytes()))
	}
	slices.SortStableFunc(out, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b))
	})
	return out
}

// Redact replaces every occurrence of a loaded value, then every pattern
// match, with [redacted]; url-credentials keeps the scheme and host. Values
// are replaced one at a time, longest first, and only between existing
// [redacted] markers, so overlapping values leave no fragment and Redact is
// idempotent.
func (r Redactor) Redact(s string) string {
	for _, v := range r.loaded() {
		parts := strings.Split(s, redactedMark)
		for i, p := range parts {
			parts[i] = strings.ReplaceAll(p, v, redactedMark)
		}
		s = strings.Join(parts, redactedMark)
	}
	for _, p := range secretPatterns() {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// Hit reports which secrets a new blob adds compared with an old one. It never
// carries the matched text.
type Hit struct {
	// Loaded is true when some loaded value occurs in new and not in old.
	Loaded bool
	// Patterns lists, sorted and unique, the names of patterns with a match in
	// new that does not occur in old.
	Patterns []string
}

// NewHits compares old and new contents, which may be binary.
func (r Redactor) NewHits(old, new []byte) Hit {
	var h Hit
	for _, v := range r.loaded() {
		if bytes.Contains(new, []byte(v)) && !bytes.Contains(old, []byte(v)) {
			h.Loaded = true
			break
		}
	}
	for _, p := range secretPatterns() {
		for _, m := range p.re.FindAll(new, -1) {
			if !bytes.Contains(old, m) {
				h.Patterns = append(h.Patterns, p.name)
				break
			}
		}
	}
	slices.Sort(h.Patterns)
	h.Patterns = slices.Compact(h.Patterns)
	return h
}
