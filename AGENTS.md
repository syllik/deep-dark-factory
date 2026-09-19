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

## Reuse rule

Before implementing a generic infrastructure primitive, research maintained open-source libraries/projects that already solve it. Prefer a small dependency or adapter when that reduces owned infrastructure without creating unacceptable lock-in.

Researching an existing project does not imply adopting or copying it.

## Change discipline

Keep architecture decisions explicit and small. Avoid creating directories, abstractions, services, or configuration formats speculatively.

During the research phase, substantive work should update the research/architecture documents and reference the governing Issue. Merge remains human-only.
