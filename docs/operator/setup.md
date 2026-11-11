# Operator setup

Purpose: the target installation of DDF on one host by one operator: service user, control plane, container runtime, operator config and secrets.

Status: implemented in M5

Nothing on this page is installable yet. It describes the target layout that M5 delivers so that every earlier milestone builds toward it. Rationale: [spec §8.1](../specs/2026-11-03-ddf-v1-design.md#81-host), [spec §3](../specs/2026-11-03-ddf-v1-design.md#3-configuration-layers), milestone M5 in [spec §14](../specs/2026-11-03-ddf-v1-design.md#14-milestones).

## Target layout

Two runtime profiles ([spec §8.1](../specs/2026-11-03-ddf-v1-design.md#81-host)). The **Linux container profile** is the reference deployment: a Linux host with the control plane (`ddfd`, the store, the secrets) in a container. The **macOS colima profile** is the second runtime profile: `ddfd` runs from launchd as `ddf`, not in a container. Rootful or rootless runtime on Linux is open, decided by the probe spike; every sentence of the Linux profile holds for both.

| Piece | Target |
|---|---|
| Service user | dedicated non-admin OS user `ddf`, private home (mode 700) |
| Daemon | `ddfd`, single instance per host (exclusive file lock); Linux container profile: runs in the control-plane container, supervised by systemd; macOS colima profile: started by launchd as `ddf` |
| DDF code | a checkout of a maintainer-promoted ref of DDF: a release tag (a maintainer-made tag on a `master` commit that passed CI), never the moving tip of the integration branch |
| Container runtime | per runtime profile: container runtime on Linux, colima on the macOS colima profile ([sandbox-docker](../adapters/sandbox-docker.md)) |
| Operator config | a private repository or folder chosen by the operator, scaffolded by `ddf setup`; Linux container profile: the read-only `config` mount ([config-reference.md](config-reference.md)) |
| Secrets | the read-only `secrets` mount, never in Git |
| Daemon clones | in the `data` mount |
| Run logs | in the `data` mount, kept 30 days, secrets redacted by pattern and by loaded secret values |

The control-plane container has three named mounts: `config` (operator config, read-only), `secrets` (read-only) and `data` (store, clones, run logs). The fixed home-directory locations are the defaults of the non-container macOS colima profile: `~ddf/.config/ddf/secrets/` (mode 0600), `~ddf/work/<forge>/<owner>/<repo>` and `~ddf/runs/<task>/<run>/`.

The operator's own home directory and personal forge login are not accessible to the `ddf` user.

**Weak host:** a host with 1–2 GB of memory orchestrates; heavy verify runs in forge CI. A project's CI may use a self-hosted runner: that is project CI, which DDF only reads as required checks; DDF itself needs no runner. Such a runner is ephemeral, isolated and never runs as the control-plane host user.

## Steps (target order)

1. **Create the service user.** A non-admin account named `ddf` with a home directory readable only by itself. The operator's personal account keeps its own forge login; DDF never uses it.
2. **Install the container runtime** (Linux container profile: the container runtime on the Linux host; macOS colima profile: colima for the `ddf` user, plus the boot-time colima starter, [M0 recipe](#m0-recipe)). The scheduler admits runs against the host capacity (CPU, memory, disk) the runtime reports, with an operator override; per-run limits come from the sizing rule in [#34](https://github.com/syllik/deep-dark-factory/issues/34) ([routing.md](../concepts/routing.md#limits)).
3. **Check out DDF** at a promoted ref (macOS colima profile: into the `ddf` home). Updating the daemon is always a human action: promote, then move the checkout.
4. **Scaffold the operator config** with `ddf setup`. It creates `routing.yaml` and `projects.yaml` in the chosen private repository or folder. That repository must never be connected as a DDF project ([config-layers.md](../concepts/config-layers.md#rules)).
5. **Create the GitHub Apps** (GitHub projects only) with `ddf apps create`: three Apps, owned by the operator, installed only on selected repositories ([forge-github](../adapters/forge-github.md)).
6. **Place secrets** in the `secrets` mount (macOS colima profile: `~ddf/.config/ddf/secrets/`; see [Secrets layout](#secrets-layout)).
7. **Register the daemon service** (Linux container profile: systemd unit supervising the control-plane container; macOS colima profile: launchd job running `ddfd` as `ddf`), restarted automatically after host sleep or crash.
8. **First start in dry-run** (`ddfd --dry-run`): every forge write is logged, not performed. Check reads and intended writes before dropping `--dry-run`.
9. **Run the isolation probes** listed in [security-model.md](security-model.md#probes).

## Service manager

- **macOS colima profile:** a launchd job for the `ddf` user. launchd restarts the daemon (never the VM) after host sleep; expired run leases are released on start ([spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery)).
- **macOS colima profile:** launchd supervises `ddfd`, not the container VM: `ddfd` checks VM health and restarts the VM itself. A separate launchd colima job is only a boot-time starter (`RunAtLoad`, no `KeepAlive`, so it never races `ddfd`'s own restart) ([spec §8.1](../specs/2026-11-03-ddf-v1-design.md#81-host)).
- **Linux container profile:** a systemd unit that runs the control-plane container as the host user `ddf`.
- On start the daemon takes the instance lock and lists execution resources by label (task key, run ID): resources whose task is terminal or unknown to the store are destroyed and verified, and the teardown is recorded; resources of a non-terminal task are, in V1, killed and their run discarded (retried within budget). It then reconciles unresolved forge intents by task-key marker ([lifecycle.md](../concepts/lifecycle.md#store-intents-and-leases), [spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery)).

## Container runtime

Run containers have the isolation properties of [sandbox-docker](../adapters/sandbox-docker.md#container-rules) in every profile: no route to the host, the control plane or private address ranges (DNS excepted); all capabilities dropped; no new privileges; non-root user; pids and memory limits; no runtime socket. The same probe set checks them ([security-model.md](security-model.md#probes)). Before every container start the daemon checks the isolation rules and runs the probes. Isolation rules absent or changed while no runtime restart was observed (same runtime instance identifier), even when a re-apply would succeed: global pause with reason `security`, no automatic lift. Isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure, or the runtime unavailable after the daemon's own restart attempts: global pause with reason `runtime` and a notification, lifted automatically once health check, isolation-rule check and probes pass again ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers), [lifecycle.md](../concepts/lifecycle.md#global-pause)).

- **Linux container profile:** the runtime's per-container options and network rules provide the properties; how exactly is decided by the probe spike together with rootful or rootless.
- **macOS colima profile:** colima runs a VM per user. The VM firewall rules (no container access to the host loopback gateway or to the VM itself, except DNS) do not survive a VM restart: the daemon applies them after every VM start, and checks and probes them before every container start ([sandbox-docker](../adapters/sandbox-docker.md#container-rules)).
- **macOS colima profile:** if the VM is down, `ddfd` restarts it as `ddf` (`colima start`, bounded attempts). Only when that or the firewall re-apply fails: pause `runtime`. Rules missing without an observed VM restart: pause `security`.
- Free disk below the floor (`limits.minFreeDiskGb`, default 15 GB) → no new runs, notification.

## Operator config

```text
<operator-config>/
  routing.yaml     # tiers and roles → agent adapters and models (schema ddf.routing/v1)
  projects.yaml    # connected projects, max autonomy, allow-listed humans, notifications, limits (schema ddf.projects/v1)
```

Every key, type and default: [config-reference.md](config-reference.md). Model names appear only in this layer.

## Secrets layout

All secrets live in the read-only `secrets` mount (macOS colima profile: `~ddf/.config/ddf/secrets/`, owned by `ddf`, mode 0600), and are read by name through the `secrets-file` adapter. They are never committed and never referenced by value from `routing.yaml` or `projects.yaml`.

| Secret | Used by | Enters containers |
|---|---|---|
| GitHub App private keys (`worker`, `review-correctness`, `review-security`) | token broker on the host | never |
| Review-signing key (HMAC) | daemon only | never |
| Telegram bot token | `notify-telegram` | never |
| Agent CLI tokens and provider keys | agent adapters | yes, only the routed adapter's credential: a subscription token that cannot be proxied, an accepted risk ([security-model.md](security-model.md#accepted-risks)), or for a pay-per-token provider entry the per-run token from the credential source, never the real key; such an entry is unusable until the per-run token implementation of the credential source exists |

Exact file names are fixed by `ddf setup` in M5.

## M0 recipe

macOS colima profile. M0 verified colima under a non-login service user via launchd ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 5). The full recipe, evidence and the address-free firewall script: [M0 facts, Recipe for M5](../research/2026-11-m0-facts.md#recipe-for-m5). In short:

1. **Service user:** a hidden, non-admin user `ddf` with its home created explicitly and set to mode 700. Run setup with `umask 022`, and run every `sudo -u ddf -H …` step from `cd ~ddf`.
2. **Boot-time starter:** a system LaunchDaemon with `UserName=ddf` that runs `colima start --foreground` with the VM size, `vz` and `virtiofs`, `RunAtLoad=true` and **no `KeepAlive`** (launchd does not restart the VM, and the job must not race `ddfd`'s own restart).
3. **Docker access:** `ddfd` uses the `ddf` user's colima Docker socket through an absolute `DOCKER_HOST`.
4. **VM firewall:** applied by `ddfd` through `colima ssh` after every VM start it observes and before every container start, then the reachability probe; colima provision hooks are optional and never the guarantee.
5. **Recovery:** before every container start `ddfd` checks colima and Docker status, rule presence and the probe; it restarts the VM with `colima start` as `ddf`; global pause only after that fails.

Still unverified, closed by M3/M5 probes: start after a host reboot without login (`colima-boot`), launchd respawn when the colima process exits, provision hooks elevating with `sudo -n`, IPv6, and coexistence with a second colima instance (`coexistence`). Until M5 ships `ddf setup`, treat the research recipe as the reference, not as an installer.

## Related

- [security-model.md](security-model.md): trust boundaries, probes, accepted risks.
- [identity.md](../concepts/identity.md): host user, Apps, containers.
- [onboarding.md](../project/onboarding.md): connecting a project after setup.
