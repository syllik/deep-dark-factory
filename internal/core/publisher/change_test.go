package publisher_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/model"
	"github.com/syllik/deep-dark-factory/internal/core/publisher"
)

func changeTemplate(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "templates", "project", ".ddf", "change-template.md"))
	if err != nil {
		t.Fatalf("read change template: %v", err)
	}
	return string(b)
}

// hostileChange puts a closing keyword into every agent- or input-controlled
// field and a loaded value into the title and agent notes.
func hostileChange() publisher.ChangeText {
	return publisher.ChangeText{
		TaskRef:        "#109",
		Title:          "Fixes #2 publisher text test-secret-gamma",
		Class:          model.RiskClassSensitive,
		ChangedPaths:   []string{"b/closes #3.go", "a/`x`.go", "c/`@org/team`.go"},
		Verify:         []publisher.VerifyLine{{Command: "go test ./...", Passed: true}, {Command: "go vet ./...", Passed: false}},
		RequiredChecks: []string{"verify", "docs"},
		AgentNotes:     "Notes: closes #3 @alice [skip ci] <!-- ddf:x --> test-secret-gamma",
		Authorization:  "owner said Fixes #2",
	}
}

func TestRenderBody_RealTemplate(t *testing.T) {
	tmpl := changeTemplate(t)
	r := publisher.NewRedactor(secrets("test-secret-gamma"))
	got, err := publisher.RenderBody(tmpl, hostileChange(), r)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"starts with task line", strings.HasPrefix(got, "Task: Closes #109\n")},
		{"authorization line sanitized", strings.Contains(got, "\nAuthorization: owner said #2\n")},
		{"no comment marker", !strings.Contains(got, "<!--")},
		{"one closing keyword", len(closingRef.FindAllString(got, -1)) == 1},
		{"no placeholder", !strings.Contains(got, "{")},
		{"loaded value only redacted", !strings.Contains(got, "test-secret-gamma") && strings.Count(got, "[redacted]") == 2},
		{"summary", strings.Contains(got, "Implements #109: #2 publisher text [redacted]\n\nRisk class: sensitive\n\nChanged paths:\n- a/'x'.go\n- b/#3.go\n- c/'org/team'.go")},
		{"verify", strings.Contains(got, "- `go test ./...`: passed (local gate)\n- `go vet ./...`: failed (local gate)\nRequired checks: verify, docs")},
		{"agent notes sanitized", strings.Contains(got, "Notes: #3 alice  &lt;!-- ddf:x --&gt; [redacted]")},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: body\n%s", c.name, got)
		}
	}

	c := hostileChange()
	c.Authorization, c.AgentNotes, c.RequiredChecks, c.ChangedPaths = "", "", nil, nil
	got, err = publisher.RenderBody(tmpl, c, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Task: Closes #109\n\n## Summary", "Required checks: none", "Changed paths: none", "## Agent notes (sanitized)\n\nnone"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty fields: body lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Authorization:") {
		t.Errorf("empty authorization kept its line:\n%s", got)
	}
}

func TestRenderBody_Errors(t *testing.T) {
	full := "{task}\n{summary}\n{verify}\n{agentNotes}\n"
	withRef := func(ref string) publisher.ChangeText { c := hostileChange(); c.TaskRef = ref; return c }
	badClass := hostileChange()
	badClass.Class = "risky"
	cases := []struct {
		name string
		tmpl string
		c    publisher.ChangeText
		want error
	}{
		{"missing verify", "{task}\n{summary}\n{agentNotes}\n", hostileChange(), publisher.ErrInvalidTemplate},
		{"unknown placeholder", full + "{other}\n", hostileChange(), publisher.ErrInvalidTemplate},
		{"unterminated comment", full + "<!-- x\n", hostileChange(), publisher.ErrInvalidTemplate},
		{"task ref with space", full, withRef("a b"), publisher.ErrInvalidChangeText},
		{"empty task ref", full, withRef(""), publisher.ErrInvalidChangeText},
		{"task ref too long", full, withRef(strings.Repeat("a", 101)), publisher.ErrInvalidChangeText},
		{"unknown class", full, badClass, publisher.ErrInvalidChangeText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := publisher.RenderBody(tc.tmpl, tc.c, publisher.NewRedactor(nil)); !errors.Is(err, tc.want) {
				t.Errorf("RenderBody error = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := publisher.RenderBody(full+"<!-- {other} -->", hostileChange(), publisher.NewRedactor(nil)); err != nil {
		t.Errorf("placeholder inside a comment: %v", err)
	}
}

func TestSquashMessage(t *testing.T) {
	r := publisher.NewRedactor(secrets("test-secret-gamma"))
	got, err := publisher.SquashMessage(hostileChange(), r)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if n := len(closingRef.FindAllString(got, -1)); n != 1 {
		t.Errorf("closing keywords = %d, want 1:\n%s", n, got)
	}
	if last := lines[len(lines)-1]; last != "Closes #109" || !strings.HasSuffix(got, "\n") {
		t.Errorf("last line = %q", last)
	}
	if lines[0] != "#2 publisher text [redacted]" || strings.Contains(got, "Notes") || strings.Contains(got, "test-secret") {
		t.Errorf("message:\n%s", got)
	}
	if _, err := publisher.SquashMessage(publisher.ChangeText{TaskRef: "#1"}, r); !errors.Is(err, publisher.ErrInvalidChangeText) {
		t.Errorf("empty class error = %v", err)
	}
}

// leaks returns the first fragment of a value (any window of five bytes, also
// of the value as Sanitize changes it) that occurs in out.
func leaks(out string, values []string) string {
	const n = 5
	for _, v := range values {
		for _, form := range []string{v, publisher.Sanitize(v), publisher.SanitizeLine(v)} {
			for i := 0; i+n <= len(form); i++ {
				if strings.Contains(out, form[i:i+n]) {
					return form[i : i+n]
				}
			}
		}
	}
	return ""
}

// TestRenderBody_RedactsBeforeSanitizing uses loaded values that the
// sanitizer would change or cut, so only redaction before sanitizing hides
// them.
func TestRenderBody_RedactsBeforeSanitizing(t *testing.T) {
	mention := "Pw!" + "@Zq7xP2"
	comment := "Kv--" + ">Mj4yR8"
	hidden := "Hn\r\nW\u200bb5Lt9"
	cut := "CtQ9wXb3VmZ7"
	line := "LnJ6pYd2HsK4"
	values := []string{mention, comment, hidden, cut, line}
	r := publisher.NewRedactor(secrets(values...))
	several := mention + " " + comment + " " + hidden
	cases := []struct {
		name string
		c    publisher.ChangeText
	}{
		{"altered values", publisher.ChangeText{
			Title:        "t " + several,
			ChangedPaths: []string{"d/" + several + ".go"},
			AgentNotes:   "n " + several,
		}},
		{"byte cap and line cap cut the value", publisher.ChangeText{
			Title:        strings.Repeat("t", publisher.MaxLineRunes-6) + line,
			ChangedPaths: []string{strings.Repeat("p", publisher.MaxLineRunes-8) + line},
			AgentNotes:   strings.Repeat("a", publisher.MaxSanitizedBytes-6) + cut,
		}},
		{"other input fields", publisher.ChangeText{
			Title:          "t",
			Verify:         []publisher.VerifyLine{{Command: "go test " + mention, Passed: true}},
			RequiredChecks: []string{"check " + comment},
			Authorization:  "owner " + hidden,
		}},
	}
	tmpl := changeTemplate(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.TaskRef, tc.c.Class = "#109", model.RiskClassSensitive
			body, err := publisher.RenderBody(tmpl, tc.c, r)
			if err != nil {
				t.Fatal(err)
			}
			msg, err := publisher.SquashMessage(tc.c, r)
			if err != nil {
				t.Fatal(err)
			}
			for name, out := range map[string]string{"body": body, "squash message": msg} {
				if f := leaks(out, values); f != "" {
					t.Errorf("%s leaks fragment %q:\n%s", name, f, out)
				}
			}
			if !strings.Contains(body, "[redac") {
				t.Errorf("body has no marker:\n%s", body)
			}
		})
	}
}
