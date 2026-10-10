# DDF v0 — standalone controller decision

Status: **proposed for review in DDF #1**, not production/live acceptance.
Date: 2026-10-10. Source of truth: this repository.

## Product boundary: no external project dependency

Deep Dark Factory (DDF) is an autonomous, self-contained software factory for **arbitrary projects**. It must install, configure, test and run without access to the maintainer's other repositories, organization settings, AI-workflow rules, task schemas, Project boards or legacy runners.

Any existing repository may be used as an **optional example or integration target**, never as a required authority, dependency or default. DDF owns its contracts, policies, admission rules, runtime state, capability model, approvals and publication interface. External systems are adapters.

```text
TaskSource (manual, GitHub, GitLab, Forgejo, local, ...)
    -> DDF admission / approval / project policy
    -> JobSpec v1 + context / budget
    -> DDF Controller + transactional StateStore
        -> AgentRuntime(role: Planner|Architect|Executor|Reviewer|Auditor)
            -> ModelProvider (Codex, Claude, local, ...)
            -> RunnerBackend (Actions, dedicated machine, local)
    -> deterministic Verifier -> independent optional review
    -> trusted ForgePublisher -> ready PR/MR/change proposal
    -> HUMAN merge (never controller)
```

Task identity is **opaque**, not a GitHub `owner/repo#issue`. Each JobSpec explicitly defines `projectId`, task `id`, `source:{kind,ref,revision?}`, target revision, branch, DDF policy revision, approval, budget and backend selection. GitHub Issues are the **first adapter only**. A manually supplied task, GitLab issue, Forgejo ticket or offline task may use the same core. Github Projects, Issue Type, assignees and organization fields are **not DDF admission invariants**.

The DDF policy contract is versioned **`ddf.policy/v1`**; admission is **`ddf.admission/v1`**; JobSpec and Result are **`ddf.jobspec/v1`** and **`ddf.result/v1`**. No external schema version or other team's workflow may gate the core. Forge-specific admission validation (where wanted) is configured in an adapter and returns the canonical DDF admission receipt. A task URL/queued event is not completion.

## Scope

One approved task -> trusted DDF admission -> bounded context -> pinned policy/base -> isolated executor -> deterministic verification -> trusted branch/PR/MR publication -> optional current-head independent review and up to two **separately authorized** corrections -> ready for human merge. No automatic merging or deployments. This is not a generalized DAG engine, distributed scheduler, dashboard or production-scale multi-agent system.

Roles (Planner, Architect, Executor, Reviewer, Auditor) describe capabilities; the role does not select the model. The Trusted Publisher is **not an agent role**. DDF does not export publisher credentials to any model or untrusted runner.

## Decision matrix

| Capability | Decision | Owner / implementation |
| --- | --- | --- |
| Task source ingestion | REDESIGN | `TaskSource` port; manual + GitHub first, later any forge |
| Task identity and admission | REDESIGN | DDF-native opaque identity and versioned `AdmissionReader` |
| Policy/approval | REDESIGN | DDF policy v1 and trusted permission read-back, no external policy |
| Workflow graph | REDESIGN | Small explicit v0 state machine, no generic DAG |
| Persisted state / checkpoints | REDESIGN | DDF versioned state and transactional StateStore |
| Crash recovery / idempotency | KEEP concept, REDESIGN code | Durable pre-call intent; reconcile before retry |
| Locks/concurrency | REDESIGN | Atomic reservation, task uniqueness, worker fencing |
| Context assembly | REDESIGN | Project-local instructions/adapters, lazy bounded, no global registry |
| Context limit | KEEP invariant | 32768 UTF-8 bytes and provenance digest |
| USD/token budgets | REDESIGN | One task-wide cap, fallback and corrections included |
| Agent roles | KEEP concept | Five DDF-owned role contracts independent of provider |
| Model routing | REDESIGN | Capability/risk/cost selection in DDF, no vendor default |
| ModelProvider | REDESIGN | Provider-specific protocol, usage/accounting, auth adapter |
| AgentRuntime | REDESIGN | Role tools/prompt/context boundary without publisher credentials |
| RunnerBackend | REDESIGN | Actions first, also dedicated-machine and local transports |
| Sandbox / secrets | REDESIGN | Credential isolation, no host HOME/write tokens for executor |
| Git worktree | EXTERNALIZE | Native Git primitives via workspace adapter |
| Deterministic verification | KEEP | Configured project build/test/verify, actual SHA/diff |
| Optional model review | REDESIGN | Independent read-only reviewer, green current-head CI first |
| Retry and corrections | KEEP bound / REDESIGN | Max one approved fallback, max two approved corrections |
| Human approval gates | KEEP | DDF v1 task approval + numeric paid grant + correction grant |
| Publication | REDESIGN | Trusted forge-neutral idempotent publisher port |
| Review ingestion | REDESIGN | Current published-SHA dedup, bounded findings |
| Audit | MOVE | DDF-native sanitized receipts/events |
| Webhooks/scheduling | EXTERNALIZE | Adapter-specific; no required server in v0 |
| CI | EXTERNALIZE | Free GitHub-hosted offline checks |
| GitHub Projects | DROP as requirement | Optional UI adapter only |
| External global workspace/routing | DROP | Project-local settings, DDF self-contained defaults |
| Legacy Mac runner | DROP | Audit ideas only, never execution dependency |

## Research comparisons (reference only)

- [OpenHands Software Agent SDK](https://github.com/OpenHands/software-agent-sdk): agent-workspace separation; potentially useful replaceable AgentRuntime implementation, not adopted by v0.
- [mini-SWE-agent](https://github.com/SWE-agent/mini-swe-agent): small bounded coding loop; conceptual reference only, no copied source.
- [LangGraph](https://github.com/langchain-ai/langgraph): durable/checkpointed graphs are heavier than required for one-task v0.
- [Temporal](https://github.com/temporalio/sdk-typescript): durable activity retries; service overhead deferred; blind retry of model calls would be unsafe.
- [Fabro](https://github.com/fabro-sh/fabro): research reference only; not a fork or transitive dependency.

No upstream source code is copied; a later dependency needs maintenance, security and license review. Legacy project audits are **historical design input only**; neither their runtime code nor their policy files are imported or read by DDF.

## Minimal stack and stores

**Node.js 24.21.0, ESM JavaScript, Node test runner** for offline slice. Contracts validate unknown versions and reject untrusted approvals.

- Offline single-process/single-host: local atomic file store (fsync, rename, single-task lock). This is **not safe for multi-run ephemeral Actions**.
- Production Actions: DDF-owned external **ACID StateStore** (PostgreSQL is a recommended backend, but not a product dependency or required hosted service). Self-hostable, versioned schema, transaction-before-dispatch, unique task/invocation keys, durable outbox, lease/fencing, callback authentication.
- Dedicated machine and local mode may choose a local-durable StateStore implementation when it meets their concurrency/fencing requirements; no dependency on Actions or a dedicated hosted database is imposed by core.
- TaskSource, AdmissionReader, ProjectPolicy, ApprovalReader, ModelProvider, AgentRuntime, RunnerBackend, Verifier, ReviewReader, TrustedPublisher, and StateStore are DDF-owned interfaces.

DDF accepts task approval and numeric budget only via a trusted grant reader bound to exact task/policy/base and allowed model providers. Single task+fallback cap; reserve worst case **durably before** a model invocation. Unknown usage charges the full reservation, not zero. Unexpected overage blocks and records an incident. Uncertain remote outcomes block automatic retries until verified reconciliation.

Publisher holds independent short-lived forge credentials; executor never gets them. CI and review are for the **published commit SHA**. Reviewer does not mutate code; no self-review. Correction requires a current-head and correction-batch-bound separate authorization. Publishing and marking ready persist intents and reconcile idempotently before attempting any repeat.

## Acceptance / dependency order

1. Human review/merge of this DDF-only architecture and explicit acceptance of research Issue #1. The research-only gate in `AGENTS.md` remains in effect until then.
2. A separate dependent draft PR provides runnable **offline** core with negative, crash/recovery and credential-boundary tests. Fake receipts remain clearly simulated.
3. Implement real forge/task adapters, self-contained DDF-native admission and policy loading; no repository-specific guard in core.
4. Make Actions safe with shared transactional state, authenticated callback, fencing, and an isolated trusted publisher. Dedicated/local acceptance independently.
5. Independently accept Codex, Claude and local endpoint provider implementations. Passing one provider never implicitly accepts another.
6. Human specifies exactly **one** canary task/source/target and numeric USD cap/max calls/per-call ceilings and approved providers; trusted read-back, not a string in task text.
7. Run one authorized live canary to ready PR/MR; human merges. Exact deployment stays separate.

**No paid authorization:** deliver only runnable offline path and pending acceptance checklist, never claim a complete live lifecycle.

## Activation decisions (not design dependencies)

Select first demo project/source/forge and target CI; choose external StateStore placement if Actions is used; establish independently trusted identity and reviewer; approve numeric paid grant. Do **not** make a maintainer's other repository, personal workflow, organization Project, local Mac or a platform's CI prerequisite for installing DDF.

## Audit-driven contract clarification (2026-10-11; architecture review)

These invariants are mandatory for the offline prototype and any future live adapter, not evidence of production acceptance:

1. **Scoped identity:** a task is addressed by a DDF-generated canonical `taskKey` derived from `projectId`, `source.kind`, `source.ref` and the source-local opaque `task.id`. Every StateStore key, lock, invocation, reservation, grant and callback must bind this namespace. No bare source-local ID may key shared state. Source revision/policy/base are separately frozen by JobSpec digest and fresh admission.
2. **One paid-invocation ledger:** any billable Planner/Architect/Executor/Reviewer/Auditor operation, including external review requests and fallbacks/corrections, atomically reserves against one per-task cap and max-call count before dispatch. Unknown usage consumes its full reservation; ambiguous dispatch requires read-only reconciliation. Paid capability and independently trusted provider grant checks apply equally to reviewer calls. Declared roles without a runnable adapter remain **PENDING**, not accepted.
3. **Forge-neutral ChangeRef:** publication returns a `changeRef:{kind,id}` describing a PR, MR or change proposal. A forge-specific numeric PR number is adapter metadata, never a core requirement. Review, ready-intent and terminal state are keyed by ChangeRef and immutable published SHA; terminal phase is `READY_CHANGE` (human merge remains separate).
4. **Principal versus provider:** independent review is determined by the authenticated execution **principal** (actor/session) differing from the implementation principal, not by different model vendors. A Reviewer may use the same ModelProvider as an Executor; reviewer principal and read-only authorization must be attested by its adapter. Provider IDs remain accounting/routing identities only.
5. **Offline versus live:** `FileStateStore` is strictly single-host. There is no live Actions acceptance without transactions, fencing, signed callbacks, workspace isolation, separate publisher credentials, and current-head CI/review read-back.

### Issue #1 acceptance review

The capability matrix, migration exclusions, optional backend choice, dependency stack, human gates and single-task vertical-slice plan satisfy the **documentation-level** research deliverables. This note does **not** close Issue #1, merge PR #7, authorize production code, or release PR #9; explicit human sign-off and merge are still required. The offline dependent draft is an experimental implementation behind that gate.
