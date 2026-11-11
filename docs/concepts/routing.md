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

`routing.yaml` lives in the operator config. Shape (example; adapter, provider and model values are placeholders):

```yaml
# <operator-config>/routing.yaml — example
tiers:
  deep:     [{adapter: claude-code, provider: anthropic, model: <model>, effort: high}]
  standard: [{adapter: claude-code, provider: anthropic, model: <model>, effort: medium},
             {adapter: opencode, provider: <provider>, model: <provider/model>}]   # fallback on rate limit
  routine:  [{adapter: claude-code, provider: anthropic, model: <model>, effort: medium}]
  fast:     [{adapter: claude-code, provider: anthropic, model: <model>, effort: low}]
roles:
  r2: {prefer: [{adapter: opencode, provider: <other-provider>, model: <other-provider/model>}]}   # optional
limits:
  maxSubagentsPerRun: 3
  # maxConcurrentContainers: 2   # optional operator cap; capacity comes from the Sandbox port
```

## Selection and fallback

1. A role resolves to a tier (default from [roles.md](roles.md), raised by risk class or project).
2. Entries of the tier are tried in order; a `roles.<role>.prefer` list is tried first when present.
3. An entry whose run fails with `rate-limited` is marked cooling until its reset time; the next entry of the tier is used. A `retryable` failure backs off on the same entry, not fallback. A `credential` failure is a global pause, never a fallback ([Port level](#port-level)).
4. With no entry left, the task waits and one notification is sent (once per reset window).

A routing entry has `adapter`, `provider`, `model` and optional `effort`. `provider` is required (pattern `^[a-z][a-z0-9-]*$`) and names the model provider the entry calls. DDF compares only this field and never derives it from `model`. With `reviewers.r2DifferentProvider: true`, R2 uses only entries whose `provider` differs from the `provider` of the entry that ran the Executor for the reviewed head. Adapter and model are not compared. With no such entry, the no-entry-left rule of step 4 applies.

## Limits

- **Capacity:** the scheduler admits runs against the host capacity (CPU, memory, disk) reported by the Sandbox port, with an operator override: an operator setting in routing `limits` that replaces the port-reported values; its exact key and schema land with the sizing rule in [#34](https://github.com/syllik/deep-dark-factory/issues/34). Per-run limits come from that sizing rule. Every container counts — Executor, each reviewer, local gate, `forge-local` CI ([spec §7.2](../specs/2026-11-03-ddf-v1-design.md#72-operator-routing), [spec §4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider)).
- `maxConcurrentContainers` (optional, no default): an operator cap on concurrent containers, applied on top of capacity; not the scheduling input.
- `maxSubagentsPerRun` (default 3): advisory, passed to the agent; not enforceable by the CLIs.
- Per-run limits must fit the reported capacity; `ddf status` reports a misfit.
- Optional per-task `maxCostUsd` for API-key-billed entries. No per-stage human grants and no per-call USD reservations.

## Agent adapter contract

### Port level

Every agent adapter implements `run(role, entry, brief, workspace, limits, credentialSource) → (result, patch, usage)` or one failure class ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters), [spec §7.3](../specs/2026-11-03-ddf-v1-design.md#73-agent-adapter-contract)).

- **Inputs:** role; the routing entry (`provider`, `model`, `effort`); the brief; the workspace content (the run's workspace copy, read-only for Planner and reviewers); limits (wall-clock timeout, `maxSubagentsPerRun` as advice, optional `maxCostUsd`); the credential source.
- **Outputs:** the result, validated against `schemas/<role>.result.json`; for the Executor, the patch made by the trusted wrapper ([identity.md](identity.md#workspaces-and-publication)); usage (model ID, tokens, cost), recorded in the journal.
- **Failure classes.** Exactly one failure class, chosen by the adapter:
  - `credential`: global pause with reason `credential` ([lifecycle.md](lifecycle.md#global-pause)), never retried. This includes a credential source that yields nothing or an empty value. The adapter checks for that before any provider call.
  - `rate-limited` with a reset time: the entry cools until that time, then the next entry is used ([Selection and fallback](#selection-and-fallback)).
  - `retryable`: backoff on the same entry, not fallback.
  - `fatal`: a failed run within the bounds of [lifecycle.md](lifecycle.md#bounds). This covers a missing or invalid result and a timeout.
- Every call uses the entry's model, never an adapter default or a free fallback model.
- **Configuration comes only from DDF.** The adapter loads no repository-controlled agent configuration, and project instructions (`AGENTS.md`, read from the integration branch) reach the agent only through the brief.
- Role prompts live once in `prompts/`, and each adapter renders them into its native form outside the workspace.
- The adapter receives only the routed entry's credential. All agent-originated output passes the redaction of [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers) before any forge write or notification ([lifecycle.md](lifecycle.md#publishing)).
- **Credential source.** The Agent port receives its credential through a credential source, a broker interface. The core never passes the credential as a value. The first implementation returns the routed entry's stored credential from the Secrets port, as today. A later implementation can return a per-run token that is valid only against a control-plane endpoint proxying the provider, with no port change. Accepted schedule ([spec §15](../specs/2026-11-03-ddf-v1-design.md#15-accepted-risks-v1)):
  - run-container egress is restricted per adapter in M3;
  - no real pay-per-token provider key enters a run container: the per-run token implementation is in place before the first pay-per-token key is loaded;
  - subscription tokens that cannot be proxied stay visible inside their routed adapter's container. This is an accepted risk, on a dedicated account. Publishing any of them stays not accepted (spec §8.4 redaction).

### CLI-in-container profile

These rules apply only to an adapter that runs a CLI inside the run container. `agent-claude-code` and `agent-opencode` implement this profile.

1. Runs a non-interactive CLI inside the container as a non-root user. Permission prompts are disabled inside the container; the container is the security boundary.
2. **Pinned CLIs.** The base image records exact CLI versions (the Claude Code npm package; opencode 1.x, 2.x unsupported); the adapter refuses an unknown version.
3. **Rendered settings bundle.** The CLI starts with DDF's rendered role bundle as the only settings source; project and user settings, hooks, MCP servers and plugins from the workspace are disabled by the isolation switches. Residual Claude Code sources the switches do not disable are covered by a dedicated account, and the M3 probe `claude-config-isolation` fails closed.
4. **Credential.** The adapter takes the credential from the credential source and delivers it into the container as environment; the container holds only that credential. Before starting the CLI the adapter checks that the credential is present and non-empty.
5. Inputs mounted read-only: brief, role bundle.
6. Outputs: `/out/result.json` (the result) and, for the Executor, `/out/change.patch`.
7. Usage (model ID, token usage, cost) is parsed from the CLI output.
8. Every call passes the entry's model explicitly, never a CLI default.
9. **No run-time installs.** The rule covers only the agent CLI and its own packages; the base image pre-installs opencode's plugin SDK. A run in which the CLI or its own packages are installed from a registry at run time fails (M3 probes `cli-version-pin`, `agent-home-state`). Project dependencies come from the project image (`image`) by default. A project whose verify installs at run time lists the registry hosts in the top-level key `registries` of `.ddf/project.yaml` ([project-yaml-reference.md](../project/project-yaml-reference.md#registries)); `.ddf/**` is a forbidden path, so only a human-merged change can add a registry.
10. **Egress per run**, enforced from M3 by each runtime profile's own mechanism: an agent run reaches the provider endpoint of its routed adapter; Executor runs, the local gate and `forge-local` CI also reach the declared registries; nothing else. DNS behaves as in the isolation properties ([identity.md](identity.md#containers)).
11. **Failure mapping** onto the four classes of the [Port level](#port-level). Fields read: Claude Code `is_error`, `api_error_status` and the assistant `error` field, never `subtype`; opencode `error` event `statusCode`/`isRetryable`.
    - `credential`: authentication signals, meaning `authentication_failed`, a `result` starting with "Not logged in", a non-zero exit with no `api_error_status` and authentication text, or opencode 401/403.
    - `rate-limited`: HTTP 429 only.
    - `retryable`: other retryable errors (5xx, `overloaded`, `isRetryable: true`).
    - `fatal`: everything else (opencode `UnknownError` with the stderr log attached, other non-zero exits, missing or invalid `/out/result.json`, timeout).

V1 adapters: `agent-claude-code`, `agent-opencode` (decision D3). The verified isolation switches, versions and error fields per CLI: [agent-claude-code.md](../adapters/agent-claude-code.md), [agent-opencode.md](../adapters/agent-opencode.md); full command lines and evidence in [the M0 facts](../research/2026-11-m0-facts.md#recommended-adapter-command-lines).
