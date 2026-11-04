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

## M0 facts (2026-11)

Source: `docs/research/2026-11-m0-facts.md`. Format: fact → consequence → spec section → confidence → later probe.

- §16.1 App approvals probably count (no direct doc); ≤ 1 free machine account per person → D6 unchanged; machine-user fallback limited to one free machine reviewer → §16 → unverified (terms: documented) → M4 `app-approval-counts`.
- §16.2 Token narrowing by `repository_ids` + `permissions`, ≤ 1 h; omitting `permissions` widens the token → broker treats any failed narrowing as hard failure → none → documented → M4 `token-narrowing`.
- §16.3 App without `workflows` gets 403 on `update-branch` (pushed merge likely too) when base changed workflow files → keep withholding `workflows`; refused sync → `needs-owner`, no sync batch used; human branch update → full re-review → §5.2, §6.6 rule 4, §16 → unverified → M4 `sync-with-workflow-change`.
- §16.4 Both CLIs run non-interactively as non-root; isolation = Claude `--setting-sources ''` + `--strict-mcp-config`, opencode `OPENCODE_*` + `--pure`; opencode npm 1.x and GitHub 2.x incompatible → pin npm 1.x, explicit model, per-call timeout, classify errors from `is_error`/`api_error_status`/`error`; host never runs agent CLIs on a repo copy → §7.3, §8.3, §8.4, §16 → verified (rate-limit shapes unverified) → M3 `cli-version-pin`, `claude-config-isolation`, `opencode-config-isolation`, `*-error-shape`, `*-rate-limit-shape`.
- §16.5 colima as `ddf` via LaunchDaemon works, limits enforced; VM rules lost on restart, provision hooks fail silently, launchd does not restart the VM → daemon applies/verifies rules before every container start and restarts the VM; global pause only after that fails → §8.1, §8.4, §9, §16 → verified (reboot without login, launchd respawn unverified; owner rerun pending) → M3 `vm-firewall-reapply`, `runtime-recovery`; M5 `colima-boot`.
- §16.6 `node:sqlite` no warning on Node 24.21.0, stability 1.2 since 24.15.0 → pin Node ≥ 24.15.0, store access only via `store` → §4.4, §16 → verified (stability: documented) → none (M2 store tests).
