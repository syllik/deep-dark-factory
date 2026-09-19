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
- Deterministic verification should precede any model-based review or correction.
- No concrete reviewer provider is selected or embedded in the core architecture.
- Independent model review, if retained, is an optional provider-neutral workflow stage with read-only access by default, bounded context, structured findings, and bounded correction loops.
- Provider-specific reviewer setup, authentication, prompts, quotas, and project-local configuration stay outside the core unless a future explicit architecture decision says otherwise.
- Minimize token consumption through bounded/lazy context, minimal agent calls, and bounded correction loops.
- Reuse mature generic open-source primitives when appropriate instead of rebuilding infrastructure by default.
- Branch roles are fixed as follows: `dungeon-master` is production, `master` is staging/integration, and agent development branches use `slave/<feature>`.
- Agent changes should reach `master` through pull requests; `dungeon-master` is not a direct agent-development target.

## Internal naming note

Deep Dark Factory is an internal gachi reference. Do not mention or explain that reference in the README or other user-facing documentation.

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

Independent model review remains an optional boundary to evaluate during Issue #1; it is not tied to any concrete provider.

No production source-tree architecture is considered settled until that research is complete.
