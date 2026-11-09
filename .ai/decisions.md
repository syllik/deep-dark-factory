# Durable decisions

- MIT license; greenfield implementation. Sources: `AGENTS.md`, `.ai/context.md`, Issue #1.
- Fabro is a research reference only, not the implementation base or a dependency. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, Issue #1.
- GitHub is the first forge adapter, not the core abstraction; GitHub Issues are the first task source; GitHub Projects are optional and not canonical execution state. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1.
- Agent/model providers are replaceable. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`.
- Deterministic verification precedes model review/correction; human merge remains the final/default authority. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1.
- The architecture has no dependency on a local Mac/self-hosted runner or GitHub Actions product execution. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, Issue #1.
- Context and token use stay bounded/lazy, with minimal agent calls and bounded correction loops. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`.
- Branch roles are fixed: `dungeon-master` production, `master` staging/integration, `slave/<feature>` agent development. Sources: `AGENTS.md`, `.ai/context.md`. (superseded 2026-11-05)
- 2026-10-22 (owner decision): DDF stays a managed/active `syllik/ai-workflow` project; development follows the generated routing block in `AGENTS.md`. "Standalone" applies only to the DDF product runtime. Sources: `AGENTS.md`. (superseded 2026-11-03)
- The project remains in research/bootstrap phase; no production engine code is created before the v0 research/migration boundary is accepted. Sources: `AGENTS.md`, `.ai/context.md`, `ARCHITECTURE.md`, `RESEARCH.md`, Issue #1. (superseded 2026-11-03)


## Proposed, not yet accepted — standalone DDF controller v0 (2026-10-10)

- DDF is a self-contained, universal product. At product runtime: no dependency on other repositories, external routing/workflow policies, organization Issue fields, GitHub Projects, or legacy runner implementation.
- Core versioned `ddf.jobspec/v1`, `ddf.result/v1`, `ddf.policy/v1`, `ddf.admission/v1`. Opaque task identity and configurable TaskSource; built-in manual task admission and local policy are required.
- Provider-independent Planner/Architect/Executor/Reviewer/Auditor, independent ModelProvider / AgentRuntime / RunnerBackend, separate trusted Publisher.
- Actions-first optional backend; dedicated machine and local endpoint supported through adapters. Fenced shared persistence is required for multi-run Actions, not to install DDF generally.
- Task-wide conservative reservation, no paid call without exact numeric authorization, current published-SHA review, at most two separately authorized corrections and human merge. Exact-deploy not part of controller.
- Details: [v0 decision](../docs/controller-v0-decision.md). Proposed only; does not lift research-only gate.
- Superseded by the 2026-11-03 V1 design below.

## 2026-11-03 V1 design

Source: [V1 design](../docs/specs/2026-11-03-ddf-v1-design.md) §2. Supersedes the research-phase entries above where they conflict, including the 2026-10-22 managed-routing decision (see D12).

- Research-only gate lifted by the maintainer (2026-11-03).
- D1: V1 autonomous merge into the integration branch only for DDF itself and one private pilot product. Every other project starts at `propose` (human merge).
- D2: V1 host: a macOS workstation under a separate non-admin user; later a server. The core is host-independent.
- D3: Agent adapters in V1: Claude Code CLI and opencode CLI.
- D4: Agent roles: Planner, Executor, Reviewer-correctness (R1), Reviewer-security (R2). Trusted non-AI roles: Publisher, Merge-gate. The daemon is the orchestrator.
- D5: Reviewer count is a policy outcome of the risk class (1 or 2), never a hard-coded constant.
- D6: Three GitHub Apps: `worker`, `review-correctness`, `review-security`. Fallback if GitHub does not count App approvals: machine users (subject to GitHub terms, §16).
- D7: Two intake modes: free-form request (Planner creates the tasks) and an existing task marked for DDF.
- D8: No GitHub Projects board in the core. DDF must work on a plain Git remote.
- D9: Operator channel: CLI and Telegram.
- D10: Isolation: one container per run (colima on macOS), no forge credentials inside.
- D11: Operator config lives in a private per-operator repository or folder; secrets never in Git.
- D12: The old personal workflow repository is archived after migration; DDF `docs/` becomes the single canon for autonomous and interactive (manual) orchestration.
- D13: Local workspace cleanup is a separate track before the pilot.
- D14: Build approach: new core generalized from the 2026-10-28 autonomous-flow design; harvest selected ideas from PR #9; close PR #9 as superseded.
- D15: Model tiers: `frontier` (never selected automatically), `deep`, `standard`, `routine`, `fast`. Subagents per run: at most 3 by default (advisory, §7.3).


## M0 facts (2026-11)

Source: `docs/research/2026-11-m0-facts.md`. Format: fact → consequence → spec section → confidence → later probe.

- §16.1 App approvals probably count (no direct doc); ≤ 1 free machine account per person → D6 unchanged; machine-user fallback limited to one free machine reviewer → §16 → unverified (terms: documented) → M4 `app-approval-counts`.
- §16.2 Token narrowing by `repository_ids` + `permissions`, ≤ 1 h; omitting `permissions` widens the token → broker treats any failed narrowing as hard failure → none → documented → M4 `token-narrowing`.
- §16.3 App without `workflows` gets 403 on `update-branch` (pushed merge likely too) when base changed workflow files → keep withholding `workflows`; refused sync → `needs-owner`, no sync batch used; human branch update → full re-review → §5.2, §6.6 rule 4, §16 → unverified → M4 `sync-with-workflow-change`.
- §16.4 Both CLIs run non-interactively as non-root; isolation = Claude `--setting-sources ''` + `--strict-mcp-config`, opencode `OPENCODE_*` + `--pure`; opencode npm 1.x and GitHub 2.x incompatible → pin npm 1.x, explicit model, per-call timeout, classify errors from `is_error`/`api_error_status`/`error`; host never runs agent CLIs on a repo copy → §7.3, §8.3, §8.4, §16 → Claude Code verified; opencode verified with free models only (no provider credential available); rate-limit shapes unverified; open: `opencode-executor-output`, `opencode-reviewer-readonly` → M3 `cli-version-pin`, `agent-home-state`, `claude-config-isolation`, `opencode-config-isolation`, `*-error-shape`, `*-rate-limit-shape`, `secret-exfil-redaction`.
- §16.5 colima as `ddf` via LaunchDaemon works, limits enforced; VM rules lost on restart, provision hooks fail silently, launchd does not restart the VM → daemon applies/verifies rules before every container start and restarts the VM; global pause only after that fails → §8.1, §8.4, §9, §16 → verified; unverified: coexistence with a second colima instance (owner's own), launchd respawn when the colima process exits, provision hooks with `sudo -n`, IPv6, host reboot (owner rerun pending) → M3 `vm-firewall-reapply`, `runtime-recovery`, `coexistence`; M5 `colima-boot`, `coexistence`.
- §16.6 `node:sqlite` no warning on Node 24.21.0, stability 1.2 since 24.15.0 → Node ≥ 24.15.0 (documented release candidate), pinned and verified on 24.21.0; store access only via `store` → §4.4, §16 → verified (stability: documented) → none (M2 store tests). (superseded 2026-11-06)

## 2026-11-05 Branch model (owner decision)

Source: Issue #20 (owner decision of 2026-11-05, see #12). Supersedes the branch-roles entry above.

- Single long-lived branch `master`: the default, integration and release-source branch.
- No production branch; the production branch named in the superseded entry above is never created.
- A release is a tag on an exact `master` commit that passed CI.
- Task branches are `<type>/issue-<number>-<slug>` (for example `docs/issue-20-branch-model`), branched from `master` and targeting `master` by pull request.
- 2026-11-05 AGENT-DEFAULT: Which values may `<type>` take in `<type>/issue-<number>-<slug>`? → The Conventional Commits type of the change (feat, fix, docs, refactor, test, ci, chore); `<slug>` is a short kebab-case slug. Task: #20.
- 2026-11-05 AGENT-DEFAULT: With no production branch, what replaces "the production branch or a release tag" as the ref the daemon runs from? → A release tag (a maintainer-made tag on a `master` commit that passed CI), never the moving tip of the integration branch. Task: #20.
- 2026-11-05 AGENT-DEFAULT: Where and how is the new branch-model decision recorded in `.ai/decisions.md`? → A new section "2026-11-05 Branch model (owner decision)" at the end of the file; the old branch-roles line gets the suffix "(superseded 2026-11-05)". Task: #20.
- 2026-11-05 AGENT-DEFAULT: Does a class raised to `sensitive` after planning (computed at publishing or raised by R1) re-run the Planner at `deep`? → No. Only a READY result proposing `sensitive` from a pass below `deep` triggers the one re-run at `deep`; a later raise changes the reviewer set only. Task: #22.
- 2026-11-05 AGENT-DEFAULT: Does asking in chat instead of a full needs-owner round for a needed forbidden-path change apply only when the need is known at planning time? → It applies whenever the need is found, at any step, as long as the owner is present: they have written in this session's chat during this task and the session is still running. Task: #22.

## 2026-11-06 Runtime model and language (Issue #31)

Source: Issue #31, research [`docs/research/2026-11-runtime-and-language.md`](../docs/research/2026-11-runtime-and-language.md). Owner's choices in the task session's chat on 2026-11-06: hybrid, Go. Accepted by the owner's merge. These ADRs supersede the §16.6 `node:sqlite` line above and the stack of spec §4.4 as approved on 2026-11-03.

- 2026-11-06 AGENT-DEFAULT: What are the name and location of the research document? → `docs/research/2026-11-runtime-and-language.md`, next to `docs/research/2026-11-m0-facts.md`. Task: #31.
- 2026-11-06 AGENT-DEFAULT: Where do the two ADRs go in `.ai/decisions.md`? → A new section "2026-11-06 Runtime model and language (Issue #31)" at the end of the file; entries the ADRs replace get the suffix "(superseded 2026-11-06)". Task: #31.

### ADR 1: runtime model

- **Decision:** Hybrid. One long-lived control plane per host (`ddfd`) holds the store, the secrets, the scheduler and the tick, as in spec §4, §8.1 and §9. Each task's execution side is a disposable **task cell**: run containers, workspace copies, scratch volumes and any per-task network, all labelled with the task key. When the task leaves the conveyor, a verified teardown destroys the cell (#33). Run slots and container limits adapt to the host (#34). The control plane can also run as one container on a Linux host (#35).
- **Alternatives:** (A) an ephemeral factory container per task, with state on the forge or in an external store, started by a timer or hosted CI; (B) the spec's long-lived daemon with no per-task teardown or adaptive limits.
- **Pros:**
  - The long waits (CI up to 2 h, owner answers, human merge) and the cross-task state stay in one store, with no new service. That state is zones, slots, the global pause, the 24 h `revert-red` window, the run budget, finding identity and intents.
  - The forge keys and the review-signing key stay in one trusted process, and run containers never get the runtime socket.
  - `forge-local` and plain Git (D8), the macOS V1 host (D2), D10 and "no required runner" all hold.
  - The per-task execution side becomes a black box that is destroyed on purpose.
- **Cons:**
  - The control plane remains a long-lived process the operator runs and backs up.
  - The factory as a whole does not self-destroy per task, only its execution side.
  - The teardown rules and adaptive limits are new spec work.
- **Rejected (A) because:**
  - It needs either a long-lived container per task or external transactional state with fencing (the v0 finding).
  - It needs a long-lived trigger anyway.
  - It copies the keys into every factory container that can start containers. Nesting needs the root-equivalent socket, privileged DinD or a Linux-only runtime (Sysbox).
  - It loses the audit record and the intents unless they are exported first.
- **Consequences:**
  - Spec §4.4 records the model.
  - #33 specifies the task cell and its §14.1 probe.
  - #34 replaces the fixed 3 GB / 2 CPU defaults with host-derived sizing.
  - #35 specifies the container deployment of the control plane for M5.
  - What survives a task: forge objects, store rows and the journal, sanitized run logs (30 days). What does not: workspace contents, container filesystems, agent homes, loaded credentials.

### ADR 2: implementation language

- **Decision:** Go for `ddfd`, `ddf` and the core. Toolchain pinned in `go.mod`, `CGO_ENABLED=0`, static binaries for linux and darwin on arm64 and amd64, standard library first. An exhaustiveness analyzer (`nishanths/exhaustive` or equivalent) runs as a required CI step. Every dependency has a recorded reason. `scripts/docs-check.mjs` stays a Node docs tool.
- **Alternatives:** TypeScript on Node 24 (the stack inherited from the v0 decision without a language comparison); Rust.
- **Pros (measured on a macOS arm64 workstation, see the research file):**
  - Idle RSS after the tick workload is 8 MiB, against 69 MiB for Node.
  - The tick runs in 19–31 ms, against 81–110 ms for Node.
  - The output is one static binary of 7.1 MiB (3.0 MiB gzip) with SQLite inside. It cross-builds for linux arm64 and amd64 from any machine without a C toolchain.
  - The image can be about 1 MiB of base (distroless static) plus the binary, against `node:24.21.0-alpine` at 59 MiB and a 117 MiB `node` runtime per host.
  - Builds are fast (8 s cold, 0.8 s after an edit).
  - The ecosystem is mature for subprocess, container and GitHub work (`go-github`).
- **Cons:**
  - Go has no sum types: the lifecycle, policy and gate rely on enum-like constants, sealed interfaces, the exhaustiveness analyzer and tests.
  - SQLite, JSON Schema 2020-12 and YAML become dependencies (cgo-free SQLite driver, `santhosh-tekuri/jsonschema`, `yaml/go-yaml`); Node had SQLite built in.
  - The M2 plan's stack sections must be rewritten.
- **Rejected:**
  - TypeScript: a runtime install per host, about 8× Go's idle memory, npm supply-chain exposure, and `node:sqlite` still a release candidate. Its one advantage is zero switching cost.
  - Rust: the strongest correctness (compiler-checked exhaustive `match`), but the Linux cross-build with bundled SQLite failed without a C cross-toolchain, the cold build took 20 s for a tiny crate, `serde_yaml` is archived, and agents need more compile-fix rounds (judged).
- **Consequences:**
  - Spec §4.1 `store` and the §4.4 package and stack bullets change in this PR, with Changelog lines.
  - #32 rewrites the M2 plan: Tech Stack, Reuse, CI, Tasks 0–2, the OD items, and the profile's manifest files (`go.mod`, `go.sum`, the boundary test, instead of the npm files). It also writes the code-conventions doc.
  - No M2 task starts before #32 merges.


## 2026-11-06 M2 plan for Go (Issues #32, #37)

Source: Issues #32 and #37, [M2 plan](../docs/plans/2026-11-05-m2-offline-core.md) OD-15 and OD-16. Owner's choices in the task session's chat on 2026-11-06. Accepted by the owner's merge.

- Package layout (OD-15): the Go layout `internal/core/<module>`, `internal/adapters/<port>/<name>`, `internal/daemon`, `cmd/ddfd`, `cmd/ddf`; the profile, spec §4.4 and §12 globs follow it. Chosen over keeping `src/` (the research recommendation).
- Import boundaries (OD-16): an own test in `test/boundaries/` using only the standard library (`go list -deps -json`), a `ciEntrypoint`; Go `internal/` placement where it encodes a rule. No boundary linter.
- 2026-11-06 AGENT-DEFAULT: Where does the code-conventions doc live, and which class do its later edits get? → `docs/development/code-conventions.md`; `docs/development/**` joins the profile's `standard` agent-steering row. Task: #32.
- 2026-11-06 AGENT-DEFAULT: How is the #37 "complete list" expressed once the rewrite is done? → The plan header note and the research #32 row state the rule (every section, task and OD that named npm, TypeScript or Node) and list the items by number; the plan header is a past-tense record. Task: #32.
- 2026-11-06 AGENT-DEFAULT: What happens to Task 0, which #28 already executed? → Kept with its number and marked done: the first rows by #28, the Go paths by #32. Task: #32.
- 2026-11-06 AGENT-DEFAULT: How are the two owner-decided items numbered in the plan? → New OD-15 (package layout) and OD-16 (boundary enforcement), each with an "Owner choice (2026-11-06, in chat)" line; existing OD numbers unchanged. Task: #32.
- 2026-11-06 AGENT-DEFAULT: Is there a verify wrapper script, and how are analyzers pinned? → No wrapper script (it would be one more `ciEntrypoint`); the plan's Tech Stack defines the verify sequence once and CI mirrors it; analyzers are `tool` lines in `go.mod`. Task: #32.
- 2026-11-06 AGENT-DEFAULT: Is `govulncheck` part of the required `verify` check? → No: a separate, non-required CI check `vulncheck`, so the required check stays deterministic. Task: #32.
- 2026-11-06 AGENT-DEFAULT: What does the plan's Status line say after the rewrite? → "accepted 2026-11-05 (#24); Go rewrite (Issues #32, #37) accepted when the owner merges that PR". Task: #32.

## 2026-11-07 Context budget for interactive sessions (Issue #40)

- 2026-11-07 Interactive sessions rotate on a measured context budget (soft 120000, hard 160000 tokens by default, operator-chosen), measured by a deterministic hook rather than the model's own estimate; daemon runs are unaffected. Rules: `docs/orchestrator/handoff.md#context-budget`; hook recipe: `docs/adapters/agent-claude-code.md#interactive-sessions`. Task: #40.
