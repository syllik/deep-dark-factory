package policy

import (
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// lockfileGlobs is the sync:lockfiles block (spec §6.2).
func lockfileGlobs() []string {
	return []string{
		"**/package-lock.json", "**/npm-shrinkwrap.json", "**/yarn.lock", "**/pnpm-lock.yaml",
		"**/bun.lock", "**/bun.lockb", "**/go.sum", "**/Cargo.lock", "**/poetry.lock",
		"**/Pipfile.lock", "**/uv.lock", "**/Gemfile.lock", "**/composer.lock", "**/mix.lock",
		"**/pubspec.lock", "**/Package.resolved", "**/packages.lock.json", "**/gradle.lockfile",
		"**/flake.lock",
	}
}

// agentInstructionGlobs is the sync:agent-instruction-files block (spec §6.2).
func agentInstructionGlobs() []string {
	return []string{
		"**/AGENTS.md", "**/CLAUDE.md", "**/GEMINI.md", "**/SKILL.md", "**/.cursorrules",
		"**/.windsurfrules", "**/.github/copilot-instructions.md", "**/.agents/**", "**/.claude/**",
		"**/.codex/**", "**/.cursor/**", "**/.opencode/**", "**/.mcp.json", "**/opencode.json",
	}
}

// hardStopGlobs is the sync:hard-stop-globs block (spec §6.4).
func hardStopGlobs() []string {
	return []string{
		"Dockerfile*", "**/Dockerfile*", "docker-compose*.y*ml", "**/*.tf", "**/*.tfvars",
		"k8s/**", "helm/**", "deploy/**", "infra/**", ".terraform/**", "ansible/**",
	}
}

// forbiddenGlobs is the default forbidden list (spec §6.5): the backticked
// tokens of the sync:forbidden block in order, the forge and CI equivalents,
// then the agent configuration directories at any depth.
func forbiddenGlobs() []string {
	return []string{
		".ddf/**",
		".github/**", ".gitlab-ci.yml",
		".gitmodules", ".gitattributes", ".gitignore",
		"AGENTS.md", "CLAUDE.md", ".claude/**", ".mcp.json", "opencode.json", ".opencode/**", ".codex/**", ".cursor/**",
		"CODEOWNERS", "LICENSE", ".env*",
		// Forge and CI equivalents.
		".gitlab/**", ".gitea/**", ".forgejo/**", ".circleci/**", ".buildkite/**", ".woodpecker/**",
		".woodpecker.yml", ".woodpecker.yaml", ".drone.yml", ".travis.yml", "bitbucket-pipelines.yml",
		"azure-pipelines.yml", "Jenkinsfile", "docs/CODEOWNERS",
		// spec §6.5: agent configuration directories at any depth.
		"**/.opencode/**", "**/.claude/**",
	}
}

// DefaultLockfileGlobs returns a copy of the lockfile list (spec §6.2).
func DefaultLockfileGlobs() []string { return lockfileGlobs() }

// DefaultAgentInstructionGlobs returns a copy of the agent-instruction file
// list (spec §6.2).
func DefaultAgentInstructionGlobs() []string { return agentInstructionGlobs() }

// DefaultHardStopGlobs returns a copy of the default hard-stop globs (spec
// §6.4).
func DefaultHardStopGlobs() []string { return hardStopGlobs() }

// DefaultForbiddenGlobs returns a copy of the default forbidden globs (spec
// §6.5). Symbolic links are a mode rule, not a glob.
func DefaultForbiddenGlobs() []string { return forbiddenGlobs() }

// match reports whether name matches pattern on the full repository-relative
// path; fold lowercases both for the tightening rules. A pattern error is
// returned, never treated as no match.
func match(pattern, name string, fold bool) (bool, error) {
	if fold {
		pattern, name = strings.ToLower(pattern), strings.ToLower(name)
	}
	ok, err := doublestar.Match(pattern, name)
	if err != nil {
		return false, fmt.Errorf("match glob %q: %w", pattern, err)
	}
	return ok, nil
}

// matchAny reports whether name matches any of globs.
func matchAny(globs []string, name string, fold bool) (bool, error) {
	for _, g := range globs {
		if ok, err := match(g, name, fold); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}
