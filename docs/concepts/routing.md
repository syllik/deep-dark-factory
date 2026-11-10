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
  maxSubagentsPerRun: 3
  # maxConcurrentContainers: 2   # optional operator cap; capacity comes from the Sandbox port
```

## Selection and fallback

1. A role resolves to a tier (default from [roles.md](roles.md), raised by risk class or project).
2. Entries of the tier are tried in order; a `roles.<role>.prefer` list is tried first when present.
3. A rate-limited entry (HTTP 429 only) is marked cooling until its reset time; the next entry of the tier is used. Other retryable errors back off on the same entry; an authentication failure is a credential failure, not a fallback ([Agent adapter contract](#agent-adapter-contract)).
4. With no entry left, the task waits and one notification is sent (once per reset window).

## Limits

- **Capacity:** the scheduler admits runs against the host capacity (CPU, memory, disk) reported by the Sandbox port, with an operator override: an operator setting in routing `limits` that replaces the port-reported values; its exact key and schema land with the sizing rule in [#34](https://github.com/syllik/deep-dark-factory/issues/34). Per-run limits come from that sizing rule. Every container counts — Executor, each reviewer, local gate, `forge-local` CI ([spec §7.2](../specs/2026-11-03-ddf-v1-design.md#72-operator-routing), [spec §4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider)).
- `maxConcurrentContainers` (optional, no default): an operator cap on concurrent containers, applied on top of capacity; not the scheduling input.
- `maxSubagentsPerRun` (default 3): advisory, passed to the agent; not enforceable by the CLIs.
- Per-run limits must fit the reported capacity; `ddf status` reports a misfit.
- Optional per-task `maxCostUsd` for API-key-billed entries. No per-stage human grants and no per-call USD reservations.

## Agent adapter contract

Every agent adapter implements `run(role, brief, workspace, limits) → result` ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters)):

1. Runs a non-interactive CLI inside the container as a non-root user. Permission prompts are disabled inside the container; the container is the security boundary.
2. **Configuration comes only from DDF.** The CLI starts with DDF's rendered role bundle as the only settings source; project and user settings, hooks, MCP servers and plugins from the workspace are disabled. Project instructions (`AGENTS.md`) reach agents only through the brief, read from the integration branch.
3. Inputs mounted read-only: brief, role bundle.
4. Outputs: `/out/result.json`, validated against `schemas/<role>.result.json` (missing or invalid → failed run, bounded by [lifecycle.md](lifecycle.md#bounds)); for the Executor also `/out/change.patch`.
5. Records model ID, token usage and cost from the CLI output into the journal.
6. Role prompts live once in `prompts/`; each adapter renders them into the CLI's native form in a directory outside the workspace.
7. **Pinned CLIs.** The base image records exact CLI versions; the adapter refuses an unknown version.
8. **No run-time installs.** A run in which any package-registry install happens at run time fails.
9. Each container receives only the credential of its routed adapter. All agent-originated output passes the redaction of §8.4 before any forge write or notification ([lifecycle.md](lifecycle.md#publishing)).
10. Every call passes an explicit model from routing (never a CLI default or a free fallback model) and runs under a wall-clock timeout.
11. **Failure classification** is deterministic: authentication failure → credential failure (global pause, reason `credential`; [lifecycle.md](lifecycle.md#global-pause)), never retried; only HTTP 429 is a rate limit → the next routing entry; other retryable errors (5xx) → backoff on the same entry, not fallback.

V1 adapters: `agent-claude-code`, `agent-opencode` (decision D3). The verified isolation switches, versions and error fields per CLI: [agent-claude-code.md](../adapters/agent-claude-code.md), [agent-opencode.md](../adapters/agent-opencode.md); full command lines and evidence in [the M0 facts](../research/2026-11-m0-facts.md#recommended-adapter-command-lines).
