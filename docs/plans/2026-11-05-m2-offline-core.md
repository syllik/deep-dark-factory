# M2 Offline Core Implementation Plan

Purpose: the task-by-task plan for milestone M2, the offline core that runs the whole DDF lifecycle in tests with fakes.

Status: proposed; accepted when the owner merges this PR

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Subagents never commit, push or change GitHub; the orchestrator session commits, opens PRs and closes Issues. Every task runs by [manual mode](../orchestrator/manual-mode.md) and the owner merges.

**Goal:** Implement the offline core of [spec §4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider) (all modules except `context`, which is M3), plus `forge-local`, `sandbox-fake`, a fake agent and the daemon wiring needed to run the whole lifecycle in tests: offline, with no network, forge or provider ([spec §14](../specs/2026-11-03-ddf-v1-design.md#14-milestones), M2 row).

**Architecture:** One npm package ([spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries)).

- `src/core/<module>/` per core module; `src/core/ports/` holds the port interfaces of [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters) (types only).
- `src/adapters/forge-local/`, `src/adapters/sandbox-fake/`.
- The engine (tick: advance tasks through pure lifecycle and policy, call ports, record intents; recovery) lives in `src/daemon/` (OD-5). The fake agent lives in `test/fakes/` (OD-6). End-to-end scenarios live in `test/e2e/`. Unit tests sit next to their module as `*.test.ts`. No `src/cli` in M2.
- Core never imports adapters or the daemon. Adapters import only core (ports, model and the workspace git helper). Only the daemon wires them.
- `lifecycle`, `policy` and `scheduler` are pure: they take a clock and inputs and do no I/O. `store` is the only module that touches SQLite ([M0 fact](../research/2026-11-m0-facts.md#nodesqlite-16-item-6), §16.6 line in [`.ai/decisions.md`](../../.ai/decisions.md)). `workspace` is the only core module that spawns `git`.

**Tech Stack:**

- Node 24.21.0; `engines` `>=24.15.0 <25` (`node:sqlite` needs ≥ 24.15.0: [spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries), [M0 fact](../research/2026-11-m0-facts.md#nodesqlite-16-item-6)). The pin lives in CI `setup-node` and `engines`; no `.nvmrc`.
- TypeScript sources run directly by Node's built-in type stripping (no warning on 24.21.0). Erasable syntax only: no `enum`, `namespace` or parameter properties. `tsconfig.json`: `strict`, `noEmit`, `erasableSyntaxOnly`, `verbatimModuleSyntax`, `allowImportingTsExtensions`, `module`/`moduleResolution` `nodenext`; imports carry `.ts` extensions. Type check with `tsc --noEmit` (line per OD-1). No build step in M2; packaging is decided by the plan that ships `ddfd`.
- Tests: `node:test` + `node:assert/strict`. `npm test` = `node --test "src/**/*.test.ts" "test/**/*.test.ts"`. Deterministic: injected clock and ID source, temp dirs under `os.tmpdir()`, no network. Git fixtures are made with the `git` CLI in temp dirs, identity passed by `-c user.name=… -c user.email=…` (no global config).
- `package.json` scripts: `typecheck`, `test`, `verify` (= typecheck + test + `node scripts/docs-check.mjs`).
- Git: the system `git` CLI through `node:child_process.execFile` (argument arrays, no shell). Scrubbed env (`GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_TERMINAL_PROMPT=0`), `-c core.hooksPath=<empty dir>`, `-c core.fsmonitor=false`, no filter drivers ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)). `git patch-id --verbatim` verified on git 2.56; Task 8 records the minimum git version its tests need and fails with a clear message on an older git.

**Spec:** [`docs/specs/2026-11-03-ddf-v1-design.md`](../specs/2026-11-03-ddf-v1-design.md): §4 (modules, ports, boundaries, store), §5 (lifecycle, bounds), §6 (policy, gate, signatures, profile), §8.3, §9, §10.1, §12, §14, [§14.1](../specs/2026-11-03-ddf-v1-design.md#141-v1-acceptance). Operational canon: [lifecycle](../concepts/lifecycle.md), [policy](../concepts/policy.md), [roles](../concepts/roles.md#publisher), [identity](../concepts/identity.md#workspaces-and-publication), [config layers](../concepts/config-layers.md), [forge-local](../adapters/forge-local.md#contract), [schemas](../../schemas/).

## Global Constraints

- English only. Public repository: no IPs, hostnames, tokens, private repository names or personal logins. No model names; roles name tiers.
- Every dependency has a recorded reason ([spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries)). All dependencies are added in Task 1 only. A later task that needs one stops and asks: `package.json` is a `ciEntrypoint`, so that task's class becomes `sensitive`.
- Each task = one GitHub Issue (child of #24, created after this plan merges) = one branch `<type>/issue-<n>-<slug>` = one PR run by manual mode; the owner merges. PRs stay small: one module plus its tests per PR, target < ~600 changed lines excluding the lockfile; split further if larger.
- Code comments link a spec or concept anchor only where a rule is enforced (e.g. `// spec §6.6 rule 4`). No restated tables in code docs.
- Each refusal reason, bound and pause reason is an enumerated value with its own test ([spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate)).
- The host never executes repository content: `sandbox-fake` returns scripted verify outcomes and never runs workspace files ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)).
- No executor edits a [forbidden path](../orchestrator/manual-mode.md#forbidden-paths). The two forbidden files M2 needs (`.github/workflows/ci.yml`, root `.gitignore`) land per OD-3.
- Rules, states, bounds and gate conditions are implemented from the linked spec and concept sections; this plan names them, it does not restate them.

## Review Focus

1. Core importing an adapter, the daemon, network or provider modules: the import-boundary test (Task 2) fails.
2. `evaluateMerge` returning `MERGE` on any partial condition: one test per refusal reason of [spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate), fail-closed on read errors.
3. Review signatures: canonical JSON exactly as §6.6, a replayed or forged marker rejected ([§14.1](../specs/2026-11-03-ddf-v1-design.md#141-v1-acceptance) probe 1), the key only injected through the secrets port.
4. Patch apply: symlink, `..` or absolute path, `.git/` path and forbidden path refused before any forge write ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication), probes 3 and 4).
5. An intent written before every forge write and reconciled by task-key marker on restart; no blind retry ([lifecycle.md](../concepts/lifecycle.md#store-intents-and-leases), probe 7).
6. Bounds and pause reasons exactly as [lifecycle.md#bounds](../concepts/lifecycle.md#bounds) and [#global-pause](../concepts/lifecycle.md#global-pause); only `runtime` lifts automatically.
7. Pinned merge: a head moved between evaluation and merge fails the compare-and-swap (probe 8).

## Reuse (generic primitives)

| Primitive | Chosen | Considered | Reason |
|---|---|---|---|
| Persistence | `node:sqlite` ([spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries)) | better-sqlite3; drizzle, kysely | The spec mandates `node:sqlite` with no dependency. Migrations are ordered SQL files applied by `store`, tracked in a `schema_version` table. |
| Schema validation (role results, config) | `ajv` 8.x (`ajv/dist/2020`, draft 2020-12, MIT; npm latest 8.20.0 on 2026-11-06) (OD-2) | hand-written validators; zod; @cfworker/json-schema | `schemas/*.json` stay the single normative source; zod would duplicate them; hand-written validators drift. |
| YAML (`.ddf/project.yaml`, `routing.yaml`, `projects.yaml`) | `yaml` 2.x (ISC, no dependencies; npm latest 2.9.1) (OD-2) | js-yaml; JSON-only configs | Node has no YAML parser; the config formats are fixed by the spec. |
| Test runner, assertions | `node:test`, `node:assert` | vitest; jest | Built-ins first; no transform needed with type stripping. |
| TypeScript execution | Node built-in type stripping | tsx; ts-node; a build step | No dependency; verified on 24.21.0. |
| Type checking | `typescript`, dev only (OD-1) | skipping type checks | Strict types are the cheapest deterministic check. |
| Node types | `@types/node` 24.x, dev only | none | Matches the runtime line. |
| CLI argument parsing | not needed in M2; `node:util` `parseArgs` when the CLI comes (M4) | commander | The built-in suffices for the [§4.3](../specs/2026-11-03-ddf-v1-design.md#43-entry-points) commands. |
| Glob matching (risk paths, forbidden, zones, sensitive) | `node:path` `matchesGlob` (OD-11) | picomatch; minimatch | Built-in. Semantics pinned by a fixture test of every glob in [spec §6.5](../specs/2026-11-03-ddf-v1-design.md#65-path-policy), [§12](../specs/2026-11-03-ddf-v1-design.md#12-ddfs-own-profile) and [`project.yaml`](../../templates/project/.ddf/project.yaml). A gap means adopting picomatch through OD-2's process. |
| Canonical JSON for signatures | own function: sorted keys, UTF-8; strings, integers, booleans, null, arrays, objects only; refuses anything else | `canonicalize` (RFC 8785) | The signed payload is fixed and small; no dependency inside sensitive signature code; RFC 8785 number rules are not needed (no floats). |
| HMAC-SHA256, SHA-256, random IDs | `node:crypto` | none | Built-in. |
| Git access | `git` CLI via `execFile` | isomorphic-git; simple-git | isomorphic-git lacks `apply` with the needed refusals and `patch-id --verbatim`; simple-git is a thin wrapper that adds a dependency without removing risk. |
| Import-boundary test | own `node:test` test parsing import/export specifiers in `src/**/*.ts` | dependency-cruiser (MIT, maintained, 18.x) | A handful of rules do not justify a large dev dependency tree; revisit if the rules grow. |
| Lint/format | none in M2 (OD-4) | Biome; ESLint + Prettier | See OD-4. |
| Instance file lock | lock port with an in-memory fake; the OS lock comes with `ddfd` (OD-9) | proper-lockfile | No consumer before `ddfd`. |

## Hosted CI and repository settings

- New workflow `.github/workflows/ci.yml` (lands per OD-3): `on: pull_request` and `push` to `master`; `permissions: contents: read`; one job with check name `verify` on `ubuntu-latest` (OD-12); `actions/checkout` with `persist-credentials: false`; `actions/setup-node` with `node-version: 24.21.0` and npm cache; steps `npm ci`, `npm run typecheck`, `npm test`. No secrets. Actions pinned by full commit SHA (OD-13). `docs-check.yml` stays unchanged and required.
- Required checks after M2 starts: `docs-check` and `verify`.
- Owner actions (sessions never change repository settings; [what goes to the owner](../orchestrator/manual-mode.md#what-goes-to-the-owner)):
  1. after the Task 1 PR has run `verify` once, add `verify` to the required status checks of the `master` ruleset, next to `docs-check`;
  2. keep squash-only merges and no bypass actors;
  3. optional: enable Dependabot security alerts for npm.
- Until owner action 1 is done, sessions treat `verify` as required anyway (manual mode step 6 waits for every check).

---

Risk classes below come from [DDF's own profile](../orchestrator/manual-mode.md#ddfs-own-profile-until-ddfprojectyaml-exists): first matching row per file, highest over the files, as amended by Task 0.

### Task 0: DDF profile update (OD-7)

**Files:** Modify `docs/orchestrator/manual-mode.md` ("DDF's own profile" rows only).
**Risk class:** `standard` (`docs/orchestrator/**` row).
**Depends on:** this plan merged. Lands before Task 1 so later classes are computed from it.

- [ ] **Step 1:** `ciEntrypoints` += `package-lock.json`, `tsconfig.json`, `test/boundaries.test.ts`.
- [ ] **Step 2:** `sensitive` += `src/core/publisher/**`, `src/core/store/migrations/**` (merge code stays in forge adapters, already sensitive).
- [ ] **Verify:** `node scripts/docs-check.mjs` → `docs-check: OK`.

### Task 1: Scaffold and CI

**Files:** Create `package.json`, `package-lock.json`, `tsconfig.json`, `src/core/index.ts` (empty export), `test/smoke.test.ts`; `.github/workflows/ci.yml` and root `.gitignore` (`node_modules/`) per OD-3. Modify `.ai/decisions.md` (section "Dependencies", OD-2).
**Risk class:** `sensitive` (`package.json` and `package-lock.json` are `ciEntrypoints`; two forbidden paths).
**Depends on:** Task 0.

- [ ] **Step 1:** `package.json` with `engines`, `type: module`, scripts `typecheck`, `test`, `verify`; exact versions (`save-exact`) of the OD-2 set; lockfile committed.
- [ ] **Step 2:** `tsconfig.json` as in Tech Stack; `src/core/index.ts`; `test/smoke.test.ts` (one passing test).
- [ ] **Step 3:** `.ai/decisions.md` "Dependencies": one line per dependency with its reason.
- [ ] **Step 4:** `ci.yml` and `.gitignore` edited by the session itself under the owner's chat authorization (OD-3, option a).
- [ ] **Verify:** `npm ci && npm run verify`; hosted `verify` green on the PR.

### Task 2: Model, ports, import boundaries

**Files:** Create `src/core/model/**`, `src/core/model/results.ts`, `src/core/ports/**`, `test/boundaries.test.ts`.
**Risk class:** `sensitive` (`test/boundaries.test.ts` is a `ciEntrypoint` after Task 0; the rest would be `standard` by size).
**Depends on:** Task 1.
**Interfaces:** Produces the record types and the forge, agent, sandbox, notify, secrets, clock and lock ports every later task consumes.

- [ ] **Step 1:** Types for Task, Run, Change, Review, Finding, Question, Decision, Intent, Event; `ddf.<name>/v1` ID constants (OD-8); the task-key digest ([spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)).
- [ ] **Step 2:** Port interfaces from [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters), every forge write with dry-run.
- [ ] **Step 3:** `results.ts`: validate role results with `ajv` against `schemas/*.result.json`; tests with the `schemas/examples/` files and one invalid result per schema.
- [ ] **Step 4:** `test/boundaries.test.ts` rules: core ↛ adapters/daemon/cli; core ↛ `node:http|https|net|tls|dgram|dns` or global `fetch`; only `src/core/workspace/**` imports `node:child_process`; only `src/core/store/**` imports `node:sqlite`; adapters ↛ other adapters. A fixture proves each rule fails.
- [ ] **Verify:** `npm run verify`.

### Task 3: Store

**Files:** Create `src/core/store/**` (including `migrations/**`).
**Risk class:** `sensitive` (`src/core/store/migrations/**` row from Task 0; the rest would be `standard`).
**Depends on:** Task 2.

- [ ] **Step 1:** Tables of [spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents), migrations, transactions.
- [ ] **Step 2:** Append-only `events` (no update or delete API); intents with before/after and an unresolved query; leases with expiry from the clock ([lifecycle.md](../concepts/lifecycle.md#store-intents-and-leases)).
- [ ] **Step 3:** Tests: journal append-only, intent lifecycle, expired lease released, reopen of a file database.
- [ ] **Verify:** `npm run verify`.

### Task 4: Policy A, classification

**Files:** Create `src/core/policy/**` (classification part).
**Risk class:** `sensitive` (`src/core/policy/**` row).
**Depends on:** Task 2.

- [ ] **Step 1:** Effective autonomy as the minimum of the layers ([policy.md](../concepts/policy.md#autonomy-levels), [config layers](../concepts/config-layers.md#resolution-order)).
- [ ] **Step 2:** Risk class from diff and profile ([policy.md](../concepts/policy.md#risk-classes)): lockfile and rename exclusion, `ciEntrypoints`, agent-instruction files never `docs`; reviewer set; blocking findings ([#findings](../concepts/policy.md#findings)).
- [ ] **Step 3:** Path policy: defaults of [spec §6.5](../specs/2026-11-03-ddf-v1-design.md#65-path-policy) plus project `forbidden`, symlinks; hard-stop detection by glob ([#hard-stops](../concepts/policy.md#hard-stops)).
- [ ] **Step 4:** Glob fixture suite (OD-11).
- [ ] **Verify:** `npm run verify`.

### Task 5: Policy B, merge gate and signatures

**Files:** Modify `src/core/policy/**` (add the gate and signature files).
**Risk class:** `sensitive` (`src/core/policy/**` row; review-signature code).
**Depends on:** Task 4.
**Interfaces:** Produces `evaluateMerge` and its refusal enum (consumed by Task 6).

- [ ] **Step 1:** `evaluateMerge` on inputs only (no git): one enumerated refusal per [§6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate) condition, including the ancestor / daemon base merge / unchanged patch-id exception; fail-closed on read errors.
- [ ] **Step 2:** Review signature sign/verify, canonical JSON, `findingsDigest`; key passed in, never read from disk by policy.
- [ ] **Step 3:** One test per refusal; forged and replayed signatures rejected (probe 1).
- [ ] **Verify:** `npm run verify`.

### Task 6: Lifecycle

**Files:** Create `src/core/lifecycle/**`.
**Risk class:** `sensitive` (`src/core/lifecycle/**` row).
**Depends on:** Tasks 2, 5 (refusal enum).

- [ ] **Step 1:** Pure transitions for every state and flag of [lifecycle.md#conveyor](../concepts/lifecycle.md#conveyor); planner, executor and reviewer verdict routing.
- [ ] **Step 2:** Finding identity across rounds, follow-up and minors routing with loop guard ([#fixing](../concepts/lifecycle.md#fixing)).
- [ ] **Step 3:** Bounds counters for every row of [#bounds](../concepts/lifecycle.md#bounds), with the revision definition; merge-refusal mapping ([#merge-refusals](../concepts/lifecycle.md#merge-refusals)).
- [ ] **Step 4:** Revert rule with the 24 h window from the clock ([#revert](../concepts/lifecycle.md#revert)); global pause set and lift rule, only `runtime` automatic ([#global-pause](../concepts/lifecycle.md#global-pause)).
- [ ] **Verify:** `npm run verify`.

### Task 7: Scheduler

**Files:** Create `src/core/scheduler/**`.
**Risk class:** default row: `standard` if > 200 lines, else `low`.
**Depends on:** Task 2.

- [ ] **Step 1:** Slots (`maxConcurrentContainers` counts every container); zones held from running to merge; dependencies → waiting.
- [ ] **Step 2:** Priority order of [lifecycle.md#ready-and-waiting](../concepts/lifecycle.md#ready-and-waiting); paused → no new runs.
- [ ] **Verify:** `npm run verify`.

### Task 8: Workspace

**Files:** Create `src/core/workspace/**`.
**Risk class:** `sensitive` (`src/core/workspace/**` row).
**Depends on:** Task 2.

- [ ] **Step 1:** Git helper with scrubbed env (Tech Stack); minimum git version check.
- [ ] **Step 2:** Clean clone with hooks, fsmonitor and filters neutralised; per-run copy without remotes ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)).
- [ ] **Step 3:** Patch export (the trusted-wrapper command of §8.3 as a function); patch apply with refusals: symlink, outside the repository, `.git/`, additions under `.opencode/`, `.claude/`, `node_modules/`.
- [ ] **Step 4:** `patch-id --verbatim`; daemon base merge that records its SHA; conflict detection; workflow-file change check since the merge base ([#sync-with-base](../concepts/lifecycle.md#sync-with-base)).
- [ ] **Step 5:** Tests include planted hooks and config that must not run (probe 4).
- [ ] **Verify:** `npm run verify`.

### Task 9: Fakes

**Files:** Create `src/adapters/sandbox-fake/**`, `test/fakes/agent-fake.ts` (OD-6), `test/fakes/ports.ts`.
**Risk class:** `sensitive` (`src/adapters/sandbox-*/**` row).
**Depends on:** Task 2.

- [ ] **Step 1:** `sandbox-fake`: create/run/kill, scripted verify outcomes, container accounting, list and kill containers of a previous instance; never runs workspace files.
- [ ] **Step 2:** `agent-fake`: scripted per role; results validated by the real schemas; the executor script returns a patch.
- [ ] **Step 3:** `test/fakes/ports.ts`: in-memory fakes for the notify (records sent notifications), secrets (by name), clock (manual advance) and lock (the OD-9 fake) ports.
- [ ] **Verify:** `npm run verify`.

### Task 10: forge-local

**Files:** Create `src/adapters/forge-local/**`.
**Risk class:** `sensitive` (merge code in `src/adapters/forge-*/**` row).
**Depends on:** Tasks 3, 8.

- [ ] **Step 1:** Full forge port per [forge-local.md#contract](../adapters/forge-local.md#contract) on a `file://` bare repository plus the store: tasks; intake actor = local operator; change = branch with task-key marker; reviews in the store.
- [ ] **Step 2:** Checks = verify through the sandbox port; merge by compare-and-swap ref update pinned to the head ([spec §10.1](../specs/2026-11-03-ddf-v1-design.md#101-forge-local)); revert; dry-run for every write.
- [ ] **Step 3:** Tests: moved integration branch → `behind base`; moved head → merge fails (probe 8).
- [ ] **Verify:** `npm run verify`.

### Task 11: Publisher

**Files:** Create `src/core/publisher/**`.
**Risk class:** `sensitive` (Publisher row, `src/core/publisher/**` from Task 0).
**Depends on:** Tasks 4, 8.

- [ ] **Step 1:** Apply the patch to a clean clone (workspace); path policy and risk class on the applied tree; local gate through the sandbox port before push; one commit and one push per revision ([roles.md#publisher](../concepts/roles.md#publisher)).
- [ ] **Step 2:** Change body and squash message from [`change-template.md`](../../templates/project/.ddf/change-template.md); sanitizer for closing keywords, `[skip ci]`-style directives and mentions; `Closes` only for the task.
- [ ] **Step 3:** Redaction by secret patterns and loaded values; a patch carrying a loaded value is refused (OD-10, [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)).
- [ ] **Verify:** `npm run verify`.

### Task 12: Engine and recovery

**Files:** Create `src/daemon/**` (OD-5).
**Risk class:** `standard` (default row by size) unless it touches a sensitive path.
**Depends on:** Tasks 3, 6, 7, 9, 10, 11.

- [ ] **Step 1:** Tick: read forge → lifecycle, policy and scheduler decisions → port calls; each forge write wrapped in an intent; run timeouts from the clock; dry-run mode.
- [ ] **Step 2:** Recovery on start per [spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery): lock port (OD-9), kill previous containers, discard runs within budget, reconcile intents by task-key marker, unreconcilable → `needs-owner`.
- [ ] **Verify:** `npm run verify`.

### Task 13: End-to-end lifecycle suite (M2 exit)

**Files:** Create `test/e2e/**`.
**Risk class:** `standard` (default row by size).
**Depends on:** Task 12.

Scenarios on `forge-local` + `sandbox-fake` + `agent-fake` + a file store:

- [ ] **Step 1:** Happy path intake → merge (`merge-integration`) and propose (stops at a ready change); decompose with ≤ and > `maxAutoChildren`.
- [ ] **Step 2:** Fixing with re-review; the same finding twice → `needs-owner`; CI rerun flake.
- [ ] **Step 3:** Sync clean, sync conflict batch; base workflow change → `needs-owner` without consuming a batch.
- [ ] **Step 4:** Every row of [lifecycle.md#merge-refusals](../concepts/lifecycle.md#merge-refusals); revert, and a second red within 24 h → pause `revert-red`.
- [ ] **Step 5:** Restart mid-run and mid-intent (probe 7: no duplicate task or change); probes 1, 3, 4, 5, 8 with fakes; every bound exceeded → `needs-owner`.
- [ ] **Verify:** `npm run verify`; the scenario names map 1:1 to the [§14](../specs/2026-11-03-ddf-v1-design.md#14-milestones) M2 exit criterion: intake → merge, fixing, sync, gate refusals, revert, restart recovery, with fakes.

### Task 14: M2 close-out docs

**Files:** Modify `.ai/context.md`, `ARCHITECTURE.md`, `README.md` (status lines); adapter and concept `Status:` lines that say "implemented in M2".
**Risk class:** `standard` (`.ai/**` row).
**Depends on:** Task 13.

- [ ] **Step 1:** Update the status lines; check every "implemented in M2" claim is true.
- [ ] **Verify:** `node scripts/docs-check.mjs`.

## PR order and parallelism

Each PR is branched from up-to-date `master` after its dependencies merged; at most 3 lanes at once; the owner merges in order. Parallel PRs never touch the same files (no `package.json` edits after Task 1).

```text
0 → 1 → 2 ─┬─ wave A: 3 store | 4 policy A | 7 scheduler
           ├─ wave B: 8 workspace | 9 fakes | 5 policy B (after 4)
           ├─ wave C: 6 lifecycle (after 5) | 10 forge-local (after 3, 8) | 11 publisher (after 4, 8)
           └─ 12 engine (after 3, 6, 7, 9, 10, 11) → 13 e2e → 14 close-out
```

## Open decisions for the owner

Tasks assume each **Recommendation**.

**OD-1 TypeScript line.** Which `typescript` major? Options: 7.0.x (native compiler, npm `latest` 7.0.2, platform binary packages); 6.0.x (last JS-based line, 6.0.3). **Recommendation:** 7.0.x pinned exactly; fall back to 6.0.x if `tsc` 7 misbehaves on CI. Pros: maintained line, fast. Cons: very new major, per-platform binaries.

**OD-2 Dependency set and where reasons are recorded.** Options: runtime `ajv`, `yaml`, dev `typescript`, `@types/node`, nothing else, reasons in `.ai/decisions.md`; no runtime dependencies (hand-written validators, JSON-only configs); reasons in a separate `docs/dependencies.md`. **Recommendation:** the four packages, exact versions (`save-exact`), lockfile committed, one reason line each in `.ai/decisions.md` section "Dependencies", added in Task 1. Pros: schemas stay normative, small tree, one decision log. Cons: two runtime dependencies to track.

**OD-3 How the forbidden files land** (`.github/workflows/ci.yml`, root `.gitignore`). Options: (a) the owner authorizes the session in chat to edit exactly these two paths in Task 1 ([manual mode option 2](../orchestrator/manual-mode.md#forbidden-paths)); (b) the owner adds them in their own PR right after Task 1 merges. **Recommendation:** (a). Pros: Task 1 is `sensitive` anyway and gets a hosted `verify` run. Cons of (b): Task 1 has no hosted `verify` run and must keep `node_modules/` out by hand.

**OD-4 Lint/format in M2.** Options: none (tsc strict + tests); Biome (one dev dependency, fast, platform binaries); ESLint + Prettier (large tree). **Recommendation:** none. Pros: no dependency, no config churn. Cons: style drift across agent-written PRs.

**OD-5 Where the engine lives.** Options: `src/daemon/`; `src/core/engine/`. **Recommendation:** `src/daemon/`. Pros: "the daemon wires them" ([§4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries)); core stays pure. Cons: §4.4 lists only `src/core`, `src/adapters`, `src/cli`, so this adds a top-level folder; `src/core/engine/` would need core to call ports, blurring "pure".

**OD-6 Fake agent location.** Options: `test/fakes/agent-fake.ts`; `src/adapters/agent-fake/`. **Recommendation:** `test/fakes/`. Pros: not a V1 adapter, cannot be wired into a release, default risk rules. Cons: `src/adapters/agent-fake/` would match `src/adapters/agent-*/**` → `sensitive`, R2 at `deep` for test code; the two fakes live in different trees (`sandbox-fake` stays in `src/adapters/` because §4.2 names it an adapter).

**OD-7 DDF profile additions (Task 0).** Options: add `ciEntrypoints` `package-lock.json`, `tsconfig.json`, `test/boundaries.test.ts` and `sensitive` `src/core/publisher/**`, `src/core/store/migrations/**`; leave the profile as it is. **Recommendation:** add them as Task 0, before Task 1. Pros: the Publisher row gets a path; migrations and CI inputs get R2. Cons: more R2-at-`deep` reviews; spec §12 prose may want the same paths (a Changelog line if so).

**OD-8 Versioned schemas for internal records** (Task, Run, … [§4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider)). Options: TypeScript types plus `ddf.<name>/v1` ID constants stored with each persisted JSON payload, JSON Schema files only for data that crosses a trust boundary (role results, config); JSON Schema for every record. **Recommendation:** the first. Pros: no duplicate of the types. Cons of the alternative: more `schemas/**` files, all `sensitive`.

**OD-9 Instance lock in M2.** Options: a lock port with an in-memory fake, the real exclusive file lock with the `ddfd` entry point; the OS lock now. **Recommendation:** the port and fake. Pros: no code without a consumer. Cons: the real lock is untested until `ddfd`.

**OD-10 Redaction ([§8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)) in M2.** Options: the pure redaction function (patterns + loaded secret values, refuse a patch with a loaded value) in the Publisher now; defer to M3 with real containers. **Recommendation:** now. Pros: one sensitive review, testable with fakes. Cons: Task 11 grows.

**OD-11 Glob semantics.** Options: `path.matchesGlob` with a fixture suite; picomatch now. **Recommendation:** `matchesGlob`. Pros: no dependency. Cons: its stability label on Node 24 was not confirmed from the docs (no warning observed on 24.21.0).

**OD-12 Required check shape.** Options: one job `verify`; separate `typecheck`, `test`, `boundaries` checks. **Recommendation:** one job `verify`. Pros: matches the [§6.7](../specs/2026-11-03-ddf-v1-design.md#67-project-profile) example `checks.required: [verify]`; one ruleset entry. Cons: a failure needs the log to see which step broke.

**OD-13 Action pinning in the new workflow.** Options: full commit SHA; major tags as in `docs-check.yml`. **Recommendation:** full commit SHA. Pros: immutable inputs to CI. Cons: manual bumps; a later follow-up may pin `docs-check.yml` too (forbidden path, owner).

## M2 exit

M2 is done when Task 13 is merged with `verify` and `docs-check` green and its scenario list covers the [§14](../specs/2026-11-03-ddf-v1-design.md#14-milestones) exit criterion. The next plan (M3) starts after the owner accepts it.
