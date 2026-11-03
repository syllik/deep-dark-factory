# Manual mode

Purpose: the procedure an interactive LLM session follows to run one DDF task without the daemon, from intake to a PR ready for a human merge.

Status: normative

> **In manual mode a human merges. The session never merges, force-pushes or edits forbidden paths.**
> The session stops at "ready for merge" and hands the PR to the owner. It never approves its own PR, never enables auto-merge, never pushes to the integration or production branch directly.

Manual mode keeps the same lifecycle, roles, bounds and policy as the daemon ([spec §11](../specs/2026-11-03-ddf-v1-design.md#11-documentation-set-the-orchestrator-canon)); only the actors change. DDF itself is built this way until M5. This file is self-sufficient: a session that has read only `AGENTS.md` and this file can run a task. Open the linked concepts when a step points at them.

## Who does what

| Daemon role | In manual mode |
|---|---|
| Orchestrator | this session ("the session") |
| [Planner](../concepts/roles.md#planner) | the session itself, or one subagent given [`prompts/planner.md`](../../prompts/planner.md) |
| [Executor](../concepts/roles.md#executor) | subagents with exact briefs ([below](#executor-brief)); at most 3 in parallel, disjoint files |
| [Publisher](../concepts/roles.md#publisher) | the session: local verify, path check, one commit and one push per revision |
| [R1](../concepts/roles.md#reviewer-correctness) / [R2](../concepts/roles.md#reviewer-security) | **separate interactive sessions** (not subagents), read-only, same head |
| [Merge-gate](../concepts/roles.md#merge-gate) | **the human owner** |

Tiers: executor subagents run at `standard`, search/read helpers at `fast` (they never write code), reviewers at `standard` (`deep` for R2 on `sensitive`). `frontier` is never selected. Roles name tiers, never models ([routing.md](../concepts/routing.md#tiers)).

## Procedure

Each step names the [lifecycle state](../concepts/lifecycle.md#states) it stands for. Any step that hits a bound, a hard stop or a doubt goes to [needs-owner](#what-goes-to-the-owner).

### 1. Intake check (`inbox`)

Proceed only if all hold; otherwise stop and ask the owner:

- the task Issue was opened, or marked for DDF (label `ddf` or comment `/ddf`), by an allow-listed human (the owner), or the owner named it in this session's brief;
- it carries none of `deferred*`, `needs-owner`, `ddf:hold`; a bare `ddf:request` is inert until the owner accepts it;
- it is not already in flight (no open PR that closes it; check the latest [handoff](handoff.md) comment).

Rules: [lifecycle.md#intake](../concepts/lifecycle.md#intake).

### 2. Planner pass (`planning`)

Run the Planner either yourself or as one read-only subagent whose brief is: the Issue text, `AGENTS.md`, the project profile (`.ddf/project.yaml` if present) and [`prompts/planner.md`](../../prompts/planner.md). In manual mode there is no `/out`: the result is a single JSON object, returned as one fenced `json` block, valid against [`schemas/planner.result.json`](../../schemas/planner.result.json) (`"schema": "ddf.planner.result/v1"`).

Validate it before using it: it parses, has `schema` and `verdict`, uses only the enums and fields the schema allows. Invalid → one retry, then `needs-owner`.

**Record the pass:** post one comment on the task Issue headed `DDF planner result` containing the JSON block. That comment is the spec, acceptance criteria and risk proposal the rest of the task is held to.

| Verdict | Next |
|---|---|
| `READY` | step 3 |
| `DECOMPOSE` | ≤ `planning.maxAutoChildren` (default 3) children: create one child Issue each (English spec), run them one by one; more → `needs-owner` with the plan |
| `ASK` / `REJECT` | `needs-owner` with the question or reason |

`agentDefaults` in the result are the only `AGENT-DEFAULT` decisions the executor may write ([lifecycle.md#planning](../concepts/lifecycle.md#planning)).

### 3. Branch and worktree (`ready` → `running`)

- Check dependencies (`dependsOn`) and zones: if a dependency is open or another in-flight task holds an overlapping zone, the task waits ([lifecycle.md#ready-and-waiting](../concepts/lifecycle.md#ready-and-waiting)); say so in the handoff and stop.
- Create a worktree on a new branch from the up-to-date integration branch. DDF: `slave/<feature>` from `master`. Other projects: `change.branch` from their profile.

### 4. Executor subagents (`running`)

Split the work into briefs with **disjoint file lists**; run at most 3 in parallel. Each subagent gets exactly the [brief below](#executor-brief) plus [`prompts/executor.md`](../../prompts/executor.md). Subagents return a JSON object valid against [`schemas/executor.result.json`](../../schemas/executor.result.json) as one fenced `json` block (no `/out` in manual mode).

- `COMPLETE` → review the subagent's diff yourself against its file list; files outside the list → reject and redo.
- `BLOCKED` → retry once with a corrected brief if the cause is the brief; otherwise `needs-owner`.

### 5. Local verify and publish (`publishing`)

1. Run the profile's verify commands (DDF: the commands in the task's brief and `node scripts/docs-check.mjs` once it exists). Red → back to step 4 as a correction batch.
2. Path check: no file under [forbidden paths](../concepts/policy.md#forbidden-paths), no symlink, nothing under `.git/`. Hit → stop, `needs-owner`.
3. Compute the [risk class](../concepts/policy.md#risk-classes) from the diff and the profile; take the higher of it and the Planner's proposal. Never lower it.
4. **One commit and one push per revision**, made by the session. No force-push, rebase or amend. No `[skip ci]` or similar directives.
5. Open the PR to the integration branch with the body from [`templates/project/.ddf/change-template.md`](../../templates/project/.ddf/change-template.md) (or the project's own `.ddf/change-template.md`): `Task:` line, summary, verify evidence, sanitized agent notes. Add `Closes #<task>` for this task only. State the risk class in the summary.

### 6. Wait for checks (`ci`)

Wait for every check in the profile's `checks.required` on the current head (no list: at least one check, all green). GitHub example: `gh pr checks <pr> --watch`. One rerun of a failed check per head; pass-on-rerun is a flake, note it. Red → CI-fix attempt (step 4 brief scoped to the failure), at most 2 per revision. Waiting more than 2 h → `needs-owner`.

### 7. Reviews (`review`)

Reviewers are **separate interactive sessions**, never subagents of this session: no shared transcript, no executor output beyond the PR. All reviewers of a round review the **same head SHA**, which must be green.

| Risk class | Reviewer sessions |
|---|---|
| `docs`, `low`, `revert` | R1 (also runs the hard-stop checklist) |
| `standard` | R1 + R2 |
| `sensitive` | R1 + R2, R2 at `deep` |

If R1 returns `raiseRisk`, add R2 for the same head. Start each session (or, if this session cannot start sessions, give the owner the text to paste) with exactly:

```text
You are DDF reviewer <R1|R2> in manual mode.
Read and follow <prompts/reviewer-correctness.md | prompts/reviewer-security.md> in this repository.
Task: Issue #<task>. Change: PR #<pr>, head <full SHA>, risk class <class>.
Planner result: the "DDF planner result" comment on the Issue.
Review only that head. Use read/search tools only; do not run any command, test or script.
There is no /out directory: return one fenced json block valid against
schemas/reviewer.result.json ("schema": "ddf.reviewer.result/v1", "role": "<r1|r2>").
```

Collect each result, validate it against [`schemas/reviewer.result.json`](../../schemas/reviewer.result.json) (invalid → one retry, then `needs-owner`) and post it on the PR as one comment headed `DDF review <r1|r2> @ <head SHA>`. A review counts only for the head it names.

Routing of findings ([policy.md#findings](../concepts/policy.md#findings), [lifecycle.md#review](../concepts/lifecycle.md#review)):

- blocking (P0, P1, security High/Medium, category `hard-stop`) → step 8;
- `hard-stop` category or checklist `hit` → `needs-owner`, never "fixed and merged" without the owner;
- P2 → one follow-up Issue per PR (label `ddf:followup`); P3 → append to the project's standing minors Issue.

### 8. Correction batches (`fixing`)

One batch = one executor brief addressing **all** blocking findings (the executor may answer "won't fix + reason" in `wontFix`), then step 5 (new commit, push), step 6, and a **full re-review** of the new head by every required reviewer, given the prior findings. At most **3 batches** per PR. The same finding (`role`, `category`, `location`, problem) blocking twice → `needs-owner`.

**Behind base:** merge the integration branch into the PR branch (a merge commit, never a rebase), then step 6. Conflicts → an executor sync batch (at most 2), then full re-review.

### 9. Hand to the human (`merge`)

When every required reviewer approved the current head with zero blocking findings and required checks are green on it, comment on the PR:

```text
Ready for human merge: head <SHA>, risk class <class>, reviews <r1/r2 comment links>, checks green.
```

Then post a [handoff](handoff.md) and **stop. The owner merges** (squash, pinned to that head). A head that moves after this comment voids the reviews: back to step 6.

### 10. Post-merge cleanup (`done`)

Only after the PR state is `MERGED`: delete the worktree, the local branch and the remote branch; confirm the Issue closed. A PR closed without merge, or converted to draft → `hold`: leave the branch, note it in the handoff.

## Executor brief

Every executor subagent brief contains exactly these sections, nothing else:

1. **Goal:** one sentence, from the Planner's `spec`.
2. **Files:** the exact list of files to create or modify. Nothing outside it; disjoint from other running briefs.
3. **Done criteria:** the acceptance items this brief covers, checkable by reading the diff or running verify.
4. **Verify commands:** the exact commands to run before returning, and their expected result.
5. **Context:** paths to read (spec sections, concept anchors, neighbouring files); no history retelling.
6. **Decisions:** `AGENT-DEFAULT` entries it may write, or "none".
7. **Rules:** never commit, push, open PRs or touch the forge; never edit [forbidden paths](../concepts/policy.md#forbidden-paths); no secrets; English; stop and return `BLOCKED` instead of guessing.
8. **Output:** one fenced `json` block valid against `schemas/executor.result.json`.

In a correction batch add: the findings JSON to address, and the head SHA they were raised on.

## Bounds

Same bounds as the daemon ([lifecycle.md#bounds](../concepts/lifecycle.md#bounds)); in manual mode timeouts are wall-clock guidance for the session.

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

## What goes to the owner

`needs-owner` in manual mode: post one comment on the task Issue with the category and a one-line question, add the `needs-owner` label if the repository has it, post a [handoff](handoff.md), and stop that task.

- **Hard stops** ([policy.md#hard-stops](../concepts/policy.md#hard-stops)): secrets and credentials, deploy and infrastructure, real money, destructive data operations, forge or repository settings, other repositories, purchases, plus the project's `hardStops`.
- **Path policy:** any needed change to a [forbidden path](../concepts/policy.md#forbidden-paths) or a symlink. The owner makes it or explicitly authorizes it.
- **Gate refusals that map to `needs-owner`** ([lifecycle.md#merge-refusals](../concepts/lifecycle.md#merge-refusals)): path policy or hard stop.
- Planner `ASK`, `REJECT`, or more children than `maxAutoChildren`.
- Executor `BLOCKED` that a better brief cannot fix.
- Any [bound](#bounds) exceeded; the same finding blocking twice; a flaky test seen in two changes (quarantine proposal, never auto-executed).
- A PR closed or set to draft by a human, or the task closed: do nothing further on it.

An owner answer resumes the task. It never turns this session into the merger.

## When in doubt

Stop and ask the owner. In particular, never:

- merge, approve your own PR, enable auto-merge, or push to `master`/`dungeon-master` directly;
- force-push, rebase, amend or rewrite published history;
- edit forbidden paths, repository settings, rulesets, secrets or other repositories;
- lower a risk class, skip a required reviewer, reuse a review from an older head, or review in this session's own context;
- exceed a bound "just once", or guess an answer the task does not give;
- delete a worktree or branch whose PR is not `MERGED`.
