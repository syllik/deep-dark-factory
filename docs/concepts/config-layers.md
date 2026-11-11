# Configuration layers

Purpose: where each piece of DDF configuration lives, who owns it, and how layers combine.

Status: normative

Rationale and full design: [spec §3](../specs/2026-11-03-ddf-v1-design.md#3-configuration-layers).

## Layers

| Layer | Location | Owner | Contents |
|---|---|---|---|
| Product | this repository | DDF maintainers | defaults, role prompts, schemas, templates |
| Operator | a private repo or folder chosen by the operator (`ddf setup` scaffolds it) | the person running DDF | `routing.yaml`, `projects.yaml` (connected projects, forge, **maximum autonomy**, allow-listed humans), limits, notification settings, App IDs |
| Project | `.ddf/project.yaml` in the target repository, always read from the integration branch | the project | branches, verify commands, required checks, risk rules, sensitive/forbidden paths, hard stops, zones, requested autonomy |
| Secrets | the read-only `secrets` mount of the control-plane container; on the non-container macOS profile `~ddf/.config/ddf/secrets/` on the host (directory 0700, files 0600) | the operator | App private keys, review-signing key, Telegram token, agent CLI tokens and provider keys |

On the Linux container profile (reference deployment) the control-plane container has three named mounts ([identity.md](identity.md#host)):

- `config` (read-only): the operator layer;
- `secrets` (read-only): the secrets layer;
- `data`: the store, daemon clones and run logs.

The fixed home-directory locations (`~ddf/.config/ddf/secrets/`, `~ddf/work/`, `~ddf/runs/`) are the defaults of the non-container macOS profile.

## Rules

1. Policy only tightens across layers. A project can never loosen a product invariant; an operator can never loosen a project's policy.
2. **Effective autonomy = min(operator maximum for that project, project request).** A repository cannot grant itself autonomy ([policy.md](policy.md#autonomy-levels)).
3. Model names appear only in the operator layer ([routing.md](routing.md)).
4. The operator config repository can never be connected as a DDF project.
5. Secrets never enter Git and never enter containers as forge credentials ([identity.md](identity.md)).

## Resolution order

When DDF needs a value:

1. Product defaults (this repository) set the floor: invariants, default forbidden paths, default hard stops, default bounds.
2. The project profile, read from the integration branch copy (never from the change under review), may add paths, hard stops, sensitive globs and required checks, raise tiers, and request an autonomy level.
3. The operator config caps autonomy per project, picks adapters and models per tier, and sets limits and allow-listed humans.
4. Secrets are read by name through the secrets port only when an operation needs them.

## Where to find the details

- Project profile keys: [spec §6.7](../specs/2026-11-03-ddf-v1-design.md#67-project-profile).
- Operator routing: [routing.md](routing.md#operator-routing-file).
- Secrets and host layout: [identity.md](identity.md#host).
