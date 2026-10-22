# Durable decisions

- MIT license; greenfield implementation. Sources: `AGENTS.md`, `.ai/context.md`, Issue #1.
- Fabro is a research reference only, not the implementation base or a dependency. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, Issue #1.
- GitHub is the first forge adapter, not the core abstraction; GitHub Issues are the first task source; GitHub Projects are optional and not canonical execution state. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1.
- Agent/model providers are replaceable. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`.
- Deterministic verification precedes model review/correction; human merge remains the final/default authority. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1.
- The architecture has no dependency on a local Mac/self-hosted runner or GitHub Actions product execution. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, Issue #1.
- Context and token use stay bounded/lazy, with minimal agent calls and bounded correction loops. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`.
- Branch roles are fixed: `dungeon-master` production, `master` staging/integration, `slave/<feature>` agent development. Sources: `AGENTS.md`, `.ai/context.md`.
- 2026-10-22 (owner decision): DDF stays a managed/active `syllik/ai-workflow` project; development follows the generated routing block in `AGENTS.md`. "Standalone" applies only to the DDF product runtime. Sources: `AGENTS.md`.
- The project remains in research/bootstrap phase; no production engine code is created before the v0 research/migration boundary is accepted. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1.


## Proposed, not yet accepted — standalone DDF controller v0 (2026-10-10)

- DDF is a self-contained, universal product. At product runtime: no dependency on other repositories, external routing/workflow policies, organization Issue fields, GitHub Projects, or legacy runner implementation.
- Core versioned `ddf.jobspec/v1`, `ddf.result/v1`, `ddf.policy/v1`, `ddf.admission/v1`. Opaque task identity and configurable TaskSource; built-in manual task admission and local policy are required.
- Provider-independent Planner/Architect/Executor/Reviewer/Auditor, independent ModelProvider / AgentRuntime / RunnerBackend, separate trusted Publisher.
- Actions-first optional backend; dedicated machine and local endpoint supported through adapters. Fenced shared persistence is required for multi-run Actions, not to install DDF generally.
- Task-wide conservative reservation, no paid call without exact numeric authorization, current published-SHA review, at most two separately authorized corrections and human merge. Exact-deploy not part of controller.
- Details: [v0 decision](../docs/controller-v0-decision.md). Proposed only; does not lift research-only gate.
