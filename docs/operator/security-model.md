# Security model

Purpose: what DDF trusts, what it isolates, the probes that must hold before autonomy, and the risks V1 accepts.

Status: normative

Operational detail of each mechanism lives in [identity.md](../concepts/identity.md) and [policy.md](../concepts/policy.md); this page collects the boundaries an operator must understand before enabling DDF. Rationale: [spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries), [spec §8](../specs/2026-11-03-ddf-v1-design.md#8-identity-secrets-and-isolation).

## Trust boundaries

From [spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries):

- Core never imports adapters; adapters never import each other; the daemon wires them. A CI test fails on a forbidden import.
- Trusted code = daemon + adapters on the host. Agents run only inside containers, receive a brief and a workspace copy, and return files in `/out`. They never hold forge credentials or the review-signing key.
- **The daemon runs only from a maintainer-promoted ref** of this repository (the production branch or a release tag), never from the integration branch. Autonomous merges into DDF's integration branch therefore take effect only after a human promotion.

From [spec §8](../specs/2026-11-03-ddf-v1-design.md#8-identity-secrets-and-isolation):

- **Host** (§8.1): A dedicated non-admin OS user `ddf` with a private home (mode 700). `ddfd` runs from launchd (macOS) or systemd (Linux) as `ddf`, from a maintainer-promoted DDF ref (§4.4). The operator's own home directory and personal forge login are not accessible.
- **Forge identity** (§8.2): The token broker mints an installation token per operation, narrowed to one repository and the permissions that operation needs (lifetime ≤ 1 h). Tokens never enter containers. The missing `workflows` permission makes GitHub reject pushes that touch workflow files. App permissions: [forge-github.md](../adapters/forge-github.md#app-permissions).
- **Workspaces** (§8.3): Daemon clones live in `~ddf/work/<forge>/<owner>/<repo>`, used only by trusted code, with hooks disabled (`core.hooksPath` to an empty directory, no fsmonitor, no filter drivers). Each run gets a fresh copy (its own `.git`, no remotes, no credentials) that exists only inside the container.
- **Publication** (§8.3): After the Executor finishes, the adapter's trusted wrapper (not the agent) runs `git add -A && git diff --cached --binary <base>` in the container and writes `/out/change.patch`. The Publisher applies it to the daemon's clean clone with hooks and config neutralised, refuses symlinks, paths outside the repository and any path under `.git/`, and evaluates path policy on the applied tree.
- **The host never executes repository content.** Verify, the local gate and `forge-local` CI always run in fresh containers. The local gate runs on the applied tree **before** the commit is pushed, so a failing gate produces no forge write.
- **Containers** (§8.4): One per run; base image from `images/` or the project's `image`; non-root user; default limits 3 GB RAM, 2 CPU, pids limit; no Docker socket. **No access to host services.** On colima the VM's gateway to the host loopback is dropped by firewall rules inside the VM, re-applied by the daemon on every VM start or recreation; containers reach only the internet. A probe verifies this at daemon start and in §14.1.
- **Agent configuration** (§7.3): agents start with DDF's rendered role bundle as the only settings source; workspace settings, hooks, MCP servers and plugins are disabled ([routing.md](../concepts/routing.md#agent-adapter-contract)).
- **CI prerequisites** (§8.5): A project may be set to `merge-integration` only if its change-triggered CI workflows run with read-only repository permissions and no deployment or production secrets, and any self-hosted CI runner holds no secrets beyond what tests need ([onboarding.md](../project/onboarding.md#ci-prerequisites-for-autonomy)).

## Authority

- Human merge is the default (`propose`). Autonomous merge is limited to the integration branch, per project, and needs both the project request and the operator maximum ([policy.md](../concepts/policy.md#autonomy-levels)). No level promotes to production.
- The merge gate is fail-closed and pinned to the evaluated head; reviews are HMAC-signed with a key only the daemon holds ([policy.md](../concepts/policy.md#merge-gate)).
- Hard stops and path policy always go to the owner, and such a change is never merged autonomously, even after an owner answer ([policy.md](../concepts/policy.md#hard-stops)).
- Intake and owner answers are accepted only from allow-listed actors ([config-reference.md](config-reference.md#project)).

## Secrets

| Secret | Location | Reaches containers |
|---|---|---|
| GitHub App private keys | `~ddf/.config/ddf/secrets/`, mode 0600 | never |
| Review-signing key | same | never |
| Telegram bot token | same | never |
| Agent CLI tokens and provider keys | same | yes (accepted risk below) |

Run logs are written with secrets redacted by pattern. The journal records the App, permission set and token expiry per action, never the token.

## Kill switch

- `ddf pause` or Telegram `/pause` stops new runs.
- Uninstalling the GitHub Apps revokes forge access immediately.
- Credential failures and a second DDF-attributed red integration CI within 24 h trigger a global pause.

## Probes

From [spec §14.1](../specs/2026-11-03-ddf-v1-design.md#141-v1-acceptance). Probes, each must hold:

1. a forged or replayed review marker is rejected;
2. a reviewer cannot be made to approve through workspace agent config (planted hooks/settings are ignored);
3. a change touching a forbidden path or a symlink is not published;
4. planted Git hooks/config in the Executor copy never run on the host;
5. an intake label or answer from a non-allow-listed account is ignored;
6. a container cannot reach host loopback services;
7. a daemon restart mid-run produces no duplicate task or change;
8. a head pushed between gate evaluation and merge makes the merge fail;
9. a Claude rate limit falls back to the opencode entry.

## Accepted risks

From [spec §15](../specs/2026-11-03-ddf-v1-design.md#15-accepted-risks-v1):

- Agent CLI tokens and provider keys are visible inside containers.
- Network egress from containers is not filtered.
- The host is a workstation: availability depends on it being on.
- Private repositories on plans without rulesets have no forge-side protection; there the merge gate is the only enforcement.
- Project CI executes agent-written code; mitigated only by the prerequisites in §8.5.

## Recommended forge settings

On repositories that support rulesets, DDF recommends: pull request required, required checks, one approval, no bypass actors. Reviewer count by risk class stays enforced by the gate ([spec §8.6](../specs/2026-11-03-ddf-v1-design.md#86-audit-kill-switch-rulesets)).
