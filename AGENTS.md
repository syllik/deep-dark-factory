# Agent Instructions

Deep Dark Factory is currently in the research and architecture-definition phase.

## Source of truth

Read, in order:

1. `.ai/context.md`
2. `ARCHITECTURE.md`
3. `RESEARCH.md`
4. the relevant GitHub Issue

Do not infer current architecture from historical conversations or from the legacy repositories.

## Current constraints

- This is a greenfield implementation.
- The license is MIT.
- Fabro is a research reference only. Do not fork it, copy its implementation, or introduce it as a dependency unless a future explicit decision changes this.
- Do not create production engine code until the v0 research/migration boundary is accepted.
- Do not introduce GitHub Projects as required state.
- Do not make a local Mac or self-hosted GitHub Actions runner an architectural requirement.
- GitHub is the first forge adapter, not the core abstraction.
- Codex/OpenAI/Claude or any other agent/model must be replaceable providers.
- Human merge remains the default authority boundary.
- Prefer deterministic checks over LLM calls whenever possible.
- Minimize loaded context and repeated model work.
- Never commit credentials, tokens, private keys, environment secrets, or authentication material.

## Branching model

Use the following branch roles consistently:

- `dungeon-master` — production branch.
- `master` — staging/integration branch.
- `slave/<feature>` — agent development branches. Use a short kebab-case feature/task slug after `slave/`.

Agent work must branch from the appropriate staging baseline and target `master` through a pull request unless an explicit task says otherwise. Do not merge agent work directly into `dungeon-master`.

## Reuse rule

Before implementing a generic infrastructure primitive, research maintained open-source libraries/projects that already solve it. Prefer a small dependency or adapter when that reduces owned infrastructure without creating unacceptable lock-in.

Researching an existing project does not imply adopting or copying it.

## Change discipline

Keep architecture decisions explicit and small. Avoid creating directories, abstractions, services, or configuration formats speculatively.

During the research phase, substantive work should update the research/architecture documents and reference the governing Issue. Merge remains human-only.

## Naming note

The project name has an internal gachi reference. Keep that reference internal to agent/project context; do not mention or explain it in the README or other user-facing documentation.

<!-- ai-workflow:agents-routing:start -->
Canonical AI routing:
1. Read the canonical workflow: https://github.com/syllik/ai-workflow/blob/HEAD/FLOW.md.
2. Select one GitHub record from https://github.com/syllik/ai-workflow/blob/HEAD/workspace.yaml / https://github.com/syllik/ai-workflow/blob/HEAD/projects/index.md.
3. Read role rules from https://github.com/syllik/ai-workflow/blob/HEAD/global/architect.md, https://github.com/syllik/ai-workflow/blob/HEAD/global/executor.md, or https://github.com/syllik/ai-workflow/blob/HEAD/global/reviewer.md.
4. On that record's `integrationBranch`, read target `AGENTS.md`, then `.ai/context.md`.
5. Read relevant `.ai/decisions.md`, task files, and required declared `contextDependencies`; block if required dependency context is unavailable.

GitHub Issue/PR entry never bypasses this route; use GitHub records only, no auto-discovery; legacy contexts are migration-only.
Canonical root: ~/Desktop/WORK
<!-- ai-workflow:agents-routing:end -->
