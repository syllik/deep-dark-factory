# forge-local adapter

Purpose: how DDF runs a project on a plain Git remote with no forge: tasks, changes, review projections, CI and merge, with forge-local's own storage outside the DDF store.

Status: implemented in M2

`forge-local` is used for fully offline operation and as the forge of DDF's own end-to-end tests. Rationale: [spec §10.1](../specs/2026-11-03-ddf-v1-design.md#101-forge-local), port contract in [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters).

## Contract

`forge-local` implements the same forge port as [forge-github](forge-github.md#forge-port-contract): list candidate tasks, read intake actor, create task, find change by task key, publish change, update change body, update branch from base, read checks, rerun checks, post review, read answers, merge pinned to a head SHA, revert, close/reopen task, set projection labels. Every write supports dry-run.

## Mapping

| DDF concept | forge-local |
|---|---|
| remote | any Git URL: `file://` bare repository or `ssh://` (`remote` in [projects.yaml](../operator/config-reference.md#project)) |
| task and request | `ddf ask` or [task files](#task-files) in `inbox/`; kept as numbered [task records](#records) in forge-local's own [storage](#storage), never in the DDF store; [task source](#task-source) |
| intake actor / allow-list | the local operator |
| change | a branch on the remote plus a [change record](#records) carrying the [task-key marker](#task-key-marker) |
| review and findings | signed reviews live in the DDF store, as on any forge; post review writes a projection into forge-local's own storage |
| checks / CI | one check `verify` per head: the project profile's `verify`, run by the daemon in a fresh container ([checks](#checks)) |
| merge | the merge gate with a compare-and-swap ref update pinned to the evaluated head ([merge](#merge)) |
| projection labels | DDF-owned labels in the task record; shown by `ddf status` / `ddf show` ([other writes](#other-writes)) |

## Rules

- forge-local keeps its own storage: plain files in a directory of its own, never the DDF store and never through the `store` package, which no adapter imports ([spec §10.1](../specs/2026-11-03-ddf-v1-design.md#101-forge-local)). Location, format and layout: [Storage](#storage).
- The host never runs repository content: CI is the profile's `verify` in a fresh container, as are the local gate and the Executor's verify ([identity.md](../concepts/identity.md#workspaces-and-publication)).
- The compare-and-swap ref update expects the integration branch's old SHA and checks that the change head still equals the evaluated head. A moved integration branch → refusal `behind base` → sync; a moved change head → `ci` for the new head.
- The same lifecycle, bounds and policy apply as on any forge ([lifecycle.md](../concepts/lifecycle.md), [policy.md](../concepts/policy.md)); hard stops and path policy still go to the owner.
- Owner answers come through `ddf answer` or the configured Telegram user.

## Storage

- Location: `<data>/forge-local/<project id>/` in the `data` mount (macOS colima profile: `~ddf/work/forge-local/<project id>/`), one directory per project. It is never inside the remote, a daemon clone, a run copy or a container.
- Permissions: owned by `ddf`; directories mode 0700, files mode 0600.
- Format marker: a file `format` holding `ddf.forge-local/v1`. A missing `format` file in a non-empty directory, or any other value, makes the adapter refuse to start (fail-closed). An empty directory is initialised with the marker.
- The daemon alone writes task, change, review and check records and the counter; it runs as a single instance under its instance lock, so task numbers come from one counter. `ddf ask` and `ddf answer` only add new, uniquely named files to `inbox/` and `answers/<n>/` (atomic writes, below) and never change an existing file.

```text
<data>/forge-local/<project id>/
  format                                    # ddf.forge-local/v1
  counter                                   # last allocated task number
  inbox/                                    # operator task files, ddf ask requests
    rejected/                               # invalid files, each with a reason file
  tasks/<n>.json                            # task and request records
  answers/<n>/<unique>.json                 # ddf answer files
  changes/<id>.json                         # change records
  reviews/<change id>/<head sha>/<reviewer id>.json   # review projections
  checks/<head sha>/<attempt>.json          # check attempts
```

### Records

- Each record file holds one JSON object with `"schema": "ddf.forge-local.<kind>/v1"`. A record with an unknown field or another schema is refused.
- Atomic writes: a temp file in the same directory, fsync, rename. Stray temp files are removed on start.

| Record | Fields |
|---|---|
| task (`tasks/<n>.json`) | number; kind `task` \| `request`; title; body; state `open` \| `closed`; labels; parent key; task-key marker when DDF created it; created by; source file name and content digest (inbox ones); comments |
| answer (`answers/<n>/<unique>.json`) | body; author; time |
| change (`changes/<id>.json`) | id; kind `change` \| `revert`; branch; base branch; head SHA; title; body; task-key marker; state `open` \| `merged` \| `closed`; merge SHA; `revertOf` |
| check attempt (`checks/<head sha>/<attempt>.json`) | name `verify`; head SHA; attempt number; status `running` \| `passed` \| `failed` \| `lost`; run ID; start and finish times |
| review projection (`reviews/…/<reviewer id>.json`) | change id; head SHA; reviewer; verdict; body |

## Task source

A forge-local task source ([spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)) is: forge `local`, repository = the project `id`, source kind `task` or `request`, source id = the decimal task number.

## Task files

- **Task file:** the operator drops a UTF-8 Markdown file named `<name>.task.md` into `inbox/`. The first line is `# <title>`; the rest is the body. Size bound: 64 KiB.
- **`ddf ask`:** writes `<unique>.request.md` into `inbox/` with the same format; the title is the first line of the text, truncated.
- **Ingestion:** only the daemon ingests inbox files. It writes the numbered task record with the source file name and content digest, then deletes the file.
- **Crash safety:** on start, an inbox file whose name and digest already have a record is deleted, never ingested twice.
- **Rejection:** an invalid file (bad encoding, no title line, too large, unknown suffix) moves to `inbox/rejected/` with a reason file, and the operator is notified.
- **Intake actor:** the local operator for inbox files: only `ddf` or root can write the 0700 directory, so a file's presence is the operator's intake mark. For tasks DDF creates (children, follow-ups) the intake actor is the daemon.
- **`ddf answer`:** writes a uniquely named answer file under `answers/<n>/` with author = local operator. Read answers returns these files.
- List candidate tasks returns the open task records.

## Task-key marker

- Task records DDF creates and every change record carry `ddf:task-key=<key>` ([spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)).
- Find change by task key reads change records only: the open one, else the most recent. It never reads branch names, commit messages or refs. Nothing marker-related is written into Git.
- Create task and publish change are idempotent per key: intent reconciliation adopts an existing record instead of creating a second ([lifecycle.md](../concepts/lifecycle.md#store-and-intents)).

## Checks

- One check named `verify` per head: the project's required check on forge-local ([spec §5.2](../specs/2026-11-03-ddf-v1-design.md#52-states-and-transitions), CI).
- Publish change, update branch from base and merge (for the merge commit on the integration branch, which the revert rule reads) start a check attempt.
- An attempt runs the profile's `verify` in a fresh container through the Sandbox port, on that head's tree passed in as content ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)), never on the host.
- **Read checks** returns the latest attempt for the head: no attempt → an empty list (checks missing); `running` → not completed.
- **Rerun checks** starts a new attempt and keeps the old ones. The one-automatic-rerun-per-head bound and flake recording stay in the lifecycle ([lifecycle.md](../concepts/lifecycle.md#ci)).
- On daemon start, a `running` attempt with no live run cell is marked `lost` and replaced by a new attempt. A lost attempt is not a rerun.
- Verify output goes to run logs, redacted; never to the storage.

## Merge

- The compare-and-swap rule under [Rules](#rules) applies.
- The squash commit has the change head's tree and the integration tip as its only parent; its message is the change title and body as stored.
- The push carries a compare-and-swap lease on the integration branch's old SHA, after checking that the change head equals the evaluated head. A moved head → `ErrHeadMoved`.
- Merge returns the merge commit SHA, records the change as `merged` and starts a check attempt for the merge commit.

## Revert

- Revert ([spec §5.2](../specs/2026-11-03-ddf-v1-design.md#52-states-and-transitions), Revert; [lifecycle.md](../concepts/lifecycle.md#revert)) runs `git revert` of the DDF merge commit on the integration tip, in the daemon's clean clone with hooks and config neutralised ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)).
- The result is pushed to a new branch named by the profile's `change.branch` pattern with `{type}` = `revert` and slug `revert-<first 12 hex of the merge SHA>` ([project-yaml-reference.md](../project/project-yaml-reference.md#change)).
- It is recorded as a change of kind `revert` with the same task-key marker and `revertOf`, then checked, reviewed (class `revert`, R1) and gated like any change.
- A revert that does not apply cleanly pushes nothing and returns an error; the daemon routes it to `needs-owner`.
- Reopening the task is the lifecycle's separate reopen task call.

## Other writes

- **Create task:** allocates the next number and writes a task record with the marker; idempotent per key ([Task-key marker](#task-key-marker)).
- **Publish change:** creates or updates the change record for the key with the pushed head, and starts a check attempt.
- **Update change body:** rewrites the body in the change record.
- **Update branch from base:** a daemon merge commit of the base in the clean clone, pushed with a lease on the old head; returns the new head and starts a check attempt. The sync rules stay in the lifecycle ([lifecycle.md](../concepts/lifecycle.md#sync-with-base)).
- **Post review:** writes the review projection file; the signed review stays in the DDF store.
- **Close task / reopen task:** set the record state and append the daemon's comment.
- **Set projection labels:** replaces the DDF-owned labels in the task record, shown by `ddf status` / `ddf show`.
- **Dry-run:** every write is logged and changes neither the storage nor the remote; reads work normally.

## SSH credential

- A `file://` remote needs no credential. On the Linux container profile the bare repository's path must be reachable inside the control-plane container, through a mount the operator adds.
- An `ssh://` remote uses one dedicated SSH deploy key per project, with write access to that repository only, plus a pinned known_hosts for its host. Both live in the `secrets` mount and are read through `secrets-file` ([setup.md](../operator/setup.md#secrets-layout), [security-model.md](../operator/security-model.md#secrets)).
- Only the daemon's git on the host uses them: identities only from that key, `StrictHostKeyChecking=yes` against the pinned known_hosts, batch mode (no prompts), no ssh-agent, no `~/.ssh` and no user ssh config.
- The key never enters a container or a run copy and is never passed as a command argument or environment value; it reaches ssh only as a file readable by `ddf` alone. Plan Task 10 picks the mechanism within these rules.
