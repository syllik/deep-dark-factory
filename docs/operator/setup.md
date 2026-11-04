# Operator setup

Purpose: the target installation of DDF on one host by one operator: service user, daemon service, container runtime, operator config and secrets.

Status: implemented in M5

Nothing on this page is installable yet. It describes the target layout that M5 delivers so that every earlier milestone builds toward it. Rationale: [spec §8.1](../specs/2026-11-03-ddf-v1-design.md#81-host), [spec §3](../specs/2026-11-03-ddf-v1-design.md#3-configuration-layers), milestone M5 in [spec §14](../specs/2026-11-03-ddf-v1-design.md#14-milestones).

## Target layout

| Piece | Target |
|---|---|
| Service user | dedicated non-admin OS user `ddf`, private home (mode 700) |
| Daemon | `ddfd`, single instance per host (exclusive file lock), started by launchd (macOS) or systemd (Linux) as `ddf` |
| DDF code | a checkout of a maintainer-promoted ref of DDF (the production branch or a release tag), never the integration branch |
| Container runtime | colima on macOS, Docker on Linux ([sandbox-docker](../adapters/sandbox-docker.md)) |
| Operator config | a private repository or folder chosen by the operator, scaffolded by `ddf setup` ([config-reference.md](config-reference.md)) |
| Secrets | `~ddf/.config/ddf/secrets/`, mode 0600, never in Git |
| Daemon clones | `~ddf/work/<forge>/<owner>/<repo>` |
| Run logs | `~ddf/runs/<task>/<run>/`, kept 30 days, secrets redacted by pattern and by loaded secret values |

The operator's own home directory and personal forge login are not accessible to the `ddf` user.

## Steps (target order)

1. **Create the service user.** A non-admin account named `ddf` with a home directory readable only by itself. The operator's personal account keeps its own forge login; DDF never uses it.
2. **Install the container runtime** for the `ddf` user (colima on macOS, Docker Engine on Linux); on macOS add the boot-time colima starter ([M0 recipe](#m0-recipe)). Size the VM so that `limits.maxConcurrentContainers` × the container limits (default 3 GB RAM, 2 CPU each) fit; `ddf status` reports a misfit ([routing.md](../concepts/routing.md#limits)).
3. **Check out DDF** at a promoted ref into the `ddf` home. Updating the daemon is always a human action: promote, then move the checkout.
4. **Scaffold the operator config** with `ddf setup`. It creates `routing.yaml` and `projects.yaml` in the chosen private repository or folder. That repository must never be connected as a DDF project ([config-layers.md](../concepts/config-layers.md#rules)).
5. **Create the GitHub Apps** (GitHub projects only) with `ddf apps create`: three Apps, owned by the operator, installed only on selected repositories ([forge-github](../adapters/forge-github.md)).
6. **Place secrets** in `~ddf/.config/ddf/secrets/` (see [Secrets layout](#secrets-layout)).
7. **Register the daemon service** (launchd job on macOS, systemd unit on Linux) running `ddfd` as `ddf`, restarted automatically after host sleep or crash.
8. **First start in dry-run** (`ddfd --dry-run`): every forge write is logged, not performed. Check reads and intended writes before dropping `--dry-run`.
9. **Run the isolation probes** listed in [security-model.md](security-model.md#probes).

## Service manager

- **macOS:** a launchd job for the `ddf` user. launchd restarts the daemon (never the VM) after host sleep; expired run leases are released on start ([spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery)).
- launchd supervises `ddfd`, not the container VM: `ddfd` checks VM health and restarts the VM itself. A separate launchd colima job is only a boot-time starter (`RunAtLoad`, no `KeepAlive`, so it never races `ddfd`'s own restart) ([spec §8.1](../specs/2026-11-03-ddf-v1-design.md#81-host)).
- **Linux:** a systemd unit with `User=ddf`.
- On start the daemon takes the instance lock, kills containers from a previous instance, discards their runs (retried within budget) and reconciles unresolved forge intents by task-key marker.

## Container runtime

- colima runs a VM per user. The VM firewall rules (no container access to the host loopback gateway or to the VM itself, except DNS) do not survive a VM restart: the daemon applies them after every VM start, and checks and probes them before every container start ([sandbox-docker](../adapters/sandbox-docker.md#container-rules)).
- If the VM is down, `ddfd` restarts it as `ddf` (`colima start`, bounded attempts). Only when that or the firewall re-apply fails: global pause with reason `runtime` and a notification, lifted automatically once health check, firewall check and probe pass again. Rules missing without an observed VM restart: global pause with reason `security`, no automatic lift ([lifecycle.md](../concepts/lifecycle.md#global-pause)).
- Free disk below the floor (`limits.minFreeDiskGb`, default 15 GB) → no new runs, notification.

## Operator config

```text
<operator-config>/
  routing.yaml     # tiers and roles → agent adapters and models (schema ddf.routing/v1)
  projects.yaml    # connected projects, max autonomy, allow-listed humans, notifications, limits (schema ddf.projects/v1)
```

Every key, type and default: [config-reference.md](config-reference.md). Model names appear only in this layer.

## Secrets layout

All secrets live under `~ddf/.config/ddf/secrets/`, owned by `ddf`, mode 0600, and are read by name through the `secrets-file` adapter. They are never committed and never referenced by value from `routing.yaml` or `projects.yaml`.

| Secret | Used by | Enters containers |
|---|---|---|
| GitHub App private keys (`worker`, `review-correctness`, `review-security`) | token broker on the host | never |
| Review-signing key (HMAC) | daemon only | never |
| Telegram bot token | `notify-telegram` | never |
| Agent CLI tokens and provider keys | agent adapters | yes, only the routed adapter's credential; accepted risk ([security-model.md](security-model.md#accepted-risks)) |

Exact file names are fixed by `ddf setup` in M5.

## M0 recipe

M0 verified colima under a non-login service user via launchd ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 5). The full recipe, evidence and the address-free firewall script: [M0 facts, Recipe for M5](../research/2026-11-m0-facts.md#recipe-for-m5). In short:

1. **Service user:** a hidden, non-admin user `ddf` with its home created explicitly and set to mode 700. Run setup with `umask 022`, and run every `sudo -u ddf -H …` step from `cd ~ddf`.
2. **Boot-time starter:** a system LaunchDaemon with `UserName=ddf` that runs `colima start --foreground` with the VM size, `vz` and `virtiofs`, `RunAtLoad=true` and **no `KeepAlive`** (launchd does not restart the VM, and the job must not race `ddfd`'s own restart).
3. **Docker access:** `ddfd` uses the `ddf` user's colima Docker socket through an absolute `DOCKER_HOST`.
4. **VM firewall:** applied by `ddfd` through `colima ssh` after every VM start it observes and before admitting runs, then the reachability probe; colima provision hooks are optional and never the guarantee.
5. **Recovery:** before each run `ddfd` checks colima and Docker status, rule presence and the probe; it restarts the VM with `colima start` as `ddf`; global pause only after that fails.

Still unverified, closed by M3/M5 probes: start after a host reboot without login (`colima-boot`), launchd respawn when the colima process exits, provision hooks elevating with `sudo -n`, IPv6, and coexistence with a second colima instance (`coexistence`). Until M5 ships `ddf setup`, treat the research recipe as the reference, not as an installer.

## Related

- [security-model.md](security-model.md): trust boundaries, probes, accepted risks.
- [identity.md](../concepts/identity.md): host user, Apps, containers.
- [onboarding.md](../project/onboarding.md): connecting a project after setup.
