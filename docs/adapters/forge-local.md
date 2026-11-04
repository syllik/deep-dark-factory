# forge-local adapter

Purpose: how DDF runs a project on a plain Git remote with no forge: tasks, changes, reviews, CI and merge kept in the DDF store.

Status: implemented in M2

`forge-local` is used for fully offline operation and as the forge of DDF's own end-to-end tests. Rationale: [spec §10.1](../specs/2026-11-03-ddf-v1-design.md#101-forge-local), port contract in [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters).

## Contract

`forge-local` implements the same forge port as [forge-github](forge-github.md#forge-port-contract): list candidate tasks, read intake actor, create task, find change by task key, publish change, update change body, update branch from base, read checks, rerun checks, post review, read answers, merge pinned to a head SHA, revert, close/reopen task, set projection labels. Every write supports dry-run.

## Mapping

| DDF concept | forge-local |
|---|---|
| remote | any Git URL: `file://` bare repository or `ssh://` (`remote` in [projects.yaml](../operator/config-reference.md#project)) |
| task and request | `ddf ask` or task files; stored in the DDF store |
| intake actor / allow-list | the local operator |
| change | a branch on the remote |
| review and findings | rows in the store (signed like any review) |
| checks / CI | `verify` from the project profile, run by the daemon in a fresh container |
| merge | the merge gate with a compare-and-swap ref update pinned to the evaluated head |
| projection labels | store state only; shown by `ddf status` / `ddf show` |

## Rules

- The host never runs repository content: CI is the profile's `verify` in a fresh container, as are the local gate and the Executor's verify ([identity.md](../concepts/identity.md#workspaces-and-publication)).
- The compare-and-swap ref update expects the integration branch's old SHA and checks that the change head still equals the evaluated head. A moved integration branch → refusal `behind base` → sync; a moved change head → `ci` for the new head.
- The same lifecycle, bounds and policy apply as on any forge ([lifecycle.md](../concepts/lifecycle.md), [policy.md](../concepts/policy.md)); hard stops and path policy still go to the owner.
- Owner answers come through `ddf answer` or the configured Telegram user.
