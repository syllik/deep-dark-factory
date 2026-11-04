# Routing

Purpose: how DDF maps role tiers to agent adapters and models, how it falls back on rate limits, and what every agent adapter must do.

Status: normative

Rationale and full design: [spec §7.2](../specs/2026-11-03-ddf-v1-design.md#72-operator-routing) and [spec §7.3](../specs/2026-11-03-ddf-v1-design.md#73-agent-adapter-contract). Roles and their default tiers: [roles.md](roles.md).

## Tiers

Tiers: `frontier`, `deep`, `standard`, `routine`, `fast`.

- `frontier` is never selected automatically.
- Roles name tiers; model names appear only in the operator layer ([config-layers.md](config-layers.md#rules)).
- Projects may only raise tiers or set `reviewers.r2DifferentProvider`.

## Operator routing file

`routing.yaml` lives in the operator config. Shape (example; adapter and model values are placeholders):

```yaml
# <operator-config>/routing.yaml — example
tiers:
  deep:     [{adapter: claude-code, model: <model>, effort: high}]
  standard: [{adapter: claude-code, model: <model>, effort: medium},
             {adapter: opencode, model: <provider/model>}]   # fallback on rate limit
  routine:  [{adapter: claude-code, model: <model>, effort: medium}]
  fast:     [{adapter: claude-code, model: <model>, effort: low}]
roles:
  r2: {prefer: [{adapter: opencode, model: <other-provider/model>}]}   # optional
limits:
  maxConcurrentContainers: 2
  maxSubagentsPerRun: 3
```

## Selection and fallback

1. A role resolves to a tier (default from [roles.md](roles.md), raised by risk class or project).
2. Entries of the tier are tried in order; a `roles.<role>.prefer` list is tried first when present.
3. A rate-limited entry is marked cooling until its reset time; the next entry of the tier is used.
4. With no entry left, the task waits and one notification is sent (once per reset window).

## Limits

- `maxConcurrentContainers`: every container counts — Executor, each reviewer, local gate, `forge-local` CI.
- `maxSubagentsPerRun` (default 3): advisory, passed to the agent; not enforceable by the CLIs.
- Container limits must fit the container VM size; `ddf status` reports a misfit.
- Optional per-task `maxCostUsd` for API-key-billed entries. No per-stage human grants and no per-call USD reservations.

## Agent adapter contract

Every agent adapter implements `run(role, brief, workspace, limits) → result` ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters)):

1. Runs a non-interactive CLI inside the container as a non-root user. Permission prompts are disabled inside the container; the container is the security boundary.
2. **Configuration comes only from DDF.** The CLI starts with DDF's rendered role bundle as the only settings source; project and user settings, hooks, MCP servers and plugins from the workspace are disabled. Project instructions (`AGENTS.md`) reach agents only through the brief, read from the integration branch.
3. Inputs mounted read-only: brief, role bundle.
4. Outputs: `/out/result.json`, validated against `schemas/<role>.result.json` (missing or invalid → failed run, bounded by [lifecycle.md](lifecycle.md#bounds)); for the Executor also `/out/change.patch`.
5. Records model ID, token usage and cost from the CLI output into the journal.
6. Role prompts live once in `prompts/`; each adapter renders them into the CLI's native form in a directory outside the workspace.

V1 adapters: `agent-claude-code`, `agent-opencode` (decision D3). Exact CLI flags are verified in M0 ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0)).
