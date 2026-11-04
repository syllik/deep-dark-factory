# Supervisor mode

Purpose: how an interactive session operates a running DDF factory through the `ddf` CLI: submitting requests, relaying owner answers and unblocking tasks.

Status: implemented in M4

The supervisor session is an operator console, not a worker ([spec §11](../specs/2026-11-03-ddf-v1-design.md#11-documentation-set-the-orchestrator-canon)). The daemon is the orchestrator; the supervisor never replaces any role.

## Rules

- Use **only** these `ddf` commands: `status`, `show`, `ask`, `answer`, `pause`, `resume` ([spec §4.3](../specs/2026-11-03-ddf-v1-design.md#43-entry-points), [spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery)).
- **Never write code**, never edit a repository, never commit, push, open or merge a change, never post forge reviews. Code reaches a repository only through DDF runs.
- Never edit operator config, project profiles, secrets or the store by hand.
- `ddf answer` carries the **owner's** decision. The session may draft an answer, but sends it only with the owner's explicit approval of that text. On a hard stop or path policy, an answer resumes the task but never authorizes an autonomous merge ([policy.md#hard-stops](../concepts/policy.md#hard-stops)).
- Prefer `ddf status` / `ddf show` over reading forge labels: the store is canonical, labels are a projection ([lifecycle.md#conveyor](../concepts/lifecycle.md#conveyor)).
- When unsure, `ddf pause` is always safe; ask the owner before `ddf resume`.

## Commands

| Command | Use |
|---|---|
| `ddf status` | conveyor overview: tasks per [state](../concepts/lifecycle.md#states), flags, global pause, rate-limit cooling, container-limit misfit |
| `ddf show <task>` | one task: state, runs, change, head, reviews, findings, open question |
| `ddf ask <project> "<text>"` | submit a free-form request; the Planner decomposes it ([lifecycle.md#intake](../concepts/lifecycle.md#intake)) |
| `ddf answer <task> "<text>"` | answer a `needs-owner` question with the owner's decision |
| `ddf pause` | stop new runs globally (kill switch) |
| `ddf resume` | lift a global pause after the cause is fixed |

## Session loop

1. `ddf status`. Note every `needs-owner` task, global pause and cooling entry.
2. For each `needs-owner` task: `ddf show <task>`, identify the category ([below](#unblocking-needs-owner)), summarize it to the owner in one line with the option(s).
3. Relay the owner's decision with `ddf answer`, or leave the task flagged.
4. Submit new requests the owner gives with `ddf ask`.
5. End with a [handoff](handoff.md) comment.

## Unblocking `needs-owner`

Every exceeded [bound](../concepts/lifecycle.md#bounds), [hard stop](../concepts/policy.md#hard-stops) and some [merge refusals](../concepts/lifecycle.md#merge-refusals) land here.

| Category | Source | How to unblock |
|---|---|---|
| Planner `ASK` | planning | relay the owner's answer to the question; the task returns to `planning` |
| Planner `REJECT` | planning | owner either rewrites the task (forge edit by the owner) and answers "replan", or closes the task |
| Too many children | `DECOMPOSE` > `planning.maxAutoChildren` | owner approves the plan as is, asks for a smaller split, or closes the request |
| Executor `BLOCKED` | running | relay the missing fact or decision; if it needs a forbidden-path or settings change, the owner makes it on the integration branch first |
| Hard stop | policy, reviewer checklist | owner decides; the change continues at `propose` (human merge) whatever the answer |
| Path policy | publishing, merge gate | owner makes the forbidden-path change themselves or rejects the task; the change continues at `propose` |
| Base changed workflow files | sync with base | owner updates the change branch from the base by hand, then answers "resume"; the new head gets a full re-review ([lifecycle.md#sync-with-base](../concepts/lifecycle.md#sync-with-base)) |
| Same finding blocking twice | fixing | owner rules on the finding: accept the "won't fix", or give direction for one more batch |
| Bound exceeded (timeouts, invalid results, CI-fix, correction, sync, run budget) | any | owner chooses: retry with guidance, split the task, or close it |
| CI wait over 2 h | ci | owner checks the project's CI health; answer "retry" once CI runs again |
| Flake quarantine proposal | ci | owner accepts (and creates the quarantine task themselves) or rejects; never auto-executed |
| Unreconcilable forge intent | recovery | owner inspects the forge object named in `ddf show` and answers "adopt" or "discard"; never a blind retry |

Not `needs-owner`, but reported:

| Situation | Action |
|---|---|
| Global pause (a set of reasons: `owner`, `credential`, `revert-red`, `runtime`, `security`; [lifecycle.md#global-pause](../concepts/lifecycle.md#global-pause)) | report every reason; `runtime` lifts automatically once the runtime is healthy again; for `owner`, resume when the owner says so; for `credential`, `revert-red` and `security` the owner fixes the cause first; `ddf resume` only with the owner's approval |
| Rate limit, no routing entry left | tasks wait; nothing to do until the reset time |
| Free disk below floor | report; owner frees space |
| Change set to draft or closed by a human (`hold`) | report; the owner reopens or closes the task |

## Boundaries

- Telegram `/status`, `/pause`, `/resume` are equivalent channels; answers via Telegram come only from the configured user ID ([spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery)).
- The supervisor never selects models or tiers; routing is operator config ([routing.md](../concepts/routing.md)).
- To work on a task by hand instead, stop supervising and follow [manual-mode.md](manual-mode.md) on a project at `propose`.
