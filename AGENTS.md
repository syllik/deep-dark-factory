# Agent Instructions

Entry point for every agent or LLM session working on Deep Dark Factory (DDF).

Status: normative.

## How DDF is developed

DDF is developed by its own documentation. Until milestone M5 every task runs in **manual mode**: an interactive session follows `docs/orchestrator/manual-mode.md`, and **a human merges**. No session merges its own work.

## Reading order

1. This file.
2. `docs/orchestrator/manual-mode.md` (or `docs/orchestrator/supervisor-mode.md` when operating a running factory through `ddf`).
3. `docs/concepts/*`, only the concepts the task needs.
4. The task Issue.

Design record: `docs/specs/` (V1: `docs/specs/2026-11-03-ddf-v1-design.md`). Docs link to spec sections for rationale; when a doc and the spec disagree, the spec wins and the doc gets fixed. `RESEARCH.md` and `docs/controller-v0-decision.md` are historical.

Do not infer current architecture from historical conversations or other repositories.

## Constraints

- License: MIT. Greenfield implementation.
- Fabro is a research reference only: do not fork it, copy its implementation, or add it as a dependency.
- No GitHub Projects as required state. DDF must work on a plain Git remote.
- GitHub is the first forge adapter, not the core abstraction.
- Agent CLIs, providers and models are replaceable. Roles name tiers (`frontier|deep|standard|routine|fast`), never models.
- Human merge is the default. Autonomous merge into the integration branch is a per-project opt-in with operator consent.
- Prefer deterministic checks over LLM calls; keep loaded context and repeated model work minimal.
- Before building a generic infrastructure primitive, look for a maintained open-source solution; researching one does not mean adopting or copying it.
- Never commit credentials, tokens, private keys, environment secrets or authentication material.
- English only in the repository.

## Branch roles

- `master`: the only long-lived branch: default, integration and release-source branch. There is no production branch.
- Releases: a release is a tag on an exact `master` commit that passed CI.
- `<type>/issue-<number>-<slug>`: task branches, for example `docs/issue-20-branch-model`; `<type>` is the Conventional Commits type of the change, `<slug>` a short kebab-case slug.

Agent work branches from `master` and targets `master` through a pull request unless a task says otherwise. Never push agent work directly to `master`.

## Naming note

The project name carries an internal reference. Do not mention or explain it in the README or any other user-facing documentation.
