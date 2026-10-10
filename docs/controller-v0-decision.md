# Decision proposal: v0 Issue → ready PR controller

Status: **proposed for human acceptance in Issue #1**, not an implementation authorization or successful live acceptance.
Date: 2026-10-10. Owner repository: `syllik/deep-dark-factory`.
The existing `AGENTS.md` research gate remains enforced until this proposal is accepted.

## Scope and vertical slice

One approved external Issue → trusted admission → bounded context and pinned task policy → isolated implementation on an execution backend → deterministic tests → *trusted* publication of exactly one branch/PR revision → SHA-pinned independent review (if enabled) → at most two *separately authorized* correction batches → ready PR → **human merge**. This controller **belongs to DDF**, not `ai-workflow`, ChipIn `.github`, or the archived Mac runner.

DDF core is forge-, provider-, and backend-neutral. GitHub Issues/Actions are the **first adapters**, not mandatory services. The initial controller is **one task at a time**; no scheduler, dashboard, auto-merge, exact deployment or distributed graph engine. For ChipIn, a read-only adapter consumes org #53 `chipin-issue-admission/v1` fresh native Issue/Project read-back, and the trusted publication adapter reuses #381 PR metadata/native-link/Project writer. Never build a second intake or Project writer.

## Research comparison (architecture evidence, not selected dependencies)

| Candidate | Evidence and useful concept | Choice for v0 |
| --- | --- | --- |
| [OpenHands Software Agent SDK](https://github.com/OpenHands/software-agent-sdk) | Separates agent/workspace SDK, remote Agent Server and automation; ephemeral Docker/Kubernetes workspace support | **EXTERNALIZE** potential future agent runtime; avoid importing Python/agent-server stack for the small JS slice |
| [mini-SWE-agent / SWE-agent](https://github.com/SWE-agent/mini-swe-agent), [SWE-agent architecture](https://swe-agent.com/latest/background/architecture/) | Small coding-agent loop and isolated environment; SWE-agent's heavier runtime is in maintenance mode | **KEEP** bounded execution/agent-computer interface ideas; no fork |
| [LangGraph persistence](https://langchain-ai.github.io/langgraph/concepts/durable_execution/) | Checkpoints, interrupt/resume for graphs | **DEFER** graph runtime: explicit small state machine fits one-task v0 |
| [Temporal TypeScript SDK](https://docs.temporal.io/develop/typescript) | Durable workflows, activities, lifecycle retries, versioning | **DEFER** service/worker overhead; revisit when multi-machine scheduling is justified. Its automatic retry is *not* permission to replay non-idempotent model calls |
| [Fabro](https://github.com/fabro-sh/fabro) | Research reference requested by #1; portability/ownership lessons only | **DO NOT FORK**, adopt no runtime dependency pending an independent license/maintenance examination |

All upstream packages retain their own licenses; no upstream code is copied. Dependency adoption requires explicit compatibility and security review. The current implementation proposal uses only Node standard-library primitives for the offline proof, deliberately keeping dependency surface small.

## Completed legacy capability audit

`KEEP` / `MOVE` / `REDESIGN` / `DROP` / `EXTERNALIZE` refers to *concept ownership*, not to importing legacy source code.

| Capability | Decision | Target v0 boundary |
| --- | --- | --- |
| Task ingestion | REDESIGN | `TaskSource` + GitHub Issue adapter; one exact `owner/repo#number` |
| Workflow graph | REDESIGN | Minimal stage transitions, not generalized DAG |
| State and checkpoints | REDESIGN | Versioned durable JobSpec/Result and transactional task ledger |
| Crash recovery | KEEP concept / REDESIGN implementation | Durable intent, read-only reconciliation; unknown outcome blocks |
| Locking / concurrency | REDESIGN | Atomic reservation and fencing across workers |
| Context discovery | MOVE | Explicit repository AGENTS/context/registry snapshot, lazy bounded loading |
| Context budgeting | KEEP | 32,768 UTF-8 bytes maximum assembled context, provenance digest |
| Token + USD budget | REDESIGN | One task-wide sum including fallback/corrections; max calls and per-call reserved ceiling |
| Roles | KEEP | Planner, Architect, Executor, Reviewer, Auditor independent of provider |
| Model routing | REDESIGN | Capabilities/policy; no hard-coded vendor in core |
| ModelProvider | REDESIGN | Vendor protocol/usage/auth mapping; separate adapter acceptance |
| AgentRuntime | REDESIGN | Role and tool/context orchestration, no publication credentials |
| RunnerBackend | REDESIGN | Actions first, dedicated host, local endpoint; transport only |
| Sandbox and secret separation | REDESIGN | Untrusted executor container/job; trusted publisher separate identity |
| Git isolation | EXTERNALIZE | Git native worktree/clone and hardened runner tooling |
| Deterministic verification | KEEP | Repo-selected local checks against actual diff and SHA |
| Retry / correction | KEEP bounds / REDESIGN execution | No blind replay; one initial implementation, at most one permitted fallback and two authorized corrections |
| Human approvals | KEEP | v2 scope/publication grant and distinct numeric paid grant |
| Publication | REDESIGN | Idempotent trusted publisher; no rewrite, human merge |
| Review ingestion | REDESIGN | Optional independently identified read-only Reviewer; dedup per SHA, green CI first |
| Audit / observability | MOVE | No secrets, structured receipts and task events |
| Schedules / webhooks | EXTERNALIZE | GitHub Actions integration, no own server in v0 |
| CI | EXTERNALIZE | Free hosted offline negative/recovery tests |
| GitHub Projects | EXTERNALIZE | Optional forge UI; ChipIn admission needs existing Project #5 only |
| Mac host proof / old runner engine | DROP | Archived `syllik/codex-local-runner` as evidence only |

## Implementation language / boundaries

**Node.js 24.21.0, ESM JavaScript first**, matching the active `ai-workflow` runtime policy; typed schemas and TypeScript migration can follow a proved slice. `JobSpec` and `Result` start at explicit v1 and reject unknown versions. The roles, `ModelProvider`, `AgentRuntime`, `RunnerBackend`, `StateStore`, `AdmissionReader`, `Verifier`, `ReviewReader`, `TrustedPublisher` remain separate ports, with **no GitHub or provider imports in core**. Machine/provider protocol specifics live in adapters.

**Offline/local persistence:** single-host file store with fsync/atomic rename and per-task lock only for deterministic tests. **Live Actions persistence:** external ACID transactional store (PostgreSQL preferred) with uniqueness on task/invocation/idempotency keys, `SELECT FOR UPDATE` transaction before remote dispatch, lease epoch/fencing, durable outbox and immutable request digest. GitHub-hosted Actions ephemeral disks cannot serve as shared state. No vendor/connection/credentials are assumed or provisioned by this research PR. Dedicated-machine/local endpoint can use the same store abstraction. This is a design choice, **not** a claim the prototype implements Postgres.

A reserve is durable **before** any model/backend invocation. Require paid approval bound to task/allowed providers/cap/call maximum and an operator-controlled trusted read-back. Unknown provider usage conservatively debits the full reserved ceiling; not zero. Late results and unexpected higher usage become incidents, not hidden budget overruns. Uncertain invocation cannot be retried until reconciliation; no duplicate branch/PR publication after process crash.

**Runtime security:** Executor receives no GitHub write/token or publisher credentials, no trusted gate implementation credentials, no host home. Only publisher job may create commits/PRs, with separate short-lived credentials. PR readiness requires fresh head-specific CI, independent review evidence (when enabled), and native read-back. The DDF orchestrator never merges.

## Gate / acceptance sequence

1. Human reviews and merges this **research-only** decision into `master`; only then close #1 as accepted and change `AGENTS.md` from research-only to implementation-allowed for the *one v0 controller slice*. Do not unilaterally waive this gate.
2. Implement the existing offline reference as a scoped **separate draft PR** based on this research branch (stacked); update base to `master` after research merge. Its fake receipts must remain unmistakably labelled mock.
3. Run free offline negative/recovery/permission-boundary tests in Actions; establish full route/JobSpec/Result and stable state.
4. Implement live transactional state and remote callback attestation before invoking anything billable.
5. Independently accept Codex, Claude and local endpoint protocols/usage/identity/secret isolation. Acceptance of one never implies another.
6. Select an exact newly-admitted target Issue and **explicit numeric task+fallback cap**, per-call ceilings, max calls and allowed providers, verified by trusted grant reader. Without that authorization: runnable offline path and **PENDING** live acceptance.
7. After all gates, **one** live Issue→ready PR canary, preserve current-head green CI/review and human merge. Do not conflate this with exact-deploy.

## Edges deliberately left for deployment approval

- Where the shared Postgres store runs, who owns its credential and transaction/outbox gateway (not needed to merge offline PR).
- First real target Issue/repo and approved independent reviewer identity, exact required CI checks.
- Whether local endpoint can produce a trustworthy cost ceiling/usage bound; without it reject live local execution.
- Numeric paid authorization. **No amount is inferred or auto-approved.**

These are activation decisions, not reasons to copy the legacy runner or relax current research-only rules.
