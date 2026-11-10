# sandbox-docker adapter

Purpose: how DDF creates, runs, kills and destroys one isolated, labelled run cell per run, with the container runtime of the runtime profile ([spec §8.1](../specs/2026-11-03-ddf-v1-design.md#81-host)). Every profile gives run containers the same isolation properties: no route to the host, the control plane or private address ranges (DNS excepted); all capabilities dropped; no new privileges; non-root user; pids and memory limits; no runtime socket.

Status: implemented in M3

Rationale: [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters), [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers). The test double `sandbox-fake` implements the same port for the offline core.

## Contract

Sandbox port operations ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters), [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)):

- create a run cell (container plus any volume or network it needs), labelled with task key and run ID;
- put content in;
- run;
- get files out;
- kill;
- list resources by label (task key, run ID);
- destroy by task key, then verify by listing that nothing with that key remains;
- report host capacity (CPU, memory, disk).

No operation takes a host path. An unverifiable teardown is a reported failure (journal and notification), never silent.

## Container rules

- One per run; base image from `images/` or the project's `image`; fresh, empty home; pids limit and memory limit; per-run limits come from the sizing rule (Issue #34).
- **Isolation properties.** Every run container has: no route to the host, the control plane or private address ranges (DNS excepted); all capabilities dropped; no new privileges; a non-root user; pids and memory limits; no container runtime socket. One probe set checks them ([security-model.md](../operator/security-model.md#probes), §14.1 probe 6). Each runtime profile says how it provides them and must pass the same probes. Before every container start the daemon checks the isolation rules and runs the probes. Precedence ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)): isolation rules absent or changed while no runtime restart was observed (same runtime instance identifier), even when a re-apply would succeed → pause `security`, no automatic lift; isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure, or the daemon's own runtime restart attempts failed → pause `runtime`, lifted automatically.
- **Linux container profile:** provides the properties through the runtime's per-container options and network rules; how exactly is decided by the probe spike together with rootful or rootless.
- **macOS colima profile:** colima firewall rules, applied via `colima ssh`, are this profile's way to provide "no route". On colima, firewall rules inside the VM drop container traffic to the VM's gateway to the host loopback and to the VM itself, except DNS; containers reach only the internet. The rules do not survive a VM restart. The daemon applies them (`colima ssh -- sudo …`) after every VM start or recreation; colima provision hooks may add an early layer but are never the guarantee. Before every container start the daemon checks that the rules exist and runs the host-reachability probe; on failure it follows the §8.4 precedence. Rules found missing while no VM restart was observed (VM boot ID, this profile's runtime instance identifier, unchanged since the daemon last applied them) are a tamper signal: global pause with reason `security` (§9), no automatic lift. If the VM is down, `ddfd` restarts it as `ddf` (`colima start`, bounded attempts), then applies and probes the rules. The probe also holds in §14.1 ([security-model.md](../operator/security-model.md#probes), probe 6).
- Services (databases, browsers) run inside the container.
- Network egress per run, enforced from M3 by each runtime profile's own mechanism ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers), [security-model.md](../operator/security-model.md#accepted-risks)): an agent run reaches the provider endpoint of its routed adapter; Executor runs, the local gate and `forge-local` CI also reach the project's declared registries ([`registries`](../project/project-yaml-reference.md#registries)); nothing else. DNS behaves as in the isolation properties. IPv6 is not filtered; the isolation probe fails if a container gets a global IPv6 route.
- No forge credentials and no review-signing key inside. Each container receives only the credential of its routed adapter (accepted risk). The credential comes through the credential source ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)), and the CLI-in-container profile ([routing.md](../concepts/routing.md#cli-in-container-profile)) delivers it as environment.
- **Redaction before publication.** Before every forge write and every notification send, all agent-originated text (review projection comments, change body, commit message, patch content, Telegram messages) is redacted by secret patterns and by the exact values of the secrets loaded into that run's container. A patch containing a loaded secret value is refused: hard stop `secrets` (§6.4), failed run. Any process in the container can read its environment, so tool restrictions are not a secret control; redaction on the way out is.

## Content transport

The port takes content in (brief, role bundle, workspace copy) and returns files out (`result.json`, `change.patch`). How the adapter moves it (an archive stream, or a per-run volume the control plane fills) is the adapter's matter ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)). The run's copy is per-run, never shared with the host's working files or another run, reachable only by that run's container, and destroyed with the cell; it is never a bind mount of a control-plane or host path.

| Run | Content in, read-only | Content in, writable | Files out |
|---|---|---|---|
| Executor run | brief, role bundle | the run's workspace copy (own `.git`, no remotes, no credentials) | `result.json`, `change.patch` |
| Planner and reviewer runs | brief, role bundle, the run's workspace copy (own `.git`, no remotes, no credentials) | none | `result.json` |
| Local gate, `forge-local` CI | none beyond the image | the applied tree, as content | check results |

Planner and reviewers get the workspace copy read-only and only read/search tools ([roles.md](../concepts/roles.md#reviewer-correctness)). After the Executor, the adapter's trusted wrapper writes `change.patch` ([identity.md](../concepts/identity.md#workspaces-and-publication)).

## Capacity

- The port reports host capacity (CPU, memory, disk); the scheduler admits runs against it, with an operator override in routing config `limits` that replaces the reported values ([spec §4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider), [routing.md](../concepts/routing.md#limits)). Every run cell counts: Executor, each reviewer, local gate, `forge-local` CI. Per-run limits come from the sizing rule (Issue #34). `limits.maxConcurrentContainers` is an optional operator cap, not the scheduling input ([config-reference.md](../operator/config-reference.md#limits)).
- Container limits must fit the resources of the container runtime; `ddf status` reports a misfit.
- **Recovery on daemon start** ([spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery)): the daemon lists execution resources by label. Resources whose task is terminal or unknown to the store are destroyed and verified, and the teardown is recorded in the store (destroyed, verified, time; [spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)). Resources of a non-terminal task are, in V1, killed and their run discarded (retried within the run budget). The decision is per task key and run ID from labels, so re-adopting live runs later changes only that branch, not the port.
- An OOM kill (exit 137 with `OOMKilled`) is a distinct run failure reason.
- Isolation rules absent or changed while no runtime restart was observed (same runtime instance identifier), even when a re-apply would succeed → global pause with reason `security`, no automatic lift. Container runtime unavailable (isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure, or the daemon's own runtime restart attempts failed) → global pause with reason `runtime` and a notification; lifted automatically once health check, isolation-rule check and probes pass again ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers), [lifecycle.md](../concepts/lifecycle.md#global-pause)).

## M0 facts

macOS colima profile evidence. Verified in M0 ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 5; evidence in [M0 facts](../research/2026-11-m0-facts.md#colima-under-a-service-user-16-item-5)): colima runs as `ddf` from a LaunchDaemon; the limits above are enforced in the container cgroup; without the rules a container reaches the host's loopback services through `host.lima.internal` and the VM itself; the rules block both while internet and DNS stay open; the rules are lost on every VM restart; launchd does not restart the VM. The firewall content (chains hooked from `DOCKER-USER` and `INPUT`, addresses discovered at run time) is in the research appendix; host steps are in [setup.md](../operator/setup.md#m0-recipe). M3 probes: `vm-firewall-reapply`, `runtime-recovery`, `coexistence`.
