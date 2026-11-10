# Identity, secrets and isolation

Purpose: who DDF acts as, where credentials live, and how agent code is kept away from the host and from forge credentials.

Status: normative

Rationale and full design: [spec §8](../specs/2026-11-03-ddf-v1-design.md#8-identity-secrets-and-isolation) and trust boundaries in [spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries).

## Trust boundary

- Trusted code = daemon + adapters on the host.
- Agents run only inside containers, receive a brief and a workspace copy, and return files in `/out`. They never hold forge credentials or the review-signing key.
- The daemon runs only from a maintainer-promoted ref of DDF: a release tag (a maintainer-made tag on a `master` commit that passed CI), never the moving tip of the integration branch.

## Host

Two runtime profiles; both run `ddfd` from a maintainer-promoted DDF ref and give run containers the same isolation properties ([Containers](#containers)).

- **Linux container profile** (reference deployment from V1): a Linux host; the control plane (`ddfd`, the store, the secrets) runs in a container; a dedicated non-admin host user `ddf` owns its mounts: `config` (read-only), `secrets` (read-only) and `data` (store, clones, run logs) ([config-layers.md](config-layers.md#layers)). The control-plane container holds the container runtime's socket to start run containers; on a rootful runtime that socket is host-equivalent, which is why rootful or rootless is open until the probe spike decides it. Everything here holds for both.
- **macOS colima profile** (second runtime profile): a dedicated non-admin OS user `ddf` with a private home (mode 700); `ddfd` runs from launchd as `ddf`, not in a container. launchd supervises `ddfd`, not the container VM: `ddfd` checks VM health and restarts the VM itself; a launchd colima job is only a boot-time starter (`RunAtLoad`, no `KeepAlive`, so it never races `ddfd`'s own restart).
- The operator's own home directory and personal forge login are not accessible.
- Secrets live in the `secrets` mount (on the macOS colima profile `~ddf/.config/ddf/secrets/` mode 0600); never in Git ([config-layers.md](config-layers.md)).
- A 1–2 GB host orchestrates; heavy verify runs in forge CI. A project's self-hosted CI runner is project CI, which DDF only reads as required checks; DDF itself needs no runner, and such a runner is ephemeral, isolated and never runs as the control-plane host user.

Rationale: [spec §8.1](../specs/2026-11-03-ddf-v1-design.md#81-host).

## GitHub Apps

Three Apps created by `ddf apps create` (GitHub App manifest flow), owned by the operator, installed only on selected repositories:

| App | Permissions | Not granted |
|---|---|---|
| `worker` | contents: write, pull_requests: write, issues: write, checks: read, statuses: read, actions: write (rerun), metadata: read | administration, workflows |
| `review-correctness` | contents: read, pull_requests: write, metadata: read | contents write |
| `review-security` | same as `review-correctness` | contents write |

Rules:

1. The token broker mints an installation token per operation, narrowed to one repository and the permissions that operation needs (lifetime ≤ 1 h).
2. Tokens never enter containers.
3. The missing `workflows` permission makes GitHub reject pushes that touch workflow files. A sync that would bring base workflow-file changes into the branch is therefore never attempted: the daemon detects it deterministically and the task goes to `needs-owner` ([lifecycle.md](lifecycle.md#sync-with-base)).
4. If GitHub does not count App approvals, the fallback is machine users ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0) item 1: unverified, closed by the M4 probe `app-approval-counts`).

Rationale: [spec §8.2](../specs/2026-11-03-ddf-v1-design.md#82-github-identity).

## Workspaces and publication

1. Daemon clones live in `~ddf/work/<forge>/<owner>/<repo>`, used only by trusted code, with hooks disabled (`core.hooksPath` to an empty directory, no fsmonitor, no filter drivers).
2. Each run gets a fresh copy (its own `.git`, no remotes, no credentials) that exists only inside the container.
3. After the Executor finishes, the adapter's trusted wrapper (not the agent) runs `git add -A && git diff --cached --binary <base>` in the container and writes `/out/change.patch`.
4. The [Publisher](roles.md#publisher) applies it to the daemon's clean clone with hooks and config neutralised, refuses symlinks, paths outside the repository and any path under `.git/`, and evaluates path policy on the applied tree.
5. The host never executes repository content. Verify, the local gate and `forge-local` CI always run in fresh containers. The local gate runs on the applied tree before the commit is pushed, so a failing gate produces no forge write. The host never runs an agent CLI against a repository copy, not even `opencode models` or `opencode debug`: opencode loads project plugins on every invocation.
6. The Publisher refuses a patch that adds a `.opencode/` or `.claude/` path (already forbidden by §6.5) or any `node_modules/` path unless the project profile allows it: failed run, with the reason in the journal.

Rationale: [spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication).

## Containers

- One per run; base image from `images/` or the project's `image`; fresh, empty home; default limits 3 GB RAM, 2 CPU, pids limit.
- **Isolation properties.** Every run container has: no route to the host, the control plane or private address ranges (DNS excepted); all capabilities dropped; no new privileges; a non-root user; pids and memory limits; no container runtime socket. One probe set checks them ([security-model.md](../operator/security-model.md#probes)); each runtime profile says how it provides them and must pass the same probes. Before every container start the daemon checks the isolation rules and runs the probes; on failure it re-applies the rules once and re-probes; still failing → container runtime unavailable (§9). Rules absent or unverifiable → pause `runtime`; rules changed without a runtime restart → pause `security`.
- **Linux container profile:** the runtime's per-container options and network rules; details decided by the probe spike together with rootful or rootless.
- **macOS colima profile:** colima firewall rules, applied via `colima ssh`, are this profile's way to provide "no route". On colima, firewall rules inside the VM drop container traffic to the VM's gateway to the host loopback and to the VM itself, except DNS; containers reach only the internet. The rules do not survive a VM restart. The daemon applies them (`colima ssh -- sudo …`) after every VM start or recreation; colima provision hooks may add an early layer but are never the guarantee. Before every container start the daemon checks that the rules exist and runs the host-reachability probe; on failure it re-applies the rules once and re-probes; still failing → container runtime unavailable (§9). Rules found missing while no VM restart was observed (VM boot ID unchanged since the daemon last applied them) are a tamper signal: global pause with reason `security` (§9), no automatic lift. If the VM is down, `ddfd` restarts it as `ddf` (`colima start`, bounded attempts), then applies and probes the rules. The probe also holds in §14.1.
- Services (databases, browsers) run inside the container.
- Network egress to the internet is open in V1 (model APIs, package registries) — accepted risk ([spec §15](../specs/2026-11-03-ddf-v1-design.md#15-accepted-risks-v1)).
- Each container receives only the credential of its routed adapter.
- **Redaction before publication.** Before every forge write and every notification send, all agent-originated text (review projection comments, change body, commit message, patch content, Telegram messages) is redacted by secret patterns and by the exact values of the secrets loaded into that run's container. A patch containing a loaded secret value is refused: hard stop `secrets` (§6.4), failed run.

Pause reasons and their lift rules: [lifecycle.md](lifecycle.md#global-pause).

Rationale: [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers).

## CI prerequisites for autonomy

A project may be set to `merge-integration` only if:

- its change-triggered CI workflows run with read-only repository permissions and no deployment or production secrets;
- any self-hosted CI runner holds no secrets beyond what tests need.

`ddf init` checks what it can and records the rest as an onboarding checklist item. Rationale: [spec §8.5](../specs/2026-11-03-ddf-v1-design.md#85-ci-prerequisites-for-autonomous-projects).

## Audit and kill switch

- The journal records per action: role, task, App and permission set and token expiry (never the token), commits, model, usage.
- `ddf pause` or Telegram `/pause` stops new runs (pause reason `owner`); uninstalling the Apps revokes forge access immediately.
- On repositories that support rulesets, DDF recommends: pull request required, required checks, one approval, no bypass actors. Reviewer count by risk class stays enforced by the gate.

Rationale: [spec §8.6](../specs/2026-11-03-ddf-v1-design.md#86-audit-kill-switch-rulesets).
