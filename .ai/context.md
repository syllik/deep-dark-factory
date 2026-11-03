# Deep Dark Factory — Project Context

## Status

V1 design approved 2026-11-03; M1 docs in progress.

Design record: [docs/specs/2026-11-03-ddf-v1-design.md](../docs/specs/2026-11-03-ddf-v1-design.md). Decisions D1–D15 are logged in [decisions.md](decisions.md). The research-only gate is lifted.

## Product intent

A self-hosted factory that drives a software task from intake to a merge into the project's integration branch, for any Git repository, through a forge adapter (GitHub first) or a plain Git remote. Human merge is the default; autonomous merge into the integration branch is a per-project opt-in with operator consent.

## Confirmed decisions

- Repository: `syllik/deep-dark-factory`. Public, MIT, greenfield.
- Fabro is a research reference only, not a fork, base or dependency.
- GitHub is the first forge adapter, not a core dependency; GitHub Projects are never state.
- Agent CLIs, providers and models are replaceable; roles name tiers, never models.
- Deterministic checks before LLM calls; bounded context and bounded loops.
- Branch roles: `dungeon-master` production, `master` staging/integration, `slave/<feature>` agent development; agent changes reach `master` through pull requests.
- DDF is developed by its own docs: manual mode (`docs/orchestrator/manual-mode.md`) until M5, a human merges.

## Internal naming note

The project name carries an internal reference. Do not mention or explain it in the README or other user-facing documentation.

## Historical sources

`RESEARCH.md` and `docs/controller-v0-decision.md` are historical. DDF does not load configuration, role policy or credentials from any other repository at runtime or as a build/test prerequisite.
