# agent-claude-code adapter

Purpose: how DDF runs a role through the Claude Code CLI inside a run container.

Status: implemented in M3

Routing name: `adapter: claude-code` in the operator's `routing.yaml` ([config-reference.md](../operator/config-reference.md#entry)). Rationale: [spec §7.3](../specs/2026-11-03-ddf-v1-design.md#73-agent-adapter-contract); the shared contract is in [routing.md](../concepts/routing.md#agent-adapter-contract).

## Contract

The adapter implements `run(role, brief, workspace, limits) → result` ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters)):

- Non-interactive CLI inside the container, as a non-root user (`claude -p --output-format json …`). Permission prompts are disabled inside the container; the container is the security boundary.
- **Configuration comes only from DDF.** The adapter starts the CLI with DDF's rendered role bundle as the only settings source: project and user settings, hooks, MCP servers and plugins from the workspace are disabled. Project instructions (`AGENTS.md`) reach agents only through the brief, read from the integration branch.
- Inputs mounted read-only: brief, role bundle. Outputs: `/out/result.json` (validated against `schemas/<role>.result.json`; missing or invalid → failed run) and, for the Executor, `/out/change.patch`.
- The adapter records model ID, token usage and cost from the CLI output into the journal.
- Role prompts live once in `prompts/`; the adapter renders them into Claude Code's native form (system prompt and agent files) in a directory outside the workspace.
- Optional per-task `maxCostUsd` for API-key-billed entries.

## Role specifics

- Reviewers (R1, R2) get read/search tools only and no command execution ([roles.md](../concepts/roles.md#reviewer-correctness)).
- The Executor gets full tools inside the container; `limits.maxSubagentsPerRun` is passed as advice, not enforced ([routing.md](../concepts/routing.md#limits)).
- The patch is produced by the adapter's trusted wrapper, not by the agent ([identity.md](../concepts/identity.md#workspaces-and-publication)).

## Rate limits

A rate-limit error marks the routing entry as cooling until its reset time; the next entry of the tier is used ([routing.md](../concepts/routing.md#selection-and-fallback)). Acceptance probe 9 checks the fallback to the opencode entry ([security-model.md](../operator/security-model.md#probes)).

## Pending M0 facts

The exact CLI flags for settings isolation (disabling workspace settings, hooks, MCP servers and plugins, and using only DDF's bundle), the read-only tool restriction for reviewers, non-interactive behaviour as non-root with a long-lived token, and the rate-limit error shape come from M0 ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 4). This page states only the contract above; it does not prescribe flags.
