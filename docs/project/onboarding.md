# Project onboarding

Purpose: how a repository becomes a DDF project: the `ddf init` flow, the files it adds, the CI prerequisites for autonomy, and cleanup of old routing blocks.

Status: implemented in M4

`ddf init <repo>` ships with the M4 CLI. Until then, do the same steps by hand: copy `templates/project/` and open the change yourself. Rationale: [spec §3](../specs/2026-11-03-ddf-v1-design.md#3-configuration-layers), [spec §8.5](../specs/2026-11-03-ddf-v1-design.md#85-ci-prerequisites-for-autonomous-projects), [spec §13](../specs/2026-11-03-ddf-v1-design.md#13-migration).

## Before you start

- The operator has DDF installed ([setup.md](../operator/setup.md)).
- The operator adds the project to `projects.yaml` with `maxAutonomy: propose` and the allow-listed humans ([config-reference.md](../operator/config-reference.md#project)).
- GitHub projects: the three DDF Apps are installed on the repository ([forge-github.md](../adapters/forge-github.md)). Plain Git projects: a `file://` or `ssh://` remote ([forge-local.md](../adapters/forge-local.md)).
- The repository has an integration branch (dev/staging). DDF never merges anywhere else.

## `ddf init` flow

1. **Read the repository** from its integration branch: languages, existing CI workflows, existing `AGENTS.md`/`CLAUDE.md`.
2. **Propose the profile**: render `.ddf/project.yaml` from [`templates/project/.ddf/project.yaml`](../../templates/project/.ddf/project.yaml) with the integration branch, verify commands and required check names filled in. `autonomy` starts at `propose`.
3. **Add the change template**: [`templates/project/.ddf/change-template.md`](../../templates/project/.ddf/change-template.md) as `.ddf/change-template.md` (referenced by `change.template`).
4. **Add issue forms** (GitHub): [`ddf-task.yml`](../../templates/project/.github/ISSUE_TEMPLATE/ddf-task.yml), [`ddf-request.yml`](../../templates/project/.github/ISSUE_TEMPLATE/ddf-request.yml), [`ddf-bug.yml`](../../templates/project/.github/ISSUE_TEMPLATE/ddf-bug.yml) into `.github/ISSUE_TEMPLATE/`. No form applies the `ddf` label; the request form applies only `ddf:request`, which is inert until an allow-listed actor adds `ddf` ([lifecycle.md](../concepts/lifecycle.md#intake)).
5. **Point `AGENTS.md` at the profile**: keep the project rules and add the line from [`templates/project/AGENTS.md`](../../templates/project/AGENTS.md): "This project is run by DDF; see `.ddf/project.yaml`." Create the file from the skeleton if the project has none.
6. **Check CI prerequisites** (below): record what can be checked automatically, list the rest as an onboarding checklist in the change body.
7. **Open one change** with all of the above. Every file in it is on the forbidden-path list ([policy.md](../concepts/policy.md#forbidden-paths)), so a human always reviews and merges it.

After the merge the daemon reads the profile from the integration branch on its next tick. The profile is never read from a change branch.

## Profile essentials

Every key with type, default and example: [project-yaml-reference.md](project-yaml-reference.md). The ones to set on day one:

- `integrationBranch`, `verify` (commands run inside containers only), `checks.required` (exact forge check names).
- `risk.docs`, `risk.sensitive`, `ciEntrypoints` (scripts and manifests CI relies on).
- `forbidden` and `hardStops` for anything project-specific on top of the product defaults.

## CI prerequisites for autonomy

Required before the project's `autonomy` is raised to `merge-integration` and the operator raises `maxAutonomy` ([spec §8.5](../specs/2026-11-03-ddf-v1-design.md#85-ci-prerequisites-for-autonomous-projects)). `ddf init` checks what it can; the owner confirms the rest.

- [ ] Change-triggered CI workflows (pull request and push to agent branches) run with read-only repository permissions.
- [ ] Change-triggered CI workflows have no access to deployment or production secrets.
- [ ] Any self-hosted CI runner used by change-triggered workflows holds no secrets beyond what tests need.
- [ ] Every check in `checks.required` exists and runs on every change head.
- [ ] Scripts and manifests CI depends on are listed in `ciEntrypoints`, so changes to them get the `sensitive` class.
- [ ] Where the forge supports rulesets: pull request required, required checks, one approval, no bypass actors ([security-model.md](../operator/security-model.md#recommended-forge-settings)).

Under decision D1, V1 enables `merge-integration` only for DDF itself and one private pilot product; every other project stays at `propose` (human merge).

## Removing old routing blocks

Repositories that were managed by the old personal agent-workflow repository carry a managed routing block in `AGENTS.md` (and sometimes `CLAUDE.md`). During onboarding:

1. Delete the managed routing block and its markers entirely.
2. Keep the project's own rules in `AGENTS.md`.
3. Add the single line pointing to `.ddf/project.yaml` (step 5 above).
4. If `CLAUDE.md` only included the routing block, reduce it to a reference to `AGENTS.md`.

These edits touch forbidden paths and are part of the human-merged onboarding change ([spec §13](../specs/2026-11-03-ddf-v1-design.md#13-migration)).

## After onboarding

- Mark tasks for DDF with the `ddf` label or a `/ddf` comment as an allow-listed actor, or use `ddf ask <project> "<text>"`.
- Without the daemon, run tasks by [manual-mode.md](../orchestrator/manual-mode.md).
- When the integration branch changes CI configuration (GitHub: `.github/workflows/**`), DDF cannot bring it into an open change branch: such changes go to `needs-owner` and the owner updates the branch by hand; the new head is fully re-reviewed ([lifecycle.md](../concepts/lifecycle.md#sync-with-base)).