# Deep Dark Factory — Project Context

## Status

Research/bootstrap phase. The first architecture and migration audit is tracked in GitHub Issue #1.

## Product intent

Build a universal open-source dark software factory for software engineering. It should work for the owner's GitHub repositories while remaining useful to other users and portable to other forges/providers.

## Confirmed decisions

- Repository: `syllik/deep-dark-factory`.
- Public open-source project.
- License: MIT.
- Greenfield implementation.
- Do not fork Fabro and do not use Fabro as the implementation base.
- Fabro and similar systems may be researched for architectural lessons.
- GitHub is the first forge adapter, not a core dependency.
- GitHub Issues are the first task-source integration.
- GitHub Projects are optional and must not be canonical execution state.
- Human merge remains the default final authority.
- No architectural dependency on the existing local MacBook runner.
- No architectural dependency on GitHub Actions for product execution.
- Agent/model providers must be replaceable.
- Deterministic verification should precede model-based review.
- Minimize token consumption through bounded/lazy context, minimal agent calls, and bounded correction loops.
- Reuse mature generic open-source primitives when appropriate instead of rebuilding infrastructure by default.

## Legacy sources to audit

- `syllik/ai-workflow`
- `syllik/codex-local-runner`

Migrate validated concepts and invariants, not legacy implementation by default.

## Immediate objective

Complete Issue #1 and determine the minimal architecture for one vertical slice:

```text
GitHub Issue
-> policy/admission
-> relevant repository context
-> isolated agent execution
-> deterministic verification
-> safe branch/PR publication
-> human merge
```

No production source-tree architecture is considered settled until that research is complete.
