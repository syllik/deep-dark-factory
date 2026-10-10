# Deep Dark Factory — Project Context

## Status

Research/bootstrap phase. The first architecture and migration audit is tracked in GitHub Issue #1.

## Product intent

Build a universal, standalone open-source software factory for **any** software project. No private maintainer workflow, organization schema, GitHub Issue, forge or external repository is needed for installation, execution, or policy.

## Confirmed decisions

- Repository: `syllik/deep-dark-factory`.
- Public open-source project.
- License: MIT.
- Greenfield implementation.
- Do not fork Fabro and do not use Fabro as the implementation base.
- Fabro and similar systems may be researched for architectural lessons.
- GitHub is the first forge adapter, not a core dependency.
- GitHub Issues may be a first task-source adapter; built-in manual/local tasks work without GitHub.
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

- Admission, task identity and policy formats are DDF-owned, with no dependency on another repository's workflow rules.

## Internal naming note

Deep Dark Factory is an internal gachi reference. Do not mention or explain that reference in the README or other user-facing documentation.

## Research-only legacy sources

Historical audits may reference external workflow/runner repositories for ideas. DDF must not load their configurations, role policy, metadata rules, contracts or credentials at runtime or as a test/build prerequisite.

## Immediate objective

Complete Issue #1 and determine the minimal architecture for one vertical slice:

```text
Any task source (manual, forge, local)
-> DDF-owned policy/admission
-> relevant repository context
-> isolated agent execution
-> deterministic verification
-> safe branch/PR publication
-> human merge
```

Independent model review remains an optional boundary to evaluate during Issue #1; it is not tied to any concrete provider.

No production source-tree architecture is considered settled until that research is complete.
