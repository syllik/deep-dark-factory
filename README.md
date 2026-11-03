# Deep Dark Factory

Deep Dark Factory (DDF) is a self-hosted factory that takes a software task and drives it, without a human in the loop, from intake to a merge into the project's **integration branch** (dev/staging). It works with any Git repository: through a forge adapter (GitHub first) or with a plain Git remote and no forge at all.

Human merge stays the default. Autonomous merge into the integration branch is a per-project opt-in that requires operator consent; DDF never promotes to production.

DDF is open source under the MIT license. It is not a Fabro fork and does not use Fabro as its implementation base.

## Status

V1 in development. The design is approved; milestone M1 (documentation) is in progress.

## Documentation

- [V1 design](docs/specs/2026-11-03-ddf-v1-design.md): the design record.
- [docs/](docs/): concepts, orchestrator procedures, operator, project and adapter references.
- [ARCHITECTURE.md](ARCHITECTURE.md): short architecture summary.
- [AGENTS.md](AGENTS.md): entry point for agent sessions working on DDF.

## Roadmap

V1 milestones ([spec §14](docs/specs/2026-11-03-ddf-v1-design.md#14-milestones)):

| Milestone | Content |
|---|---|
| M0 Prep | Local workspace cleanup; fact check of external assumptions |
| M1 Docs | V1 spec, `docs/`, `prompts/`, `schemas/`, new `AGENTS.md` |
| M2 Offline core | Core modules, `forge-local`, fakes; full lifecycle in tests |
| M3 Real execution | Containers, context builder, Claude Code and opencode adapters |
| M4 GitHub + channel | `forge-github` with GitHub Apps, Telegram, CLI; dry-run |
| M5 Install + pilot | Host install; DDF and one pilot project merging autonomously |
| M6 Retire | Retire the previous workflow canon |
