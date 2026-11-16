package policy

import (
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/syllik/deep-dark-factory/internal/core/model"
)

type globList struct {
	globs []string
	fold  bool // tightening rules match case-insensitively
}

func defaultLists() map[string]globList {
	return map[string]globList{
		"lockfiles": {lockfileGlobs(), false},
		"agent":     {agentInstructionGlobs(), true},
		"hard-stop": {hardStopGlobs(), true},
		"forbidden": {forbiddenGlobs(), true},
	}
}

// globRow is one OD-11 fixture; an empty glob matches against the whole list.
type globRow struct {
	list, glob, path string
	want             bool
}

// pair returns a matching and a non-matching row for one glob.
func pair(list, glob, yes, no string) []globRow {
	return []globRow{{list, glob, yes, true}, {list, glob, no, false}}
}

func globRows() []globRow {
	rows := [][]globRow{
		pair("lockfiles", "**/package-lock.json", "package-lock.json", "package-lock.json5"),
		pair("lockfiles", "**/npm-shrinkwrap.json", "a/npm-shrinkwrap.json", "npm-shrinkwrap.jsonc"),
		pair("lockfiles", "**/yarn.lock", "a/yarn.lock", "Yarn.lock"),
		pair("lockfiles", "**/pnpm-lock.yaml", "web/pnpm-lock.yaml", "pnpm-lock.yml"),
		pair("lockfiles", "**/bun.lock", "bun.lock", "bun.lock.bak"),
		pair("lockfiles", "**/bun.lockb", "a/b/bun.lockb", "bun.lockB"),
		pair("lockfiles", "**/go.sum", "go.sum", "GO.SUM"),
		pair("lockfiles", "**/Cargo.lock", "crates/x/Cargo.lock", "cargo.lock"),
		pair("lockfiles", "**/poetry.lock", "poetry.lock", "poetry.lock/x"),
		pair("lockfiles", "**/Pipfile.lock", "py/Pipfile.lock", "Pipfile"),
		pair("lockfiles", "**/uv.lock", "uv.lock", "uv.locks"),
		pair("lockfiles", "**/Gemfile.lock", "Gemfile.lock", "gemfile.lock"),
		pair("lockfiles", "**/composer.lock", "php/composer.lock", "composer.json"),
		pair("lockfiles", "**/mix.lock", "mix.lock", "mix.exs"),
		pair("lockfiles", "**/pubspec.lock", "app/pubspec.lock", "pubspec.yaml"),
		pair("lockfiles", "**/Package.resolved", "Package.resolved", "package.resolved"),
		pair("lockfiles", "**/packages.lock.json", "src/packages.lock.json", "packages.json"),
		pair("lockfiles", "**/gradle.lockfile", "gradle.lockfile", "gradle.properties"),
		pair("lockfiles", "**/flake.lock", "nix/flake.lock", "flake.nix"),

		pair("agent", "**/AGENTS.md", "a/AGENTS.md", "a/AGENTS.md.bak"),
		pair("agent", "**/CLAUDE.md", "CLAUDE.md", "CLAUDE.txt"),
		pair("agent", "**/GEMINI.md", "x/gemini.md", "GEMINI"),
		pair("agent", "**/SKILL.md", "skills/x/SKILL.md", "SKILLS.md"),
		pair("agent", "**/.cursorrules", "a/.cursorrules", "cursorrules"),
		pair("agent", "**/.windsurfrules", ".windsurfrules", ".windsurfrules.md"),
		pair("agent", "**/.github/copilot-instructions.md", ".github/copilot-instructions.md", "copilot-instructions.md"),
		pair("agent", "**/.agents/**", "a/.agents/x.md", "agents/x.md"),
		pair("agent", "**/.claude/**", ".claude/settings.json", "claude/settings.json"),
		pair("agent", "**/.codex/**", "a/.codex/config.toml", "codex/config.toml"),
		pair("agent", "**/.cursor/**", ".cursor/rules/x.mdc", ".cursorx/rules"),
		pair("agent", "**/.opencode/**", "a/.OpenCode/agent.md", "a/opencode/agent.md"),
		pair("agent", "**/.mcp.json", "a/.mcp.json", "mcp.json"),
		pair("agent", "**/opencode.json", "opencode.json", "opencode.jsonc"),

		pair("hard-stop", "Dockerfile*", "Dockerfile.dev", "a/Dockerfile"),
		pair("hard-stop", "**/Dockerfile*", "a/b/Dockerfile", "a/Dockerfile/x"),
		pair("hard-stop", "docker-compose*.y*ml", "docker-compose.prod.yaml", "a/docker-compose.yml"),
		pair("hard-stop", "**/*.tf", "infra2/main.tf", "main.tfstate"),
		pair("hard-stop", "**/*.tfvars", "prod.tfvars", "prod.tfvars.json"),
		pair("hard-stop", "k8s/**", "k8s", "a/k8s/x.yaml"),
		pair("hard-stop", "helm/**", "helm/chart.yaml", "helmet/x"),
		pair("hard-stop", "deploy/**", "deploy/prod.yaml", "a/deploy/prod.yaml"),
		pair("hard-stop", "infra/**", "infra/main.go", "infrastructure/x"),
		pair("hard-stop", ".terraform/**", ".terraform/modules/x", "a/.terraform/x"),
		pair("hard-stop", "ansible/**", "ansible/site.yml", "ansible.cfg"),

		pair("forbidden", ".ddf/**", ".ddf/project.yaml", "a/.ddf/project.yaml"),
		pair("forbidden", ".github/**", ".github/workflows/ci.yml", "a/.github/x"),
		pair("forbidden", ".gitlab-ci.yml", ".gitlab-ci.yml", "a/.gitlab-ci.yml"),
		pair("forbidden", ".gitmodules", ".gitmodules", "a/.gitmodules"),
		pair("forbidden", ".gitattributes", ".gitattributes", "a/.gitattributes"),
		pair("forbidden", ".gitignore", ".gitignore", "a/.gitignore"),
		pair("forbidden", "AGENTS.md", "AGENTS.md", "a/AGENTS.md"),
		pair("forbidden", "CLAUDE.md", "claude.md", "docs/CLAUDE.md"),
		pair("forbidden", ".claude/**", ".claude/settings.json", "claude/settings.json"),
		pair("forbidden", ".mcp.json", ".mcp.json", "a/.mcp.json"),
		pair("forbidden", "opencode.json", "opencode.json", "a/opencode.json"),
		pair("forbidden", ".opencode/**", ".opencode/agent.md", "opencode/agent.md"),
		pair("forbidden", ".codex/**", ".codex/config.toml", "a/.codex/config.toml"),
		pair("forbidden", ".cursor/**", ".cursor/rules", "a/.cursor/rules"),
		pair("forbidden", "CODEOWNERS", "CODEOWNERS", "a/CODEOWNERS"),
		pair("forbidden", "LICENSE", "license", "LICENSE.md"),
		pair("forbidden", ".env*", ".env.local", "a/.env"),
		pair("forbidden", ".gitlab/**", ".gitlab/ci.yml", "gitlab/ci.yml"),
		pair("forbidden", ".gitea/**", ".gitea/workflows/x.yml", "a/.gitea/x"),
		pair("forbidden", ".forgejo/**", ".forgejo/workflows/x.yml", "forgejo/x"),
		pair("forbidden", ".circleci/**", ".circleci/config.yml", "circleci/config.yml"),
		pair("forbidden", ".buildkite/**", ".buildkite/pipeline.yml", "a/.buildkite/x"),
		pair("forbidden", ".woodpecker/**", ".woodpecker/build.yml", "woodpecker/build.yml"),
		pair("forbidden", ".woodpecker.yml", ".woodpecker.yml", "a/.woodpecker.yml"),
		pair("forbidden", ".woodpecker.yaml", ".woodpecker.yaml", ".woodpecker.yaml.bak"),
		pair("forbidden", ".drone.yml", ".drone.yml", "a/.drone.yml"),
		pair("forbidden", ".travis.yml", ".travis.yml", "a/.travis.yml"),
		pair("forbidden", "bitbucket-pipelines.yml", "bitbucket-pipelines.yml", "a/bitbucket-pipelines.yml"),
		pair("forbidden", "azure-pipelines.yml", "azure-pipelines.yml", "a/azure-pipelines.yml"),
		pair("forbidden", "Jenkinsfile", "Jenkinsfile", "a/Jenkinsfile"),
		pair("forbidden", "docs/CODEOWNERS", "docs/CODEOWNERS", "docs/a/CODEOWNERS"),
		pair("forbidden", "**/.opencode/**", "a/.opencode/x", "a/opencode/x"),
		pair("forbidden", "**/.claude/**", "x/y/.claude/settings.json", "x/claude.md"),

		// Whole-list rows.
		{{"forbidden", "", "AGENTS.md", true}, {"forbidden", "", "a/AGENTS.md", false}, {"agent", "", "a/AGENTS.md", true}},
		{{"forbidden", "", "x/y/.claude/settings.json", true}, {"forbidden", "", "a/.opencode/x", true}, {"forbidden", "", ".CLAUDE/settings.json", true}},
		{{"forbidden", "", ".gitignore", true}, {"forbidden", "", "a/.gitignore", false}, {"forbidden", "", ".env.local", true}, {"forbidden", "", "a/.env", false}},
		{{"forbidden", "", ".gitlab/ci.yml", true}, {"forbidden", "", "src/main.go", false}},
		{{"hard-stop", "", "a/b/Dockerfile", true}, {"hard-stop", "", "DOCKERFILE", true}, {"hard-stop", "", "a/docker-compose.yml", false}},
		{{"hard-stop", "", "k8s", true}, {"hard-stop", "", "deploy/prod.yaml", true}, {"hard-stop", "", "Deploy/x", true}},
		{{"lockfiles", "", "go.sum", true}, {"lockfiles", "", "a/yarn.lock", true}, {"lockfiles", "", "Yarn.lock", false}},
	}
	var out []globRow
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}

func TestDefaultGlobs_Fixtures(t *testing.T) {
	lists := defaultLists()
	for _, r := range globRows() {
		l, ok := lists[r.list]
		if !ok {
			t.Fatalf("unknown list %q", r.list)
		}
		var got bool
		var err error
		if r.glob == "" {
			got, err = matchAny(l.globs, r.path, l.fold)
		} else {
			got, err = match(r.glob, r.path, l.fold)
		}
		if err != nil || got != r.want {
			t.Errorf("%s %q vs %q: got %v (err %v), want %v", r.list, r.glob, r.path, got, err, r.want)
		}
	}
}

func TestDefaultGlobs_EveryGlobCovered(t *testing.T) {
	type key struct{ list, glob string }
	seen := map[key][2]bool{}
	for _, r := range globRows() {
		k := key{r.list, r.glob}
		s := seen[k]
		if r.want {
			s[0] = true
		} else {
			s[1] = true
		}
		seen[k] = s
	}
	for name, l := range defaultLists() {
		for _, g := range l.globs {
			if !doublestar.ValidatePattern(g) {
				t.Errorf("%s glob %q is not a valid pattern", name, g)
			}
			if s := seen[key{name, g}]; !s[0] || !s[1] {
				t.Errorf("%s glob %q lacks a want=true or want=false row", name, g)
			}
		}
	}
}

// TestDefaultGlobs_ProjectProfile pins the project-profile globs of
// templates/project/.ddf/project.yaml and spec §12 through Classify.
func TestDefaultGlobs_ProjectProfile(t *testing.T) {
	p := Profile{
		Autonomy:      AutonomyPropose,
		LowMaxLines:   DefaultLowMaxLines,
		Docs:          []string{"docs/**", "**/*.md"},
		Sensitive:     []string{"internal/core/policy/gate/**", "prompts/**", "schemas/**"},
		CIEntrypoints: []string{"package.json", "scripts/docs-check.mjs", "go.mod", "go.sum", "test/boundaries/**"},
	}
	tests := []struct {
		path string
		want model.RiskClass
	}{
		{"docs", model.RiskClassDocs},
		{"docs/a/b.txt", model.RiskClassDocs},
		{"README.md", model.RiskClassDocs},
		{"a/b/notes.md", model.RiskClassDocs},
		{"Docs/x.txt", model.RiskClassLow},
		{"README.MD", model.RiskClassLow},
		{"docs/AGENTS.md", model.RiskClassLow},
		{"package.json", model.RiskClassSensitive},
		{"PACKAGE.JSON", model.RiskClassSensitive},
		{"web/package.json", model.RiskClassLow},
		{"internal/core/policy/gate", model.RiskClassSensitive},
		{"internal/core/policy/gate/gate.go", model.RiskClassSensitive},
		{"internal/core/policy/paths.go", model.RiskClassLow},
		{"prompts/executor.md", model.RiskClassSensitive},
		{"schemas/x.json", model.RiskClassSensitive},
		{"scripts/docs-check.mjs", model.RiskClassSensitive},
		{"scripts/other.mjs", model.RiskClassLow},
		{"go.mod", model.RiskClassSensitive},
		{"go.sum", model.RiskClassSensitive},
		{"test/boundaries/boundaries_test.go", model.RiskClassSensitive},
		{"test/other_test.go", model.RiskClassLow},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			c := ChangedPath{Status: ChangeStatusAdded, Path: tt.path, OldMode: ModeAbsent, NewMode: ModeRegular,
				OldBlob: strings.Repeat("0", 40), NewBlob: strings.Repeat("a", 40), Added: 1}
			got, err := Classify(p, []ChangedPath{c})
			if err != nil || got.Class != tt.want {
				t.Fatalf("got %q (err %v), want %q", got.Class, err, tt.want)
			}
		})
	}
}

func TestMatch_ErrorIsNotNoMatch(t *testing.T) {
	if ok, err := match("[", "a", false); err == nil || ok {
		t.Fatalf("match([) = %v, %v; want an error", ok, err)
	}
}
