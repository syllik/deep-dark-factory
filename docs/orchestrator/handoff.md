# Handoff

Purpose: how a session hands work to the next session, so a fresh session can continue from forge state alone.

Status: normative

Handoffs **replace dated local handoff files**. State lives in the forge (Issues, PRs, review comments) and, once the daemon runs, in `ddf status`; a handoff only points at it ([spec §11](../specs/2026-11-03-ddf-v1-design.md#11-documentation-set-the-orchestrator-canon)).

## Rules

- A handoff is **one comment on the tracking Issue**, filled from [`templates/handoff-comment.md`](../../templates/handoff-comment.md).
- Tracking Issue: the task Issue for a single-task session; for a multi-task session, the Issue the owner designated (for a milestone, its tracking Issue).
- The newest handoff comment supersedes earlier ones. Never edit an old handoff; post a new one.
- No local files: no `handoff-<date>.md`, no notes in the worktree, no state in the session transcript only.
- Post a handoff whenever the session stops: at the end of work, at `needs-owner`, at "ready for human merge", while waiting for an owner action, or when the session reaches its [context budget](#context-budget). An owner action is a step the owner must take that is not `needs-owner`: a reviewer session the owner must start (PR, full head SHA, roles) or a comment text handed to the owner to post (target and heading); see [manual-mode.md#when-the-harness-refuses-an-action](manual-mode.md#when-the-harness-refuses-an-action).
- Facts only, each with a link: Issue/PR numbers, full head SHAs, states from [lifecycle.md#states](../concepts/lifecycle.md#states). No transcript retelling, no reasoning history.
- Public repositories: no hostnames, absolute local paths, IPs, tokens or private repository names. Worktrees are named by branch only. Local temp directories (for example a reviewer-input directory) are never named; for work in flight the comment names the PR, the head SHA and the state only.

## Sections

| Section | Contents |
|---|---|
| `Done` | merged PRs and closed Issues since the last handoff |
| `In flight (PR, head SHA, state)` | one line per open PR: PR link, full head SHA, lifecycle state (`ci`, `review`, `fixing`, `merge`), reviews present for that head, correction batches used of 3 |
| `Waiting on owner` | each `needs-owner` item: category and the one-line question, with a link to the comment that asks it; each owner action that is not `needs-owner`: reviewer sessions to start (PR, full head SHA, roles) and comment texts to post (target and heading); PRs ready for human merge |
| `Next` | the next concrete step per task, in order; first line is what the next session does first |
| `Worktrees` | branch of each live worktree and whether it may be deleted (only when its PR is `MERGED`) |

Write `none` in an empty section; never drop a section.

## Context budget

An interactive session re-sends its whole context on every turn. Prompt caching makes the re-read cheaper, not free, and a cache miss after a pause rewrites the whole context at full price. A long session therefore pays more per turn for the same work and loses detail. Rotation keeps sessions short: the handoff carries the state, not the history.

- **Context size** is the sum of input, cache-read and cache-write tokens of the session's latest main-conversation model turn (subagent turns do not count). The session reads it from a deterministic source ([Claude Code hook](../adapters/agent-claude-code.md#interactive-sessions)), never from its own estimate.
- **Checkpoint:** a PR reaches `merge`, a task ends, or the session starts waiting on the owner.

| Limit | Default | Action |
|---|---|---|
| soft | 120000 tokens | at the next checkpoint: post a handoff; the work continues in a new session started from it |
| hard | 160000 tokens | finish the current step, post a handoff, stop |

- Past the soft limit, before a pause expected to outlast the prompt cache, post a handoff first.
- The limits are operator-chosen; the defaults assume a 200000-token context window. Auto-compaction is not a substitute: it keeps the session going with a summary the session did not write.
- Daemon runs are out of scope: each run is a fresh process with a bounded brief ([agent-claude-code.md](../adapters/agent-claude-code.md#contract)).

## Starting from a handoff

A new session reads `AGENTS.md`, the mode doc ([manual-mode.md](manual-mode.md) or [supervisor-mode.md](supervisor-mode.md)), then the newest handoff comment on the tracking Issue. Before acting it re-checks each `In flight` line against the forge (head SHA, checks, reviews); the forge wins over the handoff.
