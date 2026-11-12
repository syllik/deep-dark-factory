# M2 Offline Core Implementation Plan

Purpose: the task-by-task plan for milestone M2, the offline core that runs the whole DDF lifecycle in tests with fakes.

Status: accepted 2026-11-05 (#24); Go rewrite (Issues #32, #37) accepted when the owner merges that PR

> **Rewritten for Go (2026-11-06, Issues #32 and #37).** ADR 2 in [`.ai/decisions.md`](../../.ai/decisions.md) replaced TypeScript on Node 24 with Go. Rule: every section, task and open decision that named the old stack was rewritten. By number: Architecture, Tech Stack, Global Constraints (dependency line), Reuse, Hosted CI, PR order, Tasks 0–2, the Verify steps of Tasks 3–13, the Task 9 files, OD-1, OD-2, OD-3, OD-4, OD-6, OD-7, OD-8, OD-11, OD-12, OD-14, and the new OD-15 and OD-16 (owner choices). `scripts/docs-check.mjs` stays a Node docs tool, not product code.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Subagents never commit, push or change GitHub; the orchestrator session commits, opens PRs and closes Issues. Every task runs by [manual mode](../orchestrator/manual-mode.md) and the owner merges. Code follows the [code conventions](../development/code-conventions.md).

**Goal:** Implement the offline core of [spec §4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider) (all modules except `context`, which is M3), plus `forge-local`, `sandbox-fake`, a fake agent and the daemon wiring needed to run the whole lifecycle in tests: offline, with no network, forge or provider ([spec §14](../specs/2026-11-03-ddf-v1-design.md#14-milestones), M2 row).

**Architecture:** One Go module, `github.com/syllik/deep-dark-factory`, `go.mod` at the repository root ([spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries)).

- `internal/core/<module>/` is one package per core module; `internal/core/ports/` holds the port interfaces of [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters) (interfaces and their argument types only).
- Adapters live in `internal/adapters/<port>/<name>/`: `internal/adapters/forge/local/` (`forge-local`) and `internal/adapters/sandbox/fake/` (`sandbox-fake`) in M2; later `forge/github`, `agent/claudecode`, `agent/opencode`, `sandbox/docker`.
- The engine (tick: advance tasks through pure lifecycle and policy, call ports, record intents; recovery) lives in `internal/daemon/` (OD-5). The binaries' `main` packages go in `cmd/ddfd/` and `cmd/ddf/`; there is no `cmd/` in M2.
- The fake agent and the in-memory port fakes live in `test/fakes/` (OD-6). End-to-end scenarios live in `test/e2e/`; the import-boundary test in `test/boundaries/`. Unit tests sit next to their package as `*_test.go`; fixtures in `testdata/`.
- Go's `internal/` rule keeps every package out of reach of other modules, so no package API becomes an external contract. It does not order the packages inside the module; the boundary test (Task 2) does: core never imports adapters, the daemon or `test/**`; adapters import only core (ports, model, the workspace git helper and the `internal/core/store` package, OD-14) and never another adapter; only the daemon wires them; nothing outside `test/**` imports `test/**`.
- `lifecycle`, `policy` and `scheduler` are pure: they take a clock and inputs and do no I/O. `store` is the only package that imports the SQLite driver or `database/sql`. `workspace` is the only core package that imports `os/exec`.

**Tech Stack:**

- Go: `go.mod` carries `go 1.27` and `toolchain go1.27.4` (OD-1, [decision log](../../.ai/decisions.md#dependencies)). `CGO_ENABLED=0` everywhere, in tests too. Static binaries for linux and darwin on arm64 and amd64 come with the plan that ships `ddfd`; M2 already cross-compiles `./...` for those four targets in **verify**, so a cgo or platform dependency fails early.
- Standard library first. Runtime dependencies (OD-2): `modernc.org/sqlite`, `github.com/santhosh-tekuri/jsonschema/v6`, `go.yaml.in/yaml/v3`, `github.com/bmatcuk/doublestar/v4`. Analyzers are pinned as `tool` lines in `go.mod` (already a `ciEntrypoint`) and run with `go tool` (OD-4): `exhaustive`, `staticcheck`, `govulncheck`. All versions are exact in `go.mod`; `go.sum` is committed.
- Tests: the standard `testing` package, no assertion library. Table-driven tests with `t.Run`; temp dirs from `t.TempDir()`; injected clock and ID source; no network. Git fixtures are made with the `git` CLI in temp dirs, identity passed by `-c user.name=… -c user.email=…` (no global config).
- **verify**, defined once here, run by every task and mirrored by the CI jobs `verify` (items 1–7) and `docs-check` (item 8) (no wrapper script, which would be one more `ciEntrypoint`), with `CGO_ENABLED=0`. Before items 1–7 the CI job `verify` runs the step "Go version equals the toolchain line": it compares `go env GOVERSION` with the `toolchain` line of `go.mod` and fails on any difference. That step, in the `verify` job only, pins the exact Go version:
  1. `test -z "$(gofmt -l .)"`;
  2. `go mod verify`;
  3. `go vet ./...`;
  4. `go tool exhaustive ./...`;
  5. `go tool staticcheck ./...`;
  6. `go test ./...`;
  7. `go build ./...` for each of `linux/arm64`, `linux/amd64`, `darwin/arm64`, `darwin/amd64` (`GOOS`/`GOARCH`);
  8. `node scripts/docs-check.mjs`.
- `go tool govulncheck ./...` runs as a separate, non-required CI check `vulncheck` (OD-12): it reads an external vulnerability database, and the required check stays deterministic. Tasks run it locally when they change `go.mod`.
- Git: the system `git` CLI through `os/exec` (`exec.CommandContext` with an argument slice, no shell). Explicit env (`GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_TERMINAL_PROMPT=0`), `-c core.hooksPath=<empty dir>`, `-c core.fsmonitor=false`, no filter drivers ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)). `git patch-id --verbatim` verified on git 2.56; Task 8 records the minimum git version its tests need and fails with a clear message on an older git.

**Spec:** [`docs/specs/2026-11-03-ddf-v1-design.md`](../specs/2026-11-03-ddf-v1-design.md): §4 (modules, ports, boundaries, store), §5 (lifecycle, bounds), §6 (policy, gate, signatures, profile), §8.3, §9, §10.1, §12, §14, [§14.1](../specs/2026-11-03-ddf-v1-design.md#141-v1-acceptance). Operational canon: [lifecycle](../concepts/lifecycle.md), [policy](../concepts/policy.md), [roles](../concepts/roles.md#publisher), [identity](../concepts/identity.md#workspaces-and-publication), [config layers](../concepts/config-layers.md), [forge-local](../adapters/forge-local.md#contract), [schemas](../../schemas/).

## Global Constraints

- English only. Public repository: no IPs, hostnames, tokens, private repository names or personal logins. No model names; roles name tiers.
- Every dependency has a recorded reason ([spec §4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries)). All dependencies are added in Task 1 only. A later task that needs one stops and asks: `go.mod` and `go.sum` are `ciEntrypoints`, so that task's class becomes `sensitive`.
- Each task = one GitHub Issue (child of #24, created after this plan merges) = one branch `<type>/issue-<n>-<slug>` = one PR run by manual mode; the owner merges. PRs stay small: one package plus its tests per PR, target < ~600 changed lines excluding `go.sum`; split further if larger.
- Code follows [code conventions](../development/code-conventions.md). Code comments link a spec or concept anchor only where a rule is enforced (e.g. `// spec §6.6 rule 4`). No restated tables in code docs.
- Each refusal reason, bound and pause reason is a value of a named enum-like type with its own test; every `switch` over such a type is exhaustive (`exhaustive` analyzer) ([spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate)).
- The host never executes repository content: `sandbox-fake` returns scripted verify outcomes and never runs workspace files ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)).
- No executor edits a [forbidden path](../orchestrator/manual-mode.md#forbidden-paths). The one forbidden file M2 needs (`.github/workflows/ci.yml`) lands per OD-3.
- Rules, states, bounds and gate conditions are implemented from the linked spec and concept sections; this plan names them, it does not restate them.

## Review Focus

1. Core importing an adapter, the daemon, `test/**`, network or provider packages: the import-boundary test (Task 2) fails.
2. `EvaluateMerge` returning `Merge` on any partial condition: one test per refusal reason of [spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate), fail-closed on read errors.
3. Review signatures: canonical JSON exactly as §6.6, a replayed or forged marker rejected ([§14.1](../specs/2026-11-03-ddf-v1-design.md#141-v1-acceptance) probe 1), the key only injected through the secrets port.
4. Patch apply: symlink, `..` or absolute path, `.git/` path and forbidden path refused before any forge write ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication), probes 3 and 4).
5. An intent written before every forge write and reconciled by task-key marker on restart; no blind retry ([lifecycle.md](../concepts/lifecycle.md#store-intents-and-leases), probe 7).
6. Bounds and pause reasons exactly as [lifecycle.md#bounds](../concepts/lifecycle.md#bounds) and [#global-pause](../concepts/lifecycle.md#global-pause); only `runtime` lifts automatically.
7. Pinned merge: a head moved between evaluation and merge fails the compare-and-swap (probe 8).
8. An error ignored or turned into a default value on a gate, policy or publish path (see [code conventions](../development/code-conventions.md#errors)).

## Reuse (generic primitives)

Versions, licenses and maintenance were read on 2026-11-06 from the Go module proxy (`proxy.golang.org/<module>/@latest`) and the source repositories (not archived, recent commits).

| Primitive | Chosen | Considered | Reason |
|---|---|---|---|
| Persistence | `modernc.org/sqlite` v1.62.0 (BSD-3-Clause, cgo-free; v1.61.1 was the version read on 2026-11-06; [decision log](../../.ai/decisions.md#dependencies)) through `database/sql` (OD-2) | `github.com/ncruces/go-sqlite3` v0.36.0 (cgo-free, Wasm runtime); `mattn/go-sqlite3` (needs cgo) | Measured in the [research](../research/2026-11-runtime-and-language.md#measurements): static cross-build with SQLite inside. Migrations are ordered SQL files embedded with `embed` and applied by `store`, tracked in a `schema_version` table. |
| Schema validation (role results, config) | `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3 (Apache-2.0, draft 2020-12) (OD-2) | hand-written validators; `xeipuuv/gojsonschema` (no 2020-12) | `schemas/*.json` stay the single normative source; hand-written validators drift. |
| YAML (`.ddf/project.yaml`, `routing.yaml`, `projects.yaml`) | `go.yaml.in/yaml/v3` v3.0.6 (Apache-2.0, the YAML organisation's `yaml/go-yaml`) (OD-2) | `go.yaml.in/yaml/v4` v4.0.0 (released 2026-11-04); `gopkg.in/yaml.v3` (repository archived); JSON-only configs | The standard library has no YAML parser; the config formats are fixed by the spec. v4 is two days old: revisit after it has a patch release. Decoded YAML is validated against the JSON Schema before it is used. |
| Glob matching (risk paths, forbidden, zones, sensitive) | `github.com/bmatcuk/doublestar/v4` v4.10.4 (MIT, no dependencies) (OD-11) | `path.Match` (no `**`); own matcher | The spec globs use `**`. Semantics pinned by a fixture test of every glob in [spec §6.5](../specs/2026-11-03-ddf-v1-design.md#65-path-policy), [§12](../specs/2026-11-03-ddf-v1-design.md#12-ddfs-own-profile) and [`project.yaml`](../../templates/project/.ddf/project.yaml). |
| Test runner, assertions | `testing` | `testify`; `go-cmp` | Standard library first; table tests and small helpers suffice. |
| Exhaustive `switch` on enum-like types | `github.com/nishanths/exhaustive` v0.17.1 (BSD-2-Clause), `go tool exhaustive` (ADR 2) | golangci-lint's bundled copy | Go has no sum types; this is the required substitute check. |
| Static analysis beyond `go vet` | `honnef.co/go/tools` (staticcheck) v0.8.2 (2026.2.2, MIT), `go tool staticcheck` (OD-4) | `github.com/golangci/golangci-lint/v2` v2.15.0 (GPL-3.0, large tree) | Finds unused results, deprecated calls and dead code that `go vet` misses. |
| Vulnerability check | `golang.org/x/vuln` v1.9.0 (BSD-3-Clause), `go tool govulncheck`, CI check `vulncheck` (OD-12) | `golang/govulncheck-action`; GitHub dependency review (needs the dependency graph, a repository setting) | Reports only vulnerabilities the code can reach; no extra action. |
| CLI argument parsing | not needed in M2; `flag` when the CLI comes (M4) | `spf13/cobra` | The standard library suffices for the [§4.3](../specs/2026-11-03-ddf-v1-design.md#43-entry-points) commands. |
| Canonical JSON for signatures | own function: sorted keys, UTF-8; strings, integers, booleans, null, arrays, objects only; refuses anything else; `encoding/json` escapes `<`, `>`, `&` unless `SetEscapeHTML(false)`, so the function sets it and a test pins it | an RFC 8785 library | The signed payload is fixed and small; no dependency inside sensitive signature code; RFC 8785 number rules are not needed (no floats). |
| HMAC-SHA256, SHA-256, random IDs | `crypto/hmac`, `crypto/sha256`, `crypto/rand` | none | Standard library. |
| Git access | `git` CLI via `os/exec` | `go-git/go-git` | go-git has no `git apply` with the needed refusals and no `patch-id --verbatim`; one more large tree in sensitive code. |
| Import-boundary test | own test in `test/boundaries/` over `go list -deps -json ./...` output per verify target, standard library only (OD-16) | `fe3dback/go-arch-lint` v1.19.1 (MIT); depguard through golangci-lint | A handful of rules do not justify a dev dependency tree; revisit if the rules grow. |
| Lint/format | `gofmt` plus the analyzers above (OD-4) | golangci-lint | See OD-4. |
| Instance file lock | lock port with an in-memory fake; the OS lock comes with `ddfd` (OD-9) | `gofrs/flock` | No consumer before `ddfd`. |

## Hosted CI and repository settings

- New workflow `.github/workflows/ci.yml` (lands per OD-3): `on: pull_request` and `push` to `master`; `permissions: contents: read`; one job with check name `verify` on `ubuntu-latest` (OD-12); `actions/checkout` with `persist-credentials: false`; `actions/setup-go` with `go-version-file: go.mod` (it uses the `toolchain` line) and `cache: false` (no cache shared between PR runs; a cold build takes seconds); `GOTOOLCHAIN=local` and `CGO_ENABLED=0` in the job env; `GOTOOLCHAIN=local` only prevents a toolchain download or switch: the go command uses the installed Go and fails only when it is older than the `go` line, so it does not pin the exact version. The `verify` job runs the step "Go version equals the toolchain line" right after setup-go: it fails unless `go env GOVERSION` equals the `toolchain` line of `go.mod`, and it is what pins the exact version (`verify` job only, not `vulncheck`). Then one step per **verify** item 1–7; item 8 is the existing required `docs-check` check. A second job `vulncheck` runs `go tool govulncheck ./...` with the same setup; it is not required (OD-12). No secrets. Actions pinned by full commit SHA with the tag in a comment (OD-13): `actions/checkout` v7.0.1 `3d3c42e5aac5ba805825da76410c181273ba90b1`, `actions/setup-go` v7.0.0 `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` (latest releases read from the GitHub API on 2026-11-06; Task 1 re-reads them). `docs-check.yml` stays unchanged and required.
- Required checks after M2 starts: `docs-check` and `verify`.
- Owner actions (sessions never change repository settings; [what goes to the owner](../orchestrator/manual-mode.md#what-goes-to-the-owner)):
  1. after the Task 1 PR has run `verify` once, add `verify` to the required status checks of the `master` ruleset, next to `docs-check`;
  2. keep squash-only merges and no bypass actors;
  3. optional: enable Dependabot security alerts for Go modules.
- Until owner action 1 is done, sessions treat `verify` as required anyway (manual mode step 6 waits for every check).

---

Risk classes below come from [DDF's own profile](../orchestrator/manual-mode.md#ddfs-own-profile-until-ddfprojectyaml-exists): first matching row per file, highest over the files.

### Task 0: DDF profile update (OD-7)

Done. #28 (PR #29) added the M2 rows for the old stack; this rewrite (#32) replaced them with the Go paths below, in the profile and in [spec §12](../specs/2026-11-03-ddf-v1-design.md#12-ddfs-own-profile).

- `sensitive`: `internal/core/policy/**`, `internal/core/lifecycle/**`, `internal/core/workspace/**`, `internal/core/store/migrations/**`, `internal/core/publisher/**`, `internal/daemon/**`, `cmd/ddfd/**`, `internal/adapters/agent/**`, `internal/adapters/sandbox/**`, the merge code in `internal/adapters/forge/**`.
- `ciEntrypoints`: `scripts/docs-check.mjs`; `go.mod`, `go.sum` and `test/boundaries/**` when they exist.
- `standard`: `docs/development/**` (the code conventions steer executors) joins the agent-steering row.

### Task 1: Scaffold and CI

**Files:** Create `go.mod`, `go.sum`, `internal/core/doc.go` (package comment only), `test/smoke/smoke_test.go`; `.github/workflows/ci.yml` per OD-3. Modify `.ai/decisions.md` (section "Dependencies", OD-2).
**Risk class:** `sensitive` (`go.mod` and `go.sum` are `ciEntrypoints`; one forbidden path).
**Depends on:** this plan merged.

- [ ] **Step 1:** `go.mod` with the module path, `go` and `toolchain` lines (OD-1), the OD-2 requirements and the OD-4 `tool` lines, all at the exact versions of the Reuse table (re-read from the module proxy; a newer patch release is taken and noted in the PR); `go mod tidy`; `go.sum` committed.
- [ ] **Step 2:** `internal/core/doc.go`; `test/smoke/smoke_test.go` (one passing test that imports the four runtime dependencies so `tidy` keeps them).
- [ ] **Step 3:** `.ai/decisions.md` "Dependencies": one line per runtime dependency and per tool with its reason.
- [ ] **Step 4:** `ci.yml` (jobs `verify` and `vulncheck`) edited by the session itself under the owner's chat authorization (OD-3, option a).
- [ ] **Verify:** verify; hosted `verify` green on the PR.

### Task 2: Model, ports, import boundaries

**Files:** Create `internal/core/model/**`, `internal/core/model/results.go`, `internal/core/ports/**`, `schemas/embed.go`, `test/boundaries/**`.
**Risk class:** `sensitive` (`test/boundaries/**` is a `ciEntrypoint`; `schemas/embed.go` is under `schemas/**`; the rest would be `standard` by size).
**Depends on:** Task 1.
**Interfaces:** Produces the record types and the forge, agent, sandbox, notify, secrets, clock and lock ports every later task consumes.

- [ ] **Step 1:** Struct types for Task, Run, Change, Review, Finding, Question, Decision, Intent, Event; `ddf.<name>/v1` ID constants (OD-8); the task-key digest ([spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)); enum-like types for states and verdicts.
- [ ] **Step 2:** Port interfaces from [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters), every forge write with dry-run; I/O methods take `context.Context` first.
- [ ] **Step 3:** `results.go`: validate role results with `jsonschema/v6` against `schemas/*.result.json`; tests with the `schemas/examples/` files and one invalid result per schema. `go:embed` cannot reach `..`, so a one-file package `schemas/embed.go` embeds the JSON files where they are; they stay the single source, under the `schemas/**` `sensitive` row.
- [ ] **Step 4:** `test/boundaries/boundaries_test.go` runs `go list -deps -json ./...` with `CGO_ENABLED=0` once per verify target `linux/arm64`, `linux/amd64`, `darwin/arm64`, `darwin/amd64` (`GOOS`/`GOARCH`) and checks every rule over the union of packages and imports of the four runs. It checks each module package's direct `Imports`; for the network rule it also follows imports through this module's own packages only, per run. Third-party modules are not traversed: their standard-library reach is reviewed when the dependency is admitted (OD-2). Measured with `go list -deps` on 2026-11-06 for linux and darwin: `github.com/santhosh-tekuri/jsonschema/v6` reaches `net/url` and `net/netip`; `modernc.org/sqlite` reaches `net` and `os/exec` through `modernc.org/libc` and `github.com/google/uuid`. Rules: `internal/core/**` ↛ `internal/adapters/**`, `internal/daemon/**`, `cmd/**`, `test/**`; no package under `internal/core/**` imports `net`, `crypto/tls` or any `net/...` package other than `net/url` and `net/netip` (both parse only), directly or through another package of this module (`net/http` reaches `net`, so it is covered); no package under `internal/core/**` imports `os/exec` except in `internal/core/workspace/**`; only packages of this module under `internal/core/store/**` import `database/sql` or `modernc.org/sqlite`; an adapter ↛ another adapter's package; adapters import `internal/core/store` but no package below it (OD-14; under its option (b) any adapter import of `internal/core/store/...` fails); nothing outside `test/**` imports `test/**`; third-party imports (import paths that are neither standard library nor this module) from `internal/core/**` follow a per-package allowlist, where an entry covers the module path and paths below it: `internal/core/store/**` → `modernc.org/sqlite`, `internal/core/model/**` → `github.com/santhosh-tekuri/jsonschema/v6`, `internal/core/policy/**` → `github.com/bmatcuk/doublestar/v4`, the config loader package (its path named by the task that creates it, which adds the entry then) → `go.yaml.in/yaml/v3`; any other third-party import from `internal/core/**` fails. A new or changed entry edits `test/boundaries/**`, a `ciEntrypoint`, so that change is `sensitive`. Fixture modules under `test/boundaries/testdata/` (ignored by `./...`) prove each rule fails, including a core package whose only `net` import is in a `_darwin.go` file (fails on any host) and a core package that imports `github.com/google/uuid` (not on the allowlist).
- [ ] **Verify:** verify.

### Task 3: Store

**Files:** Create `internal/core/store/**` (including `migrations/**`).
**Risk class:** `sensitive` (`internal/core/store/migrations/**` row; the rest would be `standard`).
**Depends on:** Task 2.

- [ ] **Step 1:** Tables of [spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents), migrations, transactions.
- [ ] **Step 2:** Append-only `events` (no update or delete API); intents with before/after and an unresolved query; leases with expiry from the clock ([lifecycle.md](../concepts/lifecycle.md#store-intents-and-leases)).
- [ ] **Step 3:** Tests: journal append-only, intent lifecycle, expired lease released, reopen of a file database.
- [ ] **Verify:** verify.

### Task 4: Policy A, classification

**Files:** Create `internal/core/policy/**` (classification part).
**Risk class:** `sensitive` (`internal/core/policy/**` row).
**Depends on:** Task 2.

- [ ] **Step 1:** Effective autonomy as the minimum of the layers ([policy.md](../concepts/policy.md#autonomy-levels), [config layers](../concepts/config-layers.md#resolution-order)).
- [ ] **Step 2:** Risk class from diff and profile ([policy.md](../concepts/policy.md#risk-classes)): lockfile and rename exclusion, `ciEntrypoints`, agent-instruction files never `docs`; reviewer set; blocking findings ([#findings](../concepts/policy.md#findings)).
- [ ] **Step 3:** Path policy: defaults of [spec §6.5](../specs/2026-11-03-ddf-v1-design.md#65-path-policy) plus project `forbidden`, symlinks; hard-stop detection by glob ([#hard-stops](../concepts/policy.md#hard-stops)).
- [ ] **Step 4:** Glob fixture suite (OD-11).
- [ ] **Verify:** verify.

### Task 5: Policy B, merge gate and signatures

**Files:** Modify `internal/core/policy/**` (add the gate and signature files).
**Risk class:** `sensitive` (`internal/core/policy/**` row; review-signature code).
**Depends on:** Task 4.
**Interfaces:** Produces `EvaluateMerge` and its refusal type (consumed by Task 6).

- [ ] **Step 1:** `EvaluateMerge` on inputs only (no git): one refusal value per [§6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate) condition, including the ancestor / daemon base merge / unchanged patch-id exception; fail-closed on read errors.
- [ ] **Step 2:** Review signature sign/verify, canonical JSON, `findingsDigest`; key passed in, never read from disk by policy.
- [ ] **Step 3:** One test per refusal; forged and replayed signatures rejected (probe 1).
- [ ] **Verify:** verify.

### Task 6: Lifecycle

**Files:** Create `internal/core/lifecycle/**`.
**Risk class:** `sensitive` (`internal/core/lifecycle/**` row).
**Depends on:** Tasks 2, 5 (refusal type).

- [ ] **Step 1:** Pure transitions for every state and flag of [lifecycle.md#conveyor](../concepts/lifecycle.md#conveyor); planner, executor and reviewer verdict routing.
- [ ] **Step 2:** Finding identity across rounds, follow-up and minors routing with loop guard ([#fixing](../concepts/lifecycle.md#fixing)).
- [ ] **Step 3:** Bounds counters for every row of [#bounds](../concepts/lifecycle.md#bounds), with the revision definition; merge-refusal mapping ([#merge-refusals](../concepts/lifecycle.md#merge-refusals)).
- [ ] **Step 4:** Revert rule with the 24 h window from the clock ([#revert](../concepts/lifecycle.md#revert)); global pause set and lift rule, only `runtime` automatic ([#global-pause](../concepts/lifecycle.md#global-pause)).
- [ ] **Verify:** verify.

### Task 7: Scheduler

**Files:** Create `internal/core/scheduler/**`.
**Risk class:** default row: `standard` if > 200 lines, else `low`.
**Depends on:** Task 2.

- [ ] **Step 1:** Slots (`maxConcurrentContainers` counts every container); zones held from running to merge; dependencies → waiting.
- [ ] **Step 2:** Priority order of [lifecycle.md#ready-and-waiting](../concepts/lifecycle.md#ready-and-waiting); paused → no new runs.
- [ ] **Verify:** verify.

### Task 8: Workspace

**Files:** Create `internal/core/workspace/**`.
**Risk class:** `sensitive` (`internal/core/workspace/**` row).
**Depends on:** Task 2.

- [ ] **Step 1:** Git helper with explicit env (Tech Stack); minimum git version check.
- [ ] **Step 2:** Clean clone with hooks, fsmonitor and filters neutralised; per-run copy without remotes ([spec §8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication)).
- [ ] **Step 3:** Patch export (the trusted-wrapper command of §8.3 as a function); patch apply with refusals: symlink, outside the repository, `.git/`, additions under `.opencode/`, `.claude/`, `node_modules/`.
- [ ] **Step 4:** `patch-id --verbatim`; daemon base merge that records its SHA; conflict detection; workflow-file change check since the merge base ([#sync-with-base](../concepts/lifecycle.md#sync-with-base)).
- [ ] **Step 5:** Tests include planted hooks and config that must not run (probe 4).
- [ ] **Verify:** verify.

### Task 9: Fakes

**Files:** Create `internal/adapters/sandbox/fake/**`, `test/fakes/agent.go` (OD-6), `test/fakes/ports.go`.
**Risk class:** `sensitive` (`internal/adapters/sandbox/**` row).
**Depends on:** Task 2.

- [ ] **Step 1:** `sandbox-fake`: create/run/kill, scripted verify outcomes, container accounting, list and kill containers of a previous instance; never runs workspace files.
- [ ] **Step 2:** Fake agent: scripted per role; results validated by the real schemas; the executor script returns a patch.
- [ ] **Step 3:** `test/fakes/ports.go`: in-memory fakes for the notify (records sent notifications), secrets (by name), clock (manual advance) and lock (the OD-9 fake) ports.
- [ ] **Verify:** verify.

### Task 10: forge-local

**Files:** Create `internal/adapters/forge/local/**`.
**Risk class:** `sensitive` (merge code in `internal/adapters/forge/**` row).
**Depends on:** Tasks 3, 8, 9 (`sandbox-fake` for checks).

- [ ] **Step 1:** Full forge port per [forge-local.md#contract](../adapters/forge-local.md#contract) on a `file://` bare repository plus the store (through the `store` package API, OD-14): tasks; intake actor = local operator; change = branch with task-key marker; reviews in the store.
- [ ] **Step 2:** Checks = verify through the sandbox port; merge by compare-and-swap ref update pinned to the head ([spec §10.1](../specs/2026-11-03-ddf-v1-design.md#101-forge-local)); revert; dry-run for every write.
- [ ] **Step 3:** Tests: moved integration branch → `behind base`; moved head → merge fails (probe 8).
- [ ] **Verify:** verify.

### Task 11: Publisher

**Files:** Create `internal/core/publisher/**`.
**Risk class:** `sensitive` (Publisher row, `internal/core/publisher/**`).
**Depends on:** Tasks 4, 8, 9 (`sandbox-fake` for the local gate, the secrets fake for redaction).

- [ ] **Step 1:** Apply the patch to a clean clone (workspace); path policy and risk class on the applied tree; local gate through the sandbox port before push; one commit and one push per revision ([roles.md#publisher](../concepts/roles.md#publisher)).
- [ ] **Step 2:** Change body and squash message from [`change-template.md`](../../templates/project/.ddf/change-template.md); sanitizer for closing keywords, `[skip ci]`-style directives and mentions; `Closes` only for the task.
- [ ] **Step 3:** Redaction by secret patterns and loaded values; a patch carrying a loaded value is refused (OD-10, [spec §8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)).
- [ ] **Verify:** verify.

### Task 12: Engine and recovery

**Files:** Create `internal/daemon/**` (OD-5).
**Risk class:** `sensitive` (`internal/daemon/**` row, OD-7).
**Depends on:** Tasks 3, 6, 7, 9, 10, 11.

- [ ] **Step 1:** Tick: read forge → lifecycle, policy and scheduler decisions → port calls; each forge write wrapped in an intent; run timeouts from the clock; dry-run mode.
- [ ] **Step 2:** Recovery on start per [spec §9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery): lock port (OD-9), kill previous containers, discard runs within budget, reconcile intents by task-key marker, unreconcilable → `needs-owner`.
- [ ] **Verify:** verify.

### Task 13: End-to-end lifecycle suite (M2 exit)

**Files:** Create `test/e2e/**`.
**Risk class:** `standard` (default row by size).
**Depends on:** Task 12.

Scenarios on `forge-local` + `sandbox-fake` + the fake agent + a file store:

- [ ] **Step 1:** Happy path intake → merge (`merge-integration`) and propose (stops at a ready change); decompose with ≤ and > `maxAutoChildren`.
- [ ] **Step 2:** Fixing with re-review; the same finding twice → `needs-owner`; CI rerun flake.
- [ ] **Step 3:** Sync clean, sync conflict batch; base workflow change → `needs-owner` without consuming a batch.
- [ ] **Step 4:** Every row of [lifecycle.md#merge-refusals](../concepts/lifecycle.md#merge-refusals); revert, and a second red within 24 h → pause `revert-red`.
- [ ] **Step 5:** Restart mid-run and mid-intent (probe 7: no duplicate task or change); probes 1, 3, 4, 5, 8 with fakes; every bound exceeded → `needs-owner`.
- [ ] **Verify:** verify; the test names map 1:1 to the [§14](../specs/2026-11-03-ddf-v1-design.md#14-milestones) M2 exit criterion: intake → merge, fixing, sync, gate refusals, revert, restart recovery, with fakes.

### Task 14: M2 close-out docs

**Files:** Modify `.ai/context.md`, `ARCHITECTURE.md`, `README.md` (status lines); adapter and concept `Status:` lines that say "implemented in M2".
**Risk class:** `standard` (`.ai/**` row).
**Depends on:** Task 13.

- [ ] **Step 1:** Update the status lines; check every "implemented in M2" claim is true.
- [ ] **Verify:** `node scripts/docs-check.mjs`.

## PR order and parallelism

Each PR is branched from up-to-date `master` after its dependencies merged; at most 3 lanes at once; the owner merges in order. Parallel PRs never touch the same files (no `go.mod` or `go.sum` edits after Task 1).

```text
1 → 2 ─┬─ wave A: 3 store | 4 policy A | 7 scheduler
       ├─ wave B: 8 workspace | 9 fakes | 5 policy B (after 4)
       ├─ wave C: 6 lifecycle (after 5) | 10 forge-local (after 3, 8, 9) | 11 publisher (after 4, 8, 9)
       └─ 12 engine (after 3, 6, 7, 9, 10, 11) → 13 e2e → 14 close-out
```

## Open decisions for the owner

Tasks assume each **Recommendation**; OD-15 and OD-16 carry the owner's choice.

**OD-1 Go toolchain line.** Options (supported releases on `go.dev/dl`, 2026-11-06): 1.27.x (`toolchain go1.27.3`, the newest line); 1.26.x (`go1.26.10`, the previous line). **Recommendation:** `go 1.27` and `toolchain go1.27.3`. Pros: the version the research measured; the longest support window. Cons: patch bumps are manual `go.mod` edits, each a `sensitive` change. Outcome: accepted with `toolchain go1.27.4`, the newer patch release taken in Task 1 ([decision log](../../.ai/decisions.md#dependencies)).

**OD-2 Dependency set and where reasons are recorded.** Options: the four runtime modules of the Reuse table plus the three `go tool` analyzers, reasons in `.ai/decisions.md`; no runtime dependencies (own SQLite-free store, hand-written validators, JSON-only configs: not possible for SQLite under ADR 2); reasons in a separate `docs/dependencies.md`. **Recommendation:** the Reuse set, exact versions in `go.mod`, `go.sum` committed, one reason line each in `.ai/decisions.md` section "Dependencies", added in Task 1. Pros: schemas stay normative, one decision log. Cons: four runtime modules (with `modernc.org/sqlite`'s transitive `modernc.org/*` modules) to track. Each dependency's reason line in `.ai/decisions.md` also states which of `net`, `os/exec` and `crypto/tls` it reaches (for example, `modernc.org/sqlite` reaches `net` and `os/exec`).

**OD-3 How the forbidden file lands** (`.github/workflows/ci.yml`). A root `.gitignore` is no longer needed in M2: Go writes no dependency tree into the repository and M2 builds no binaries; it comes with the plan that ships `ddfd` (build output). Options: (a) the owner authorizes the session in chat to edit exactly that path in Task 1 ([manual mode option 2](../orchestrator/manual-mode.md#forbidden-paths)); (b) the owner adds it in their own PR right after Task 1 merges. **Recommendation:** (a). Pros: Task 1 is `sensitive` anyway and gets a hosted `verify` run. Cons of (b): Task 1 has no hosted `verify` run.

**OD-4 Lint/format in M2.** Options: `gofmt`, `go vet` and `exhaustive` only; the same plus `staticcheck`; golangci-lint (GPL-3.0 tool, large tree, many linters, frequent config changes). **Recommendation:** `gofmt`, `go vet`, `exhaustive`, `staticcheck`, all in **verify**. Pros: deterministic, no config file, one small extra tool. Cons: no style linting beyond `gofmt`.

**OD-5 Where the engine lives.** Options: `internal/daemon/`; `internal/core/engine/`. **Recommendation:** `internal/daemon/`. Pros: "the daemon wires them" ([§4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries)); core stays pure. Cons: `internal/core/engine/` would need core to call ports, blurring "pure".

**OD-6 Fake agent location.** Options: `test/fakes/agent.go`; `internal/adapters/agent/fake/`. **Recommendation:** `test/fakes/`. Pros: not a V1 adapter; the boundary test keeps `test/**` out of every non-test package, so it cannot be wired into a release; default risk rules. Cons: `internal/adapters/agent/fake/` would match `internal/adapters/agent/**` → `sensitive`, R2 at `deep` for test code; the two fakes live in different trees (`sandbox-fake` stays an adapter because §4.2 names it one).

**OD-7 DDF profile rows.** Options: (a) security-relevant code `sensitive` by glob: the Task 0 globs, including `internal/daemon/**` and `cmd/ddfd/**`, plus the `ciEntrypoints`; (b) only gate, signature and Publisher code `sensitive`, the daemon and the entry points by the default size rules; (c) all of `internal/**` and `cmd/**` `sensitive`. **Recommendation:** (a). Pros: R2 runs at `deep` where the gate, the merge, the intents and the signing key are handled; the daemon calls `EvaluateMerge` before the forge merge, writes and reconciles intents and passes the review-signing key from the secrets port. Cons: more `deep` runs on daemon changes. Cons of (b): the daemon drives the gate and the merge and passes the signing key, so a change there would skip `deep` review. Cons of (c): every model or scheduler change runs at `deep`. Done by Task 0 (#28, PR #29), Go paths by #32, `cmd/ddfd/**` by #39, in the profile and in spec §12. No open choice remains.

**OD-8 Versioned schemas for internal records** (Task, Run, … [§4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider)). Options: Go struct types plus `ddf.<name>/v1` ID constants stored with each persisted JSON payload, JSON Schema files only for data that crosses a trust boundary (role results, config); JSON Schema for every record. **Recommendation:** the first. Pros: no duplicate of the types. Cons of the alternative: more `schemas/**` files, all `sensitive`.

**OD-9 Instance lock in M2.** Options: a lock port with an in-memory fake, the real exclusive file lock with the `ddfd` entry point; the OS lock now. **Recommendation:** the port and fake. Pros: no code without a consumer. Cons: the real lock is untested until `ddfd`.

**OD-10 Redaction ([§8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)) in M2.** Options: the pure redaction function (patterns + loaded secret values, refuse a patch with a loaded value) in the Publisher now; defer to M3 with real containers. **Recommendation:** now. Pros: one sensitive review, testable with fakes. Cons: Task 11 grows.

**OD-11 Glob semantics.** Options: `doublestar/v4` with a fixture suite; an own `**` matcher; `path.Match` with the spec globs rewritten without `**`. **Recommendation:** `doublestar/v4`. Pros: small, no dependencies, `**` as the spec uses it. Cons: one runtime dependency in sensitive policy code; an own matcher would avoid it at the cost of sensitive own code.

**OD-12 Required check shape.** Options: one required job `verify` with the **verify** steps, plus a non-required `vulncheck` job; the same with `govulncheck` inside the required `verify`; separate required `vet`, `test`, `exhaustive`, `boundaries` checks. **Recommendation:** the first. Pros: matches the [§6.7](../specs/2026-11-03-ddf-v1-design.md#67-project-profile) example `checks.required: [verify]`; one ruleset entry; the required check depends only on the repository, so a newly published advisory cannot turn unrelated PRs red. Cons: a failure needs the log to see which step broke; a red `vulncheck` blocks nothing, so the session reads it on every PR and opens a follow-up. Pros of `govulncheck` inside `verify`: a reachable vulnerability blocks merges until fixed.

**OD-13 Action pinning in the new workflow.** Options: full commit SHA; major tags as in `docs-check.yml`. **Recommendation:** full commit SHA. Pros: immutable inputs to CI. Cons: manual bumps; a later follow-up may pin `docs-check.yml` too (forbidden path, owner).

**OD-14 Adapter access to the store.** `forge-local` keeps tasks, reviews and findings in the DDF store ([spec §10.1](../specs/2026-11-03-ddf-v1-design.md#101-forge-local)), and only `internal/core/store/**` may import the SQLite driver. Options: (a) adapters import the `internal/core/store` package; its exported identifiers are its API; the boundary test fails on an adapter import of any package below it, and any helper package of the store goes under `internal/core/store/internal/`, where the compiler also refuses it to adapters (migrations are embedded SQL files, not a package); (b) a narrow store port in `internal/core/ports/` that `store` implements and the daemon passes to `forge-local`; the boundary test fails on any adapter import of `internal/core/store/...`. **Recommendation:** (a). Pros: spec §4.4 forbids only core → adapters and adapter → adapter; no extra interface; `forge-local` tests run on a real file store in a temp dir. Cons: an adapter can reach every exported store operation, intents and leases included; the store's exported API becomes an adapter contract. Pros of (b): adapters see only what `forge-local` needs; a fake can stand in for the store. Cons of (b): one more port to design in Task 2 before the store exists, kept in step with the store by hand.

**OD-15 Package layout.** Options: (A) keep the `src/` tree (`src/core/<module>`, `src/adapters/<name>`, `src/daemon`, `src/cmd/…`), so the profile's `src/**` globs stay valid; (B) the Go layout `internal/core/…`, `internal/adapters/<port>/<name>`, `internal/daemon`, `cmd/ddfd`, `cmd/ddf`, with every glob in the profile, spec §4.4 and §12 rewritten. Pros of (B): the layout Go tools and agents expect; `internal/` keeps the packages out of other modules, so no API becomes an external contract; renaming now is cheap because no code exists and this change rewrites the profile anyway. Cons of (B): every `src/**` glob changes; `internal/` does not order packages inside the module, so the boundary test is still needed. Pros of (A): no glob changes. Cons of (A): unusual import paths (`…/src/core/policy`); packages importable by any module. **Owner choice (2026-11-06, in chat):** (B) `internal/` + `cmd/`, against the research's recommendation (A).

**OD-16 Import-boundary enforcement.** Options: (a) an own test using only the standard library (`go list -deps -json`), run by `go test`, itself a `ciEntrypoint` (`test/boundaries/**`); (b) a linter, `fe3dback/go-arch-lint` v1.19.1 (MIT) or depguard through golangci-lint v2.15.0 (versions from the Go module proxy, 2026-11-06), whose config file becomes the `ciEntrypoint`; (c) Go `internal/` placement only. Pros of (a): no dependency, one more `go test`, rules next to their fixtures. Cons of (a): about 150 lines of own code to maintain. Cons of (b): a dev dependency tree and a second config format. Cons of (c): cannot express core ↛ adapters or the standard-library rules. **Owner choice (2026-11-06, in chat):** (a), plus `internal/` where it encodes a rule (OD-14). **Owner choice (2026-11-12, in chat, Issue #39):** the test also enforces a per-package allowlist of third-party imports from `internal/core/**` (Task 2 Step 4).

## M2 exit

M2 is done when Task 13 is merged with `verify` and `docs-check` green and its scenario list covers the [§14](../specs/2026-11-03-ddf-v1-design.md#14-milestones) exit criterion. The next plan (M3) starts after the owner accepts it.
