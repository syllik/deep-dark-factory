# Deep Dark Factory

Deep Dark Factory is a greenfield, open-source software engineering factory for autonomous and human-supervised development workflows.

The project is intentionally vendor-neutral. It is not a Fabro fork and does not use Fabro as its implementation base; Fabro and similar systems may be studied as architecture references during research.

## Goals

- self-hostable and portable;
- forge-independent core, with GitHub as the first adapter;
- provider-independent agents and models;
- no architectural dependency on GitHub Actions, Codex, OpenAI, macOS, or a single sandbox provider;
- deterministic verification before model-based review;
- minimal context loading, agent calls, and token spend;
- human-controlled merge by default;
- zero-cost CI must be possible for this public open-source repository;
- prefer small mature dependencies over rebuilding generic infrastructure.

## v0 vertical slice

The first implementation target is deliberately narrow:

```text
Issue
  -> admission/policy
  -> relevant context
  -> isolated agent execution
  -> deterministic verification
  -> branch / pull request
  -> stop before merge
```

GitHub Issues are the first task-source adapter. GitHub Projects may be supported later as an optional dashboard/integration, but must not become canonical execution state.

## Current status

Research and architecture definition only. Implementation begins after the migration and capability audit in [Issue #1](https://github.com/syllik/deep-dark-factory/issues/1).

See [ARCHITECTURE.md](ARCHITECTURE.md), [RESEARCH.md](RESEARCH.md), and [.ai/context.md](.ai/context.md).
