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
| Run logs | `~ddf/runs/<task>/<run>/`, kept 30 days, secrets redacted by pattern |

The operator's own home directory and personal forge login are not accessible to the `ddf` user.

## Steps (target order)

1. **Create the service user.** A non-admin account named `ddf` with a home directory readable only by itself. The operator's personal account keeps its own forge login; DDF never uses it.
2. **Install the container runtime** for the `ddf` user (colima on macOS, Docker Engine on Linux). Size the VM so that `limits.maxConcurrentContainers` × the container limits (default 3 GB RAM, 2 CPU each) fit; `ddf status` reports a misfit ([routing.md](../concepts/routing.md#limits)).
3. **Check out DDF** at a promoted ref into the `ddf` home. Updating the daemon is always a human action: promote, then move the checkout.
4. **Scaffold the operator config** with `ddf setup`. It creates `routing.yaml` and `projects.yaml` in the chosen private repository or folder. That repository must never be connected as a DDF project ([config-layers.md](../concepts/config-layers.md#rules)).
5. **Create the GitHub Apps** (GitHub projects only) with `ddf apps create`: three Apps, owned by the operator, installed only on selected repositories ([forge-github](../adapters/forge-github.md)).
6. **Place secrets** in `~ddf/.config/ddf/secrets/` (see [Secrets layout](#secrets-layout)).
7. **Register the daemon service** (launchd job on macOS, systemd unit on Linux) running `ddfd` as `ddf`, restarted automatically after host sleep or crash.
8. **First start in dry-run** (`ddfd --dry-run`): every forge write is logged, not performed. Check reads and intended writes before dropping `--dry-run`.
9. **Run the isolation probes** listed in [security-model.md](security-model.md#probes).

## Service manager

- **macOS:** a launchd job for the `ddf` user. launchd restarts the daemon after host sleep; expired run leases are released on start ([spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery)).
- **Linux:** a systemd unit with `User=ddf`.
- On start the daemon takes the instance lock, kills containers from a previous instance, discards their runs (retried within budget) and reconciles unresolved forge intents by task-key marker.

## Container runtime

- colima runs a VM per user; the daemon re-applies the VM firewall rules that drop the gateway to the host loopback on every VM start or recreation ([sandbox-docker](../adapters/sandbox-docker.md)).
- Container runtime unavailable → global pause and a notification.
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
| Agent CLI tokens and provider keys | agent adapters | yes, accepted risk ([security-model.md](security-model.md#accepted-risks)) |

Exact file names are fixed by `ddf setup` in M5.

## M0 recipe

The verified, step-by-step commands for this host setup (colima under a non-login service user via launchd, resource limits, VM firewall, coexistence with other users' services) are pending `docs/research/2026-11-m0-facts.md` ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 5). Until those facts are recorded, follow only the target order above and do not treat any command as verified.

## Related

- [security-model.md](security-model.md): trust boundaries, probes, accepted risks.
- [identity.md](../concepts/identity.md): host user, Apps, containers.
- [onboarding.md](../project/onboarding.md): connecting a project after setup.
