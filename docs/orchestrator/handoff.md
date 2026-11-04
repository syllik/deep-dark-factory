# Handoff

Purpose: how a session hands work to the next session, so a fresh session can continue from forge state alone.

Status: normative

Handoffs **replace dated local handoff files**. State lives in the forge (Issues, PRs, review comments) and, once the daemon runs, in `ddf status`; a handoff only points at it ([spec §11](../specs/2026-11-03-ddf-v1-design.md#11-documentation-set-the-orchestrator-canon)).

## Rules

- A handoff is **one comment on the tracking Issue**, filled from [`templates/handoff-comment.md`](../../templates/handoff-comment.md).
- Tracking Issue: the task Issue for a single-task session; for a multi-task session, the Issue the owner designated (for a milestone, its tracking Issue).
- The newest handoff comment supersedes earlier ones. Never edit an old handoff; post a new one.
- No local files: no `handoff-<date>.md`, no notes in the worktree, no state in the session transcript only.
- Post a handoff whenever the session stops: at the end of work, at `needs-owner`, at "ready for human merge", or before the context grows too long.
- Facts only, each with a link: Issue/PR numbers, full head SHAs, states from [lifecycle.md#states](../concepts/lifecycle.md#states). No transcript retelling, no reasoning history.
- Public repositories: no hostnames, absolute local paths, IPs, tokens or private repository names. Worktrees are named by branch only. Local temp directories (for example a reviewer-input directory) are never named; for work in flight the comment names the PR, the head SHA and the state only.

## Sections

| Section | Contents |
|---|---|
| `Done` | merged PRs and closed Issues since the last handoff |
| `In flight (PR, head SHA, state)` | one line per open PR: PR link, full head SHA, lifecycle state (`ci`, `review`, `fixing`, `merge`), reviews present for that head, correction batches used of 3 |
| `Waiting on owner` | each `needs-owner` item: category and the one-line question, with a link to the comment that asks it; PRs ready for human merge |
| `Next` | the next concrete step per task, in order; first line is what the next session does first |
| `Worktrees` | branch of each live worktree and whether it may be deleted (only when its PR is `MERGED`) |

Write `none` in an empty section; never drop a section.

## Starting from a handoff

A new session reads `AGENTS.md`, the mode doc ([manual-mode.md](manual-mode.md) or [supervisor-mode.md](supervisor-mode.md)), then the newest handoff comment on the tracking Issue. Before acting it re-checks each `In flight` line against the forge (head SHA, checks, reviews); the forge wins over the handoff.
