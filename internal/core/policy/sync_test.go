package policy_test

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/syllik/deep-dark-factory/internal/core/policy"
)

var (
	itemLine = regexp.MustCompile("^- `([^`]+)`$")
	token    = regexp.MustCompile("`([^`]+)`")
)

// forbiddenTail is the Go-only tail of DefaultForbiddenGlobs: the forge and
// CI equivalents, then the spec §6.5 agent directories at any depth.
func forbiddenTail() []string {
	return []string{
		".gitlab/**", ".gitea/**", ".forgejo/**", ".circleci/**", ".buildkite/**", ".woodpecker/**",
		".woodpecker.yml", ".woodpecker.yaml", ".drone.yml", ".travis.yml", "bitbucket-pipelines.yml",
		"azure-pipelines.yml", "Jenkinsfile", "docs/CODEOWNERS",
		"**/.opencode/**", "**/.claude/**",
	}
}

// syncBlock returns the lines between <!-- sync:NAME --> and
// <!-- /sync:NAME -->; the text must hold exactly one such block.
func syncBlock(text, name string) ([]string, error) {
	start, end := "<!-- sync:"+name+" -->", "<!-- /sync:"+name+" -->"
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var s, e []int
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case start:
			s = append(s, i)
		case end:
			e = append(e, i)
		}
	}
	if len(s) != 1 || len(e) != 1 || e[0] <= s[0] {
		return nil, fmt.Errorf("sync:%s: want exactly one block, found %d starts and %d ends", name, len(s), len(e))
	}
	return lines[s[0]+1 : e[0]], nil
}

// compareList checks that every block line is one backticked glob and that
// the globs equal want in length, order and items.
func compareList(block, want []string) error {
	var got []string
	for i, l := range block {
		m := itemLine.FindStringSubmatch(l)
		if m == nil {
			return fmt.Errorf("line %d %q is not a single backticked glob", i+1, l)
		}
		got = append(got, m[1])
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("doc list %q != Go list %q", got, want)
	}
	return nil
}

// compareForbidden checks the prose sync:forbidden block against
// DefaultForbiddenGlobs.
func compareForbidden(block, want []string) error {
	if len(block) != 6 {
		return fmt.Errorf("forbidden block has %d lines, want 6", len(block))
	}
	if !strings.Contains(block[1], "equivalents") {
		return fmt.Errorf("line 2 %q lacks 'equivalents'", block[1])
	}
	if !strings.HasPrefix(block[5], "- symbolic links") {
		return fmt.Errorf("line 6 %q is not the symbolic-links rule", block[5])
	}
	var got []string
	for _, l := range block {
		for _, m := range token.FindAllStringSubmatch(l, -1) {
			got = append(got, m[1])
		}
	}
	if len(want) < len(got) || !slices.Equal(got, want[:len(got)]) || len(got) != 17 {
		return fmt.Errorf("doc tokens %q != Go head %q", got, want[:min(17, len(want))])
	}
	if !slices.Equal(want[17:], forbiddenTail()) {
		return fmt.Errorf("Go tail %q != %q", want[17:], forbiddenTail())
	}
	return nil
}

func TestSyncBlocks(t *testing.T) {
	lists := []struct {
		name string
		want []string
	}{
		{"lockfiles", policy.DefaultLockfileGlobs()},
		{"agent-instruction-files", policy.DefaultAgentInstructionGlobs()},
		{"hard-stop-globs", policy.DefaultHardStopGlobs()},
	}
	for _, doc := range []string{"../../../docs/specs/2026-11-03-ddf-v1-design.md", "../../../docs/concepts/policy.md"} {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, l := range lists {
			block, err := syncBlock(text, l.name)
			if err == nil {
				err = compareList(block, l.want)
			}
			if err != nil {
				t.Errorf("%s: %v", doc, err)
			}
		}
		block, err := syncBlock(text, "forbidden")
		if err == nil {
			err = compareForbidden(block, policy.DefaultForbiddenGlobs())
		}
		if err != nil {
			t.Errorf("%s: %v", doc, err)
		}
		if strings.Contains(doc, "specs") && !strings.Contains(text, "`**/.opencode/**`, `**/.claude/**`") {
			t.Errorf("%s: lacks the §6.5 agent directories at any depth", doc)
		}
	}
}

func TestSyncBlocks_DetectDrift(t *testing.T) {
	want := []string{"a/**", "b", "c"}
	lines := func(items ...string) []string {
		var out []string
		for _, i := range items {
			out = append(out, "- `"+i+"`")
		}
		return out
	}
	if err := compareList(lines(want...), want); err != nil {
		t.Fatalf("equal lists: %v", err)
	}
	drifts := map[string][]string{
		"reordered": lines("b", "a/**", "c"),
		"extra":     lines("a/**", "b", "c", "d"),
		"missing":   lines("a/**", "c"),
		"prose":     append(lines("a/**", "b"), "- `c` and more"),
	}
	for name, block := range drifts {
		if err := compareList(block, want); err == nil {
			t.Errorf("%s: no mismatch reported", name)
		}
	}
	if _, err := syncBlock("<!-- sync:x -->\n- `a`\n<!-- /sync:x -->\n<!-- sync:x -->\n<!-- /sync:x -->", "x"); err == nil {
		t.Error("two blocks: no error")
	}
	if _, err := syncBlock("no block", "x"); err == nil {
		t.Error("no block: no error")
	}

	fb := []string{
		"- `.ddf/**`;",
		"- forge and CI configuration: `.github/**`, `.gitlab-ci.yml`, equivalents;",
		"- Git behaviour: `.gitmodules`, `.gitattributes`, `.gitignore` of the repository root;",
		"- agent configuration and instructions: `AGENTS.md`, `CLAUDE.md`, `.claude/**`, `.mcp.json`, `opencode.json`, `.opencode/**`, `.codex/**`, `.cursor/**`;",
		"- `CODEOWNERS`, `LICENSE`, `.env*`;",
		"- symbolic links (any added or changed symlink is refused in V1).",
	}
	full := policy.DefaultForbiddenGlobs()
	if err := compareForbidden(fb, full); err != nil {
		t.Fatalf("equal forbidden: %v", err)
	}
	swapped := slices.Clone(full)
	swapped[1], swapped[2] = swapped[2], swapped[1]
	for name, w := range map[string][]string{
		"reordered":    swapped,
		"missing head": slices.Delete(slices.Clone(full), 3, 4),
		"extra tail":   append(slices.Clone(full), "x/**"),
		"missing tail": full[:len(full)-1],
	} {
		if err := compareForbidden(fb, w); err == nil {
			t.Errorf("forbidden %s: no mismatch reported", name)
		}
	}
}
