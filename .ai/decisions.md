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
- 2026-11-10 Amendment (Issue #46): `internal/core/workspace` runs the `git` binary, so the control-plane image carries `git`; the "distroless static" base in the Pros bullet above does not hold as written (the base must provide `git`). The original bullet stays as approved on 2026-11-06. Spec [§4.4](../docs/specs/2026-11-03-ddf-v1-design.md#44-boundaries). Task: #46.
- 2026-11-10 AGENT-DEFAULT: What is the operator override for capacity and what happens to limits.maxConcurrentContainers? → Docs name the override as an operator setting in routing config `limits` that replaces the port-reported values; its exact key and schema land with #34 with the sizing rule. `limits.maxConcurrentContainers` stays optional with no default, documented as an optional operator cap, not the scheduling input; schemas/routing.json is not edited here. Task: #46.
- 2026-11-10 AGENT-DEFAULT: Where is the teardown record stored? → Indicative only, in line with §4.5 'Tables (indicative)': a per-task record (destroyed, verified, time) scoped by task key; the concrete table/column is decided in M2. Task: #46.
- 2026-11-10 AGENT-DEFAULT: What does recovery do with live runs of a non-terminal task in V1? → Kill and discard (retried within budget), as now; the rule is keyed by task key and run ID from labels so the re-adoption task (decision 5b) changes only that branch. Task: #46.


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

## 2026-11-10 Reference host and runtime profiles (Issue #45)

Source: Issue #45, owner decisions of #43. Amends D2, D9 and D10 above; the original lines stay as approved on 2026-11-03.

- 2026-11-10 D2 amended: V1 reference deployment is a Linux host with the control plane (`ddfd`) in a container ("Linux container profile"); macOS + colima is the second runtime profile ("macOS colima profile"); rootful or rootless runtime on Linux stays open until the probe spike. Spec §2, §8.1. Task: #45.
- 2026-11-10 D10 amended: run-container isolation is a set of properties (no route to the host, the control plane or private address ranges, DNS excepted; all capabilities dropped; no new privileges; non-root user; pids and memory limits; no runtime socket) that every runtime profile provides and proves with one shared probe set. Spec §2, §8.4, §14.1 probe 6. Task: #45.
- 2026-11-10 D9 amended: Telegram deferred to V1.1; the Notify port and the CLI stay. Spec §2, §4.2, §14, §14.1. Task: #45.
- 2026-11-10 AGENT-DEFAULT: Where does the single list of isolation probes live? → `docs/operator/security-model.md` Probes holds the list; spec §14.1 probe 6 names the properties and links to it. Task: #45.
- 2026-11-10 AGENT-DEFAULT: What are the two runtime profiles called? → "Linux container profile" (reference) and "macOS colima profile". Task: #45.

## 2026-11-10 Context budget derived from the context window (Issue #44)

Source: Issue #44, owner decisions in chat on 2026-11-10. Amends the 2026-11-07 line above, which stays as approved.

- 2026-11-10 Context budget amended: soft and hard limits are operator-chosen shares of the room, not fixed token numbers. Baseline = the context size at the session's first measured turn; working context = current size minus baseline; room = the context window as the harness reports it (or the operator cap, if lower) minus baseline. Rules: `docs/orchestrator/handoff.md#context-budget`; hook recipe: `docs/adapters/agent-claude-code.md#interactive-sessions`. Task: #44.
- 2026-11-10 Owner decision: the example shares are soft 0.6 and hard 0.85 of the room, and the warning repeats every 0.1 of the room past hard. The optional operator cap (absolute tokens, for cost, absent by default, set only in the operator's own settings) acts as the effective window: shares apply to min(window, cap) minus baseline. Task: #44.
- 2026-11-10 Owner decision: soft is advice at a checkpoint; hard means start no new step, with only the closing actions allowed (post the result, post the handoff, answer the owner's questions about the result). Starting the successor session stays an owner action and is out of scope. Task: #44.
- 2026-11-10 AGENT-DEFAULT: Does the hook script fall back to default shares when the operator sets none? → Yes: it uses the documented example shares (0.6 soft, 0.85 hard, 0.1 step) as ratio defaults. These are not token numbers. Window and cap have no default. Task: #44.
- 2026-11-10 AGENT-DEFAULT: Operator setting names in the recipe? → Env vars DDF_CONTEXT_SOFT_SHARE, DDF_CONTEXT_HARD_SHARE, DDF_CONTEXT_STEP_SHARE, DDF_CONTEXT_CAP, DDF_CONTEXT_WINDOW (the last used only when the harness reports no window). DDF_CONTEXT_SOFT/HARD are removed. Task: #44.
- 2026-11-10 AGENT-DEFAULT: What happens when the cap (or window) is not above the baseline (room <= 0)? → The hook stays silent and the status line prints 'ctx -', the same as when it cannot measure. Task: #44.
- 2026-11-10 AGENT-DEFAULT: Which baseline does the transcript fallback use? → The same per-session baseline file: the first size measured by either source, written once. Task: #44.

## 2026-11-10 Agent port, routing provider and egress scope (Issue #47)

Source: Issue #47, owner decisions 3 and 4 of #43. Spec §1, §4.2, §6.7, §7.2, §7.3, §8.4 and §15.

- 2026-11-10 AGENT-DEFAULT: How does the routed model reach the adapter, given that the Issue lists the port inputs as role, brief, workspace content, limits and credential source? → The routing entry (`provider`, `model`, `effort`) is an explicit port input: `run(role, entry, brief, workspace, limits, credentialSource)`. 'Every call uses the entry's model, never a default' stays a port-level rule. Task: #47.
- 2026-11-10 AGENT-DEFAULT: Is `provider` required in a routing entry, and how is it checked? → Required, together with `adapter` and `model`, with pattern `^[a-z][a-z0-9-]*$`. `effort` stays optional. DDF compares only this field and never derives it from the `model` prefix. Task: #47.
- 2026-11-10 AGENT-DEFAULT: What exactly does `reviewers.r2DifferentProvider: true` compare? → R2 uses only routing entries whose `provider` differs from the `provider` of the entry that ran the Executor for the reviewed head. Adapter and model are not compared. With no such entry, the existing no-entry-left rule applies: the task waits and one notification is sent. Task: #47.
- 2026-11-10 AGENT-DEFAULT: What is the name and shape of the project-profile key that declares run-time registries? → Top-level `registries` in `.ddf/project.yaml`: a list of unique host names, with no scheme, path or port. Default none. Task: #47.
- 2026-11-10 AGENT-DEFAULT: Which run containers get the declared registries in their egress allow-list? → Only containers that run project commands: Executor runs, the local gate and `forge-local` CI. Planner and reviewer runs are read-only and execute no commands, so they reach only their routed adapter's provider endpoint. Task: #47.
- 2026-11-10 AGENT-DEFAULT: Which failure class covers a timeout, an unrecognised non-zero exit, or a missing or invalid result? → `fatal`: a failed run within the §5.3 bounds (one retry on an invalid result, then `needs-owner`). Task: #47.
- 2026-11-10 AGENT-DEFAULT: Do the spec example, the schema example and the starter template declare a registry, given that their verify runs `npm ci`? → Yes. Each gets `registries: [registry.npmjs.org]` with a comment that a project whose image carries its dependencies omits the key. Task: #47.
- 2026-11-10 AGENT-DEFAULT: How is the owner's schedule 'no real provider key inside a run container before the first pay-per-token key is loaded' worded? → A real pay-per-token provider key never enters a run container: the per-run token implementation of the credential source is in place before the first pay-per-token key is loaded. Subscription tokens that cannot be proxied stay an accepted risk on a dedicated account. Task: #47.
- 2026-11-10 AGENT-DEFAULT: Where are this change's decisions recorded? → A new section '## <date> Agent port, routing provider and egress scope (Issue #47)', appended at the end of `.ai/decisions.md`. It has a Source line (Issue #47, owner decisions 3 and 4 of #43) and one AGENT-DEFAULT line per default. Task: #47.

## 2026-11-11 Credential bullets and residual egress channel (Issue #54)

Source: Issue #54, DDF review r2 @ 2c01f56 on PR #53, findings r2-1 and r2-2. Spec §15.

- 2026-11-11 AGENT-DEFAULT: Scope item 2 offers two options: name the residual channel as an accepted risk, or plan M3 enforcement allowing only read/fetch methods to registries. Which one? → Name it as an accepted risk on the dedicated account in spec §15 and security-model.md Accepted risks. No doc plans method-level egress enforcement and there is no M3 plan document; M3 enforcement can be proposed when the M3 plan is written. Task: #54.
- 2026-11-11 AGENT-DEFAULT: Do the adapter docs name an environment variable or endpoint for the per-run token? → No. They say only that a pay-per-token entry receives the per-run token from the credential source, never the real key, and is unusable until that implementation exists; the delivery details are decided with the implementation. Task: #54.
- 2026-11-11 AGENT-DEFAULT: Does spec §8.4 (Egress per run, Redaction) change as well? → No. The residual channel is named only in §15 and security-model.md Accepted risks, as the Issue asks; §8.4 already states host-level egress and redaction correctly. Task: #54.
- 2026-11-11 AGENT-DEFAULT: Do docs/concepts/routing.md and identity.md get the same residual-channel wording? → No. They are outside #54's file list and are edited by the parallel task #49; routing.md's Accepted schedule already matches the credential rule. Task: #54.
- 2026-11-11 AGENT-DEFAULT: The new rule 'directories 0700' contradicts existing text calling the `~ddf/.config/ddf/secrets/` directory 'mode 0600'; fix the contradiction beyond the three places the Issue names? → Yes, in spec section 3 Secrets row, config-layers.md Secrets row, identity.md Host secrets bullet and setup.md Secrets layout paragraph; the security-model.md secrets table row is left to #54. Task: #49.

## 2026-11-11 Auto-rotation at the hard context limit (Issue #57)

Source: Issue #57, owner request in the session chat.

- 2026-11-11 At the hard context limit, after the result and the handoff are posted, an interactive session whose harness can start sessions rotates by itself: one successor with a fresh context in its own checkout, started, confirmed, released from the ending session, reported to the owner, then the ending session ends itself. Any refusal falls back to the previous behaviour (stop without ending the session). The soft limit stays advice. The owner's standing permission lives in the operator's agent settings, not in DDF. Rules: `docs/orchestrator/handoff.md#rotation`; desktop-app mapping and hook text: `docs/adapters/agent-claude-code.md#rotation-in-the-desktop-app`. Task: #57.
- 2026-11-11 AGENT-DEFAULT: Does the successor inherit the ending session's model? → Not by these rules: its tier follows routing like any session; the rotation rules do not name a tier. Task: #57.
- 2026-11-11 AGENT-DEFAULT: What happens when the hard warning repeats past the limit after a successor was started? → Nothing new: exactly one successor per session; the repeat never starts a second one. Task: #57.

## 2026-11-11 Rotation permission switch, split fallback and clean-worktree guard (Issue #59)

Source: Issue #59 (follow-up of #57 reviews), owner request in the session chat.

- 2026-11-11 A session rotates at the hard limit only with the operator's standing permission (the hook setting `DDF_CONTEXT_ROTATE=1`) and a harness that can start sessions; the successor's brief names the URL of the handoff comment just posted instead of "the newest handoff"; the session ends itself only when its worktree has no uncommitted or unpushed changes; and the single fallback splits into "start refused or unavailable" (stop, the owner starts the next session) and "started but not released, or unpushed work remains" (give the owner the successor's link, do not end this session, never ask for another). Rules: `docs/orchestrator/handoff.md#rotation`; hook text and desktop-app mapping: `docs/adapters/agent-claude-code.md#rotation-in-the-desktop-app`. Task: #59.
- 2026-11-11 AGENT-DEFAULT: Issue item 1 offers two forms (hard text conditional on the permission, or installing the text is the permission plus a non-rotating text); which one? → Both through one deterministic switch: the recipe reads the operator setting DDF_CONTEXT_ROTATE; '1' is the operator's standing permission and selects the rotating hard text, anything else selects a hard text without rotation. Task: #59.
- 2026-11-11 AGENT-DEFAULT: What does a session do when the successor started but unpushed work remains in its own worktree? → The started-but-not-released fallback: give the owner the successor's link, do not end this session, never ask for another session, then stop. Task: #59.

## Dependencies

Source: Issue #63, plan `docs/plans/2026-11-05-m2-offline-core.md` OD-2 (Reuse table). Versions live only in `go.mod`. `Reaches:` is the union over linux/arm64, linux/amd64, darwin/arm64 and darwin/amd64 with `CGO_ENABLED=0` of `go list -deps <pkg>` filtered to `net`, `os/exec` and `crypto/tls`.

- 2026-11-12 Owner decision: plan OD-1..OD-14 recommendations accepted; toolchain go1.27.4 (plan: go1.27.3); modernc.org/sqlite v1.62.0 (plan: v1.61.1, minor, owner-approved); CI verify fails when go env GOVERSION differs from the go.mod toolchain line (#39 r1-2). Task: #63. (OD-14 superseded 2026-11-14 by #43 decision 7)
- `modernc.org/sqlite` (BSD-3-Clause): persistence through `database/sql`, cgo-free, so the static cross-build carries SQLite inside. Reaches: net, os/exec. Task: #63.
- `github.com/santhosh-tekuri/jsonschema/v6` (Apache-2.0): JSON Schema draft 2020-12 validation of role results and config, so `schemas/*.json` stay the single normative source; hand-written validators drift. Reaches: none of net, os/exec, crypto/tls. Task: #63.
- `go.yaml.in/yaml/v3` (Apache-2.0): YAML for `.ddf/project.yaml`, `routing.yaml` and `projects.yaml`; the standard library has no YAML parser and the config formats are fixed by the spec. Reaches: none of net, os/exec, crypto/tls. Task: #63.
- `github.com/bmatcuk/doublestar/v4` (MIT): glob matching for risk paths, forbidden paths, zones and sensitive paths; the spec globs use `**`, which `path.Match` lacks. Reaches: none of net, os/exec, crypto/tls. Task: #63.
- `github.com/nishanths/exhaustive` (BSD-2-Clause, tool `go tool exhaustive`): exhaustive `switch` on enum-like types, the required substitute for sum types. Reaches: os/exec. Task: #63.
- `honnef.co/go/tools` (MIT, tool `go tool staticcheck`): static analysis beyond `go vet` (unused results, deprecated calls, dead code). Reaches: crypto/tls, net, os/exec. Task: #63.
- `golang.org/x/vuln` (BSD-3-Clause, tool `go tool govulncheck`, CI check `vulncheck`): reports only vulnerabilities the code can reach, with no extra action. Reaches: crypto/tls, net, os/exec. Task: #63.
- 2026-11-12 AGENT-DEFAULT: Where and under what heading does the Dependencies section go in .ai/decisions.md? → Append a new undated section `## Dependencies` at the end of the file, so later dependency changes add lines to it as code-conventions.md expects. It has a Source line (Issue #63, plan OD-2), the owner-decision line, one line per module and the AGENT-DEFAULT lines. Task: #63.
- 2026-11-12 AGENT-DEFAULT: Do dependency lines carry versions? → No. go.mod is the single source of versions. The changes from the plan's versions are stated in the owner-decision line and in the PR body. Task: #63.
- 2026-11-12 AGENT-DEFAULT: How is 'reaches net, os/exec, crypto/tls' determined? → Take the union over linux/arm64, linux/amd64, darwin/arm64 and darwin/amd64, with CGO_ENABLED=0, of `go list -deps <pkg>` filtered to exactly `net`, `os/exec` and `crypto/tls`. `<pkg>` is the imported package for a runtime module and the `tool` package path for a tool. Task: #63.
- 2026-11-12 AGENT-DEFAULT: Where does the GOVERSION check run? → As the first step after setup-go in the `verify` job only. `vulncheck` stays checkout, setup-go and govulncheck. Task: #63.
- 2026-11-12 AGENT-DEFAULT: Is plan verify item 7 one CI step or four? → One step that builds `./...` for the four GOOS/GOARCH targets in turn and fails on the first error, matching the plan's 'one step per verify item'. Task: #63.
- 2026-11-12 AGENT-DEFAULT: Workflow name and extra keys (concurrency, timeout-minutes)? → Workflow `name: ci`. No concurrency, timeout or other keys beyond the plan's Hosted CI shape. Task: #63.
- 2026-11-12 AGENT-DEFAULT: What if a dependency forces a `go` line above `go 1.27`, or `tidy` rewrites the go or toolchain lines? → Stop and return BLOCKED rather than accept a different `go` or `toolchain` line. Task: #63.

## 2026-11-12 M2 plan review follow-ups (Issue #39)

Source: Issue #39, DDF review of PR #38 (parent #32), findings r1-2, r1-3, r2-1, r2-2 and r2-3. Plan `docs/plans/2026-11-05-m2-offline-core.md`, spec §12.

- 2026-11-12 Owner decision (in chat): `cmd/ddfd/**` joins the `sensitive` row of DDF's own profile, right after `internal/daemon/**`, in `docs/orchestrator/manual-mode.md` and spec §12 (r2-2). Task: #39.
- 2026-11-12 Owner decision (in chat): the import-boundary test enforces a per-package allowlist of third-party imports from `internal/core/**`; a new or changed entry is a `sensitive` change (plan Task 2 Step 4, OD-16; r2-3). Task: #39.
- 2026-11-12 AGENT-DEFAULT: Is the CI version-equality step a numbered verify item? → No. It is named as the first step of the CI `verify` job, before items 1-7; the numbered verify items 1-8 stay unchanged, because a local run may use another Go (GOTOOLCHAIN=auto) and the item numbers are referenced elsewhere. Task: #39.
- 2026-11-12 AGENT-DEFAULT: Align the plan with go.mod (go1.27.4, modernc.org/sqlite v1.62.0) or keep the original versions? → Align: Tech Stack and the Reuse Persistence row state the merged versions with a link to the decision log Dependencies section; OD-1 keeps its original options and recommendation and gains one outcome sentence. Task: #39.
- 2026-11-12 AGENT-DEFAULT: Which r2-1 fix: union of go list over the four targets, or parsing every .go file regardless of build constraints? → Union over the four verify targets, the same method the decision log uses for 'Reaches'. Files built for none of the four targets are never compiled into a shipped binary and stay out of scope. Task: #39.
- 2026-11-12 AGENT-DEFAULT: The config loader package does not exist and the plan does not name it; how is its yaml allowlist entry written? → As 'the config loader package, path named by the task that creates it'; that task adds the entry to test/boundaries/**, which makes it sensitive. Until then no core package may import go.yaml.in/yaml/v3. Task: #39.

## 2026-11-12 M2 Task 2a: model and result validation (Issue #67)

- 2026-11-12 AGENT-DEFAULT: How is the task key encoded? → Lowercase hex SHA-256, all 64 characters, over forge, repository, source kind and source id in that order. Each field is its decimal byte length, ':' and its bytes. An empty field is an error. Task: #67.
- 2026-11-12 AGENT-DEFAULT: How do enums reject unknown values from JSON or store decoding? → Each enum type has a pointer-receiver UnmarshalText that calls its Parse<Type> function. Task: #67.
- 2026-11-12 AGENT-DEFAULT: Does results.go return typed result structs? → No. It only validates. Typed result structs come with their first consumer. Task: #67.
- 2026-11-12 AGENT-DEFAULT: How does the validator avoid reading references from disk or network? → The jsonschema compiler uses UseLoader(jsonschema.SchemeURLLoader{}), a loader that loads nothing. Each schema is added and compiled under a fixed mem:///schemas/<file name> URL. Task: #67.
- 2026-11-12 AGENT-DEFAULT: When may Event.Key be empty? → Only for global events, such as a pause. Task: #67.
- 2026-11-12 AGENT-DEFAULT: What are the IntentStatus and RunStatus values? → IntentStatus is pending, succeeded or failed. RunStatus is running, succeeded, failed or discarded. Task: #67.
- 2026-11-12 AGENT-DEFAULT: Which files does schemas/embed.go embed? → Every top-level *.json, so the later config loader can use project.json, projects.json and routing.json. schemas/examples is not embedded. Task: #67.

## 2026-11-13 M2 Task 2b: port interfaces (Issue #69)

- 2026-11-13 AGENT-DEFAULT: How is dry-run expressed on forge writes? → A WriteMode second parameter on every forge write (live, dry-run); the zero value and any unknown value are refused, not a bool or a constructor flag. Task: #69.
- 2026-11-13 AGENT-DEFAULT: How are agent failures typed? → One *AgentFailure error type with a Class; callers treat any other error as fatal. Task: #69.
- 2026-11-13 AGENT-DEFAULT: Where do IDs come from, and is a find-task-by-key read added? → An IDSource port is added next to Clock; no find-task-by-key read is added (the forge port has exactly the spec 4.2 operations; the engine task adds one if reconciliation needs it). Task: #69.
- 2026-11-13 AGENT-DEFAULT: How does Secret avoid leaking? → Secret redacts itself in fmt and refuses JSON and text marshalling. Task: #69.
- 2026-11-13 AGENT-DEFAULT: How does Merge report a moved head? → Sentinel ErrHeadMoved. Task: #69.
- 2026-11-13 AGENT-DEFAULT: Close and reopen task: one method or two? → Two methods, CloseTask and ReopenTask. Task: #69.

## 2026-11-13 M2 Task 2c: import-boundary test (Issue #68)

- 2026-11-13 AGENT-DEFAULT: What does the build-constraint rule check? → Every non-test .go file under internal/core/** is compiled for at least one of the four verify targets. This also fails files built only for other platforms, which is stricter than the owner's wording (constraints other than GOOS/GOARCH), because DDF ships only those four targets and the check needs no constraint parsing. The import rules still see only the four targets (Issue #39 default). Task: #68.
- 2026-11-13 AGENT-DEFAULT: How are cgo files under internal/core handled? → They fail through the build-constraint rule, consistent with CGO_ENABLED=0 and the code conventions. Task: #68.
- 2026-11-13 AGENT-DEFAULT: Do the rules apply to _test.go files? → No. _test.go files are exempt from every rule: they are not in Imports, never ship, and core tests use test/fakes by convention. Task: #68.
- 2026-11-13 AGENT-DEFAULT: How do fixture modules get third-party paths offline? → They replace each third-party path with a local stub module under testdata/stubs, carry no go.sum, and run with GOPROXY=off. The code-conventions no-replace rule applies to the root go.mod only. Task: #68.
- 2026-11-13 AGENT-DEFAULT: Which environment does go list run with, and is GOPROXY=off safe for the real tree? → os.Environ() followed by GOOS, GOARCH, CGO_ENABLED=0, GOFLAGS=-mod=readonly, GOWORK=off, GOPROXY=off and GOTOOLCHAIN=local, for the real tree too. Module zips are already cached when go test runs: in CI, go mod verify and the go vet and go tool steps fetch them; locally the cache holds them. A missing module fails the test closed with a message naming go mod download. Task: #68.
- 2026-11-13 AGENT-DEFAULT: What counts as an adapter-to-adapter import? → Any import under internal/adapters outside the importer's own internal/adapters/<port>/<name> subtree. A package with fewer than two path segments below internal/adapters counts as its own subtree. Task: #68.
- 2026-11-13 AGENT-DEFAULT: How are the fixtures laid out? → Shared stub modules plus one violations module and one clean module, each compared against an exact expected set of (rule, package, import or file) entries. Task: #68.
- 2026-11-13 AGENT-DEFAULT: How does the test avoid a stale cached pass, since go test tracks only files the test process opens? → Before listing, the test walks the module root, skipping .git and node_modules but not test/boundaries/testdata, and reads every .go file, go.mod and go.sum with os.ReadFile, so any change invalidates the cached result. CI is unaffected, because it runs without a shared cache. Task: #68.
- 2026-11-13 AGENT-DEFAULT: How does the test make sure the go on PATH is the toolchain that built it? → Before the first go list it runs `go env GOVERSION` with the same Env and fails unless the output equals runtime.Version(); the message says to run the test via go test. Task: #68.

## 2026-11-14 #43 architecture-review and audit decisions (Issue #74)

Source: Issue #43 (owner decisions of 2026-11-10, comment 6752693755; frontier docs audit, comment 6831303567; owner answers of 2026-11-14, comment 6877993288) and Issue #74. Spec `docs/specs/2026-11-03-ddf-v1-design.md`, plan `docs/plans/2026-11-05-m2-offline-core.md`.

- 2026-11-10 Owner decision (#43 decision 2): a weak host means option (b) now, with the port shaped for (c). Task: #74.
- 2026-11-10 Owner decision (#43 decision 5): live runs are re-adopted across restarts; per answer 2 this lands in M3, and M2 keeps kill and discard. Task: #74.
- 2026-11-10 Owner decision (#43 decision 6): review authenticity is Ed25519 with key ID and version, replacing HMAC-SHA256 (spec §6.6). Task: #74.
- 2026-11-10 Owner decision (#43 decision 7): `forge-local` keeps its own storage, and no adapter imports the store; this supersedes OD-14 (a) (spec §10.1, plan Task 2d). Task: #74.
- 2026-11-10 Owner decision (#43 decision 8): GitHub identities are decided after the approval probe; the machine-user fallback covers one reviewer identity only. Task: #74.
- 2026-11-10 Owner decision (#43 decision 10): walking skeleton first; `sensitive` only for the gate, signatures, patch apply and the Publisher; one deep security review of the integrated core at M2 exit; already-merged paths keep their review history (spec §12, manual-mode profile, plan Task 17). Task: #74.
- 2026-11-14 Owner decision (#43 answer 1): decisions 6, 7 and 10 stand. Task: #74.
- 2026-11-14 Owner decision (#43 answer 2): re-adoption of live runs lands in M3; M2 keeps kill and discard. Task: #74.
- 2026-11-14 Owner decision (#43 answer 3): Task 7 covers zones, dependencies, priority, pause and an optional count cap; its input type carries host capacity; capacity admission lands with #34. Task: #74.
- 2026-11-14 Owner decision (#43 answer 4): one schema file, freely edited until the first real install; migrations from M5; Task 3 builds and tests the schema-version mechanism; the store section (spec §4.5) is written before Task 3. Task: #74.
- 2026-11-14 Owner decision (#43 answer 5): the starter default hard-stop globs are `Dockerfile*`, `**/Dockerfile*`, `docker-compose*.y*ml`, `**/*.tf`, `**/*.tfvars`, `k8s/**`, `helm/**`, `deploy/**`, `infra/**`, `.terraform/**`, `ansible/**`; a hard-stop change is not published, the task goes to `needs-owner` before publishing and, after the owner's answer, is published at `propose` (human merge); a forbidden-path change is never published automatically (spec §6.4, §6.5, `docs/concepts/policy.md`). Task: #74.
- 2026-11-14 Owner decision (#43 answer 6): a new M2 config-loader task (plan Task 15). Task: #74.
- 2026-11-14 Owner decision (#43 answer 7): finding identity: the reviewer carries the prior finding's ID for a finding that still holds and the core compares IDs only, before Task 6 (plan Task 16, before Task 6b). Task: #74.
- 2026-11-14 Owner decision (#43 answer 8): #73 (enum fail-closed decoding) is an acceptance item of Task 3. Task: #74.
- 2026-11-14 Owner decision (#43 answer 9): the "this is data" rule and delimiters in `prompts/*.md` now, as their own small task; store backup, restore and key rotation in M5; internal events beside the tick in M3; drain on upgrade together with re-adoption in M3. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Where does the store section go (spec §4.5 or lifecycle.md)? → Spec §4.5, as normative subsections. lifecycle.md is at 196 of its 250-line docs-check limit and only links the section. Task: #74.
- 2026-11-14 AGENT-DEFAULT: What does a lease protect in V1? → Nothing: leases are dropped from V1. The instance lock and recovery by label cover restarts, and the M3 re-adoption work decides whether a lease returns. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Which tables and columns does the store have? → tasks, runs, changes, base_merges, reviews, findings, questions, intents, events, pause_reasons, cooling and flakes, with the keys and columns in the spec. A value that is filtered, ordered, joined or constrained on is a column; the rest goes in a ddf.<name>/v1 JSON payload. Task: #74.
- 2026-11-14 AGENT-DEFAULT: How are the §5.3 counters stored? → They are not stored: they are derived from runs via the purpose, revision and attempt columns, so they cannot drift. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Where is the teardown record? → Column tasks.teardown_at, set only after a verified destroy, matching the merged model field. Task: #74.
- 2026-11-14 AGENT-DEFAULT: How are times stored? → INTEGER Unix milliseconds UTC from the clock port. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Which SQLite settings apply? → journal_mode WAL, synchronous FULL (intents need durability), foreign_keys ON, busy_timeout 5000 ms, all read back after open; a single connection per process. Task: #74.
- 2026-11-14 AGENT-DEFAULT: How do the new sync blocks get checked, given docs-check.mjs only knows sync:forbidden? → Generalize docs-check.mjs to named sync blocks compared with the spec, keeping output unchanged for the existing block. It is a ciEntrypoint, so the change is sensitive. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Which lockfiles and agent-instruction files count? → The 19-entry and 14-entry lists in the spec, as fixed product defaults with no profile key in V1. Task: #74.
- 2026-11-14 AGENT-DEFAULT: How exactly are changed lines counted? → git diff --numstat -M from the merge base to the head, counting added+deleted. Lockfiles excluded; a pure rename (100%) counts 0; any binary file makes the change exceed lowMaxLines; a mode-only change counts 0; path rules match both rename paths. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Do projects add hard-stop path globs? → Not in V1: schemas/project.json has categories only. Path detection uses the default globs; project categories go to the reviewers' checklist; projects use forbidden or risk.sensitive for paths. Task: #74.
- 2026-11-14 AGENT-DEFAULT: What happens after the owner answers a hard-stop needs-owner? → The Publisher publishes that revision and the task continues at propose (human merge). An owner who declines closes the task. Task: #74.
- 2026-11-14 AGENT-DEFAULT: What happens after the owner answers a forbidden-path needs-owner? → The task returns to running with the answer in the Executor brief. A revision still touching a forbidden path is refused again; DDF never publishes it, and the owner changes forbidden paths in a human-merged change. Task: #74.
- 2026-11-14 AGENT-DEFAULT: What are the signature 'key ID and version'? → version is the format string ddf.review-signature/v1. keyId is the lowercase hex of the first 16 bytes of SHA-256 of the raw Ed25519 public key. The signature is standard padded base64. Key rotation is M5. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Which concrete paths make up the narrowed sensitive row? → internal/core/policy/gate/**, internal/core/policy/signature/**, internal/core/workspace/patch/**, internal/core/publisher/** and the forge merge code. Rows with no M2 code are kept for their milestone: store migrations (M5), cmd/ddfd/**, the token broker, internal/adapters/agent/**, internal/adapters/sandbox/docker/**. prompts/**, schemas/**, forbidden paths and ciEntrypoints are also kept. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Should a 'standard at least' floor row cover the other core and daemon code? → No. The default size rules apply, as decision 10 intends, and the M2 exit deep security review covers the integrated core. Task: #74.
- 2026-11-14 AGENT-DEFAULT: How are new and split tasks numbered? → Existing numbers are kept. Splits use letters (2d, 6a/6b, 8a/8b, 12a/12b, 13a/13b), following the Task 2a-2c precedent. New tasks are 15 (config loader), 16 (finding ID) and 17 (exit security review). Task: #74.
- 2026-11-14 AGENT-DEFAULT: Where does the config-loader task sit? → Wave B, after 2d (both edit test/boundaries/**) and 4. Package internal/core/config takes bytes, never paths, and maps the profile to policy.Profile, which Task 4 defines. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Which task carries the 'no adapter imports the store' boundary change? → New Task 2d, a child of #66, in wave B before the first adapter tasks 9 and 10. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Do spec §5.2 Fixing and lifecycle.md fixing change now for answer 7? → No. Task 16 changes them together with the reviewer schema field and the prompt rule, so the normative text never runs ahead of the schema. This change records the decision and plans Task 16. Task: #74.
- 2026-11-14 AGENT-DEFAULT: Where does the answer-9 prompt 'this is data' rule go? → Its own small sensitive Issue outside the M2 plan, recorded in .ai/decisions.md only. Task: #74.
- 2026-11-14 AGENT-DEFAULT: What is forge-local's own storage? → Plain files in a directory of its own. The boundary test allows SQLite only under internal/core/store. The file format is defined with Task 10 (audit P2-11). Task: #74.
- 2026-11-14 AGENT-DEFAULT: What is the M2 exit security review? → Task 17: one deep review (R2 role at deep) of the merged integrated core after 13b, posted on the M2 tracking Issue. P0/P1 findings are fixed by separate tasks before M2 is done. Task: #74.

## 2026-11-15 PR #75 review follow-ups (Issue #76)

- 2026-11-15 AGENT-DEFAULT: What set do the hard-stop, path-policy and Publisher rules match (r2-2)? → The changed paths: every path differing between the merge base of the integration branch and the applied tree (Publisher) or head (gate), added, modified, deleted or type-changed, mode-only included, both sides of a rename; defined in spec §6.2 and policy.md#changed-paths. The symlink rule stays 'added or changed symlink'. Task: #76.
- 2026-11-15 AGENT-DEFAULT: How does an owner-answered hard stop avoid being asked again on later revisions and heads, now that changed paths count from the merge base (r1-2)? → Approval per hard-stop entry: path plus blob ID and mode, or deleted. The answer approves the listed entries; a tree with only approved entries asks nothing new; any other entry is a new hard stop. The owner-answered revision therefore does not fail gate condition 6. Task: #76.
- 2026-11-15 AGENT-DEFAULT: How is the merge refusal for a hard stop or path policy on the head kept from looping, and where does the sticky hit_stop refusal map (r1-2)? → Gate condition 6 covers path policy and unapproved hard-stop entries on the head's changed paths; its row asks needs-owner at most once per head (questions.head_sha). A new row maps hit_stop (condition 2), and a head already asked about, to stay with no new needs-owner: the change waits at propose for a human. Task: #76.
- 2026-11-15 AGENT-DEFAULT: Which outcome for a patch that adds a .opencode/ or .claude/ path (r1-1)? → The forbidden-path flow (needs-owner before publishing, hit_stop set, back to running after the answer), stated the same way everywhere; the stricter outcome, since the owner sees every attempt. node_modules/ keeps its existing outcome. Task: #76.
- 2026-11-15 AGENT-DEFAULT: Does that rule cover nested .opencode/ and .claude/ directories? → Yes, at any depth (**/.opencode/**, **/.claude/**), as the old §8.3 wording was not limited to the root. It is stated outside the sync:forbidden block, which stays unchanged. Task: #76.
- 2026-11-15 AGENT-DEFAULT: Which r2-1 option keeps the hardened clean clone and git invocation sensitive? → A named package internal/core/workspace/git/** in the spec §12 and manual-mode sensitive rows (deterministic by path); every workspace git call goes through it; plan Task 8a becomes sensitive; Task 8b's probe-4 tests also call the exact clone and base-merge functions. Task: #76.

## 2026-11-15 M2 Task 3a: model record fields (Issue #79)

- 2026-11-15 AGENT-DEFAULT: How do enums reject unknown values from JSON or store decoding? (supersedes the #67 line 'How do enums reject unknown values from JSON or store decoding?') → UnmarshalText rejects unknown values only for JSON string values; JSON null or a missing field leaves the zero value and is caught by the record's Validate(); store columns are decoded by explicit Parse<Type> (#73). Task: #79.
- 2026-11-15 AGENT-DEFAULT: How is a zero-value enum in a decoded record rejected? → A Validate() error method per record type; required enums must parse, optional ones are zero or parse. Task: #79.
- 2026-11-15 AGENT-DEFAULT: Where does the run failure class type live, given model cannot import ports? → model.FailureClass with the same four spellings as ports.AgentFailureClass, pinned by a test. Task: #79.
- 2026-11-15 AGENT-DEFAULT: Are question category, event type and pause reason enums now? → No: question category and event type stay strings (the spec defines no closed set); a PauseReason type comes with its first consumer (lifecycle/scheduler). Task: #79.
- 2026-11-15 AGENT-DEFAULT: How are task flags represented? → Unchanged Flags []TaskFlag; Validate rejects duplicates; the store maps them to the three flag columns. Task: #79.
- 2026-11-15 AGENT-DEFAULT: How are agent defaults carried? → Task.AgentDefaults []Decision. Task: #79.

## 2026-11-15 M2 Task 3b: store open and schema (Issue #80, part of #77)

- 2026-11-15 AGENT-DEFAULT: Where does the schema version number live? → A Go const in the store package, written into schema_version on create; schema.sql holds DDL only. Task: #80.
- 2026-11-15 AGENT-DEFAULT: Are enum columns constrained in SQL? → No CHECK constraints; every enum column is decoded through its model Parse function (#73), so the enum lists have one source. Task: #80.
- 2026-11-15 AGENT-DEFAULT: How is 'the file is never changed' guaranteed on a version mismatch? → The version is read through a read-only connection before any pragma or write; the read-write handle opens only after the version is equal or the database is empty. Task: #80.
- 2026-11-15 AGENT-DEFAULT: What counts as an empty database? → Missing file, zero-length file, or a database with no tables. Task: #80.
- 2026-11-15 AGENT-DEFAULT: How are settings applied on every connection? → DSN _pragma parameters plus _txlock=immediate, with SetMaxOpenConns(1), read back after open. Task: #80.

## 2026-11-15 M2 Task 7: scheduler (Issue #78)

- 2026-11-15 AGENT-DEFAULT: Which tasks hold a zone and when does a flagged task release it? → Unflagged tasks in running, publishing, ci, review, fixing or merge hold their zones; a task with any flag (needs-owner, hold, failed) is off the conveyor and holds none. Zones match by exact string equality. Task: #78.
- 2026-11-15 AGENT-DEFAULT: When is a dependency satisfied? → Only when a task with that key is in the input in state done; an unknown key is unsatisfied (fail closed). Task: #78.
- 2026-11-15 AGENT-DEFAULT: What does 'then by satisfied dependencies' order mean? → Among band-3 tasks of equal follow-up status and priority, a task with more dependencies (all satisfied) goes first. Task: #78.
- 2026-11-15 AGENT-DEFAULT: Where do follow-ups sort? → Inside band 3 (ready, unblocked waiting, planning) all follow-ups come after all non-follow-ups, before priority is compared; follow-ups in fixing, merge, ci or review keep their band. Task: #78.
- 2026-11-15 AGENT-DEFAULT: Are planner runs scheduled? → Yes, a planning task that wants a run is a band-3 candidate ordered like a ready task; it skips zone and dependency checks because those are known only after planning. Task: #78.
- 2026-11-15 AGENT-DEFAULT: How does the count cap count a run that needs several containers? → Each task states the containers its next run needs (at least 1); admission is strict in order and stops at the first candidate that does not fit, so lower-priority work never overtakes. Task: #78.
- 2026-11-15 AGENT-DEFAULT: How is the pause set typed in the scheduler? → As a set of reason strings; any non-empty set admits nothing. The reason enum and set/lift rules belong to the lifecycle (Task 6b). Task: #78.
- 2026-11-15 AGENT-DEFAULT: Which type carries host capacity? → ports.Capacity (CPU millis, memory bytes, disk bytes), carried in the input and not used for admission until #34. Task: #78.

## 2026-11-16 M2 Task 4: policy classification (Issue #86)

- 2026-11-16 AGENT-DEFAULT: What glob semantics do policy paths use? → doublestar.Match on the full repository-relative slash path. A pattern without a leading **/ is anchored at the repository root. X/** also matches X itself. A Match error is returned as an error, never treated as no match. Task: #86.
- 2026-11-16 AGENT-DEFAULT: How does the prose-form sync:forbidden block map to globs? → Each backticked token is one glob as written (17 tokens, block order). '.gitignore of the repository root' is root .gitignore only, so nested .gitignore files stay allowed. Bare names (AGENTS.md, CLAUDE.md, .gitattributes, .env*, CODEOWNERS, LICENSE) are root-only, literally; a project adds deeper ones through forbidden. 'symbolic links' is the mode-120000 rule, not a glob. The §6.5 paragraph adds **/.opencode/** and **/.claude/**. Task: #86.
- 2026-11-16 AGENT-DEFAULT: Which paths are the forge and CI 'equivalents' in the forbidden list? → A fixed root-anchored list after the block tokens: .gitlab/**, .gitea/**, .forgejo/**, .circleci/**, .buildkite/**, .woodpecker/**, .woodpecker.yml, .woodpecker.yaml, .drone.yml, .travis.yml, bitbucket-pipelines.yml, azure-pipelines.yml, Jenkinsfile, docs/CODEOWNERS. It is pinned by the Go sync test, not by the doc block. Task: #86.
- 2026-11-16 AGENT-DEFAULT: Is glob matching case-sensitive? → Rules that tighten (default and project forbidden, hard-stop globs, agent-instruction files, risk.sensitive, ciEntrypoints) match case-insensitively by lowercasing both pattern and path, so .CLAUDE/ on a case-insensitive checkout is caught. Rules that loosen (lockfile exclusion, risk.docs) match case-sensitively. No Unicode normalization. Task: #86.
- 2026-11-16 AGENT-DEFAULT: What happens with an invalid project glob or invalid profile value? → Fail closed: Profile.Validate rejects an empty glob, a leading '/', a pattern doublestar.ValidatePattern refuses, negative lowMaxLines, an unknown autonomy, or an empty checks.required list. Classify and CheckPaths return ErrInvalidProfile before evaluating anything. Task: #86.
- 2026-11-16 AGENT-DEFAULT: Is the revert class computed by policy? → No. Classify never returns revert; DDF sets it for its own revert changes (Task 6b). Reviewers(revert) is R1 with the hard-stop checklist. In Raise, revert ranks with docs (0), so any raise above docs replaces it. Task: #86.
- 2026-11-16 AGENT-DEFAULT: How do the computed class, the Planner proposal and R1 raises combine? → Maximum by rank (docs = revert < low < standard < sensitive) through Raise(computed, planner, r1...); nothing lowers. A raise of revert, empty or an unknown value is ErrInvalidRiskClass. Task: #86.
- 2026-11-16 AGENT-DEFAULT: Do forbidden paths affect the risk class? → Yes. A default or project forbidden path forces sensitive in Classify (tightening only, as in DDF's own profile), in addition to being refused by CheckPaths. Task: #86.
- 2026-11-16 AGENT-DEFAULT: How are lockfiles, renames and binaries counted together? → An entry is excluded only if every candidate path (both sides of a rename) matches a lockfile glob. Excluded entries are skipped entirely, the binary rule included, so a binary bun.lockb does not exceed lowMaxLines. All other entries, docs files included, count added+deleted. Task: #86.
- 2026-11-16 AGENT-DEFAULT: Does policy apply profile defaults? → No. Profile values are used as given. policy exports DefaultLowMaxLines = 200, and the config loader (Task 15) fills it and autonomy propose when the keys are absent. Task: #86.
- 2026-11-16 AGENT-DEFAULT: What do Classify and CheckPaths do with no changed paths? → Both return ErrNoChangedPaths; an empty change is not classified or passed. Task: #86.
- 2026-11-16 AGENT-DEFAULT: Which changed-path inputs are invalid? → Errors wrap ErrInvalidChangedPath for: an empty path; a leading or trailing '/'; a path where path.Clean differs; a '.' or '..' segment; a NUL; a '.git' segment in any case; an unknown status or mode; OldPath present without renamed or missing for renamed; modes or blobs inconsistent with the status; a blob that is not 40 or 64 lowercase hex; a negative count; two non-deleted entries for one path. Task: #86.
- 2026-11-16 AGENT-DEFAULT: What is the state of a hard-stop path that appears on both sides (old side of a rename and added by another entry)? → The state comes from the non-deleted entry whose Path it is (its new mode and blob). A path that appears only as deleted or as a rename's old side is Deleted, with empty Mode and BlobID. Approval matches by exact struct equality. Task: #86.
- 2026-11-16 AGENT-DEFAULT: Does securityLevel block outside the security category? → Yes. securityLevel High or Medium blocks in any category, as schemas/reviewer.result.json does. Category security without a securityLevel is ErrInvalidFinding. Task: #86.
- 2026-11-16 AGENT-DEFAULT: Which document does the sync test compare against? → Both spec docs/specs/2026-11-03-ddf-v1-design.md (the source) and docs/concepts/policy.md, read at test time via relative paths, so a drift in either fails go test as well as docs-check. Task: #86.
- 2026-11-16 AGENT-DEFAULT: How are the default glob lists held in Go? → As unexported functions returning new slice literals, exported through Default*Globs() copies. There are no package-level slice variables, per the no-mutable-state rule. Task: #86.
- 2026-11-16 AGENT-DEFAULT: What does policy.Profile hold? → Autonomy (request), LowMaxLines, Docs, Sensitive, CIEntrypoints, Forbidden and RequiredChecks (nil = key absent, for the Task 5 gate). hardStops categories, zones, planning and reviewers keys are not read by policy and stay out. Task: #86.
- 2026-11-16 AGENT-DEFAULT: What if the change exceeds the plan's ~600-line PR target? → Keep it as one PR: the target is advisory, and Tasks 5, 11 and 15 need the whole root-package API. Keep test tables compact. Task: #86.

## 2026-11-16 M2 Task 2d: adapter store rule and core purity imports (Issue #89)

Source: Issue #89; Issue #43 decision 7; audit P2-14 on Issue #43; plan `docs/plans/2026-11-05-m2-offline-core.md` Task 2d.

- 2026-11-16 AGENT-DEFAULT: Do the purity import rules of audit P2-14 join this change? → Yes, as rule `core-pure`. The audit asks for them with the next test/boundaries change, and this is that change. The next one (Task 15) would otherwise touch the same file again. The rule already holds on the real tree, and it costs one condition plus five fixture files. Task: #89.
- 2026-11-16 AGENT-DEFAULT: Which packages does `core-pure` cover, and must `internal/core/lifecycle` exist first? → Module packages under `internal/core/lifecycle`, `internal/core/policy` or `internal/core/scheduler`, subtrees included, so the planned `policy/gate` and `policy/signature` are covered. Matching is by path prefix, so `lifecycle` need not exist yet: Task 6a's package is covered when it lands, and a fixture proves the rule now. Task: #89.
- 2026-11-16 AGENT-DEFAULT: Which imports are forbidden in the pure packages, and do subpackages and `math/rand/v2` count? → Standard-library paths under `math/rand`, `log` and `os`, subpackages included: `math/rand/v2`, `log/slog`, `log/syslog`, `os/exec`, `os/signal`, `os/user`. Not `crypto/rand`, `time`, `io/fs` or `syscall`: the audit did not name them, and pure code needs `time` values. A call to `time.Now` cannot be detected by imports and stays a review item. Task: #89.
- 2026-11-16 AGENT-DEFAULT: Does `core-pure` check direct or transitive imports, and test files? → Direct `Imports` of non-test files only, over the union of the four target runs, like every rule except `core-net`. `_test.go` files stay exempt (Issue #68 default). A pure package importing `os/exec` reports both `core-exec` and `core-pure`. Task: #89.
- 2026-11-16 AGENT-DEFAULT: How does the store rule read after Task 2d? → Rule ID `adapter-store` is kept. It fires on any import by a package under `internal/adapters` of a module package under `internal/core/store`, the package itself included (#43 decision 7, spec §10.1). The daemon and core keep access. Task: #89.
- 2026-11-16 AGENT-DEFAULT: Does the audit's other half (adapters importing core packages beyond the allowed ones) join this change? → No. The Issue offers only the pure-package import rules, no adapter exists yet, and the allowed core set for adapters is not fixed in a normative list. It stays open on audit P2-14 for a later boundary task. Task: #89.
- 2026-11-16 AGENT-DEFAULT: Where do the new fixtures go? → Clean: the store import moves out of `forge/a`; new `scheduler/a.go` (`time`), `scheduler/a_test.go` (`os`, `log`, `math/rand`) and `lifecyclex/a.go` (`os`); `store/a.go` also imports `os`. Violations: a new empty `internal/core/store` package, a new adapter `internal/adapters/sandbox/d` importing it, and new `lifecycle`, `policy/gate` and `scheduler` packages that import the forbidden paths. Task: #89.

## 2026-11-15 M2 Task 3c: store operations (Issue #77)

- 2026-11-16 AGENT-DEFAULT: Which tables get store operations now? → tasks, events and intents (what recovery and the engine skeleton need); runs, changes, base_merges, reviews, findings, questions, pause_reasons, cooling and flakes are schema-only until their first consumer task adds operations, each decoding enum columns through Parse (spec §4.5 SQL rules). Task: #81.
- 2026-11-16 AGENT-DEFAULT: Where does the store get time? → From the caller, who reads the clock port; the store takes time values and never reads a clock. Task: #81.
- 2026-11-16 AGENT-DEFAULT: Does resolving an intent append an event? → No; intents are their own record; only task state changes append events. Task: #81.
- 2026-11-16 AGENT-DEFAULT: How is an absent optional enum stored? → SQL NULL; "" in the column is a read error. Task: #81.

## 2026-11-16 M2 Task 8a-1: workspace git helper (Issue #93)

- 2026-11-16 AGENT-DEFAULT: Does #90 fit one PR under the ~600 changed-line target? → No: DECOMPOSE into 8a-1 (#93: git helper with explicit env and version check, clean clone, fetch, per-run copy) and 8a-2 (#94: patch-id, daemon base merge, conflict detection, workflow-file check), in that order, both sensitive under internal/core/workspace/git/**; #90 closes when 8a-2 merges. Task: #93.
- 2026-11-16 AGENT-DEFAULT: Do base merge, patch-id, conflict detection and the workflow check sit in workspace/git or in the workspace root package? → All in internal/core/workspace/git, so they stay sensitive by path (spec §12); the root package internal/core/workspace holds only doc.go, and patch export/apply stays in workspace/patch (8b). Task: #93.
- 2026-11-16 AGENT-DEFAULT: Which minimum git version, and why? → 2.39.0, the first release with 'git patch-id --verbatim' (spec §6.6 rule 4); every other feature used is older. NewRunner runs 'git version' and fails with ErrGitTooOld naming both versions. Task: #93.
- 2026-11-16 AGENT-DEFAULT: How does the core find the git binary? → The daemon passes an absolute git path and a private state directory to NewRunner; the core does no PATH lookup and reads no environment. Tests use exec.LookPath in test code only. Task: #93.
- 2026-11-16 AGENT-DEFAULT: What exactly is the explicit git environment? → Nothing inherited; exactly GIT_CONFIG_NOSYSTEM=1, GIT_CONFIG_GLOBAL=/dev/null, GIT_TERMINAL_PROMPT=0, GIT_ATTR_NOSYSTEM=1, GIT_NO_REPLACE_OBJECTS=1, GIT_CEILING_DIRECTORIES=parent of the working dir, HOME and XDG_CONFIG_HOME = an empty private dir, LC_ALL=C, PATH=/usr/bin:/bin; 8a-2 adds only the GIT_AUTHOR_*/GIT_COMMITTER_* identity keys for commit-tree. Task: #93.
- 2026-11-16 AGENT-DEFAULT: Which -c options does every git call carry? → core.hooksPath=<private empty dir> (verified empty before every call), core.fsmonitor=false, core.attributesFile=/dev/null, core.symlinks=false, core.protectHFS=true, core.protectNTFS=true, protocol.allow=never with protocol.file.allow=always (local transport only until M4), gc.auto=0 and maintenance.auto=false; command-line -c overrides any repository-local value. Task: #93.
- 2026-11-16 AGENT-DEFAULT: How are filter drivers neutralised, given filter.<x>.* names cannot be enumerated? → Through config isolation: a filter, textconv, external diff or merge driver runs only if git config defines its command, and every config source is DDF's (no system, global or inherited config, a clone never copies the source's config); diffs also pass --no-ext-diff --no-textconv, and attr.tree is not used (git 2.41+). A test plants drivers in the source config and the process environment and asserts none runs. Task: #93.
- 2026-11-16 AGENT-DEFAULT: What shape does the daemon's clean clone have? → A bare clone (clone --bare --no-local --no-tags --template=<empty dir>) of an absolute path or file:// URL, other URLs refused before git runs; no checkout, so no smudge filter, fsmonitor or worktree hook can fire, and --no-local copies no alternates. Refresh is Fetch (+refs/heads/*:refs/heads/*, --prune, no tags, no FETCH_HEAD). Task: #93.
- 2026-11-16 AGENT-DEFAULT: How is the per-run copy made without remotes or credentials? → git init --template=<empty dir> --initial-branch=ddf-run into a new directory, a fetch of the one commit by SHA from the clean clone's path without FETCH_HEAD, then checkout -B ddf-run <commit> with reflogs disabled: own object store, no alternates, no remote.* or credential config, core.symlinks=false; removed on failure, handed to the Sandbox port as content and destroyed with the run cell. Task: #93.
- 2026-11-16 AGENT-DEFAULT: How are symlinks in the base tree handled when the copy is checked out on the host? → core.symlinks=false on every call and in the copy's local config, so a symlink entry becomes a regular file holding the link text and the Sandbox adapter cannot follow it to a host file; added or changed symlinks are still refused by patch apply (8b). Task: #93.
- 2026-11-16 AGENT-DEFAULT: How is git's stderr reported, and is it redacted? → CommandError carries the arguments, the exit code and the first 2048 bytes of stderr; no redaction in the runner because no credential passes through git in M2 (file transport only); spec §8.4 redaction applies in the daemon, and M4 revisits this with https and tokens. Task: #93.
- 2026-11-16 AGENT-DEFAULT: Can git's repository discovery escape the given directory? → No: Run requires an absolute directory and sets GIT_CEILING_DIRECTORIES to its parent, so git never picks up an enclosing repository; a test runs from a dir nested in a fixture worktree and expects failure. Task: #93.

## 2026-11-17 M2 Task 15: config loader (Issue #97)

Source: Issue #97; #43 answer 6; plan Task 15.

- 2026-11-17 AGENT-DEFAULT: How does the loader get the schema bytes, given that go:embed cannot reach schemas/ from internal/core/config? → It imports the existing package github.com/syllik/deep-dark-factory/schemas and reads project.json, projects.json and routing.json through schemas.FS(), which already embeds every top-level *.json for this loader (decision #67). No new file is needed. NewLoader compiles them once with jsonschema.SchemeURLLoader{} under mem:///schemas/<file name>, the same way as model/results.go. Task: #97.
- 2026-11-17 AGENT-DEFAULT: The config package must import github.com/santhosh-tekuri/jsonschema/v6 to validate, but the Issue names only the yaml allowlist entry. Add a second entry or reuse model's validator? → Add the second allowlist entry internal/core/config → github.com/santhosh-tekuri/jsonschema/v6 in test/boundaries/boundaries_test.go. The plan's Reuse table names jsonschema/v6 for config validation, and Task 15 Step 1 requires schema validation. model.ResultValidator is specific to role results, and changing internal/core/model is outside the file list. The roughly 30 lines of schema-compile code are duplicated, not shared. The owner confirmed this entry in chat on 2026-11-17. Task: #97.
- 2026-11-17 AGENT-DEFAULT: How are the boundary fixtures changed, now that the violations fixture expects config → yaml to fail? → Clean: a new internal/core/config/a.go imports the yaml and jsonschema stubs, and clean/go.mod gains the yaml require and replace. Violations: config/a.go imports doublestar (allowed only for policy), and model/a.go also imports yaml. The expected list swaps the config → yaml entry for config → doublestar and model → yaml. Task: #97.
- 2026-11-17 AGENT-DEFAULT: Which YAML constructs does the loader accept before schema validation? → Input of at most 1 MiB of valid UTF-8, exactly one document, and nesting depth of at most 32. Anchors, aliases and merge keys are rejected, so alias expansion never happens. Mapping keys must be !!str scalars and duplicates are rejected. Scalars: !!str (no NUL); !!bool only true/True/TRUE/false/False/FALSE; !!null; !!int only as plain decimal ^-?(0|[1-9][0-9]*)$ within int64, emitted as json.Number. !!float, !!timestamp, !!binary, merge and custom tags are rejected with a line, a column and a hint to quote strings. Task: #97.
- 2026-11-17 AGENT-DEFAULT: How are the validated values turned into typed structs? → The JSON-compatible value is json.Marshal-ed and decoded with encoding/json DisallowUnknownFields into a struct pre-filled with the schema defaults. Absent keys keep their default, and the schemas allow null nowhere. yaml.Node is never decoded straight into structs. Task: #97.
- 2026-11-17 AGENT-DEFAULT: Does the loader apply schema defaults, and how is drift from the schema prevented? → Yes. It fills autonomy propose, risk.lowMaxLines policy.DefaultLowMaxLines (200), planning.maxAutoChildren 3, change.merge squash, reviewers.r2DifferentProvider false, projects limits.minFreeDiskGb 15 and maxRunsPerTask 20, and routing limits.maxSubagentsPerRun 3. routing limits.maxConcurrentContainers has no default; 0 means absent. A test compares each one with the default keyword read from the embedded schema. Task: #97.
- 2026-11-17 AGENT-DEFAULT: What are the exported API names? → ErrInvalidConfig; Loader with NewLoader() (*Loader, error); methods Project, Projects and Routing, each taking data []byte; types Project, Projects (with ConnectedProject) and Routing (with Entry); and Project.PolicyProfile() (policy.Profile, error). Task: #97.
- 2026-11-17 AGENT-DEFAULT: When is the policy.Profile validated? → At load. Loader.Project builds PolicyProfile() and returns its Validate error wrapped so that errors.Is matches both ErrInvalidConfig and policy.ErrInvalidProfile. RequiredChecks is nil only when checks.required is absent. Slices are copied. Task: #97.
- 2026-11-17 AGENT-DEFAULT: How are the closed value sets in the config files typed? → autonomy and maxAutonomy use policy.Autonomy, whose UnmarshalText exists. New config types Forge (github, local), Effort (low, medium, high; empty = absent) and MergeMethod (squash) each get a Parse function and UnmarshalText. Tier and role names are struct fields, not enums. Task: #97.
- 2026-11-17 AGENT-DEFAULT: Which checks beyond the schemas does projects.yaml get? → Fail closed on a duplicate id, a duplicate repository among forge github entries, and a duplicate remote among forge local entries. The rule that the operator config repository can never be listed as a project cannot be checked from bytes and stays with the daemon. Task: #97.
- 2026-11-17 AGENT-DEFAULT: Are zone globs and hardStops validated by the loader? → Only by the schema. They are not policy.Profile fields, and the config package does not import doublestar. Their consumer validates them when it lands. Task: #97.
- 2026-11-17 AGENT-DEFAULT: Is internal/core/config added to the boundary test's core-pure package list to enforce 'bytes, never paths'? → No. The Issue limits the boundary change to the allowlist entry and its fixture, and the code conventions name only lifecycle, policy and scheduler as pure. 'Bytes, never paths' is checked by the acceptance grep for os, io/ioutil and path/filepath imports in non-test files, and in review. Task: #97.
- 2026-11-17 AGENT-DEFAULT: How do tests reach the example and template YAML files? → _test.go files read schemas/examples/*.yaml and templates/project/.ddf/project.yaml by relative path (../../../), the way the policy sync test reads docs. Fixtures of their own go in internal/core/config/testdata with placeholder values only. Task: #97.
- 2026-11-17 AGENT-DEFAULT: What if the change exceeds the plan's ~600-line PR target? → Keep it as one PR. The target is advisory, the loader, its typed structs and the allowlist entry belong together, and #86 set the precedent. Keep test tables compact. Task: #97.

## 2026-11-17 M2 Task 9: fakes (Issue #98)

- 2026-11-17 AGENT-DEFAULT: What state survives a sandbox-fake instance restart? → A Host value holds every resource, scripted outcome and counter; each Sandbox instance is created on a Host with an instance name, so a second instance on the same Host lists, kills and destroys the first instance's resources, as recovery does after a daemon restart. Task: #98.
- 2026-11-17 AGENT-DEFAULT: Which resources does one sandbox-fake cell create, and what does Kill do? → CreateCell creates one container and one volume labelled with the task key and run ID; Kill stops the container (it stays listed, frees its capacity, refuses further Run) and DestroyByTaskKey removes every resource of the key. Task: #98.
- 2026-11-17 AGENT-DEFAULT: How are sandbox-fake run outcomes scripted? → A FIFO queue per task key of steps (outcome, output files, or an error); an empty queue gives exit code 0 and no files; GetFiles returns only the requested names the cell has, omitting missing ones without an error, so the caller decides what a missing result.json means. Task: #98.
- 2026-11-17 AGENT-DEFAULT: How does the fake agent treat credentials? → Like the spec §7.3 contract: a nil credential source, a read error or an empty credential returns class credential before any script step is used; test/fakes provides a static credential source. Task: #98.
- 2026-11-17 AGENT-DEFAULT: How does the fake agent build an executor patch from edits? → Whole-file hunks in git diff format (new, modified, deleted text files, mode 100644), with the no-newline marker; binary content or an invalid path is a fatal failure; tests check the patch with git apply, and Task 8b's apply stays the authority. Task: #98.
- 2026-11-17 AGENT-DEFAULT: Does test/fakes/ports.go add fakes beyond notify, secrets, clock and lock? → Only a sequential ID source (ports.IDSource sits next to Clock in ports/clock.go); no forge fake (forge-local is Task 10). Task: #98.

## 2026-11-17 M2 Task 8a-2: base merge and patch-id (Issue #94)

- 2026-11-17 AGENT-DEFAULT: What does 'records its SHA' mean at the workspace layer? → SyncWithBase returns SyncMerged{BaseMerge{MergeSHA, BaseSHA, ParentHeadSHA}}, the base_merges columns of spec §4.5; it writes only objects, no store row, intent or ref, and pushes nothing. The daemon writes the push intent and the base_merges row; the merge commit is deterministic, so it knows the SHA before the forge write. Task: #94.
- 2026-11-17 AGENT-DEFAULT: Which identity and time does the daemon base merge use? → Caller-supplied: Identity{Name, Email} (operator config) as both author and committer, When (clock port) as '@<unix seconds> +0000', and the message; parents are [head, base], so the first-parent chain stays on the change. The identity carries no trust: spec §6.6 rule 4 relies only on the recorded SHA. Task: #94.
- 2026-11-17 AGENT-DEFAULT: How are conflicts detected without touching the clone? → git merge-tree --write-tree --no-messages --name-only -z <head> <base>: exit 0 gives the tree, exit 1 a SyncConflict with the sorted, de-duplicated paths, any other exit an error; a clean tree becomes a commit through commit-tree, which runs no hooks. Neither writes a ref, index or worktree; tests compare for-each-ref before and after. Task: #94.
- 2026-11-17 AGENT-DEFAULT: How are the workflow paths given, and in which order does SyncWithBase check? → The forge's CI path list is a parameter (GitHub: [".github/workflows"]), each entry a clean repository-relative path matched literally through the per-pathspec ':(literal)' magic with --no-renames, so a rename counts on both sides; an empty list is ErrNoWorkflowPaths. Order: validate, commits exist, up to date, workflow check (no merge attempted on a hit, spec §5.2), conflict detection, merge commit. Task: #94.
- 2026-11-17 AGENT-DEFAULT: Which diff is fed to patch-id --verbatim? → diff --binary --full-index --no-renames --no-ext-diff --no-textconv --no-color <merge-base(base, head)> <head>: the whole change against its merge base, so a clean base merge touching other files keeps the ID, binary content and whitespace count, and a base change inside a hunk's context fails closed into a full re-review. An empty diff is ErrEmptyChange; unrelated histories are an error. Task: #94.
- 2026-11-17 AGENT-DEFAULT: What inputs identify commits? → Only full lowercase hex SHAs of 40 or 64 characters, validated before git runs (ErrInvalidSHA); refs and branch names are never accepted, so an argument is never an option or a moving ref. Task: #94.
- 2026-11-17 AGENT-DEFAULT: Which leading global options does Run accept (#96)? → Only '-c <key>=<value>' pairs whose key is user.name, user.email or core.logAllRefUpdates, compared case-insensitively; any other argument before the subcommand (-C, --exec-path, --git-dir, --work-tree, --config-env, --namespace, --literal-pathspecs, another -c key, a -c without a value) is ErrGlobalOption and git is not started. Options after the subcommand are the subcommand's own. run() additionally accepts extraEnv entries keyed only by the six GIT_AUTHOR_*/GIT_COMMITTER_* identity keys. Task: #94.

## 2026-11-17 M2 Task 8b-1: changed paths and patch export (Issue #104)

- 2026-11-17 AGENT-DEFAULT: Does #103 fit one PR under the ~600 changed-line target? → No (~850 lines with tests). DECOMPOSE into 8b-1 (changed paths, export arguments, test fixture; ~370) and 8b-2 (Apply, refusals, probe 4; ~480), in that order; both are sensitive under internal/core/workspace/patch/**, and #103 closes when 8b-2 merges. Task: #104.
- 2026-11-17 AGENT-DEFAULT: What is 'patch export (the trusted-wrapper command of §8.3) as a function'? → ExportArgs(base) returns the git argument lists of 'git add -A && git diff --cached --binary <base>' for the in-container wrapper, adding --full-index, --no-renames, --no-ext-diff, --no-textconv, --no-color, --no-relative and explicit a/ and b/ prefixes so copy-local diff config cannot change the patch. No host function runs the export: a copy's .git/config can define filter.<name> drivers the runner's fixed options cannot override; tests run the arguments only in copies the test wrote. Task: #104.
- 2026-11-17 AGENT-DEFAULT: Which git output feeds ChangedPaths? → Plumbing 'diff-tree -r -z --raw -M --no-abbrev' and 'diff-tree -r -z --numstat -M', zipped by position with equal paths required (policy.md#changed-paths); -z avoids path quoting and plumbing ignores porcelain diff config. An unknown status (C, U, X), a mismatch or a malformed record is an error, never a partial list. Task: #104.
- 2026-11-17 AGENT-DEFAULT: How do the patch tests get git fixtures, given that workspace/git's fixture_test.go cannot be imported? → patch/fixture_test.go keeps a minimal copy of the needed helpers, running git only through git.Runner.Run with -c user.name and -c user.email, and exec.LookPath only in test code. No shared test helper package (the code conventions ban helper packages), and workspace/git is outside the file list. Task: #104.

## 2026-11-17 M2 Task 8b-2: patch apply (Issue #105)

- 2026-11-17 AGENT-DEFAULT: Is an added or changed symlink an apply refusal (failed run), as the plan and Issue wording suggest, or the path-policy outcome? → The path-policy outcome: spec §6.5, §5.2 (Publisher) and roles.md send a revision with an added or changed symlink to needs-owner with hit_stop, and the spec wins over the plan wording. Apply never materialises a symlink (no checkout, objects only), reports it in ChangedPaths with mode 120000, and policy.CheckPaths lists it in Symlinks. The other refusals are failed runs. Task: #105.
- 2026-11-17 AGENT-DEFAULT: Are refusals errors or values? → Values: Apply returns Outcome, either Applied{Tree, ChangedPaths} or Refused{Reason, Paths}, with a nil error (code conventions 'expected outcomes are values', the git.SyncOutcome pattern); Reason is an enum-like string type with ParseReason. An error means no decision could be made: invalid DDF input, a git or I/O failure, a cancelled context. Task: #105.
- 2026-11-17 AGENT-DEFAULT: How does Apply get an index for a bare clean clone without GIT_INDEX_FILE, which the runner refuses, and without editing workspace/git? → A per-call linked worktree: 'worktree add --detach --no-checkout <scratch> <Base>', then read-tree, 'apply --cached' and write-tree inside it; always removed with 'worktree remove --force', falling back to RemoveAll plus 'worktree prune'. The clone's index, refs, HEAD, config and hooks are never touched and two applies never share an index; objects stay unreachable until the Publisher commits (gc.auto=0). The caller passes scratch, since the core reads no environment. Task: #105.
- 2026-11-17 AGENT-DEFAULT: How are 'outside the repository' and '.git/' paths detected deterministically? → A pre-check parses 'git apply --numstat -z' before any object is written, both sides of a rename or copy: a segment equal to .git ignoring case is git-dir; an empty path, a leading '/', or an empty, '.' or '..' segment is outside-repository. The same rules are re-checked on the applied tree's raw diff; git's verify_path with core.protectHFS and core.protectNTFS backstops variant spellings as does-not-apply. Precedence: git-dir, outside-repository, gitlink, node-modules. Task: #105.
- 2026-11-17 AGENT-DEFAULT: What counts as a node_modules addition, and how does 'unless the project profile allows it' apply? → An added entry (status A in a --no-renames diff of Base to the applied tree, so a rename into it counts) with a path segment exactly 'node_modules' at any depth; modifying or deleting tracked files is not an addition. schemas/project.json has no allow key in V1, so Apply has no allow option; a later profile key would add an Input field. Task: #105.
- 2026-11-17 AGENT-DEFAULT: What about gitlinks (mode 160000) in a patch? → Refused as a failed run (ReasonGitlink) when added or changed: 'git add -A' records a nested repository as a gitlink pointing at content no reviewer can see. Fail-closed, beyond the spec's explicit list. Task: #105.
- 2026-11-17 AGENT-DEFAULT: Against which commit are the returned changed paths measured? → Input.ChangedFrom, separate from the patch base Input.Base: per policy.md#changed-paths rule 1 the changed paths run from the merge base of the integration branch and the change to the applied tree, so a correction batch still reports every path since the merge base. Task: #105.
- 2026-11-17 AGENT-DEFAULT: Is the patch size bounded? → MaxPatchBytes = 64 MiB; a larger patch is Refused too-large before git runs, which bounds host memory and object writes. Task: #105.
- 2026-11-17 AGENT-DEFAULT: What is an empty patch? → No bytes, or an applied tree equal to Base's tree, is Refused empty, a failed run: a revision without a change cannot be published. Task: #105.
- 2026-11-17 AGENT-DEFAULT: What exactly does the probe-4 test plant, and which functions does it run? → Marker hooks in the source's .git/hooks, the clean clone's hooks/ and the run copy's .git/hooks; hook, fsmonitor, filter, diff and merge driver config in the source's and the copy's .git/config (the copy's only after its export); a committed '* filter=evil diff=evil merge=evil' .gitattributes; the same keys through HOME, XDG and GIT_CONFIG_* plus bogus GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE and GIT_OBJECT_DIRECTORY. It runs git.Runner.CleanClone, Apply (applied and refused), git.Runner.SyncWithBase (merged and conflict) and ChangedPaths; no marker may exist afterwards. All source commits precede the planting. Task: #105.
- 2026-11-17 Owner decision (#111): How does the daemon get the credential for an ssh:// forge-local remote? → A dedicated SSH deploy key per ssh:// project plus a pinned known_hosts, both in the secrets mount, read through secrets-file, used only by the daemon's git on the host; never in a container. Task: #111.
- 2026-11-17 Owner decision (#111): How does the operator create a ready task without a forge? → A Markdown task file dropped into forge-local's inbox/; the daemon ingests it into a numbered task record. Task: #111.
- 2026-11-17 AGENT-DEFAULT: Where does forge-local's own storage live (decision 7)? → <data>/forge-local/<project id>/ (macOS colima profile: ~ddf/work/forge-local/<project id>/), owned by ddf, directories 0700, files 0600; never in the remote, a daemon clone, a run copy or a container. Task: #111.
- 2026-11-17 AGENT-DEFAULT: What is the storage record format? → One JSON object per file with "schema": "ddf.forge-local.<kind>/v1"; unknown fields refused; a format file holds ddf.forge-local/v1 and any other value is refused; writes are temp file, fsync, rename. Task: #111.
- 2026-11-17 AGENT-DEFAULT: Where does the task-key marker live for a forge-local change? → In the change record in forge-local's storage (and in task records DDF creates); never in branch names, commit messages or refs; find change by task key reads change records only. Task: #111.
- 2026-11-17 AGENT-DEFAULT: What are checks and rerun on forge-local? → One check named verify per head, run as attempts in a fresh container through the Sandbox port; publish, update from base and merge start an attempt; rerun starts a new attempt; a lost attempt after restart is replaced and is not a rerun. Task: #111.
- 2026-11-17 AGENT-DEFAULT: How does forge-local revert? → git revert of the DDF merge commit on the integration tip, pushed to a new change branch (change.branch with {type} revert) and recorded as a change of kind revert with the same task-key marker; a revert that does not apply cleanly pushes nothing and returns an error. Task: #111.
- 2026-11-17 AGENT-DEFAULT: What is the forge-local task source? → forge local, repository = project id, source kind task or request, source id = decimal task number allocated by the daemon. Task: #111.
- 2026-11-17 AGENT-DEFAULT: What is the reviewer-result field for a prior finding's ID called, and does the schema version change? → `priorId` (optional string), matching `model.Finding.PriorID` and the store column `prior_id`; the schema stays `ddf.reviewer.result/v1`. Task: #110.
- 2026-11-17 AGENT-DEFAULT: Which prior findings does a re-reviewer receive and may it reference? → the findings, ids included, of the latest valid review of its own role on an earlier head of the same change, plus the wontFix list; none on a first review; `priorId` names only a finding of that review. Task: #110.
- 2026-11-17 AGENT-DEFAULT: What counts as the same finding blocking twice? → a blocking finding whose `priorId` names a finding blocking in the prior review of the same role → `needs-owner`; no transitive chains; category, location and text never compared. Task: #110.
- 2026-11-17 AGENT-DEFAULT: What happens to a `priorId` that names no finding of the prior review? → invalid result (one retry, then `needs-owner`), checked by the lifecycle (Task 6b) and by the session in manual mode. Task: #110.
- 2026-11-17 AGENT-DEFAULT: Must finding ids and priorIds be unique within one reviewer result? → both unique, checked by `model.ResultValidator.Validate` since JSON Schema cannot express it. Task: #110.
- 2026-11-17 AGENT-DEFAULT: May the change touch spec §4.5, §6.3 and `docs/concepts/policy.md` beyond the Issue's file list? → yes, one line each, so those finding shapes stay correct. Task: #110.

## 2026-11-17 M2 Task 11a: publisher text (Issue #114)

- 2026-11-17 AGENT-DEFAULT: What does {task} render to, and where may agent text appear? → {task} = 'Closes <TaskRef>', the only closing keyword DDF writes. {summary} is DDF-written (task title, risk class, changed paths); agent text appears only in {agentNotes}. HTML comments are removed from the template before substitution, substitution is single-pass, and the whole body is redacted last. Task: #114.
- 2026-11-17 AGENT-DEFAULT: What exactly does the sanitizer strip? → Closing keywords before a task reference (keyword removed, reference kept), CI-skip directives ('[skip ci]' family, 'skip-checks: true', '***NO_CI***'), the '@' of mentions (e-mail addresses kept), zero-width and bidi controls, control characters; '<!--' and '-->' are escaped; applied to a fixed point (else '[agent text withheld]'), capped at 16 KiB. Task: #114.
- 2026-11-17 AGENT-DEFAULT: What is the squash message? → Sanitized title, the DDF summary and 'Closes <TaskRef>', redacted; the same text is the revision commit message and is returned as Ready.SquashMessage for the merge. Task: #114.
- 2026-11-17 AGENT-DEFAULT: How does the redactor hold loaded values and which values does it redact? → Redactor keeps []ports.Secret and prints as '[redactor]'; every non-empty loaded value is redacted regardless of length, longest first, before the fixed pattern table (private keys, GitHub tokens and PATs, AWS key IDs, Slack, sk- API keys, Telegram bot tokens, Google API keys, JWTs, URL credentials). Task: #114.
- 2026-11-17 AGENT-DEFAULT: Does an '@' after a backtick or a non-ASCII letter keep its mention exemption (spec rule d lists backtick and 'letter, digit')? → No (review finding r1-2 on PR #117): a backtick may close a code span right before the '@', and GitHub's mention boundary is ASCII, so only an ASCII letter or digit, '_', '.', '-', '/' or '@' before the '@' keeps it; '`@x`' becomes '`x`' (fail closed). Each agent- or input-controlled field is also redacted before it is sanitized, and the whole text again last. Task: #114.

## 2026-11-17 M2 Task 11b: commit primitive and local gate (Issue #115)

- 2026-11-17 AGENT-DEFAULT: How is the revision commit made with a clock-port date when Runner.Run refuses identity env? → Task 11b adds Runner.CommitTree to internal/core/workspace/git (strictly necessary; objects only, no ref, no push); SyncWithBase may reuse it with unchanged behaviour. Task: #115.
- 2026-11-17 AGENT-DEFAULT: On what does the local gate run, given the applied tree is not a commit? → Prepare commits the applied tree first (unreachable object, not a forge write), then LocalGate makes a per-run copy of that commit with git.NewRunCopy and hands it to the Sandbox port as fs.FS content; nothing is pushed unless the gate passes. Task: #115.
- 2026-11-17 AGENT-DEFAULT: How are the profile's verify commands run in the gate? → One CellRun per command, argv ['sh','-c',cmd] inside the cell, in order, stopping at the first non-zero exit or timeout; an empty verify list is an error (fail closed); sandbox errors are errors, not gate failures. Task: #115.
