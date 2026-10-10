# Pending acceptance (DDF #8)

- [x] Offline strict JobSpec/Result v1, SHA/approval/context checks.
- [x] Offline source boundaries: ModelProvider / AgentRuntime / RunnerBackend / TrustedPublisher.
- [x] Offline durable single-host fsync+rename and pre-dispatch mkdir lock/reservation.
- [x] Offline shared conservative task+fallback/correction budget; unknown usage charged full reserve.
- [x] Offline crash/uncertain handling, idempotent callback/PR lookup, SHA review dedup and two-correction ceiling.
- [x] Offline demo marked simulated; no paid calls or GitHub publication.
- [ ] Research PR #7 human merged; Issue #1 human accepted; implementation #8 merge authorized.
- [ ] Read real current Issue through ChipIn org #53 reader and ai-workflow v2 policy; trusted task approval and per-correction grant read-back required; reject queued/stale/drift.
- [ ] ACID shared durable state for ephemeral Actions, fencing, signed/authorized callback, cost overrun audit.
- [ ] Harden Git workspace/sandbox; verify no publisher credentials reach model process; separate publisher identity.
- [ ] Reuse native Development link and org #381 PR Project writer; exact PR commit/push/read-back.
- [ ] Real current-published-SHA required CI and independent read-only review; legitimate PR ready read-back.
- [ ] Independently accept **Codex** adapter (auth, pinned binary/API, sandbox, structured result, idempotency, usage ceiling).
- [ ] Independently accept **Claude** adapter (auth, protocol, sandbox, result, cost ceiling and retry isolation).
- [ ] Independently accept **local endpoint** adapter (network allowlist/TLS, pinned model ID, concurrency/timeouts, usage ceiling and sandbox).
- [ ] Human picks one admitted Issue and **explicit numeric USD task-wide cap**, per-call reserves, max calls and allowed providers, recorded with fresh trusted grant.
- [ ] One live Issue→ready PR canary after all gates; human merge, no exact-deploy.

**No paid authorization = runnable offline path only, pending live acceptance; never claim a completed live cycle.**
