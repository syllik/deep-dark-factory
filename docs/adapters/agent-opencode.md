# agent-opencode adapter

Purpose: how DDF runs a role through the opencode CLI inside a run container.

Status: implemented in M3

Routing name: `adapter: opencode` in the operator's `routing.yaml` ([config-reference.md](../operator/config-reference.md#entry)); typically the fallback entry of a tier and a candidate for `roles.r2.prefer` when R2 should run on a different provider. Rationale: [spec §7.3](../specs/2026-11-03-ddf-v1-design.md#73-agent-adapter-contract); the shared contract is in [routing.md](../concepts/routing.md#agent-adapter-contract).

## Contract

The adapter implements `run(role, brief, workspace, limits) → result` ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters)):

- Non-interactive CLI inside the container, as a non-root user (`opencode run --format json --agent … -m …`). Permission prompts are disabled inside the container; the container is the security boundary.
- **Pinned CLI.** opencode from npm `opencode-ai`, 1.x line (verified 1.19.5). opencode's GitHub-release 2.x line has incompatible flags and config schema and is not supported in V1. The base image records the exact version; the adapter refuses an unknown version.
- **No run-time installs.** The base image pre-installs opencode's plugin SDK at a pinned version; a run in which any package-registry install happens at run time fails (M3 probes `cli-version-pin`, `agent-home-state`).
- **Configuration comes only from DDF.** The adapter starts the CLI with DDF's rendered role bundle as the only settings source: project and user settings, hooks, MCP servers and plugins from the workspace are disabled. Project instructions (`AGENTS.md`) reach agents only through the brief, read from the integration branch.
- **Isolation switches:** the `OPENCODE_*` environment set from the research doc (bundle config and config directory, project config, Claude Code compatibility, external skills, autoupdate and default plugins disabled) plus `--pure`. opencode loads project plugins on every invocation, so the host never runs opencode against a repository copy, not even `opencode models` or `opencode debug`.
- Every call passes an explicit `-m <provider/model>` from routing (never a CLI default or a free fallback model) and runs under a wall-clock timeout.
- The container receives only this adapter's credential (one provider key). All agent-originated output is redacted before any forge write or notification ([roles.md](../concepts/roles.md#publisher)).
- Inputs mounted read-only: brief, role bundle. Outputs: `/out/result.json` (validated against `schemas/<role>.result.json`; missing or invalid → failed run) and, for the Executor, `/out/change.patch`.
- The adapter records model ID, token usage and cost from the CLI output into the journal: tokens and cost summed over the `step_finish` events; the model ID from `opencode export <sessionID>` (always with an ID: without one it opens an interactive picker and hangs). Provider response headers are dropped before journaling.
- Role prompts live once in `prompts/`; the adapter renders them into opencode's native form (agent config) in a directory outside the workspace.
- Optional per-task `maxCostUsd` for API-key-billed entries.

## Role specifics

- Reviewers (R1, R2) get read/search tools only and no command execution ([roles.md](../concepts/roles.md#reviewer-security)): a bundle agent that denies every permission except read, grep, glob and list, run on a read-only workspace copy.
- The Executor gets full tools inside the container (a bundle agent allowing all permissions); `limits.maxSubagentsPerRun` is passed as advice, not enforced ([routing.md](../concepts/routing.md#limits)).
- With `reviewers.r2DifferentProvider: true` in the project profile, R2 must resolve to a different provider than the Executor ([project-yaml-reference.md](../project/project-yaml-reference.md#reviewersr2differentprovider)).

## Errors and rate limits

Failures are classified from the `error` event's `statusCode` / `isRetryable`:

| Signal | Classification |
|---|---|
| `APIError` with `statusCode` 401/403 | credential failure: global pause, reason `credential`; never retried |
| `statusCode` 429 | rate limit: the entry cools until its reset time; the next entry of the tier is used; with no entry left the task waits and one notification is sent ([routing.md](../concepts/routing.md#selection-and-fallback)) |
| other `isRetryable: true` (5xx) | backoff on the same entry, not fallback |
| `UnknownError` | failed run, with the stderr log attached (only when the routed credential was present; a missing or empty credential is caught before the CLI starts and is a credential failure) |

The rate-limit shape is unverified (M3 probe `opencode-rate-limit-shape`).

## M0 facts

Verified in M0 with opencode 1.19.5, with free models only ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 4): non-interactive runs as non-root, the isolation environment, reviewer tool removal and the JSON event stream. Open until a paid provider is available: `opencode-executor-output`, `opencode-reviewer-readonly`. The brief is passed as one argument (Linux caps one argument at 128 KiB); M3 decides between stdin or file attachment and a brief size cap. Exact command lines, the bundle and the evidence: [M0 facts](../research/2026-11-m0-facts.md#recommended-adapter-command-lines). M3 probes: `cli-version-pin`, `agent-noninteractive`, `agent-home-state`, `opencode-config-isolation`, `opencode-reviewer-readonly`, `opencode-executor-output`, `opencode-error-shape`, `opencode-rate-limit-shape`, `secret-exfil-redaction`.
