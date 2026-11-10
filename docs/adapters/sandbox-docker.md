# sandbox-docker adapter

Purpose: how DDF creates, runs and kills one isolated container per run, with the container runtime of the runtime profile ([spec §8.1](../specs/2026-11-03-ddf-v1-design.md#81-host)). Every profile gives run containers the same isolation properties: no route to the host, the control plane or private address ranges (DNS excepted); all capabilities dropped; no new privileges; non-root user; pids and memory limits; no runtime socket.

Status: implemented in M3

Rationale: [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters), [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers). The test double `sandbox-fake` implements the same port for the offline core.

## Contract

Sandbox port: create/run/kill a container with mounts, limits and network rules.

## Container rules

- One per run; base image from `images/` or the project's `image`; fresh, empty home; default limits 3 GB RAM, 2 CPU, pids limit.
- **Isolation properties.** Every run container has: no route to the host, the control plane or private address ranges (DNS excepted); all capabilities dropped; no new privileges; a non-root user; pids and memory limits; no container runtime socket. One probe set checks them ([security-model.md](../operator/security-model.md#probes), §14.1 probe 6). Each runtime profile says how it provides them and must pass the same probes. Before every container start the daemon checks the isolation rules and runs the probes. Precedence ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)): isolation rules absent or changed while no runtime restart was observed (same runtime instance identifier), even when a re-apply would succeed → pause `security`, no automatic lift; isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure, or the daemon's own runtime restart attempts failed → pause `runtime`, lifted automatically.
- **Linux container profile:** provides the properties through the runtime's per-container options and network rules; how exactly is decided by the probe spike together with rootful or rootless.
- **macOS colima profile:** colima firewall rules, applied via `colima ssh`, are this profile's way to provide "no route". On colima, firewall rules inside the VM drop container traffic to the VM's gateway to the host loopback and to the VM itself, except DNS; containers reach only the internet. The rules do not survive a VM restart. The daemon applies them (`colima ssh -- sudo …`) after every VM start or recreation; colima provision hooks may add an early layer but are never the guarantee. Before every container start the daemon checks that the rules exist and runs the host-reachability probe; on failure it follows the §8.4 precedence. Rules found missing while no VM restart was observed (VM boot ID, this profile's runtime instance identifier, unchanged since the daemon last applied them) are a tamper signal: global pause with reason `security` (§9), no automatic lift. If the VM is down, `ddfd` restarts it as `ddf` (`colima start`, bounded attempts), then applies and probes the rules. The probe also holds in §14.1 ([security-model.md](../operator/security-model.md#probes), probe 6).
- Services (databases, browsers) run inside the container.
- Network egress to the internet is open in V1 (model APIs, package registries) — accepted risk ([security-model.md](../operator/security-model.md#accepted-risks)). IPv6 is not filtered; the isolation probe fails if a container gets a global IPv6 route.
- No forge credentials and no review-signing key inside. Each container receives only the credential of its routed adapter (accepted risk), passed as environment.
- **Redaction before publication.** Before every forge write and every notification send, all agent-originated text (review projection comments, change body, commit message, patch content, Telegram messages) is redacted by secret patterns and by the exact values of the secrets loaded into that run's container. A patch containing a loaded secret value is refused: hard stop `secrets` (§6.4), failed run. Any process in the container can read its environment, so tool restrictions are not a secret control; redaction on the way out is.

## Mounts

| Run | Read-only | Writable |
|---|---|---|
| Executor run | brief, role bundle | the run's workspace copy (own `.git`, no remotes, no credentials), `/out` |
| Planner and reviewer runs | brief, role bundle, the run's workspace copy (own `.git`, no remotes, no credentials) | `/out` only |
| Local gate, `forge-local` CI | none beyond the image | the applied tree copy |

Planner and reviewers get the workspace copy read-only and only read/search tools ([roles.md](../concepts/roles.md#reviewer-correctness)). After the Executor, the adapter's trusted wrapper writes `/out/change.patch` ([identity.md](../concepts/identity.md#workspaces-and-publication)).

## Capacity

- Every container counts against `limits.maxConcurrentContainers`: Executor, each reviewer, local gate, `forge-local` CI ([config-reference.md](../operator/config-reference.md#limits)).
- Container limits must fit the resources of the container runtime; `ddf status` reports a misfit.
- On daemon start, containers left by a previous instance are killed and their runs discarded (retried within the run budget).
- An OOM kill (exit 137 with `OOMKilled`) is a distinct run failure reason.
- Isolation rules absent or changed while no runtime restart was observed (same runtime instance identifier), even when a re-apply would succeed → global pause with reason `security`, no automatic lift. Container runtime unavailable (isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure, or the daemon's own runtime restart attempts failed) → global pause with reason `runtime` and a notification; lifted automatically once health check, isolation-rule check and probes pass again ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers), [lifecycle.md](../concepts/lifecycle.md#global-pause)).

## M0 facts

macOS colima profile evidence. Verified in M0 ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 5; evidence in [M0 facts](../research/2026-11-m0-facts.md#colima-under-a-service-user-16-item-5)): colima runs as `ddf` from a LaunchDaemon; the limits above are enforced in the container cgroup; without the rules a container reaches the host's loopback services through `host.lima.internal` and the VM itself; the rules block both while internet and DNS stay open; the rules are lost on every VM restart; launchd does not restart the VM. The firewall content (chains hooked from `DOCKER-USER` and `INPUT`, addresses discovered at run time) is in the research appendix; host steps are in [setup.md](../operator/setup.md#m0-recipe). M3 probes: `vm-firewall-reapply`, `runtime-recovery`, `coexistence`.
