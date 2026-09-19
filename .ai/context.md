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
- Deterministic verification must precede model-based review and correction.
- Independent model review is a read-only, replaceable policy stage; reviewers do not edit code, publish, merge, or mutate task state.
- The first dogfood reviewer is Google Antigravity CLI using Google OAuth so the existing Google AI Pro Antigravity quota can be used without requiring a repository API secret.
- Reviewer output uses structured P0/P1/P2 findings; P0/P1 block publication unless corrected or explicitly overridden by a human policy decision.
- Corrections are performed by the execution agent, followed by deterministic verification again.
- v0 defaults to one independent review and at most one correction pass.
- The Antigravity choice is an adapter/dogfood decision, not a core dependency; other reviewer providers must remain interchangeable.
- Do not introduce a GitHub Action + `GEMINI_API_KEY` reviewer during the research phase.
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
-> independent read-only review
-> optional bounded correction + re-verification
-> safe branch/PR publication
-> human merge
```

No production source-tree architecture is considered settled until that research is complete.
