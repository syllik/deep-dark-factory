# agent-claude-code adapter

Purpose: how DDF runs a role through the Claude Code CLI inside a run container.

Status: implemented in M3

Routing name: `adapter: claude-code` in the operator's `routing.yaml` ([config-reference.md](../operator/config-reference.md#entry)). Rationale: [spec §7.3](../specs/2026-11-03-ddf-v1-design.md#73-agent-adapter-contract); the shared contract is in [routing.md](../concepts/routing.md#agent-adapter-contract).

## Contract

The adapter implements `run(role, brief, workspace, limits) → result` ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters)):

- Non-interactive CLI inside the container, as a non-root user (`claude -p --output-format json …`). Permission prompts are disabled inside the container; the container is the security boundary.
- **Pinned CLI.** Claude Code from npm `@anthropic-ai/claude-code`; the base image records the exact version (M0 verified 2.1.317); the adapter refuses an unknown version. Containers set `DISABLE_AUTOUPDATER=1`.
- **Configuration comes only from DDF.** The adapter starts the CLI with DDF's rendered role bundle as the only settings source: project and user settings, hooks, MCP servers and plugins from the workspace are disabled. Project instructions (`AGENTS.md`) reach agents only through the brief, read from the integration branch.
- **Isolation switches:** `--setting-sources ''` with `--settings <bundle>` and `--strict-mcp-config --mcp-config <bundle>`, brief on stdin; `--tools` restricts built-in tools for reviewers but is not isolation; `--bare` is unusable with a subscription token.
- Residual Claude Code sources these flags do not disable: account-synced remote settings (applied regardless of local flags) and built-in plugins and agents; DDF therefore uses a dedicated account and token, and the M3 probe `claude-config-isolation` fails closed if the remote settings carry hooks, permissions or MCP servers.
- Every call passes an explicit `--model` from routing (never a CLI default; Claude Code's own `--fallback-model` stays unused) and runs under a wall-clock timeout, with `--no-session-persistence` in a fresh, empty home.
- The container receives only this adapter's credential (`CLAUDE_CODE_OAUTH_TOKEN` or one provider key). All agent-originated output is redacted before any forge write or notification ([roles.md](../concepts/roles.md#publisher)).
- Inputs mounted read-only: brief, role bundle. Outputs: `/out/result.json` (validated against `schemas/<role>.result.json`; missing or invalid → failed run) and, for the Executor, `/out/change.patch`.
- The adapter records model ID, token usage and cost from the CLI output into the journal (`--output-format json`: the `modelUsage` keys, `usage`, `total_cost_usd`; the cost is a list-price estimate, not a bill).
- Role prompts live once in `prompts/`; the adapter renders them into Claude Code's native form (system prompt and agent files) in a directory outside the workspace.
- Optional per-task `maxCostUsd` for API-key-billed entries.

## Role specifics

- Reviewers (R1, R2) get read/search tools only and no command execution ([roles.md](../concepts/roles.md#reviewer-correctness)): `--tools Read,Grep,Glob` with `--permission-mode dontAsk`, a bundle `permissions.deny` list as a second layer, cwd outside the workspace and the read-only copy added with `--add-dir`.
- The Executor gets full tools inside the container (`--permission-mode bypassPermissions`, cwd in the workspace copy); `limits.maxSubagentsPerRun` is passed as advice, not enforced ([routing.md](../concepts/routing.md#limits)).
- The patch is produced by the adapter's trusted wrapper, not by the agent ([identity.md](../concepts/identity.md#workspaces-and-publication)).

## Errors and rate limits

Failures are classified from `is_error`, `api_error_status` and the assistant `error` field, never from `subtype` (an auth failure still reports `subtype: "success"`):

| Signal | Classification |
|---|---|
| `authentication_failed`, a `result` starting with "Not logged in", or a non-zero exit with no `api_error_status` and authentication text | credential failure: global pause, reason `credential`; never retried |
| HTTP 429 (`rate_limit`) | rate limit: the entry cools until its reset time; the next entry of the tier is used ([routing.md](../concepts/routing.md#selection-and-fallback)) |
| other retryable errors (5xx: `server_error`, `overloaded`) | backoff on the same entry, not fallback |

Acceptance probe 9 checks the fallback to the opencode entry ([security-model.md](../operator/security-model.md#probes)). The rate-limit shape is unverified (M3 probe `claude-rate-limit-shape`).

## M0 facts

Verified in M0 with Claude Code 2.1.317 ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 4): non-interactive runs as non-root with only a long-lived token and an empty home, the isolation switches, the reviewer tool restriction and the JSON output fields. Exact command lines per role, the bundle files and the evidence: [M0 facts](../research/2026-11-m0-facts.md#recommended-adapter-command-lines). M3 probes: `cli-version-pin`, `agent-noninteractive`, `agent-home-state`, `claude-config-isolation`, `claude-reviewer-readonly`, `claude-executor-output`, `claude-auth-error-shape`, `claude-rate-limit-shape`, `secret-exfil-redaction`.
