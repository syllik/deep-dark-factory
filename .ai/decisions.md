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

- 2026-11-12 Owner decision: plan OD-1..OD-14 recommendations accepted; toolchain go1.27.4 (plan: go1.27.3); modernc.org/sqlite v1.62.0 (plan: v1.61.1, minor, owner-approved); CI verify fails when go env GOVERSION differs from the go.mod toolchain line (#39 r1-2). Task: #63.
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
