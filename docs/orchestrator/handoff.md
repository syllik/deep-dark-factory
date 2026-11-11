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
| `Decided` | one line per decision taken since the last handoff, or carried over from it while its task is open: the decision and a link to where it is recorded (for example the `.ai/decisions.md` entry, a planner result comment or a PR comment); no reasoning restated. A decision that exists only in the session chat is recorded first, then linked |
| `Failed` | one line per approach tried and dropped since the last handoff, or carried over from it while its task is open: task or PR link, the approach, the reason it was dropped, and a link to its trace (PR, commit, review or comment) when one exists; not a narrative of the attempt |
| `Next` | the next concrete step per task, in order; first line is what the next session does first |
| `Worktrees` | branch of each live worktree and whether it may be deleted (only when its PR is `MERGED`) |

Write `none` in an empty section; never drop a section.

## Context budget

An interactive session re-sends its whole context on every turn. Prompt caching makes the re-read cheaper, not free, and a cache miss after a pause rewrites the whole context at full price. A long session therefore pays more per turn for the same work and loses detail. Rotation keeps sessions short: the handoff carries the state, not the history.

- **Context size** is the sum of input, cache-read and cache-write tokens of the session's latest main-conversation model turn (subagent turns do not count). The session reads it from a deterministic source ([Claude Code hook](../adapters/agent-claude-code.md#interactive-sessions)), never from its own estimate.
- **Checkpoint:** a PR reaches `merge`, a task ends, or the session starts waiting on the owner.
- **Baseline:** the context size at the session's first measured turn: the system prompt, tools and instructions that a new session pays for again anyway.
- **Working context:** the current context size minus the baseline.
- **Room:** the model's context window as the harness reports it, or the operator cap if it is lower, minus the baseline.
- **Operator cap** (optional): an absolute token count for cost, absent by default and set only in the operator's own settings. When set, the effective window is min(window, cap), and the shares below apply to the room under it.
- **Limits:** soft and hard are operator-chosen shares of the room. A limit is reached when working context ≥ share × room. There are no fixed token numbers.

| Limit | Example share of the room | Action |
|---|---|---|
| soft | 0.6 | advice: at the next checkpoint, rotate (post a handoff; the work continues in a new session started from it) when the remaining work is long or a pause will outlast the prompt cache |
| hard | 0.85, then again every 0.1 of the room past it | start no new step; finish the current one, then only the closing actions: post the result, post the handoff, answer the owner's questions about the result; then stop |

- The shares in the table are an example, not a default the rules depend on; the operator chooses them. How a harness measures the context size, the window and the baseline: [Claude Code](../adapters/agent-claude-code.md#interactive-sessions).
- A task whose required reading exceeds the budget is split in its brief before it starts.
- Auto-compaction is not a substitute: it keeps the session going with a summary the session did not write.
- Daemon runs are out of scope: each run is a fresh process with a bounded brief ([agent-claude-code.md](../adapters/agent-claude-code.md#contract)).

## Starting from a handoff

A new session reads `AGENTS.md`, the mode doc ([manual-mode.md](manual-mode.md) or [supervisor-mode.md](supervisor-mode.md)), then the newest handoff comment on the tracking Issue. Before acting it re-checks each `In flight` line against the forge (head SHA, checks, reviews); the forge wins over the handoff. It does not reopen a `Decided` item or retry a `Failed` approach unless a new fact (a forge change or an owner message) contradicts it.
