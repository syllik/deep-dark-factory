# sandbox-docker adapter

Purpose: how DDF creates, runs and kills one isolated container per run, with colima on macOS and Docker on Linux.

Status: implemented in M3

Rationale: [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters), [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers). The test double `sandbox-fake` implements the same port for the offline core.

## Contract

Sandbox port: create/run/kill a container with mounts, limits and network rules.

## Container rules

- One per run; base image from `images/` or the project's `image`; non-root user; default limits 3 GB RAM, 2 CPU, pids limit; no Docker socket.
- **No access to host services.** On colima the VM's gateway to the host loopback is dropped by firewall rules inside the VM, re-applied by the daemon on every VM start or recreation; containers reach only the internet. A probe verifies this at daemon start and in acceptance ([security-model.md](../operator/security-model.md#probes), probe 6).
- Services (databases, browsers) run inside the container.
- Network egress to the internet is open in V1 (model APIs, package registries) — accepted risk ([security-model.md](../operator/security-model.md#accepted-risks)).
- No forge credentials and no review-signing key inside; agent CLI tokens and provider keys are present (accepted risk).

## Mounts

| Run | Read-only | Writable |
|---|---|---|
| Executor run | brief, role bundle | the run's workspace copy (own `.git`, no remotes, no credentials), `/out` |
| Planner and reviewer runs | brief, role bundle, the run's workspace copy (own `.git`, no remotes, no credentials) | `/out` only |
| Local gate, `forge-local` CI | none beyond the image | the applied tree copy |

Planner and reviewers get the workspace copy read-only and only read/search tools ([roles.md](../concepts/roles.md#reviewer-correctness)). After the Executor, the adapter's trusted wrapper writes `/out/change.patch` ([identity.md](../concepts/identity.md#workspaces-and-publication)).

## Capacity

- Every container counts against `limits.maxConcurrentContainers`: Executor, each reviewer, local gate, `forge-local` CI ([config-reference.md](../operator/config-reference.md#limits)).
- Container limits must fit the VM size; `ddf status` reports a misfit.
- On daemon start, containers left by a previous instance are killed and their runs discarded (retried within the run budget).
- Container runtime unavailable → global pause and a notification.

## Pending M0 facts

colima under a non-login service user via launchd (start-up, resource limits, the VM firewall blocking the host gateway, coexistence with other users' services on the host) is verified in M0 ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 5). Host commands for the firewall rules are added to [setup.md](../operator/setup.md#m0-recipe) once recorded.
