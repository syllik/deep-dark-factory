# Roles

Purpose: what each DDF role does, which tier and tools it gets, what it outputs, and where its prompt lives.

Status: normative

Rationale and full design: [spec §7.1](../specs/2026-11-03-ddf-v1-design.md#71-roles-and-default-tiers) and decision D4 in [spec §2](../specs/2026-11-03-ddf-v1-design.md#2-maintainer-decisions-2026-11-03). Roles name tiers (`frontier|deep|standard|routine|fast`), never models; tiers map to models only in operator routing ([routing.md](routing.md)).

Agent roles run only inside containers, receive a brief and a workspace copy, write `/out/result.json` validated against their role schema, and never hold forge credentials or the review-signing key. Trusted roles are daemon code on the host. The daemon is the orchestrator.

| Role | Kind | Tier | Tools | Output |
|---|---|---|---|---|
| [Planner](#planner) | agent | `standard`; `deep` for `sensitive` | read-only | verdict + spec + acceptance + risk proposal + zones + deps |
| [Executor](#executor) | agent | `standard` | full inside the container | `COMPLETE` / `BLOCKED` + summary + verify evidence + patch |
| [Reviewer-correctness](#reviewer-correctness) (R1) | agent | `standard` | read/search only, no command execution | findings, verdict |
| [Reviewer-security](#reviewer-security) (R2) | agent | `standard`; `deep` for `sensitive` | read/search only, no command execution | findings + hard-stop checklist, verdict |
| [Publisher](#publisher) | trusted code | — | host, daemon clone | commit, push, change |
| [Merge-gate](#merge-gate) | trusted code | — | host, store | `MERGE` or an enumerated refusal |
| Helpers (subagents) | agent | `fast` for search/read; never write code | read-only | — |
| Reports, digests | agent | `routine` | none | — |

Tier rules:

- `frontier` is never selected by DDF.
- Projects may only raise tiers or set `reviewers.r2DifferentProvider`.
- Subagents per run: at most 3 by default (advisory, see [routing.md](routing.md#limits)).

## Planner

- Prompt: [`prompts/planner.md`](../../prompts/planner.md).
- Input: the task text and a bounded brief built from the integration branch.
- Verdicts: `READY`, `DECOMPOSE`, `ASK`, `REJECT`; transitions in [lifecycle.md](lifecycle.md#planning).
- May raise the risk class; never lowers it ([policy.md](policy.md#risk-classes)).
- May propose `AGENT-DEFAULT` decisions when the spec is ambiguous but product intent is clear.
- Writes the spec in English regardless of the task's language.
- Bounds: 30 min timeout, 1 retry on invalid result ([lifecycle.md](lifecycle.md#bounds)).

## Executor

- Prompt: [`prompts/executor.md`](../../prompts/executor.md).
- Runs in a container on a fresh workspace copy (no remotes, no credentials); may use subagents.
- Runs the profile's verify inside the container and reports the evidence.
- Never commits or pushes: a trusted wrapper exports `/out/change.patch` ([identity.md](identity.md#workspaces-and-publication)).
- Never touches [forbidden paths](policy.md#forbidden-paths).
- Writes `AGENT-DEFAULT` decisions only when the brief lists them.
- In correction batches, addresses all blocking findings or answers "won't fix + reason".
- Bounds: 3 h timeout, 1 retry on invalid result; CI-fix, correction and sync batches as in [lifecycle.md](lifecycle.md#bounds).

## Reviewer-correctness

- Prompt: [`prompts/reviewer-correctness.md`](../../prompts/reviewer-correctness.md).
- Role id `r1`; required for every risk class ([policy.md](policy.md#risk-classes)).
- Judges the diff, the spec, the acceptance criteria and the CI results; does not execute repository code or any command.
- Runs in a fresh read-only container, with no repository-controlled agent configuration and no executor transcript.
- May raise the risk class to `standard` or `sensitive` (adds R2).
- When R2 is not in the set, also runs the hard-stop checklist.
- Output: findings ([policy.md](policy.md#findings)) and `APPROVE` / `REQUEST_CHANGES`.
- Bounds: 1 h timeout, 1 retry on invalid result.

## Reviewer-security

- Prompt: [`prompts/reviewer-security.md`](../../prompts/reviewer-security.md).
- Role id `r2`; required for `standard` and `sensitive` (at `deep`), or when R1 raises the class.
- Focus: security, data, hard stops; always returns the hard-stop checklist.
- Same isolation as R1: read/search only, no command execution, fresh read-only container.
- With `reviewers.r2DifferentProvider: true`, runs on a different provider than the Executor.
- Output: findings with `securityLevel`, hard-stop checklist, `APPROVE` / `REQUEST_CHANGES`.
- Bounds: 1 h timeout, 1 retry on invalid result.

## Publisher

Trusted code, not an agent. Steps in [lifecycle.md](lifecycle.md#publishing):

- applies the patch to the daemon's clean clone with hooks and config neutralised;
- refuses symlinks, paths outside the repository and any path under `.git/`;
- evaluates path policy and risk class on the applied tree;
- runs the local gate in a fresh container before any push;
- one commit and one push per revision; no force-push, rebase or amend;
- writes the change body and squash message from a template; agent text only in a sanitized section;
- refuses a patch that adds a `.opencode/`, `.claude/` or (unless the profile allows it) `node_modules/` path: failed run;
- redacts all agent-originated text (review projection comments, change body, commit message, patch content, Telegram messages) by secret patterns and by the exact values of the secrets loaded into that run's container before every forge write and notification; a patch containing a loaded secret value is refused: hard stop `secrets`, failed run ([lifecycle.md](lifecycle.md#publishing)).

Rationale: [spec §5.2](../specs/2026-11-03-ddf-v1-design.md#52-states-and-transitions), [spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication), [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers).

## Merge-gate

Trusted code, not an agent.

- Runs `evaluateMerge` with the conditions in [policy.md](policy.md#merge-gate).
- Verifies signed reviews from the store; forge comments are only a projection.
- Merges only when effective autonomy is `merge-integration`, squash, pinned to the evaluated head SHA.
- Every refusal maps to a transition in [lifecycle.md](lifecycle.md#merge-refusals).
- At `propose`, the merge is left to a human.

Rationale: [spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate).
