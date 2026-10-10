# DDF Issue → ready PR Controller (offline reference)

**Status: unmerged draft / offline only / zero real provider calls / zero GitHub writes.** This is DDF-owned code, proposed after research PR #7. Its use of fake admission, CI, review and publisher receipts in tests/demo is not evidence of acceptance of a real Issue → ready PR cycle.

Prerequisites: Node.js 22+ (DDF target pin 24.21.0). No npm dependencies.

```sh
cd controller
npm test
npm run demo
```

The demo intentionally prints `READY_PR_SIMULATED`, not a real PR URL.

## Contracts

- `ddf.jobspec/v1`: exact task/Issue, pinned policy/base, v2 human grant, bounded context (32768 UTF-8 bytes), task-wide micro-USD cap and max calls, human merge.
- `ddf.result/v1`: invocation identity, role, state, nullable usage; unknown usage is conservatively charged to the full reservation.
- The injected `admissionReader` must perform a **fresh canonical read-back**. ChipIn requires org #53 `chipin-issue-admission/v1`, including mandatory FE/BE assignee and Project #5. Generic forges use a separate `task-admission/v1`; neither is accepted from untrusted Issue text.
- ModelProvider controls model protocol; AgentRuntime binds it to provider-independent Planner/Architect/Executor/Reviewer/Auditor roles; RunnerBackend is transport (Actions, dedicated machine, local endpoint). Separate trusted Publisher owns all Git/PR mutation credentials.
- Local atomic store supports **one host only**; it cannot be shared among ephemeral GitHub Actions jobs. A real Actions backend requires ACID external storage, task-level uniqueness, transactionally reserved invocation intent, worker fencing and callback authenticity before any model is invoked.
- Core never merges, closes Issues, deploys, rewrites branch history or changes Project Status.

## Safety limits

The first model invocation reserves its worst-case cost before invoking the backend. Unknown usage never becomes free. Fallback and correction calls debit the same task ledger, correction batches are separately authorized and capped at two. After crash, uncertain invocation/review/publication/ready intent is blocked until read-only reconciliation. Publication returns the **new final commit SHA**; required CI, independent review and ready read-back must reference that published SHA, not the pre-commit verification reference.

No deployed Codex/Claude/local adapter is accepted. They require independent capability tests and a trusted human numeric paid authorization. An `ActionsBackend` returns PENDING; its dispatch stub is **not** a production-capable Actions workflow.

## Dependencies and review order

1. Human-merge research PR #7 and accept DDF research Issue #1 (still research-only until merged).
2. Rebase/retarget this draft to `master` and run offline Actions CI; confirm scope.
3. Connect real canonical reader and shared transactional state and callbacks.
4. Connect real sandbox/credential separation, publisher, deterministic verification and SHA review.
5. Separate Codex, Claude, local capability acceptance. One paid canary **only after** exact numeric grant; human merge.

See `ACCEPTANCE.md`. This branch intentionally contains no live adapter activation and no secrets.
