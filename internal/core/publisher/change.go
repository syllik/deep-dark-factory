package publisher

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

// Sentinel errors of RenderBody and SquashMessage.
var (
	ErrInvalidTemplate   = errors.New("invalid change template")
	ErrInvalidChangeText = errors.New("invalid change text")
)

const maxTaskRefBytes = 100

// VerifyLine is one local-gate verify command and its result.
type VerifyLine struct {
	Command string
	Passed  bool
}

// ChangeText is the input of the change body and squash message. Only
// AgentNotes is agent-written; every other field comes from DDF.
type ChangeText struct {
	TaskRef        string
	Title          string
	Class          model.RiskClass
	ChangedPaths   []string
	Verify         []VerifyLine
	RequiredChecks []string
	AgentNotes     string
	Authorization  string
}

func (c ChangeText) validate() error {
	ref := regexp.MustCompile(`^[A-Za-z0-9._/#:-]+$`)
	if len(c.TaskRef) > maxTaskRefBytes || !ref.MatchString(c.TaskRef) {
		return fmt.Errorf("%w: task reference %q", ErrInvalidChangeText, c.TaskRef)
	}
	if _, err := model.ParseRiskClass(string(c.Class)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidChangeText, err)
	}
	return nil
}

// summary is the DDF-written {summary} text; no agent text appears in it.
func (c ChangeText) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Implements %s: %s\n\nRisk class: %s\n\n", c.TaskRef, SanitizeLine(c.Title), c.Class)
	if len(c.ChangedPaths) == 0 {
		b.WriteString("Changed paths: none")
		return b.String()
	}
	paths := make([]string, 0, len(c.ChangedPaths))
	for _, p := range c.ChangedPaths {
		paths = append(paths, "- "+strings.ReplaceAll(SanitizeLine(p), "`", "'"))
	}
	slices.Sort(paths)
	b.WriteString("Changed paths:\n" + strings.Join(paths, "\n"))
	return b.String()
}

func (c ChangeText) verify() string {
	var b strings.Builder
	for _, v := range c.Verify {
		result := "failed"
		if v.Passed {
			result = "passed"
		}
		fmt.Fprintf(&b, "- `%s`: %s (local gate)\n", SanitizeLine(v.Command), result)
	}
	checks := make([]string, 0, len(c.RequiredChecks))
	for _, rc := range c.RequiredChecks {
		checks = append(checks, SanitizeLine(rc))
	}
	if len(checks) == 0 {
		b.WriteString("Required checks: none")
	} else {
		b.WriteString("Required checks: " + strings.Join(checks, ", "))
	}
	return b.String()
}

// RenderBody renders the change body from the change template
// (templates/project/.ddf/change-template.md,
// docs/concepts/lifecycle.md#publishing). HTML comments are removed from the template first; it must then hold
// {task}, {summary}, {verify} and {agentNotes} and no other placeholder except
// {authorization}. Substitution is single-pass, so inserted text is never
// expanded again; {task} is 'Closes <TaskRef>', the only closing keyword DDF
// writes, and agent text appears only in {agentNotes}. The whole body is
// redacted last.
func RenderBody(tmpl string, c ChangeText, r Redactor) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	tmpl = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(tmpl, "")
	if strings.Contains(tmpl, "<!--") {
		return "", fmt.Errorf("%w: unterminated comment", ErrInvalidTemplate)
	}
	required := []string{"{task}", "{summary}", "{verify}", "{agentNotes}"}
	for _, tok := range regexp.MustCompile(`\{[A-Za-z0-9_]*\}`).FindAllString(tmpl, -1) {
		if tok != "{authorization}" && !slices.Contains(required, tok) {
			return "", fmt.Errorf("%w: unknown placeholder %s", ErrInvalidTemplate, tok)
		}
	}
	for _, tok := range required {
		if !strings.Contains(tmpl, tok) {
			return "", fmt.Errorf("%w: missing placeholder %s", ErrInvalidTemplate, tok)
		}
	}
	auth := SanitizeLine(c.Authorization)
	if auth == "" {
		lines := strings.Split(tmpl, "\n")
		lines = slices.DeleteFunc(lines, func(l string) bool { return strings.Contains(l, "{authorization}") })
		tmpl = strings.Join(lines, "\n")
	}
	notes := Sanitize(c.AgentNotes)
	if strings.TrimSpace(notes) == "" {
		notes = "none"
	}
	body := strings.NewReplacer(
		"{task}", "Closes "+c.TaskRef,
		"{authorization}", auth,
		"{summary}", c.summary(),
		"{verify}", c.verify(),
		"{agentNotes}", notes,
	).Replace(tmpl)
	return r.Redact(body), nil
}

// SquashMessage returns the squash message, which is also the revision's
// commit message: the sanitized title, the DDF summary and 'Closes <TaskRef>',
// redacted. It holds no agent notes.
func SquashMessage(c ChangeText, r Redactor) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	msg := SanitizeLine(c.Title) + "\n\n" + c.summary() + "\n\nCloses " + c.TaskRef + "\n"
	return r.Redact(msg), nil
}
