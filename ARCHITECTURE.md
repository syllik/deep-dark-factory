# Architecture

Short summary of the DDF V1 architecture; the full design and rationale are in [the V1 spec](docs/specs/2026-11-03-ddf-v1-design.md#4-architecture).

Status: normative (summary of spec §4; the spec wins on any difference).

## Core

The core has no network, forge or provider access. Modules:

- `model`: versioned types and schemas (Task, Run, Change, Review, Finding, Question, Decision; IDs `ddf.<name>/v1`).
- `store`: SQLite as the canonical state, append-only event journal, run leases, intents.
- `lifecycle`: pure transition functions of the task state machine.
- `scheduler`: run slots, zones, dependencies, priority order, global pause (a set of reasons).
- `policy`: risk classification, reviewer set, blocking rules, path policy, hard stops, `evaluateMerge`. Pure functions.
- `workspace`: daemon-owned clean clones, per-run copies, patch export/apply. Never executes repository content on the host.
- `context`: bounded per-role briefs built from the integration branch.

## Ports and V1 adapters

| Port | V1 adapters |
|---|---|
| Forge | `forge-local`, `forge-github` |
| Agent | `agent-claude-code`, `agent-opencode` |
| Sandbox | `sandbox-docker`, `sandbox-fake` |
| Notify | `notify-telegram`, `notify-cli` |
| Secrets | `secrets-file` |

Entry points: `ddfd` (single-instance daemon, one tick per minute, `--dry-run`) and `ddf` (CLI).

## Boundaries

- Core never imports adapters; adapters never import each other; the daemon wires them.
- Trusted code is the daemon and adapters on the host. Agents run only inside containers, receive a brief and a workspace copy, and never hold forge credentials or the review-signing key.
- The daemon runs only from a maintainer-promoted ref, never from the integration branch.
- One Go module in V1, built into static binaries for linux and darwin on arm64 and amd64; standard library first, every dependency with a recorded reason ([research](docs/research/2026-11-runtime-and-language.md)).
- Hybrid runtime: one long-lived control plane per host holds the store and the secrets; each task's execution side (run containers, workspace copies) is disposable.

## Invariants

1. Human merge is the default. Autonomous merge into the integration branch is a per-project opt-in with operator consent (effective autonomy = min(operator maximum, project request)). Nothing promotes to production.
2. The core does not require GitHub, a specific agent CLI or model provider, or one host OS. GitHub is the first forge adapter; DDF works on a plain Git remote.
3. The store is canonical. Forge labels and comments are projections; GitHub Projects are never read or written.
4. Every forge write is recorded as an intent before the call and reconciled by task key after a restart.
5. Policy only tightens across product, operator and project layers; a repository cannot grant itself autonomy.
6. The merge gate is deterministic, fail-closed and pinned to the reviewed head SHA.
7. Reviewers are independent, read-only and do not execute repository code.
8. All loops and runs are bounded; exceeding a bound escalates to the owner.
9. Agent code runs only in containers; the host never executes repository content.
10. Secrets never enter Git. Forge credentials and the review-signing key never enter containers; forge tokens are minted per operation, narrowed to one repository and the permissions that operation needs. Accepted V1 risks are listed in [spec §15](docs/specs/2026-11-03-ddf-v1-design.md#15-accepted-risks-v1).
