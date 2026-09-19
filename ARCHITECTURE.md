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

1. The core must not require GitHub, GitHub Actions, Codex, OpenAI, Claude, macOS, or one sandbox implementation.
2. GitHub is the first forge integration, not the execution engine.
3. Issues are the initial task primitive; project boards are optional projections.
4. Execution state belongs to the factory. Forge UI state must not become the sole canonical runtime state.
5. Merge authority is human-controlled by default.
6. Untrusted task/code execution must be isolated from host secrets and unrelated repositories.
7. Credentials must be scoped to the minimum operation and lifetime practical.
8. Deterministic verification runs before model-based review or correction.
9. Context is loaded lazily and bounded. Whole-repository prompt dumps are not a default.
10. Agent/model routing is replaceable and budget-aware.
11. Retry and correction loops are bounded.
12. Generic infrastructure should be bought with maintained open-source dependencies where reasonable, not rebuilt merely for ownership.
13. A local Mac may be an optional execution backend, never the control-plane assumption.
14. CI for this repository and execution of the product are separate concerns.

## Initial adapter model

The v0 integration target is GitHub:

```text
GitHub Issue -> factory run -> Git branch -> GitHub PR -> human merge
```

The core architecture should leave room for GitLab, Forgejo, Gitea, or other forges without rewriting workflow/execution logic.

## Initial execution model

The first vertical slice should support one task at a time and prove:

- stable task identity;
- explicit target repository and base revision;
- bounded context assembly;
- one isolated implementation run;
- deterministic verification;
- at most a bounded correction loop;
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
- Git/forge libraries;
- secret handling;
- server/webhook architecture;
- observability format.
