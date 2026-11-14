# Task lifecycle

Purpose: the operational rules for how a DDF task moves from intake to merge, with every bound and every gate refusal.

Status: normative

Rationale and full design: [spec §5](../specs/2026-11-03-ddf-v1-design.md#5-task-lifecycle). Policy referenced here lives in [policy.md](policy.md); roles in [roles.md](roles.md).

## Conveyor

```text
inbox → planning → ready → running → publishing → ci → review → merge → done
                     ↑                              ↑            │
                  waiting                           └─ fixing ←──┘
flags (off the conveyor): needs-owner, hold, failed; global: paused (reasons: owner, credential, revert-red, runtime, security; §9)
```

The store is canonical; forge labels are only a projection ([spec §10.2](../specs/2026-11-03-ddf-v1-design.md#102-forge-github)).

## Intake

1. **Request** (`ddf ask`, or a forge task from the "DDF request" form): the [Planner](roles.md#planner) decomposes it into child tasks.
   - More children than `planning.maxAutoChildren` (default 3) → `needs-owner` with the plan.
   - Otherwise children go to `ready`. The request closes when all children close.
2. **Existing task** marked for DDF (GitHub: label `ddf` or comment `/ddf`) → `planning`.
3. Intake is accepted only from allow-listed actors: the daemon reads who applied the label or wrote the command (GitHub: timeline events) and accepts it only if that login is in the operator allow-list, or is the `worker` App creating a child or follow-up task.
4. Issue forms never auto-apply `ddf`. The "DDF request" form applies `ddf:request`, which is inert until an allow-listed actor adds `ddf`.
5. Tasks with `deferred*`, `needs-owner` or `hold` are never picked.

Rationale: [spec §5.1](../specs/2026-11-03-ddf-v1-design.md#51-intake).

## States

Rationale for every rule below: [spec §5.2](../specs/2026-11-03-ddf-v1-design.md#52-states-and-transitions).

### planning

The [Planner](roles.md#planner) returns one verdict:

| Verdict | Next |
|---|---|
| `READY` (spec, acceptance criteria, risk class proposal, zones, dependencies) | `ready` |
| `DECOMPOSE` | children created (see [Intake](#intake)) |
| `ASK` | `needs-owner` |
| `REJECT` | `needs-owner` with reason |

- Ambiguous spec but clear product intent → the Planner proposes an `AGENT-DEFAULT` decision; the Executor writes it into the project's decision log as part of the change, so it is reviewed.
- Owner-written tasks may be in any language; the Planner writes the spec in English.

### ready and waiting

- A `ready` task whose dependencies are open, or whose zones are held, is `waiting`.
- A zone is held from `running` until the holding task is merged or leaves the conveyor.
- Two tasks overlapping in files outside any declared zone: the second is synced with the base (see [sync](#sync-with-base)) after the first merges.

**Priority order** when capacity frees:

1. tasks already in `fixing` or `merge`;
2. `ci` / `review` work;
3. `ready` tasks by priority (forge label `priority:high|normal|low`, default normal);
4. then by satisfied dependencies;
5. then oldest first;
6. follow-ups last.

### running

The [Executor](roles.md#executor) runs in a container on a workspace copy, may use subagents, runs the profile's verify inside the container, and exports a patch ([identity.md](identity.md#workspaces-and-publication)).

| Verdict | Next |
|---|---|
| `COMPLETE` | `publishing` |
| `BLOCKED` | `needs-owner` |

### publishing

The [Publisher](roles.md#publisher) (trusted code):

1. applies the patch to a clean clone;
2. checks path policy and risk class ([policy.md](policy.md));
3. runs the local gate in a fresh container;
4. makes one commit and one push per revision (no force-push, rebase or amend);
5. opens or updates the change.

<a id="revision"></a>A **revision** is the initial execution or one correction batch, as published by the Publisher (one commit and one push, with CI-fix pushes made within it counted inside it, so "CI-fix attempts per revision" in [Bounds](#bounds) is bounded per revision, not per head).

The Publisher writes the change body and squash message from a template; agent text appears only in a sanitized section (closing keywords, `[skip ci]`-style directives and mentions stripped); `Closes <task>` is added by DDF only for the task itself. Local gate failure → back to the Executor as a correction batch.

- **Redaction before publication.** Before every forge write and every notification send, all agent-originated text (review projection comments, change body, commit message, patch content, Telegram messages) is redacted by secret patterns and by the exact values of the secrets loaded into that run's container. A patch containing a loaded secret value is refused: hard stop `secrets` ([policy.md](policy.md#hard-stops)), failed run.
- **Hard stops and forbidden paths.** The Publisher checks the revision's [changed paths](policy.md#changed-paths); a deletion counts. A revision with an unapproved [hard-stop entry](policy.md#hard-stops) is not published: `needs-owner` before publishing, `hit_stop` set; the owner's answer approves exactly the listed entries, the Publisher publishes that revision and the task continues at `propose`; a later revision whose hard-stop entries are all approved is published without a new question. A revision touching a forbidden path (a `.opencode/` or `.claude/` path at any depth included) or holding an added or changed symlink is never published automatically: `needs-owner`, `hit_stop` set, and after the answer the task returns to `running`. `hit_stop` is sticky ([policy.md](policy.md#hard-stops), [policy.md](policy.md#forbidden-paths)).
- The Publisher refuses a patch that adds any `node_modules/` path unless the project profile allows it: failed run, with the reason in the journal.
- A changed path under `.opencode/` or `.claude/`, at any depth, is a [forbidden path](policy.md#forbidden-paths): the revision is not published, the task goes to `needs-owner` and `hit_stop` is set. Why one outcome: the forbidden-path flow is the stricter one; the owner sees every attempt and sticky `hit_stop` keeps the change at `propose`, while a discarded run would be retried without the owner.

Rationale: [spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication), [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers).

### ci

- Required checks ([policy.md](policy.md#merge-gate) condition 5) run on the current head. For `forge-local` the daemon runs verify in a fresh container as CI.
- One automatic rerun per head; pass-on-rerun is recorded as a flake.
- A test flaking in two changes → `needs-owner` quarantine proposal (never an auto-executed task).
- CI failure → Executor CI-fix attempt (bounded, see [Bounds](#bounds)).

### review

- The reviewer set comes from the risk class ([policy.md](policy.md#risk-classes)).
- Reviewers run in parallel on the same head, each in a fresh container, read-only, with no repository-controlled agent configuration and no executor transcript.
- Any hard-stop checklist item `unassessed` means the review inputs were incomplete: that reviewer is rerun once with complete inputs (this counts as its invalid-result retry, see [Bounds](#bounds)), then `needs-owner`. It never causes a correction batch, and the input-gap `P1` that accompanies it is not routed to `fixing`.
- Blocking findings ([policy.md](policy.md#findings)) → `fixing`.
- P2 → one follow-up task per change (`ddf:followup`, auto-picked at lowest priority, risk class at least the parent's).
- P3 → appended to the project's standing minors task, never executed.
- Findings on a follow-up task never become further follow-ups: they go to minors.

### fixing

- One correction batch = one Executor run addressing all blocking findings, then CI, then a full re-review of the new head with prior findings.
- The Executor may answer a finding "won't fix + reason".
- A finding is identified across rounds by `{role, category, location, normalized problem}`; the same finding confirmed blocking twice → `needs-owner`.

### sync with base

- Behind and clean → the daemon merges the base into the branch (a daemon-authored merge commit).
- Conflict → an Executor sync batch, then full re-review.
- **Workflow files in the base:** before any sync or publish that brings base changes into the branch (the daemon's clean merge, or the push of an Executor sync batch in the conflict case), the daemon checks deterministically whether the base changed CI configuration since the merge base (GitHub: `git diff --name-only <merge-base> <base> -- .github/workflows`; other forges: their CI config paths). If it did, the `worker` App may not write them (GitHub: no `workflows` permission, [identity.md](identity.md#github-apps)), so no forge call is made: → `needs-owner` with the reason "base changed workflow files; update the branch manually", and the sync batch is not consumed.
- A forge refusal of the same kind (GitHub: 403 "… without `workflows` permission") is only a fallback signal and maps to the same outcome.
- A branch update made by a human is not a daemon-created base merge, so the new head goes through full re-review ([policy.md](policy.md#merge-gate) condition 4).

### merge

`evaluateMerge` ([policy.md](policy.md#merge-gate)) passes → squash merge pinned to the reviewed head SHA → DDF closes the task. Refusals: see [Merge refusals](#merge-refusals).

### revert

- If the integration branch's CI is red on the DDF merge commit itself (after one rerun), DDF opens a revert change of risk class `revert` and reopens the task.
- A second DDF-attributed red within 24 h, across the project → global pause (reason `revert-red`, lifted only by `ddf resume` or `/resume`) and Telegram alert.
- Red caused by a human merge is not attributed to DDF.

### owner answers

- Accepted only from allow-listed humans (forge comments) or the configured Telegram user ID.
- An answer resumes a task from `needs-owner` but never authorizes an autonomous merge of a hard-stop or path-policy change ([policy.md](policy.md#hard-stops)).

## Merge refusals

Gate refusals map to transitions ([spec §5.2](../specs/2026-11-03-ddf-v1-design.md#52-states-and-transitions)):

| Refusal | Next |
|---|---|
| behind base | sync |
| checks missing/pending/red | `ci` |
| review missing/stale/not approved | `review` |
| hard stop or path policy on the current head (§6.6 condition 6, for example a commit DDF did not publish) | `needs-owner` once per head (a head already asked about takes the `hit_stop` row), `hit_stop` set; the change stays at `propose` (§6.4) |
| `hit_stop` set (§6.6 condition 2) and no other row applies, including the owner-answered revision and a head already asked about | stay, no new `needs-owner`: the change waits at `propose` for a human to merge or close it (§6.4) |
| flag or global pause | stay |
| change converted to draft or closed by a human | `hold`, notify |
| task closed by a human | leaves the conveyor; open change left for the human |
| mergeable state unknown, forge read error | stay, backoff |
| head moved during merge | `ci` for the new head |

## Global pause

**Global pause** is a set of reasons: `owner` (`ddf pause`, `/pause`), `credential`, `revert-red` (§5.2 Revert), `runtime`, `security`. The daemon is paused while the set is non-empty. Automatic lift clears only `runtime`; every other reason is cleared only by `ddf resume` or `/resume`.

| Reason | Set by | Lifted |
|---|---|---|
| `owner` | `ddf pause`, Telegram `/pause` | `ddf resume` / `/resume` |
| `credential` | agent authentication failure ([routing.md](routing.md#agent-adapter-contract)) | `ddf resume` / `/resume` |
| `revert-red` | second DDF-attributed red within 24 h ([revert](#revert)) | `ddf resume` / `/resume` |
| `runtime` | container runtime unavailable: the daemon's own runtime restart failed, or isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure | automatically, with a notification, once health check, isolation-rule check and probes pass again |
| `security` | isolation rules absent or changed while no runtime restart was observed (same runtime instance identifier), even when a re-apply would succeed ([spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)) | `ddf resume` / `/resume` |

Rationale: [spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery), [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers).

## Store and intents

- **Store:** the canonical state (SQLite), with an append-only event journal. Tables, columns, payloads, SQLite settings and SQL rules: [spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents). Every store row and signature is scoped by the task key, a digest of `{forge, repository, source kind, source id}`.
- **Teardown record:** the column `tasks.teardown_at`, set only when the task's execution cell was destroyed and verified (nothing labelled with the task key remains). An unverifiable teardown is a reported failure (journal and notification), never silent.
- **Intent:** every forge write (create task, push, open/update change, post review, update branch, merge, revert, close/reopen task) writes an intent before the call and its outcome after. Every forge object DDF creates carries a hidden task-key marker (`ddf:task-key=<key>`). Model runs are not intents.
- **Recovery:** on start the daemon takes the instance lock and lists execution resources by label (task key, run ID). Resources whose task is terminal or unknown to the store are destroyed and verified, and the teardown is recorded. Resources of a non-terminal task are, in V1, killed and their run discarded (retried within the run budget); the decision is per task key and run ID from the labels, so re-adopting live runs later changes only this branch. The daemon then reconciles every unresolved intent by task-key marker (for example, a change for the task already exists → adopt it). Unreconcilable intents → `needs-owner`; never a blind retry of a forge write.

V1 has no run leases: the instance lock and recovery by label cover restarts ([spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)).

Rationale: [spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents), [spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery), [spec §4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider).

## Bounds

Rationale: [spec §5.3](../specs/2026-11-03-ddf-v1-design.md#53-bounds).

| Bound | Default |
|---|---|
| Planner run timeout / invalid result | 30 min / 1 retry, then `needs-owner` |
| Executor run timeout / invalid result | 3 h / 1 retry, then `needs-owner` |
| Reviewer run timeout / invalid result | 1 h / 1 retry, then `needs-owner` |
| CI wait for one head | 2 h, then `needs-owner` |
| CI-fix attempts per revision | 2 |
| Correction batches per change | 3 |
| Sync batches per change | 2 |
| Agent runs per task, all roles | 20 (`maxRunsPerTask`) |

Exceeding any bound → `needs-owner`.

A model run interrupted by a daemon restart is discarded and retried within the task's run budget; forge writes are reconciled by intent, never blindly retried ([spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents), [spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery)).
