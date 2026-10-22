# Pending acceptance (DDF #8)

- [x] Offline strict JobSpec/Result v1, scoped taskKey, SHA/approval/context checks.
- [x] Offline source boundaries: ModelProvider / AgentRuntime / RunnerBackend / TrustedPublisher.
- [x] Offline durable single-host fsync+rename and pre-dispatch mkdir lock/reservation.
- [x] Offline shared paid-role task+fallback/review/correction budget; unknown usage charged full reserve.
- [x] Offline crash/uncertain handling, idempotent callback/ChangeRef lookup, SHA review dedup and two-correction ceiling.
- [x] Offline demo marked simulated; no paid calls or GitHub publication.
- [ ] Research PR #7 human merged; Issue #1 human accepted; implementation #8 merge authorized. Documentation-level review alone is not human approval.
- [ ] Wire standalone DDF-native task-source/admission and DDF policy v1, with trusted task approval and per-correction grant read-back; reject queued/stale/drift. No maintainer external workflow dependency.
- [ ] ACID shared durable state for ephemeral Actions, fencing, signed/authorized callback, cost overrun audit.
- [ ] Harden Git workspace/sandbox; verify no publisher credentials reach model process; separate publisher identity.
- [ ] Create a generic ForgePublisher adapter (GitHub first) with exact branch/commit/PR read-back and external idempotency; never require organization-specific writers.
- [ ] Real current-published-SHA required CI and principal-independent read-only review; neutral ChangeRef ready read-back.
- [ ] Independently accept **Codex** adapter (auth, pinned binary/API, sandbox, structured result, idempotency, usage ceiling).
- [ ] Independently accept **Claude** adapter (auth, protocol, sandbox, result, cost ceiling and retry isolation).
- [ ] Independently accept **local endpoint** adapter (network allowlist/TLS, pinned model ID, concurrency/timeouts, usage ceiling and sandbox).
- [ ] Human picks one admitted task from an arbitrary project and **explicit numeric USD task-wide cap**, per-call reserves, max calls and allowed providers, recorded with fresh trusted grant.
- [ ] One live Issue→ready PR canary after all gates; human merge, no exact-deploy.

**No paid authorization = runnable offline path only, pending live acceptance; never claim a completed live cycle.**

## Permanent AUDIT-PLAN regressions

- [x] No paid Reviewer bypass: shared pre-dispatch ledger/maxCalls/cap and conservative unknown usage.
- [x] Scoped DDF taskKey across projects/sources, callbacks, locks, invocations and grants.
- [x] ChangeRef (PR/MR/change proposal) and READY_CHANGE without mandatory numeric GitHub PR.
- [x] Principal-based reviewer independence, not forced vendor difference.
- [x] Ambiguous paid review recovery, duplicate settlement and SHA dedup.
- [ ] Planner/Architect/Auditor runnable paid role adapters (declared ports only).
- [ ] Live reviewer, callback authentication, shared storage, and all real provider adapters remain pending.
