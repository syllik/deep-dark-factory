# Durable decisions

- MIT license; greenfield implementation. Sources: `AGENTS.md`, `.ai/context.md`, Issue #1.
- Fabro is a research reference only, not the implementation base or a dependency. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, Issue #1.
- GitHub is the first forge adapter, not the core abstraction; GitHub Issues are the first task source; GitHub Projects are optional and not canonical execution state. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1.
- Agent/model providers are replaceable. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`.
- Deterministic verification precedes model review/correction; human merge remains the final/default authority. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1.
- The architecture has no dependency on a local Mac/self-hosted runner or GitHub Actions product execution. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, Issue #1.
- Context and token use stay bounded/lazy, with minimal agent calls and bounded correction loops. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`.
- Branch roles are fixed: `dungeon-master` production, `master` staging/integration, `slave/<feature>` agent development. Sources: `AGENTS.md`, `.ai/context.md`.
- The project remains in research/bootstrap phase; no production engine code is created before the v0 research/migration boundary is accepted. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1.


## Proposed, not yet accepted — DDF controller v0 (2026-10-10)

- DDF is the sole owner of Issue→ready PR Controller implementation; ai-workflow defines roles/routing/policy, ChipIn org owns canonical intake and PR metadata, legacy Runner is not a dependency.
- Versioned JobSpec/Result; independent ModelProvider/AgentRuntime/RunnerBackend; Actions-first adapter without permanent host assumption.
- Conservatively reserved single task/fallback budget; no paid canary without *independently verified* exact numerical approval; human merge, no exact-deploy.
- Research and migration details: [controller decision](../docs/controller-v0-decision.md). **Pending human review and PR merge**; this paragraph does not lift the research-only gate.
