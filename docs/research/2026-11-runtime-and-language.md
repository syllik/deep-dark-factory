# Runtime model and implementation language

Research for Issue #31, done before M2 Task 1. It compares runtime models (Part 1) and implementation languages for the chosen model (Part 2), and lists what the decisions change.

Status: recorded 2026-11-06. The decisions are ADRs in [`.ai/decisions.md`](../../.ai/decisions.md) ("2026-11-06 Runtime model and language"), proposed with the owner's in-chat choice (hybrid, Go). They take effect when the owner merges the PR for #31.

Constraints from the owner:
- Hardware runs from a weak VPS or SBC (1 vCPU, 1–2 GB RAM, possibly arm64) through a workstation or Mac to a powerful server. Hosted serverless is not a target.
- Providers are remote APIs or CLIs only. Local models are out of scope for now, and later they are just another provider.
- Fabro (`fabro-sh/fabro`, MIT; GitHub reports Rust as its main language, plus TypeScript) is a reference only: no fork, no copy, no dependency.

Confidence: `measured` = measured in this research (method in [Measurements](#measurements)); `documented` = stated by the cited primary source; `judged` = reasoning from the spec and the sources, no measurement.

## Part 1: runtime model

### Options

| | A. Ephemeral factory per task | B. Long-lived host daemon (spec) | C. Hybrid |
|---|---|---|---|
| Shape | One factory container per task holds the control logic, keys and state. It starts with the task and destroys itself at the end. | `ddfd` per host ([§4.3](../specs/2026-11-03-ddf-v1-design.md#43-entry-points)) with a SQLite store ([§4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)) and one container per run ([§8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers)). | B's control plane, kept small and portable. Each task executes in a disposable **task cell**: run containers, workspace copies, scratch volumes and a network, all labelled with the task key and destroyed with a verified teardown when the task leaves the conveyor. |

### Criteria

**Task lifetime (judged).** A task is not short. It waits for CI up to 2 h per head ([§5.3](../specs/2026-11-03-ddf-v1-design.md#53-bounds)), for reviews, for `needs-owner` answers, and at `propose` for a human merge that can take days. Under A the factory container either lives through all of that, which makes it a long-lived daemon per task (N copies of the control plane in memory), or it stops and resumes from state kept somewhere else. That turns A into "state outside plus a scheduler that restarts it".

**State between tasks (judged).** Several facts are global, not per task:
- zones and the waiting rule ([§5.2](../specs/2026-11-03-ddf-v1-design.md#52-states-and-transitions));
- run slots and priority order ([§4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider) `scheduler`);
- the global pause as a set of reasons ([§9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery));
- the `revert-red` rule (a second DDF-attributed red within 24 h across the project);
- flake tracking across changes;
- the run budget (`maxRunsPerTask`);
- intents and leases ([§4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)).

"Nothing" is not an option. Options for A:
- **The forge as the store.** It does not exist for [`forge-local`](../specs/2026-11-03-ddf-v1-design.md#101-forge-local), whose tasks, reviews and findings live in the DDF store. On GitHub, labels and comments are editable by anyone with write access, and the spec makes them a projection, not the canon (ARCHITECTURE invariant 3). Rebuilding state from the forge on every start also multiplies API calls under rate limits.
- **An external store** (a database server, or a shared file with locking and fencing) adds a service to every install. The v0 decision already found that per-run ephemeral execution needs "shared transactional state … fencing" ([v0 decision](../controller-v0-decision.md), audit clarification 5). That is the cost A brings back.

**Trigger (judged).** Something long-lived must start A: a host timer (launchd or systemd), which is a supervisor in all but name; webhooks, which are out of scope for V1 ([§1](../specs/2026-11-03-ddf-v1-design.md#1-goal-and-scope)); or hosted CI. Hosted CI conflicts with the durable decision "no dependency on a local Mac/self-hosted runner or GitHub Actions product execution" (`.ai/decisions.md`), with D8 (plain Git remote) and with [§8.5](../specs/2026-11-03-ddf-v1-design.md#85-ci-prerequisites-for-autonomous-projects) (no secrets in change-triggered CI). It is also hosted serverless in effect. B and C use the daemon's tick.

**Nested isolation (documented).** D10 keeps agents in their own containers without forge credentials. A factory that itself runs in a container must start those run containers. It can:
- mount the host runtime socket. Docker: "only trusted users should be allowed to control your Docker daemon", and the daemon "requires root privileges unless you opt-in to Rootless mode" ([Docker engine security](https://docs.docker.com/engine/security/)). The factory container is then as trusted as a host daemon, so there is no isolation gain;
- run privileged Docker-in-Docker. "`--privileged` is required for Docker-in-Docker to function properly", and it "provides full access to the host environment" ([docker image docs](https://hub.docker.com/_/docker));
- use Sysbox, which runs Docker inside containers "without insecure privileged containers" but installs on Linux hosts only ([nestybox/sysbox](https://github.com/nestybox/sysbox)). That is an extra runtime on every install, and it does not run on the macOS V1 host (D2).

Every option keeps the forge keys and the review-signing key with the code that can start containers. So nested isolation adds no protection for the keys over B. Under A they are also copied into N containers, one per task. Under B and C the keys stay with one trusted process, and run containers never get the socket (§8.4: "no Docker socket").

**What survives the end of a task (judged).** It must survive:
- forge objects: PR, review records, comments, labels;
- store rows and the journal (audit, run budget, findings identity, intents);
- sanitized run logs for 30 days (§9).

It must not survive:
- workspace copies with repository content;
- container filesystems and agent homes;
- credentials loaded into a run container.

Under A, "destroy everything" also destroys the audit record and the intents unless they are shipped out first. That shipping is the external-store problem again. C destroys exactly the second list, on purpose and with a check.

**Recovery and idempotency (judged).** B and C keep the spec's model:
- intent before every forge write;
- a task-key marker on every forge object;
- reconciliation on start;
- a run interrupted by a restart is discarded and retried within budget (§4.5, §9).

A without a long-lived store can only reconcile from markers on the forge. That covers "change exists → adopt" but not the run budget, the "same finding blocking twice" identity, the 24 h revert window or uncertain intents. Those would need new forge-side records, all writable by humans.

**Weak hardware (measured / judged).** The control plane is paid once per host under B and C, and once per active task under A. On a 1–2 GB host the agent CLI and its container dominate a run. The idle control plane is small in Go or Rust (8 MiB and 2 MiB measured) and moderate in Node (69 MiB) ([Measurements](#measurements)). B fixes the run-container defaults at 3 GB RAM and 2 CPU (§8.4). Those do not fit a 1–2 GB host, so adaptive capacity is needed under any model ([#34](https://github.com/syllik/deep-dark-factory/issues/34)).

**Fit with D2, D8, D10 and "no required runner" (judged).**
- A breaks D8 for `forge-local` unless an external store is added.
- A needs privileged or Linux-only nesting on the D2 macOS host.
- A pushes toward a hosted trigger.
- B and C keep all four.
- C adds only teardown rules and a deployment option.

### Result

| | A | B | C |
|---|---|---|---|
| Long waits (CI, owner, merge) | long-lived per task, or external state | fits | fits |
| Cross-task state (zones, pause, revert window, intents) | external store needed | store | store |
| Trigger | timer or hosted runner | tick | tick |
| Keys out of agent processes | keys copied into N factory containers that can start containers | one trusted process | one trusted process |
| Per-task black-box teardown | yes, but loses audit unless exported | no rule today | yes, verified |
| `forge-local` / plain Git (D8) | breaks without an external store | fits | fits |
| macOS V1 host (D2) | privileged nesting | fits | fits |
| Weak host | N control planes | one | one, adaptive |

**Owner choice (2026-11-06, in chat):** C, hybrid. ADR: `.ai/decisions.md`, "ADR 1: runtime model".

C meets the black-box goal at the level where it matters. Each task's execution side collapses and is destroyed. The trusted control plane, a single process with one data directory and a secrets directory, can itself run on a host user or as one container on a Linux host ([#35](https://github.com/syllik/deep-dark-factory/issues/35)).

## Part 2: implementation language

The chosen model has one long-lived control plane per host. The language therefore matters for the control plane's idle memory, distribution across arm64 and amd64, correctness of the pure core, and subprocess, git and container control. Cold start matters for the `ddf` CLI and for restarts, not for the tick.

### Criteria

| Criterion | TypeScript / Node 24 | Go | Rust |
|---|---|---|---|
| Cold start, measured (tick workload) | 81–110 ms median | 19–31 ms | 22–27 ms |
| Idle RSS, measured (after the tick workload) | 69 MiB | 8 MiB | 2 MiB |
| Deployable size, measured | runtime 117 MiB (official linux-arm64 `node` binary) plus sources and `node_modules`; base image `node:24.21.0-alpine` 59 MiB compressed (registry) | one static binary 7.1 MiB (linux-arm64 7.1 MiB, linux-amd64 7.3 MiB), 3.0 MiB gzip; base `distroless/static-debian12:nonroot` 0.68 MiB compressed (registry) | 2.1 MiB (darwin-arm64); a Linux build was not produced, see below |
| arm64 and amd64 | official `linux-arm64` and `linux-x64` builds (nodejs.org dist); one runtime per arch | measured: `CGO_ENABLED=0 GOOS=linux GOARCH=arm64|amd64` cross-builds from a macOS arm64 machine, static ELF, SQLite included; documented: GOOS/GOARCH list includes linux/arm64, linux/amd64, darwin/arm64 ([go.dev](https://go.dev/doc/install/source#environment)) | measured: `--target aarch64-unknown-linux-musl` fails in `libsqlite3-sys` (bundled SQLite) without a C cross-compiler for the target; needs a cross toolchain (for example zig or a container build) |
| Weak hardware (1 vCPU, 1–2 GB) | works; 69 MiB idle is several percent of 1 GiB; runtime install per host | best fit: small RSS, single file, no runtime | best RSS; building on the device is slow (cold release build 20 s here on 8 cores) |
| Correctness of lifecycle, policy, gate | discriminated unions with `never` exhaustiveness checks in `tsc` ([handbook](https://www.typescriptlang.org/docs/handbook/2/narrowing.html#exhaustiveness-checking)); types are erased at run time and type stripping runs unchecked, so `tsc` in CI is the guard | no sum types: proposals open since 2017 ([golang/go#19412](https://github.com/golang/go/issues/19412), [#57644](https://github.com/golang/go/issues/57644)); enum-like constants checked by the `exhaustive` analyzer ([nishanths/exhaustive](https://github.com/nishanths/exhaustive)) as a required CI step; sealed interfaces for verdicts | enums with data; "Matches in Rust are exhaustive", and the compiler rejects a missing arm (E0004) ([Rust book 6.2](https://doc.rust-lang.org/book/ch06-02-match.html)); strongest |
| Subprocess, git, containers | `child_process.execFile`; the docker CLI; no official Docker SDK | `os/exec`; the docker CLI; Docker and Podman are themselves Go programs (moby client available) | `std::process::Command`; the docker CLI; community crates |
| SQLite | `node:sqlite` built in, stability 1.2 "Release candidate" ([Node docs](https://nodejs.org/api/sqlite.html)) | `modernc.org/sqlite` v1.61.1, cgo-free (measured: static cross-build); a dependency | `rusqlite` with `bundled` (compiles SQLite with `cc`, measured by the cross-build failure); a dependency |
| Forge API (M4) | `octokit` (GitHub org, MIT) | `google/go-github` (BSD-3) | `octocrab` (community) |
| JSON Schema 2020-12 | `ajv` (MIT) | `santhosh-tekuri/jsonschema` (Apache-2.0) | `jsonschema` (Stranger6667, MIT) |
| YAML | `yaml` (ISC) | `yaml/go-yaml` (Apache-2.0); the old `go-yaml/yaml` is archived (GitHub) | `serde_yaml` is archived (GitHub); successors are community forks |
| Supply chain | npm registry; install scripts run by default; plan adds `ajv`, `yaml`, `typescript`, `@types/node` | module proxy and checksum database; prototype pulled 26 modules (`go list -m all`, including build-only ones); `govulncheck` | crates.io; prototype lock has 26 packages; build scripts run at build time (`libsqlite3-sys`); `cargo audit` |
| LLM authoring and review (judged) | very common in training data; type errors caught by `tsc` | small language, explicit code, easy to review in a diff; fast compile feedback | more correct by construction; more compile-fix rounds for agents; Multi-SWE-bench covers TS, Go and Rust ([arXiv 2504.02605](https://arxiv.org/abs/2504.02605)), but its abstract states no per-language ranking, so none is claimed here |
| Iteration speed, measured | no build (type stripping); `tsc` for checks | cold build 8 s with SQLite; rebuild after an edit 0.8 s | cold release build 20 s; rebuild after an edit 0.4 s (small crate) |
| Plan cost | M2 plan exists as is | M2 plan stack sections rewritten ([#32](https://github.com/syllik/deep-dark-factory/issues/32)) | same as Go, plus a cross-build toolchain |

### Result

**Owner choice (2026-11-06, in chat):** Go. ADR: `.ai/decisions.md`, "ADR 2: implementation language".

Go fits best the hybrid model's needs on every target host:
- one static file per arch, built from any machine;
- single-digit-MiB idle memory;
- an image of a few MiB;
- no runtime to install or patch on the host.

The cost is weaker sum-type modelling for the lifecycle, policy and gate. Rust would be the strongest choice on correctness. It loses on cross-builds with SQLite, build time and agent iteration. TypeScript costs nothing to keep, but ships a 117 MiB runtime per host and uses about 8× Go's idle memory.

## Measurements

Throwaway prototypes in a temporary directory outside the repository, deleted after the session. Each prototype performs the same **tick workload**:
1. open a file SQLite database;
2. `CREATE TABLE IF NOT EXISTS`;
3. parse a small JSON tagged union and serialize it;
4. insert it in a transaction;
5. `SELECT count(*)`;
6. spawn `git --version` and read its output;
7. close and exit.

A `hello` variant exits immediately.

- Machine: macOS 26, arm64, 8 CPU cores, 16 GiB RAM, workstation class, with other load present. This is not a weak host. The numbers compare languages; they are not absolute figures for a VPS.
- Versions: Node 24.21.0 (TypeScript run by type stripping); Go 1.27.3 darwin/arm64, `modernc.org/sqlite` v1.61.1, `go build -trimpath -ldflags='-s -w'`; Rust 1.99.1, `rusqlite` 0.37.0 with `bundled` (`libsqlite3-sys` 0.35.0), `serde`/`serde_json` 1.x, release profile with `strip = true`; git 2.56.0.
- Cold start: wall time of 40 runs after 3 warm-up runs, median and p90. Two full passes; the range is the two medians.
- Peak RSS: `/usr/bin/time -l` "maximum resident set size". It covers waited-for children, so a tick's peak includes the `git` child, which was about 8 MiB alone.
- Idle RSS: `ps -o rss` 1.5 s after the tick workload, while the process sleeps.
- Sizes: `ls -l` of the built binaries; official Node tarball checked against `SHASUMS256.txt`; image sizes are compressed layer sums from Docker Hub and `gcr.io` manifests on 2026-11-06.

| Variant | Median, pass 1 / pass 2 | p90, pass 1 / pass 2 | Peak RSS | Idle RSS |
|---|---|---|---|---|
| node hello | 40.9 / 65.2 ms | 58.5 / 111.5 ms | 41.0 MiB | — |
| node tick (`.ts`) | 80.8 / 109.9 ms | 92.2 / 147.1 ms | 70.3 MiB | 69 MiB |
| go hello | 5.3 / 8.5 ms | 7.9 / 10.9 ms | 5.6 MiB | — |
| go tick | 18.5 / 31.0 ms | 26.5 / 43.5 ms | 8.8 MiB | 8 MiB |
| rust hello | 6.5 / 7.6 ms | 9.6 / 20.7 ms | 1.5 MiB | — |
| rust tick | 21.8 / 26.7 ms | 24.5 / 48.7 ms | 8.2 MiB | 2 MiB |
| `git --version` (reference) | 40.9 / 25.8 ms | 75.8 / 43.9 ms | 8.1 MiB | — |

| Artifact | Size |
|---|---|
| Go tick, darwin-arm64 / linux-arm64 / linux-amd64 | 7,291,954 / 7,405,728 / 7,602,336 bytes; linux-arm64 gzip -9: 3,115,508 bytes |
| Rust tick, darwin-arm64 | 2,196,736 bytes |
| Node 24.21.0 `bin/node`, linux-arm64 (official tarball, 30,843,004 bytes xz) | 122,893,672 bytes |
| `node:24.21.0-alpine` (compressed, arm64 / amd64) | 59.2 / 58.8 MiB |
| `node:24.21.0-slim` (compressed, arm64 / amd64) | 77.0 / 77.1 MiB |
| `gcr.io/distroless/static-debian12:nonroot` (compressed, arm64 / amd64) | 0.68 / 0.68 MiB |

Not measured:
- agent CLI memory inside a container (needed for the weak-host floor, [#34](https://github.com/syllik/deep-dark-factory/issues/34));
- behaviour on a real 1 vCPU / 1–2 GB Linux host;
- a Rust Linux binary.

## What the decisions change

| Item | Change | Where |
|---|---|---|
| Spec [§4.1](../specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider) `store` row | `node:sqlite` → embedded SQLite through a cgo-free driver | edited in this PR, Changelog line |
| Spec [§4.4](../specs/2026-11-03-ddf-v1-design.md#44-boundaries) package and stack bullets | npm package and TypeScript on Node 24 → one Go module, static binaries; runtime-model bullet added | edited in this PR, Changelog line |
| `ARCHITECTURE.md` Boundaries | stack line | edited in this PR |
| `.ai/decisions.md` | ADR 1 and ADR 2; the §16.6 `node:sqlite` line superseded | edited in this PR |
| `RESEARCH.md` | link to this file | edited in this PR |
| M2 plan header | note that the stack sections are superseded until rewritten | edited in this PR |
| M2 plan: every section, task and OD that names npm, TypeScript or Node: Architecture, Tech Stack, Global Constraints, Reuse, Hosted CI, PR order, Tasks 0–2, the Verify steps of Tasks 3–13, the Task 9 files, OD-1, OD-2, OD-3, OD-4, OD-6, OD-7, OD-8, OD-11, OD-12, OD-14 (list completed by [#37](https://github.com/syllik/deep-dark-factory/issues/37)); spec [§12](../specs/2026-11-03-ddf-v1-design.md#12-ddfs-own-profile) and the [manual-mode profile](../orchestrator/manual-mode.md#ddfs-own-profile-until-ddfprojectyaml-exists) (manifest files, boundary test, `src/**` globs); the code-conventions doc | rewrite for Go | [#32](https://github.com/syllik/deep-dark-factory/issues/32) |
| Spec [§8.3](../specs/2026-11-03-ddf-v1-design.md#83-workspaces-and-publication), [§8.4](../specs/2026-11-03-ddf-v1-design.md#84-containers), [§9](../specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery), [§14.1](../specs/2026-11-03-ddf-v1-design.md#141-v1-acceptance) | task cell, verified teardown, orphan cleanup, probe | [#33](https://github.com/syllik/deep-dark-factory/issues/33) |
| Spec §4.1 `scheduler`, §8.4 limits, §9 disk floor | host-derived slots and limits, minimum host | [#34](https://github.com/syllik/deep-dark-factory/issues/34) |
| Spec [§8.1](../specs/2026-11-03-ddf-v1-design.md#81-host), D2 wording, §14 M5 row | control plane as a container on Linux | [#35](https://github.com/syllik/deep-dark-factory/issues/35) |
| Unchanged | §5 lifecycle, §6 policy, §7 roles and agent CLIs (still npm-distributed inside run images), §10 forges, D1–D15 decisions other than the stack; [M0 facts](2026-11-m0-facts.md) stay a historical record; `scripts/docs-check.mjs` stays a Node docs tool | — |

## Owner choices

1. **Runtime model**: A ephemeral per task / B spec daemon / C hybrid. **Recommendation:** C. **Chosen in chat:** C.
2. **Language**: TypeScript/Node / Go / Rust. **Recommendation:** Go. **Chosen in chat:** Go.
3. **Package layout for Go** (in [#32](https://github.com/syllik/deep-dark-factory/issues/32)): keep the `src/core/**` and `src/adapters/**` paths, so the profile globs stay valid, or move to Go's `internal/` layout and update the profile. **Recommendation:** keep the `src/` paths and enforce the boundaries with a test. The profile and spec §12 then change only for the manifest files. **Chosen in chat (#32):** the `internal/` + `cmd/` layout, boundaries by a standard-library test.
4. **Control-plane container in V1** ([#35](https://github.com/syllik/deep-dark-factory/issues/35)): in M5, or after V1. **Recommendation:** specify it in M5 for Linux servers. The macOS V1 host keeps the `ddf` user and launchd.
