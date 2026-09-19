# Architecture

Status: research baseline. This document defines boundaries and invariants, not a final implementation design.

## Product boundary

Deep Dark Factory coordinates software-engineering tasks from an external task source through isolated execution and deterministic verification to a proposed code change.

```text
Task Source
    |
    v
Admission / Policy
    |
    v
Context Builder
    |
    v
Workflow / Execution
    |
    v
Sandbox
    |
    v
Agent Provider
    |
    v
Deterministic Verification
    |
    v
Independent Review
    |
    v
Bounded Correction (optional)
    |
    v
Forge Publication
    |
    v
PR / MR
    |
    v
Human Merge
```

These boxes are logical boundaries. They are not a commitment to separate packages or services.

## Invariants

1. The core must not require GitHub, GitHub Actions, Codex, OpenAI, Google, Claude, macOS, or one sandbox implementation.
2. GitHub is the first forge integration, not the execution engine.
3. Issues are the initial task primitive; project boards are optional projections.
4. Execution state belongs to the factory. Forge UI state must not become the sole canonical runtime state.
5. Merge authority is human-controlled by default.
6. Untrusted task/code execution must be isolated from host secrets and unrelated repositories.
7. Credentials must be scoped to the minimum operation and lifetime practical.
8. Deterministic verification runs before model-based review or correction.
9. Independent model review is read-only with respect to the working tree and must not merge, publish, or silently rewrite the implementation.
10. Reviewer input is bounded to the task intent, repository rules, deterministic verification result, and the relevant base-to-head diff/context.
11. Reviewer output is structured findings, not free-form authority. Each actionable finding should include severity, location when available, problem, impact/rationale, and a suggested fix.
12. Corrections are performed by an execution agent/provider, followed by deterministic verification again. Review/correction loops are bounded by policy.
13. Context is loaded lazily and bounded. Whole-repository prompt dumps are not a default.
14. Agent/model routing is replaceable and budget-aware.
15. Retry and correction loops are bounded.
16. Generic infrastructure should be bought with maintained open-source dependencies where reasonable, not rebuilt merely for ownership.
17. A local Mac may be an optional execution backend, never the control-plane assumption.
18. CI for this repository and execution of the product are separate concerns.

## Review contract

Independent review is a policy-controlled stage, not a provider-specific feature.

For the first dogfood workflow, Google Antigravity CLI is the preferred experimental reviewer because it can run locally with Google OAuth and review a branch diff without requiring a repository API secret. This is an integration choice, not a core dependency.

The reviewer contract is:

```text
input:
  task/spec
  repository review rules
  base revision
  head revision
  deterministic verification summary
  bounded diff/context

output:
  findings[]:
    severity: P0 | P1 | P2
    file?: path
    line?: number
    problem: string
    why_it_matters: string
    suggested_fix?: string
```

Rules:

- reviewer access to the code/worktree is read-only;
- reviewer must not commit, push, publish, approve, request merge, or mutate issue/PR state;
- no finding is self-executing;
- the execution agent decides how to implement an accepted finding under the normal policy;
- after any correction, deterministic verification runs again;
- the default review/correction budget is one independent review plus at most one correction pass for v0;
- P0/P1 findings block publication until corrected or explicitly overridden by a human policy decision;
- P2 findings are advisory unless repository policy promotes them;
- credentials used by a reviewer adapter are supplied outside repository content and are never committed.

The product interface must remain compatible with other local CLIs, hosted reviewers, or model APIs.

## Initial adapter model

The v0 integration target is GitHub:

```text
GitHub Issue
  -> factory run
  -> Git branch
  -> deterministic verification
  -> independent read-only review
  -> optional bounded correction
  -> GitHub PR
  -> human merge
```

The core architecture should leave room for GitLab, Forgejo, Gitea, or other forges without rewriting workflow/execution logic.

## Initial execution model

The first vertical slice should support one task at a time and prove:

- stable task identity;
- explicit target repository and base revision;
- bounded context assembly;
- one isolated implementation run;
- deterministic verification;
- one independent read-only review;
- at most one bounded correction loop;
- safe publication of a branch/PR;
- no automatic merge.

Concurrency, scheduling, dashboards, distributed workers, and advanced orchestration should be added only when a demonstrated use case requires them.

## Open decisions

Issue #1 must determine, rather than assume:

- implementation language and runtime;
- persistence/checkpoint strategy;
- workflow representation;
- concurrency/locking mechanism;
- local versus container sandbox primitives;
- provider interface shape;
- reviewer adapter interface and invocation boundary;
- Git/forge libraries;
- secret handling;
- server/webhook architecture;
- observability format.
