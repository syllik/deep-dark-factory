# Security model

Purpose: what DDF trusts, what it isolates, the probes that must hold before autonomy, and the risks V1 accepts.

Status: normative

Operational detail of each mechanism lives in [identity.md](../concepts/identity.md) and [policy.md](../concepts/policy.md); this page collects the boundaries an operator must understand before enabling DDF. Rationale: [spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries), [spec §8](../specs/2026-11-03-ddf-v1-design.md#8-identity-secrets-and-isolation).

## Trust boundaries

From [spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries):

- Core never imports adapters; adapters never import each other; the daemon wires them. A CI test fails on a forbidden import.
- Trusted code = daemon + adapters on the host. Agents run only inside containers, receive a brief and a workspace copy, and return files in `/out`. They never hold forge credentials or the review-signing key.
- **The daemon runs only from a maintainer-promoted ref** of this repository: a release tag (a maintainer-made tag on a `master` commit that passed CI), never the moving tip of the integration branch. Autonomous merges into DDF's integration branch therefore take effect only after a human tags a release.

From [spec §8](../specs/2026-11-03-ddf-v1-design.md#8-identity-secrets-and-isolation):

- **Host** (§8.1): Two runtime profiles. Both run `ddfd` from a maintainer-promoted DDF ref (§4.4) and give run containers the same isolation properties (§8.4). The operator's own home directory and personal forge login are not accessible in either.
  - **Linux container profile (reference deployment from V1):** a Linux host; the control plane (`ddfd`, the store, the secrets) runs in a container; a dedicated non-admin host user `ddf` owns its mounts. The control-plane container has three named mounts: `config` (operator config, read-only), `secrets` (read-only) and `data` (store, clones, run logs). It holds the container runtime's socket to start run containers; on a rootful runtime that socket is host-equivalent. Whether the runtime is rootful or rootless is open and decided by the probe spike; this page holds for both.
  - **macOS colima profile (second runtime profile):** a dedicated non-admin OS user `ddf` with a private home (mode 700); `ddfd` runs from launchd as `ddf`, not in a container. The fixed home-directory locations are the defaults of this non-container profile in place of the mounts: `~ddf/.config/ddf/secrets/` for secrets, `~ddf/work/` and `~ddf/runs/` for data. launchd supervises `ddfd`, not the container VM: `ddfd` checks VM health and restarts the VM itself (§8.4); a launchd colima job is only a boot-time starter (`RunAtLoad`, no `KeepAlive`, so it never races `ddfd`'s own restart).
  - **Weak host:** a host with 1–2 GB of memory orchestrates; heavy verify runs in forge CI. A project's CI may use a self-hosted runner: that is project CI, which DDF only reads as required checks (§8.5); DDF itself needs no runner. Such a runner is ephemeral, isolated and never runs as the control-plane host user.
- **Forge identity** (§8.2): The token broker mints an installation token per operation, narrowed to one repository and the permissions that operation needs (lifetime ≤ 1 h). Tokens never enter containers. The missing `workflows` permission makes GitHub reject pushes that touch workflow files. App permissions: [forge-github.md](../adapters/forge-github.md#app-permissions).
- **Workspaces** (§8.3): Daemon clones live in `~ddf/work/<forge>/<owner>/<repo>`, used only by trusted code, with hooks disabled (`core.hooksPath` to an empty directory, no fsmonitor, no filter drivers). Each run gets a fresh copy (its own `.git`, no remotes, no credentials) that exists only inside the container.
- **Publication** (§8.3): After the Executor finishes, the adapter's trusted wrapper (not the agent) runs `git add -A && git diff --cached --binary <base>` in the container and writes `/out/change.patch`. The Publisher applies it to the daemon's clean clone with hooks and config neutralised, refuses symlinks, paths outside the repository and any path under `.git/`, and evaluates path policy on the applied tree.
- **The host never executes repository content.** Verify, the local gate and `forge-local` CI always run in fresh containers. The local gate runs on the applied tree **before** the commit is pushed, so a failing gate produces no forge write. The host never runs an agent CLI against a repository copy, not even `opencode models` or `opencode debug`: opencode loads project plugins on every invocation.
- The Publisher refuses a patch that adds a `.opencode/` or `.claude/` path (already forbidden by §6.5) or any `node_modules/` path unless the project profile allows it: failed run, with the reason in the journal.
- **Containers** (§8.4): One per run; base image from `images/` or the project's `image`; fresh, empty home; default limits 3 GB RAM, 2 CPU, pids limit.
  - **Isolation properties.** Every run container has: no route to the host, the control plane or private address ranges (DNS excepted); all capabilities dropped; no new privileges; a non-root user; pids and memory limits; no container runtime socket. One probe set checks them ([Probes](#probes)). Each runtime profile says how it provides them and must pass the same probes. Before every container start the daemon checks the isolation rules and runs the probes. Precedence ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)): isolation rules absent or changed while no runtime restart was observed (same runtime instance identifier), even when a re-apply would succeed → pause `security`, no automatic lift; isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure, or the daemon's own runtime restart attempts failed → pause `runtime`, lifted automatically.
  - **Linux container profile:** provides the properties through the runtime's per-container options and network rules; how exactly is decided by the probe spike together with rootful or rootless.
  - **macOS colima profile:** colima firewall rules, applied via `colima ssh`, are this profile's way to provide "no route". On colima, firewall rules inside the VM drop container traffic to the VM's gateway to the host loopback and to the VM itself, except DNS; containers reach only the internet. The rules do not survive a VM restart. The daemon applies them (`colima ssh -- sudo …`) after every VM start or recreation; colima provision hooks may add an early layer but are never the guarantee. Before every container start the daemon checks that the rules exist and runs the host-reachability probe; on failure it follows the §8.4 precedence. Rules found missing while no VM restart was observed (VM boot ID, this profile's runtime instance identifier, unchanged since the daemon last applied them) are a tamper signal: global pause with reason `security` (§9), no automatic lift. If the VM is down, `ddfd` restarts it as `ddf` (`colima start`, bounded attempts), then applies and probes the rules. The probe also holds in [Probes](#probes).
- **Redaction before publication** (§8.4): Before every forge write and every notification send, all agent-originated text (review projection comments, change body, commit message, patch content, Telegram messages) is redacted by secret patterns and by the exact values of the secrets loaded into that run's container. A patch containing a loaded secret value is refused: hard stop `secrets` (§6.4), failed run.
- **Agent configuration** (§7.3): agents start with DDF's rendered role bundle as the only settings source; workspace settings, hooks, MCP servers and plugins are disabled ([routing.md](../concepts/routing.md#agent-adapter-contract)). Residual Claude Code sources these flags do not disable: account-synced remote settings (applied regardless of local flags) and built-in plugins and agents; DDF therefore uses a dedicated account and token, and the M3 probe `claude-config-isolation` fails closed if the remote settings carry hooks, permissions or MCP servers. Each container receives only the credential of its routed adapter.
- **CI prerequisites** (§8.5): A project may be set to `merge-integration` only if its change-triggered CI workflows run with read-only repository permissions and no deployment or production secrets, and any self-hosted CI runner holds no secrets beyond what tests need ([onboarding.md](../project/onboarding.md#ci-prerequisites-for-autonomy)).

## Authority

- Human merge is the default (`propose`). Autonomous merge is limited to the integration branch, per project, and needs both the project request and the operator maximum ([policy.md](../concepts/policy.md#autonomy-levels)). No level promotes to production.
- The merge gate is fail-closed and pinned to the evaluated head; reviews are HMAC-signed with a key only the daemon holds ([policy.md](../concepts/policy.md#merge-gate)).
- Hard stops and path policy always go to the owner, and such a change is never merged autonomously, even after an owner answer ([policy.md](../concepts/policy.md#hard-stops)).
- Intake and owner answers are accepted only from allow-listed actors ([config-reference.md](config-reference.md#project)).

## Secrets

| Secret | Location | Reaches containers |
|---|---|---|
| GitHub App private keys | the read-only `secrets` mount (macOS colima profile: `~ddf/.config/ddf/secrets/`, mode 0600) | never |
| Review-signing key | same | never |
| Telegram bot token | same | never |
| Agent CLI tokens and provider keys | same | yes, only the routed adapter's credential (accepted risk below) |

Run logs are written with secrets redacted by pattern and by loaded secret values (§8.4). The journal records the App, permission set and token expiry per action, never the token.

## Kill switch

- `ddf pause` or Telegram `/pause` stops new runs (pause reason `owner`).
- Uninstalling the GitHub Apps revokes forge access immediately.
- **Global pause** is a set of reasons: `owner` (`ddf pause`, `/pause`), `credential`, `revert-red` (§5.2 Revert), `runtime`, `security`. `security`: isolation rules absent or changed while no runtime restart was observed (same runtime instance identifier), even when a re-apply would succeed. `runtime`: container runtime unavailable — isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure, or the daemon's own runtime restart attempts failed ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)). The daemon is paused while the set is non-empty. Automatic lift clears only `runtime`; every other reason is cleared only by `ddf resume` or `/resume`. Details: [lifecycle.md](../concepts/lifecycle.md#global-pause).

## Probes

From [spec §14.1](../specs/2026-11-03-ddf-v1-design.md#141-v1-acceptance). Probes, each must hold:

1. a forged or replayed review marker is rejected;
2. a reviewer cannot be made to approve through workspace agent config (planted hooks/settings are ignored);
3. a change touching a forbidden path or a symlink is not published;
4. planted Git hooks/config in the Executor copy never run on the host;
5. an intake label or answer from a non-allow-listed account is ignored;
6. a run container has the isolation properties of §8.4, checked by the isolation probes below;
7. a daemon restart mid-run produces no duplicate task or change;
8. a head pushed between gate evaluation and merge makes the merge fail;
9. a Claude rate limit falls back to the opencode entry.

### Isolation probes

One list, one probe per isolation property (§8.4). Every runtime profile must pass all of them before autonomy and before every container start:

- no route to the host: a run container cannot reach services on the host's loopback or addresses;
- no route to the control plane: a run container cannot reach the control-plane container or its services;
- no route to private address ranges: a run container cannot reach addresses in private ranges (DNS excepted, and it still resolves names);
- all capabilities dropped: the container's effective capability set is empty;
- no new privileges: the container runs with `no-new-privileges` and a privilege-gaining exec fails;
- non-root user: the container's process runs with a non-zero uid;
- pids and memory limits: a fork bomb and a memory overrun are stopped at the configured limits;
- no runtime socket: no container runtime socket is present or reachable inside the container;
- no global IPv6 route: a container that gets one fails the probe ([Accepted risks](#accepted-risks)).

## Accepted risks

From [spec §15](../specs/2026-11-03-ddf-v1-design.md#15-accepted-risks-v1):

- Agent CLI tokens and provider keys are visible inside containers (each container holds only its routed adapter's credential). Publishing them is not accepted: §8.4 redaction and secret-value scanning apply.
- Network egress from containers is not filtered.
- IPv6 egress is not filtered in V1; the isolation probe fails if a container gets a global IPv6 route.
- The host is a workstation: availability depends on it being on.
- Private repositories on plans without rulesets have no forge-side protection; there the merge gate is the only enforcement.
- Project CI executes agent-written code; mitigated only by the prerequisites in §8.5.

## Recommended forge settings

On repositories that support rulesets, DDF recommends: pull request required, required checks, one approval, no bypass actors. Reviewer count by risk class stays enforced by the gate ([spec §8.6](../specs/2026-11-03-ddf-v1-design.md#86-audit-kill-switch-rulesets)).
