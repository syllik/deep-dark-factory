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
Optional Review / Correction
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

1. The core must not require GitHub, GitHub Actions, any specific model/provider, macOS, or one sandbox implementation.
2. GitHub is the first forge integration, not the execution engine.
3. Issues are the initial task primitive; project boards are optional projections.
4. Execution state belongs to the factory. Forge UI state must not become the sole canonical runtime state.
5. Merge authority is human-controlled by default.
6. Untrusted task/code execution must be isolated from host secrets and unrelated repositories.
7. Credentials must be scoped to the minimum operation and lifetime practical.
8. Deterministic verification runs before any model-based review or correction.
9. Model-based review is optional and provider-neutral. No concrete reviewer vendor belongs in the core architecture.
10. When model review is enabled, reviewer access should be read-only by default and findings must not directly mutate code, publish changes, or merge.
11. Reviewer input must be bounded to the task intent, repository rules, verification result, and relevant diff/context.
12. Review/correction loops are bounded by policy, and deterministic verification runs again after any correction.
13. Context is loaded lazily and bounded. Whole-repository prompt dumps are not a default.
14. Agent/model routing is replaceable and budget-aware.
15. Retry and correction loops are bounded.
16. Generic infrastructure should be bought with maintained open-source dependencies where reasonable, not rebuilt merely for ownership.
17. A local Mac may be an optional execution backend, never the control-plane assumption.
18. CI for this repository and execution of the product are separate concerns.

## Review boundary

Review is a generic workflow capability, not a provider-specific subsystem.

If a workflow enables independent model review, the minimal contract should be equivalent to:

```text
input:
  task/spec
  repository rules
  base revision
  head revision
  deterministic verification summary
  bounded diff/context

output:
  findings[]:
    severity
    location?
    problem
    rationale
    suggested_fix?
```

The concrete reviewer may be a local CLI, hosted service, model API, forge integration, or another implementation. Provider selection and credentials belong to adapters/configuration, not the core contract.

## Initial adapter model

The v0 integration target is GitHub:

```text
GitHub Issue
  -> factory run
  -> Git branch
  -> deterministic verification
  -> optional bounded review/correction
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
- at most a bounded correction loop;
- safe publication of a branch/PR;
- no automatic merge.

Concurrency, scheduling, dashboards, distributed workers, advanced orchestration, and concrete reviewer integrations should be added only when a demonstrated use case requires them.

## Open decisions

Issue #1 must determine, rather than assume:

- implementation language and runtime;
- persistence/checkpoint strategy;
- workflow representation;
- concurrency/locking mechanism;
- local versus container sandbox primitives;
- provider interface shape;
- whether independent review belongs in the first executable v0 slice or a later optional workflow;
- reviewer adapter shape if/when enabled;
- Git/forge libraries;
- secret handling;
- server/webhook architecture;
- observability format.
