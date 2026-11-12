# Deep Dark Factory V1 — design

Status: **approved by the owner 2026-11-03** (revision 2 after an independent deep review).
Supersedes as the V1 architecture: `ARCHITECTURE.md` "Open decisions", `docs/controller-v0-decision.md`, research Issue #1, Issue #8 and draft PR #9.
Inputs: Issue #12 (decision to consolidate, carried-over requirements, agent identity requirements), the maintainer-approved autonomous-flow design of a private pilot project (2026-10-28), `syllik/codex-local-runner-archive` `docs/DDF-LESSONS.md`, an audit of the existing agent workflow repositories (2026-11-03).

## 1. Goal and scope

DDF is a self-hosted factory that takes a software task and drives it, without a human in the loop, from intake to a merge into the project's **integration branch** (dev/staging). It works with any Git repository: through a forge adapter (GitHub first) or with a plain Git remote and no forge at all.

V1 is done when one operator runs DDF on one host and two pilot projects (DDF itself and one private product) merge real tasks autonomously into their integration branches (acceptance in §14.1).

**In scope (V1):**
- intake from free-form requests and from existing tasks;
- planning, implementation, independent review, bounded correction, merge into the integration branch;
- per-project policy (autonomy, risk, paths, hard stops) with operator opt-in;
- per-operator routing of roles to agent CLIs and models;
- GitHub adapter with three GitHub Apps; local Git adapter;
- Telegram and CLI as the operator channel;
- documentation that any LLM orchestrator session can follow, with or without the daemon.

**Out of scope (V1):** promotion to production and deploys; dashboards and GitHub Projects sync; pinned status issue (V1.1); webhooks; multi-host workers; GitLab/Gitea/Forgejo adapters; Codex CLI adapter; an Auditor role; free-form tasks via Telegram.

## 2. Maintainer decisions (2026-11-03)

| # | Decision |
|---|---|
| D1 | V1 autonomous merge into the integration branch only for DDF itself and one private pilot product. Every other project starts at `propose` (human merge). |
| D2 | V1 reference deployment: a Linux host with the control plane (`ddfd`) in a container ("Linux container profile"); macOS + colima under a separate non-admin user is the second runtime profile ("macOS colima profile"). The core is host-independent (amended 2026-11-10, #45). |
| D3 | Agent adapters in V1: Claude Code CLI and opencode CLI. |
| D4 | Agent roles: Planner, Executor, Reviewer-correctness (R1), Reviewer-security (R2). Trusted non-AI roles: Publisher, Merge-gate. The daemon is the orchestrator. |
| D5 | Reviewer count is a policy outcome of the risk class (1 or 2), never a hard-coded constant. |
| D6 | Three GitHub Apps: `worker`, `review-correctness`, `review-security`. Fallback if GitHub does not count App approvals: machine users (subject to GitHub terms, §16). |
| D7 | Two intake modes: free-form request (Planner creates the tasks) and an existing task marked for DDF. |
| D8 | No GitHub Projects board in the core. DDF must work on a plain Git remote. |
| D9 | Operator channel: CLI; Telegram deferred to V1.1. The Notify port and the CLI stay (amended 2026-11-10, #45). |
| D10 | Isolation: one container per run, no forge credentials inside. Run-container isolation is a set of properties (§8.4) that every runtime profile provides and proves with one shared probe set (§14.1 probe 6) (amended 2026-11-10, #45). |
| D11 | Operator config lives in a private per-operator repository or folder; secrets never in Git. |
| D12 | The old personal workflow repository is archived after migration; DDF `docs/` becomes the single canon for autonomous and interactive (manual) orchestration. |
| D13 | Local workspace cleanup is a separate track before the pilot. |
| D14 | Build approach: new core generalized from the 2026-10-28 autonomous-flow design; harvest selected ideas from PR #9; close PR #9 as superseded. |
| D15 | Model tiers: `frontier` (never selected automatically), `deep`, `standard`, `routine`, `fast`. Subagents per run: at most 3 by default (advisory, §7.3). |

## 3. Configuration layers

| Layer | Location | Owner | Contents |
|---|---|---|---|
| Product | this repository | DDF maintainers | defaults, role prompts, schemas, templates |
| Operator | a private repo or folder chosen by the operator (`ddf setup` scaffolds it) | the person running DDF | `routing.yaml`, `projects.yaml` (connected projects, forge, **maximum autonomy**, allow-listed humans), limits, notification settings, App IDs |
| Project | `.ddf/project.yaml` in the target repository, always read from the integration branch | the project | branches, verify commands, required checks, risk rules, sensitive/forbidden paths, hard stops, zones, requested autonomy |
| Secrets | the read-only `secrets` mount of the control-plane container (§8.1); on the non-container macOS profile `~ddf/.config/ddf/secrets/` on the host (directory 0700, files 0600, §8.1) | the operator | App private keys, review-signing key, Telegram token, agent CLI tokens and provider keys |

Rules:
- Policy only tightens across layers. A project can never loosen a product invariant; an operator can never loosen a project's policy.
- **Effective autonomy = min(operator maximum for that project, project request).** A repository cannot grant itself autonomy.
- Model names appear only in the operator layer.
- The operator config repository can never be connected as a DDF project.

## 4. Architecture

### 4.1 Core (no network, no forge, no provider)

| Module | Responsibility |
|---|---|
| `model` | Types and versioned schemas: Task, Run, Change, Review, Finding, Question, Decision. Schema IDs `ddf.<name>/v1`. |
| `store` | SQLite (embedded, through a cgo-free driver; §4.4) as the canonical state; append-only event journal; run leases; intents. |
| `lifecycle` | Pure transition functions for the task state machine (§5). |
| `scheduler` | Admits runs against the host capacity (CPU, memory, disk) reported by the Sandbox port, with an operator override (§7.2); per-run limits come from the sizing rule (Issue #34). Zones, dependencies, priority order, global pause (a set of reasons, §9). |
| `policy` | Risk classification, reviewer set, blocking rules, path policy, hard-stop detection, `evaluateMerge` (§6). Pure functions. |
| `workspace` | Daemon-owned clean clones, per-run copies handed to the Sandbox port as content (never as a host path), patch export/apply, patch-id. Never executes repository content on the host (§8.3). |
| `context` | Builds bounded per-role briefs from the task spec, the project profile, `AGENTS.md` and referenced files, all read from the integration branch. |

### 4.2 Ports and V1 adapters

| Port | V1 adapters | Contract (operations) |
|---|---|---|
| Forge | `forge-local`, `forge-github` | list candidate tasks; read intake actor; create task (with task-key marker); find change by task key; publish change; update change body; update branch from base; read checks for a head; rerun checks; post review; read answers and their authors; merge pinned to a head SHA; revert; close/reopen task; set projection labels. Every write supports **dry-run** (logged, not performed). The port takes one or more reviewer identities; the core does not fix their number (the GitHub three-App set, §8.2, is unchanged). |
| Agent | `agent-claude-code`, `agent-opencode` | `run(role, entry, brief, workspace, limits, credentialSource) → (result, patch, usage)` or exactly one failure class: `credential`, `rate-limited` (with a reset time), `retryable` or `fatal`; the result is validated against the role schema (§7.3) |
| Sandbox | `sandbox-docker` (container runtime per runtime profile, §8.1), `sandbox-fake` | create a run cell (container plus any volume or network it needs) labelled with task key and run ID, with limits and network rules; put content in (brief, role bundle, workspace copy); run; get files out (`result.json`, `change.patch`); kill; list resources by label (task key, run ID); destroy by task key, then verify by listing that nothing with that key remains; report host capacity (CPU, memory, disk). No operation takes a host path; how content moves is the adapter's matter (§8.3). An unverifiable teardown is a reported failure (journal + notification), never silent. |
| Notify | `notify-cli`; `notify-telegram` deferred to V1.1 | send event; receive owner answers and commands with sender identity |
| Secrets | `secrets-file` | read a named secret |

### 4.3 Entry points

- `ddfd`: the daemon. Single instance per host (exclusive file lock). One tick per minute: poll forges, advance tasks, start/stop runs. `--dry-run` routes every forge write through dry-run.
- `ddf`: CLI. `setup`, `init <repo>`, `ask <project> "<text>"`, `status`, `show <task>`, `answer <task> "<text>"`, `pause`, `resume`, `apps create`.

### 4.4 Boundaries

- Core never imports adapters; adapters never import each other; the daemon wires them. A CI test (`test/boundaries/`, standard library only) fails on a forbidden import.
- Trusted code = daemon + adapters on the host. Agents run only inside containers, receive a brief and a workspace copy, and return files in `/out`. They never hold forge credentials or the review-signing key.
- **The daemon runs only from a maintainer-promoted ref** of this repository: a release tag (a maintainer-made tag on a `master` commit that passed CI), never the moving tip of the integration branch. Autonomous merges into DDF's integration branch therefore take effect only after a human tags a release.
- V1 ships as one Go module (`internal/core`, `internal/adapters`, `internal/daemon`, `cmd/ddfd`, `cmd/ddf`; layout per the M2 plan) built into static binaries (`ddfd`, `ddf`) for linux and darwin on arm64 and amd64. Split only when a second consumer exists.
- Stack: Go, toolchain pinned in `go.mod`, `CGO_ENABLED=0`; standard library first; an exhaustiveness analyzer for enum-like types runs as a required CI step; every dependency needs a recorded reason. Rationale and measurements: `docs/research/2026-11-runtime-and-language.md`; decision: `.ai/decisions.md` ADR 2 (2026-11-06). `internal/core/workspace` runs the `git` binary, so the control-plane image carries `git` (ADR 2 amendment, 2026-11-10).
- Runtime model: hybrid. One long-lived control plane per host (`ddfd`, the store, the secrets) and a disposable per-task execution side (run containers, workspace copies) that is torn down when the task leaves the conveyor. Decision: `.ai/decisions.md` ADR 1 (2026-11-06); teardown record §4.5, recovery §9; full task-cell rules in Issue #33.

### 4.5 Store and intents

Tables (indicative): `tasks`, `runs`, `changes`, `reviews`, `findings`, `questions`, `intents`, `leases`, `events`.

- Every **forge write** (create task, push, open/update change, post review, update branch, merge, revert, close/reopen task) writes an intent before the call and its outcome after.
- Every forge object DDF creates carries a hidden task-key marker (`ddf:task-key=<key>`), so reconciliation finds it by key, not by branch name or title.
- **Model runs are not intents.** A run interrupted by a restart is discarded and retried within the task's run budget (§5.3).
- Task key: a digest of `{forge, repository, source kind, source id}`; every store row, lease and signature is scoped by it.
- **Teardown record.** Per task, the store records that its run cell was destroyed and verified (Sandbox port, §4.2), with the time (indicative: a column on `tasks` or a `teardowns` row; decided in M2). Recovery (§9) writes the same record.

## 5. Task lifecycle

```text
inbox → planning → ready → running → publishing → ci → review → merge → done
                     ↑                              ↑            │
                  waiting                           └─ fixing ←──┘
flags (off the conveyor): needs-owner, hold, failed; global: paused (reasons: owner, credential, revert-red, runtime, security; §9)
```

### 5.1 Intake

- **Request:** `ddf ask`, or a forge task created from the "DDF request" form. The Planner decomposes it into child tasks. More children than `planning.maxAutoChildren` (default 3) → `needs-owner` with the plan; otherwise children go to `ready`. The request closes when all children close.
- **Existing task:** marked for DDF (GitHub: label `ddf` or comment `/ddf`) → `planning`.
- **Intake is accepted only from allow-listed actors.** The daemon reads who applied the label or wrote the command (GitHub: timeline events) and accepts it only if that login is in the operator allow-list, or is the `worker` App creating a child or follow-up task. Issue forms never auto-apply `ddf`; the "DDF request" form applies `ddf:request`, which is inert until an allow-listed actor adds `ddf`.
- Tasks with `deferred*`, `needs-owner`, `hold` are never picked.

### 5.2 States and transitions

**Planner** verdicts: `READY` (spec, acceptance criteria, risk class proposal, zones, dependencies), `DECOMPOSE`, `ASK` (→ `needs-owner`), `REJECT` (→ `needs-owner` with reason). If the spec is ambiguous but product intent is clear, the Planner proposes an `AGENT-DEFAULT` decision; the Executor writes it into the project's decision log as part of the change, so it is reviewed. Owner-written tasks may be in any language; the Planner writes the spec in English.

**waiting:** a `ready` task whose dependencies are open, or whose zones are held, waits. A zone is held from `running` until the holding task is merged or leaves the conveyor. Two tasks overlapping in files outside any declared zone: the second is synced with the base (§5.2 Sync) after the first merges.

**Priority order** when a slot frees: tasks already in `fixing` or `merge`, then `ci`/`review` work, then `ready` tasks by priority (forge label `priority:high|normal|low`, default normal), then by satisfied dependencies, then oldest first; follow-ups last.

**Executor** runs in a container on a workspace copy, may use subagents, runs the profile's verify inside the container, and exports a patch (§8.3). Verdicts: `COMPLETE` → `publishing`; `BLOCKED` → `needs-owner`.

**Publisher (trusted code)** applies the patch to a clean clone (§8.3), checks path policy and risk class, runs the local gate in a fresh container, makes one commit and one push per revision (no force-push, rebase or amend), and opens or updates the change. It writes the change body and the squash message itself from a template: agent text is included only in a sanitized section (closing keywords, `[skip ci]`-style directives and mentions stripped), and `Closes <task>` is added by DDF only for the task itself. Local gate failure → back to the Executor as a correction batch.

**CI** (§6.6 required checks) on the current head. For `forge-local` the daemon runs verify in a fresh container as CI. One automatic rerun per head; pass-on-rerun is recorded as a flake; a test flaking in two changes produces a `needs-owner` quarantine proposal (never an auto-executed task). CI failure → Executor CI-fix attempt.

**Review.** The reviewer set comes from policy (§6.2). Reviewers run in parallel on the same head, each in a fresh container, read-only, with no repository-controlled agent configuration (§8.4) and no executor transcript. Blocking findings → `fixing`. P2 → one follow-up task per change (`ddf:followup`, auto-picked at lowest priority, risk class at least the parent's). P3 → appended to the project's standing minors task, never executed. Findings on a follow-up task are never turned into further follow-ups: they go to minors.

**Fixing.** One correction batch = one Executor run addressing all blocking findings, then CI, then a full re-review of the new head with prior findings. The Executor may answer a finding "won't fix + reason". A finding is identified across rounds by `{role, category, location, normalized problem}`; the same finding confirmed blocking twice → `needs-owner`.

**Sync with base.** Behind and clean → the daemon merges the base into the branch (a daemon-authored merge commit). Conflict → an Executor sync batch, then full re-review. **Workflow files in the base:** before any sync or publish that brings base changes into the branch (the daemon's clean merge, or the push of an Executor sync batch in the conflict case), the daemon checks deterministically whether the base changed CI configuration since the merge base (GitHub: `git diff --name-only <merge-base> <base> -- .github/workflows`; other forges: their CI config paths). If it did, the `worker` App may not write them (GitHub: no `workflows` permission, §8.2), so no forge call is made: → `needs-owner` with the reason "base changed workflow files; update the branch manually", and the sync batch is not consumed. A forge refusal of the same kind (GitHub: 403 "… without `workflows` permission") is only a fallback signal and maps to the same outcome. A branch update made by a human is not a daemon-created base merge, so the new head goes through full re-review (§6.6 rule 4).

**Merge.** `evaluateMerge` passes → squash merge pinned to the reviewed head SHA → DDF closes the task. Gate refusals map to transitions:

| Refusal | Next |
|---|---|
| behind base | sync |
| checks missing/pending/red | `ci` |
| review missing/stale/not approved | `review` |
| path policy or hard stop | `needs-owner`, task drops to `propose` (§6.4) |
| flag or global pause | stay |
| change converted to draft or closed by a human | `hold`, notify |
| task closed by a human | leaves the conveyor; open change left for the human |
| mergeable state unknown, forge read error | stay, backoff |
| head moved during merge | `ci` for the new head |

**Revert.** If the integration branch's CI is red on the DDF merge commit itself (after one rerun), DDF opens a revert change of risk class `revert` (§6.2) and reopens the task. A second DDF-attributed red within 24 h, across the project → global pause (reason `revert-red`, lifted only by `ddf resume` or `/resume`) and Telegram alert. Red caused by a human merge is not attributed to DDF.

**Owner answers** are accepted only from allow-listed humans (forge comments) or the configured Telegram user ID. An answer resumes a task from `needs-owner` but never authorizes an autonomous merge of a hard-stop or path-policy change (§6.4).

### 5.3 Bounds

| Bound | Default |
|---|---|
| Planner run timeout / invalid result | 30 min / 1 retry, then `needs-owner` |
| Executor run timeout / invalid result | 3 h / 1 retry, then `needs-owner` |
| Reviewer run timeout / invalid result | 1 h / 1 retry, then `needs-owner` |
| CI wait for one head | 2 h, then `needs-owner` |
| CI-fix attempts per revision | 2 |
| Correction batches per change | 3 |
| Sync batches per change | 2 |
| Agent runs per task, all roles | 20 (`maxRunsPerTask`) |

A [revision](../concepts/lifecycle.md#revision) is the initial execution or one correction batch as published by the Publisher; CI-fix pushes made within it are counted inside it.

Exceeding any bound → `needs-owner`.

## 6. Policy

### 6.1 Autonomy levels

- `propose` (default): DDF drives the task to a ready change; a human merges.
- `merge-integration`: the merge-gate merges into the integration branch.

Effective level per §3. No level promotes to production.

### 6.2 Risk classes and reviewers

| Class | Condition (deterministic) | Reviewers |
|---|---|---|
| `docs` | only `risk.docs` paths; agent-instruction files are never `docs` | R1 |
| `low` | code, changed lines ≤ `risk.lowMaxLines` (default 200, lockfiles and pure renames excluded from the count), no `risk.sensitive` or hard-stop path | R1 |
| `standard` | other code | R1 + R2 |
| `sensitive` | any `risk.sensitive` path (money, keys, auth, data, migrations) or default hard-stop globs (deploy, infrastructure, container files, IaC) | R1 + R2, R2 at `deep` |
| `revert` | an exact revert of one DDF merge commit, produced by DDF | R1 |

The class is computed from the diff and the profile. The Planner may raise it; R1 may raise it (adds R2). Nothing lowers it. When R2 is not in the set, R1 also runs the hard-stop checklist.

### 6.3 Findings

`{id, severity: P0..P3, category: correctness|acceptance|security|data|hard-stop|docs, securityLevel?, evidence, location?, fixHint?}`. Blocking: P0, P1, security High/Medium, category `hard-stop`.

### 6.4 Hard stops

Always `needs-owner`: secrets and credentials, deploy and infrastructure, real money, destructive data operations, forge or repository settings, other repositories, purchases. Projects add categories and path globs. A change that hit a hard stop or path policy is never merged autonomously, even after an owner answer: it continues at `propose`.

### 6.5 Path policy

Forbidden by default (agents may not change them; owner-only):
<!-- sync:forbidden -->
- `.ddf/**`;
- forge and CI configuration: `.github/**`, `.gitlab-ci.yml`, equivalents;
- Git behaviour: `.gitmodules`, `.gitattributes`, `.gitignore` of the repository root;
- agent configuration and instructions: `AGENTS.md`, `CLAUDE.md`, `.claude/**`, `.mcp.json`, `opencode.json`, `.opencode/**`, `.codex/**`, `.cursor/**`;
- `CODEOWNERS`, `LICENSE`, `.env*`;
- symbolic links (any added or changed symlink is refused in V1).
<!-- /sync:forbidden -->

The project's `ciEntrypoints` (scripts and manifests CI relies on, e.g. `package.json`, guard scripts) are not forbidden but force the `sensitive` class, so dependency changes stay possible while CI weakening gets R2 at `deep`.

Evaluated from the integration branch copy of the profile, never from the change itself. Projects add more.

### 6.6 Merge gate

`evaluateMerge` returns `MERGE` only if every condition holds; each refusal reason is an enumerated value with its own test:

1. effective autonomy is `merge-integration`; no global pause;
2. task state is `merge`; no `needs-owner`/`hold` flag; the change never hit a hard stop or path policy;
3. change open, not draft;
4. every reviewer required by the risk class has a valid signed review with verdict APPROVE and zero blocking findings, for the current head **or** for an ancestor head where every later commit is a base merge the daemon itself created (its SHA recorded in the store from the daemon's own intent; author metadata does not count) and `git patch-id --verbatim` of the change is unchanged; a base merge made by anyone else (e.g. a human "Update branch") never qualifies;
5. every check in the profile's `checks.required` is present and successful on the current head (with no list: at least one check run, all successful);
6. path policy passes;
7. change not behind base and mergeable;
8. any forge read error → refusal (fail-closed).

The merge is pinned to the evaluated head (GitHub: merge with the `sha` parameter; `forge-local`: compare-and-swap ref update). A moved head fails the merge.

**Review signatures:** HMAC-SHA256 with a key only the daemon holds, over the canonical JSON (sorted keys, UTF-8) of `{taskKey, forge, repository, changeId, head, role, verdict, blockingCount, findingsDigest}`, where `findingsDigest` is SHA-256 of the canonical JSON array of findings. The gate reads reviews from the store and verifies signatures; forge comments are a projection. On GitHub, reviewer Apps also submit native reviews (APPROVE / REQUEST_CHANGES) and re-approve after a sync.

### 6.7 Project profile

```yaml
version: ddf.project/v1
integrationBranch: master
autonomy: merge-integration          # requested; effective = min(operator max, this)
verify:                              # run inside containers only
  - npm ci
  - npm run verify
checks:
  required: [verify]                 # forge check names the gate requires
image: .ddf/Dockerfile               # optional; default base image otherwise
registries: [registry.npmjs.org]     # verify runs `npm ci`; omit when the image carries the dependencies
risk:
  lowMaxLines: 200
  docs: ["docs/**", "**/*.md"]
  sensitive: ["src/payments/**", "src/auth/**", "migrations/**"]
ciEntrypoints: ["package.json", "scripts/ci/**"]   # force `sensitive`
forbidden: ["ops/**"]
hardStops: [secrets, deploy, real-money, data-destructive]
zones:
  migrations: ["migrations/**"]
  deps: ["package.json", "package-lock.json"]
planning:
  maxAutoChildren: 3
change:
  branch: "{type}/issue-{n}-{slug}"
  merge: squash
  template: .ddf/change-template.md  # optional PR/MR body template
reviewers:
  r2DifferentProvider: false         # require R2 on a different provider than the Executor
```

Project dependencies come from the project image (`image`) by default. A project whose verify installs at run time lists the registry hosts in the top-level key `registries` (unique host names, no scheme, path or port; default none). Those hosts join the egress allow-list of that project's Executor, local-gate and `forge-local` CI containers (§8.4). `.ddf/**` is a forbidden path (§6.5), so only a human-merged change can add a registry.

In `change.branch`, `{type}` is the Conventional Commits type of the change (`feat`, `fix`, `docs`, `refactor`, `test`, `ci`, `chore`), `{n}` the task number and `{slug}` a short kebab-case slug.

## 7. Roles and routing

### 7.1 Roles and default tiers

| Role | Tier | Tools | Output |
|---|---|---|---|
| Planner | `standard`; `deep` for `sensitive` | read-only | verdict + spec + acceptance + risk proposal + zones + deps |
| Executor | `standard` | full inside the container | `COMPLETE` / `BLOCKED` + summary + verify evidence + patch |
| R1 correctness | `standard` | read/search only, no command execution | findings, verdict |
| R2 security/data | `standard`; `deep` for `sensitive` | read/search only, no command execution | findings + hard-stop checklist, verdict |
| Helpers (subagents) | `fast` for search/read; never write code | read-only | — |
| Reports, digests | `routine` | none | — |

The Planner's tier is chosen before the class is known: `deep` when the task text names a path the profile classes `sensitive` (a `risk.sensitive` glob, a `ciEntrypoints` entry, a forbidden path or a default hard-stop glob), otherwise `standard`. If a pass below `deep` returns `READY` with risk proposal `sensitive`, that result is discarded (not posted as the planner result) and the Planner re-runs once at `deep`; the re-run counts toward `maxRunsPerTask` (§5.3) and is not the invalid-result retry. A class raised to `sensitive` after planning (at publishing or by R1) does not re-run the Planner; it changes the reviewer set only.

Reviewers judge the diff, the spec and the CI results; they do not execute repository code. `frontier` is never selected by DDF. Projects may only raise tiers or set `r2DifferentProvider`.

### 7.2 Operator routing

```yaml
# <operator-config>/routing.yaml — model names are examples
tiers:
  deep:     [{adapter: claude-code, provider: anthropic, model: opus-5.5, effort: high}]
  standard: [{adapter: claude-code, provider: anthropic, model: opus-5.5, effort: medium},
             {adapter: opencode, provider: <provider>, model: <provider/model>}]   # fallback on rate limit
  routine:  [{adapter: claude-code, provider: anthropic, model: sonnet-5.5, effort: medium}]
  fast:     [{adapter: claude-code, provider: anthropic, model: haiku-5.5, effort: low}]
roles:
  r2: {prefer: [{adapter: opencode, provider: <other-provider>, model: <other-provider/model>}]}   # optional
limits:
  # capacity: operator override of the host capacity reported by the Sandbox port; key and schema land with Issue #34
  # maxConcurrentContainers: 2   # optional operator cap, no default; every container counts: Executor, each reviewer, local gate, forge-local CI
  maxSubagentsPerRun: 3          # advisory, passed to the agent; not enforceable by the CLIs
```

A routing entry has `adapter`, `provider`, `model` and optional `effort`. `provider` is required (pattern `^[a-z][a-z0-9-]*$`) and names the model provider the entry calls. DDF compares only this field and never derives it from `model`. With `reviewers.r2DifferentProvider: true`, R2 uses only entries whose `provider` differs from the `provider` of the entry that ran the Executor for the reviewed head. Adapter and model are not compared. With no such entry, the no-entry-left rule below applies.

Entries are tried in order. A rate-limited entry is marked cooling until its reset time; the next entry of the tier is used; with no entry left the task waits and one notification is sent. The scheduler admits runs against the host capacity (CPU, memory, disk) reported by the Sandbox port; an operator setting in `limits` overrides the reported values (its key and schema land with the sizing rule, Issue #34). Per-run limits come from that sizing rule. `limits.maxConcurrentContainers` is an optional operator cap, not the scheduling input. Container limits must fit the resources of the container runtime; `ddf status` reports a misfit.

### 7.3 Agent adapter contract

**Port level.** Every agent adapter implements `run(role, entry, brief, workspace, limits, credentialSource) → (result, patch, usage)` or returns exactly one failure class.

- Inputs: the role; the routing entry (`provider`, `model`, `effort`, §7.2); the brief; the workspace content (the run's workspace copy, read-only for the Planner and reviewers, §8.3); limits (wall-clock timeout, `maxSubagentsPerRun` as advice, optional `maxCostUsd`); the credential source.
- Outputs: the result, validated against `schemas/<role>.result.json`; for the Executor, the patch made by the trusted wrapper (§8.3); usage (model ID, tokens, cost), recorded in the journal.
- Exactly one failure class, chosen by the adapter:
  - `credential`: global pause with reason `credential` (§9), never retried. This includes a credential source that yields nothing or an empty value. The adapter checks for that before any provider call.
  - `rate-limited` with a reset time: the entry cools until that time, then the next entry is used (§7.2).
  - `retryable`: backoff on the same entry, not fallback.
  - `fatal`: a failed run within the §5.3 bounds. This covers a missing or invalid result and a timeout.
- **Credential source.** The adapter receives its credential through a credential source, a broker interface; the core never passes the credential as a value. The adapter receives only the routed entry's credential. The first implementation returns the routed entry's stored credential from the Secrets port, as today. A later implementation can return a per-run token that is valid only against a control-plane endpoint proxying the provider, with no port change (§8.4). Accepted schedule (§15): run-container egress is restricted per adapter in M3; no real pay-per-token provider key enters a run container, because the per-run token implementation is in place before the first pay-per-token key is loaded; subscription tokens that cannot be proxied stay visible inside their routed adapter's container, an accepted risk on a dedicated account, and publishing any of them stays not accepted (§8.4 redaction).
- Every call uses the entry's model, never an adapter default or a free fallback model.
- The adapter loads no repository-controlled agent configuration. Project instructions (`AGENTS.md`) reach the agent only through the brief, read from the integration branch.
- Role prompts live once in `prompts/`; each adapter renders them into its native form in a directory outside the workspace.
- All agent-originated output passes the redaction of §8.4 before any forge write or notification.
- Optional per-task `maxCostUsd` for API-key-billed entries. No per-stage human grants and no per-call USD reservations.

**CLI-in-container profile.** These rules apply only to an adapter that runs a CLI inside the run container. `agent-claude-code` and `agent-opencode` implement this profile.

- Non-interactive CLI inside the container, as a non-root user (`claude -p --output-format json …`, `opencode run --format json --agent … -m …`). Permission prompts are disabled inside the container; the container is the security boundary.
- **Pinned CLIs.** Claude Code from npm `@anthropic-ai/claude-code`; opencode from npm `opencode-ai`, 1.x line (verified 1.19.5). opencode's GitHub-release 2.x line has incompatible flags and config schema and is not supported in V1. The base image records exact versions; the adapter refuses an unknown version.
- **Configuration comes only from DDF.** The adapter starts the CLI with DDF's rendered role bundle as the only settings source: project and user settings, hooks, MCP servers and plugins from the workspace are disabled. Isolation switches (exact command lines in `docs/research/2026-11-m0-facts.md`): Claude Code `--setting-sources ''` with `--settings <bundle>` and `--strict-mcp-config --mcp-config <bundle>`, brief on stdin; `--tools` restricts built-in tools for reviewers but is not isolation; `--bare` is unusable with a subscription token. opencode: the `OPENCODE_*` environment set from the research doc plus `--pure`. Residual Claude Code sources these flags do not disable: account-synced remote settings (applied regardless of local flags) and built-in plugins and agents; DDF therefore uses a dedicated account and token, and the M3 probe `claude-config-isolation` fails closed if the remote settings carry hooks, permissions or MCP servers.
- **Credential delivery.** The adapter takes the routed credential from the credential source and delivers it into the container as environment; the container holds only that credential.
- **No run-time installs** of the agent CLI and its own packages. The base image pre-installs the CLIs and opencode's plugin SDK at pinned versions; a run in which the CLI or its own packages are installed from a registry at run time fails (M3 probes `cli-version-pin`, `agent-home-state`). Project dependencies come from the project image (`image`) by default, or from the project's declared `registries` (§6.7).
- Inputs mounted read-only: brief, role bundle. Outputs: `/out/result.json` (the result) and, for the Executor, `/out/change.patch` (the patch).
- Every CLI call passes the entry's model explicitly (never a CLI default) and runs under the wall-clock timeout.
- Usage (model ID, token usage, cost) is parsed from the CLI output.
- Each adapter renders the role prompts into the CLI's native form (Claude Code system prompt and agent files; opencode agent config).
- **Failure classification** maps CLI signals onto the four port-level classes. Fields read: Claude Code `is_error`, `api_error_status` and the assistant `error` field, never `subtype`; opencode `error` event `statusCode`/`isRetryable`.
  - `credential`: authentication signals, meaning `authentication_failed`, a Claude Code `result` starting with "Not logged in", a non-zero exit with no `api_error_status` and authentication text, or opencode 401/403. A missing or empty credential is checked before the CLI starts (it otherwise surfaces as an opaque error).
  - `rate-limited`: HTTP 429 only.
  - `retryable`: other retryable errors (5xx, `overloaded`, `isRetryable: true`).
  - `fatal`: everything else (opencode `UnknownError` with the stderr log attached, other non-zero exits, missing or invalid `/out/result.json`, timeout).

## 8. Identity, secrets and isolation

### 8.1 Host

Two runtime profiles. Both run `ddfd` from a maintainer-promoted DDF ref (§4.4) and give run containers the same isolation properties (§8.4). The operator's own home directory and personal forge login are not accessible in either.

- **Linux container profile (reference deployment from V1):** a Linux host; the control plane (`ddfd`, the store, the secrets) runs in a container; a dedicated non-admin host user `ddf` owns its mounts. The control-plane container has three named mounts: `config` (operator config, read-only), `secrets` (read-only) and `data` (store, clones, run logs). It holds the container runtime's socket to start run containers; on a rootful runtime that socket is host-equivalent. Whether the runtime is rootful or rootless is open and decided by the probe spike; this spec holds for both.
- **macOS colima profile (second runtime profile):** a dedicated non-admin OS user `ddf` with a private home (mode 700); `ddfd` runs from launchd as `ddf`, not in a container. The fixed home-directory locations are the defaults of this non-container profile in place of the mounts: `~ddf/.config/ddf/secrets/` for secrets, `~ddf/work/` and `~ddf/runs/` for data; the operator config is the folder the operator chose (§3). launchd supervises `ddfd`, not the container VM: `ddfd` checks VM health and restarts the VM itself (§8.4); a launchd colima job is only a boot-time starter (`RunAtLoad`, no `KeepAlive`, so it never races `ddfd`'s own restart).
- On both profiles the host-side locations of `secrets` and `data` (mount sources on the Linux container profile, the `~ddf` defaults on the macOS colima profile) are owned by `ddf` and not readable by any other non-root host user: directories mode 0700, secret files mode 0600, for rootful and rootless runtimes alike.
- **Weak host:** a host with 1–2 GB of memory orchestrates; heavy verify runs in forge CI. A project's CI may use a self-hosted runner: that is project CI, which DDF only reads as required checks (§8.5); DDF itself needs no runner. Such a runner is ephemeral, isolated and never runs as the control-plane host user.

### 8.2 GitHub identity

Three Apps created by `ddf apps create` (GitHub App manifest flow), owned by the operator, installed only on selected repositories:

| App | Permissions | Not granted |
|---|---|---|
| `worker` | contents: write, pull_requests: write, issues: write, checks: read, statuses: read, actions: write (rerun), metadata: read | administration, workflows |
| `review-correctness` | contents: read, pull_requests: write, metadata: read | contents write |
| `review-security` | same as `review-correctness` | contents write |

The token broker mints an installation token per operation, narrowed to one repository and the permissions that operation needs (lifetime ≤ 1 h). Tokens never enter containers. The missing `workflows` permission makes GitHub reject pushes that touch workflow files.

### 8.3 Workspaces and publication

- Daemon clones live in the `data` mount (§8.1; macOS colima profile default: `~ddf/work/<forge>/<owner>/<repo>`), used only by trusted code, with hooks disabled (`core.hooksPath` to an empty directory, no fsmonitor, no filter drivers).
- Each run gets a fresh copy (its own `.git`, no remotes, no credentials). The copy is per run: never shared with the host's working files or another run, reachable only by that run's container, and destroyed with the run cell; it is never a bind mount of a control-plane or host path. The Sandbox port takes it in as content and returns files out (§4.2); how the adapter moves it (an archive stream, or a per-run volume the control plane fills) is the adapter's matter. Read-only for the Planner and reviewers, writable for the Executor; the local gate and `forge-local` CI get the applied tree as content the same way.
- After the Executor finishes, the adapter's trusted wrapper (not the agent) runs `git add -A && git diff --cached --binary <base>` in the container and writes `/out/change.patch`. The Publisher applies it to the daemon's clean clone with hooks and config neutralised, refuses symlinks, paths outside the repository and any path under `.git/`, and evaluates path policy on the applied tree.
- **The host never executes repository content.** Verify, the local gate and `forge-local` CI always run in fresh containers. The local gate runs on the applied tree **before** the commit is pushed, so a failing gate produces no forge write. The host never runs an agent CLI against a repository copy, not even `opencode models` or `opencode debug`: opencode loads project plugins on every invocation.
- The Publisher refuses a patch that adds a `.opencode/` or `.claude/` path (already forbidden by §6.5) or any `node_modules/` path unless the project profile allows it: failed run, with the reason in the journal.

### 8.4 Containers

- One per run, labelled with task key and run ID (§4.2); base image from `images/` or the project's `image`; fresh, empty home; memory and pids limits per run from the sizing rule (Issue #34), admitted against host capacity (§7.2).
- **Isolation properties.** Every run container has: no route to the host, the control plane or private address ranges (DNS excepted); all capabilities dropped; no new privileges; a non-root user; pids and memory limits; no container runtime socket. One probe set checks them (§14.1 probe 6). Each runtime profile says how it provides them and must pass the same probes.
- **Pause reason for isolation rules** (every profile). Each runtime profile defines a runtime instance identifier (for example a boot or daemon start identifier). Each time the daemon applies the isolation rules it records that identifier and a fingerprint of the applied rules; this is the "unchanged" baseline. Before every container start the daemon reads the current identifier, checks the rules against the fingerprint and runs the probes. A changed identifier is an observed runtime restart; a fingerprint mismatch with the same identifier is tampering. Precedence:
  1. Rules absent or changed (fingerprint mismatch) while no runtime restart was observed (same identifier) → tamper signal: global pause with reason `security` (§9), no automatic lift, even when a re-apply would succeed; a silent re-apply does not clear it.
  2. After an observed runtime restart (changed identifier), or when the rules match but a probe fails, the daemon re-applies the rules once and re-probes; rules still absent or unverifiable → container runtime unavailable, reason `runtime` (§9).
  3. Runtime down and the daemon's own restart attempts failed → `runtime` (§9).
  `runtime` lifts automatically once health check, isolation-rule check and probes pass; `security` only by `ddf resume` / `/resume`.
- **Linux container profile:** provides the properties through the runtime's per-container options and network rules; how exactly is decided by the probe spike together with rootful or rootless.
- **macOS colima profile:** colima firewall rules, applied via `colima ssh`, are this profile's way to provide "no route". On colima, firewall rules inside the VM drop container traffic to the VM's gateway to the host loopback and to the VM itself, except DNS; containers reach only the internet. The rules do not survive a VM restart. The daemon applies them (`colima ssh -- sudo …`) after every VM start or recreation; colima provision hooks may add an early layer but are never the guarantee. Before every container start the daemon checks that the rules exist and runs the host-reachability probe; on failure it follows the precedence above. Rules found missing while no VM restart was observed (VM boot ID, this profile's runtime instance identifier, unchanged since the daemon last applied them) are a tamper signal: global pause with reason `security` (§9), no automatic lift. If the VM is down, `ddfd` restarts it as `ddf` (`colima start`, bounded attempts), then applies and probes the rules. The probe also holds in §14.1.
- Services (databases, browsers) run inside the container.
- **Egress per run**, enforced from M3 by each runtime profile's own mechanism: an agent run reaches the provider endpoint of its routed adapter; Executor runs, the local gate and `forge-local` CI also reach the project's declared `registries` (§6.7); nothing else. DNS behaves as in the isolation properties. Before M3 this restriction is not yet enforced (§15).
- **Credential source.** Credentials reach a run only through the credential source, the broker interface of the Agent port (§7.3); the core never passes a credential as a value. The first implementation returns the routed entry's stored credential from the Secrets port, as today. A later implementation can return a per-run token that is valid only against a control-plane endpoint proxying the provider, with no port change.
- **Redaction before publication.** Before every forge write and every notification send, all agent-originated text (review projection comments, change body, commit message, patch content, Telegram messages) is redacted by secret patterns and by the exact values of the secrets loaded into that run's container. A patch containing a loaded secret value is refused: hard stop `secrets` (§6.4), failed run.

### 8.5 CI prerequisites for autonomous projects

Agent code runs in the project's CI. A project may be set to `merge-integration` only if its change-triggered CI workflows run with read-only repository permissions and no deployment or production secrets, and any self-hosted CI runner holds no secrets beyond what tests need. `ddf init` checks what it can and records the rest as an onboarding checklist item.

### 8.6 Audit, kill switch, rulesets

- The journal records per action: role, task, App and permission set and token expiry (never the token), commits, model, usage.
- `ddf pause` or Telegram `/pause` stops new runs (pause reason `owner`); uninstalling the Apps revokes forge access immediately.
- On repositories that support rulesets, DDF recommends: pull request required, required checks, one approval, no bypass actors. Reviewer count by risk class stays enforced by the gate.

## 9. Notifications, failures and recovery

**Telegram (immediate):** `needs-owner` (task, category, one-line question; a reply to the message is the answer), global pause with reason, rate limit (once per reset window), credential failures (→ global pause, reason `credential`). **Daily digest:** merged, running, waiting for the owner, usage. Commands: `/status`, `/pause`, `/resume`. Accepted only from the configured Telegram user ID in the configured chat.

**CLI:** `ddf status`, `ddf show <task>`, `ddf answer`, `ddf pause`, `ddf resume`.

**Global pause** is a set of reasons: `owner` (`ddf pause`, `/pause`), `credential`, `revert-red` (§5.2 Revert), `runtime`, `security`. The daemon is paused while the set is non-empty. Automatic lift clears only `runtime`; every other reason is cleared only by `ddf resume` or `/resume`. An M2 test covers the lift rule.

**Tick:** every 60 s; forge polling with conditional requests (ETag). No webhooks in V1.

**Recovery:** on start the daemon takes the instance lock and lists execution resources by label (Sandbox port, §4.2). Per task key and run ID from the labels: resources whose task is terminal or unknown to the store are destroyed and verified, and the teardown recorded (§4.5); resources of a non-terminal task are, in V1, killed and their run discarded (retried within budget). Re-adopting live runs later changes only that branch, not the port. An unverifiable teardown is reported (journal + notification). The daemon then reconciles every unresolved forge intent by task-key marker (e.g. a change for the task already exists → adopt it). Unreconcilable intents → `needs-owner`; never a blind retry of a forge write.

| Failure | Reaction |
|---|---|
| forge error | fail-closed, exponential backoff |
| free disk below floor (default 15 GB) | no new runs, notify |
| container runtime unavailable (the daemon's restart attempts failed, or isolation rules absent or unverifiable after one re-apply following an observed runtime restart or a probe failure with unchanged rules; §8.4) | global pause (reason `runtime`), notify; `runtime` is lifted automatically, with a notification, once health check, isolation-rule check and probes pass again |
| isolation rules absent or changed while no runtime restart was observed (§8.4), even when a re-apply would succeed | global pause (reason `security`), notify; no automatic lift |
| agent authentication failure (§7.3) | global pause (reason `credential`), notify; no automatic lift |
| host sleep | the daemon's supervisor restarts it; expired leases are released |

**Run logs:** in the `data` mount (macOS colima profile default: `~ddf/runs/<task>/<run>/`): brief, result, sanitized CLI output; kept 30 days; secrets redacted by pattern and by loaded secret values (§8.4) before writing.

## 10. Forge adapters

### 10.1 `forge-local`

- Remote: any Git URL (`file://` bare repository or `ssh://`).
- Tasks and requests: `ddf ask` or task files; stored in the DDF store; allow-list = local operator.
- Change: a branch; reviews and findings in the store; merge by the gate with a compare-and-swap ref update.
- CI: verify in a fresh container.
- Used for fully offline operation and as the forge of DDF's own end-to-end tests.

### 10.2 `forge-github`

| DDF concept | GitHub |
|---|---|
| task | Issue; children are sub-issues |
| request | Issue from the "DDF request" form (label `ddf:request`, inert until accepted) |
| intake mark | label `ddf` or comment `/ddf` by an allow-listed actor (read from timeline events) |
| state projection | labels `ddf:planning`, `ddf:ready`, `ddf:waiting`, `ddf:running`, `ddf:ci`, `ddf:review`, `ddf:fixing`, `ddf:merge`; flags `needs-owner`, `ddf:hold`, `ddf:followup`, `ddf:minors` |
| change | pull request opened by the `worker` App |
| review | native PR review from the reviewer App + signed marker comment (projection) |
| checks | check runs and commit statuses on the head |
| merge | squash merge with `sha` pinned |

Labels are a projection for humans; the store is canonical. DDF never reads or writes GitHub Projects.

## 11. Documentation set (the orchestrator canon)

```text
docs/
  concepts/      lifecycle, policy, roles, routing, identity, config-layers
  orchestrator/  manual-mode, supervisor-mode, handoff
  operator/      setup, config-reference, security-model (incl. accepted risks)
  project/       onboarding (ddf init, CI prerequisites), project-yaml-reference
  adapters/      forge-github (Apps), forge-local, agent-claude-code, agent-opencode, sandbox-docker
  specs/         design documents (this file)
prompts/         planner, executor, reviewer-correctness, reviewer-security
schemas/         role results, project.yaml, routing.yaml, projects.yaml
templates/project/  .ddf/project.yaml, issue forms (task, request, bug), AGENTS.md skeleton
```

- **manual-mode**: the procedure for an interactive LLM session without the daemon — same lifecycle, roles, bounds and policy; executor subagents with exact briefs; reviews in separate sessions; ≤3 correction batches. In manual mode a human always merges. DDF itself is built this way until M5.
- **supervisor-mode**: an interactive session that operates the factory through `ddf` (requests, answers, unblocking); it does not write code.
- **handoff**: replaces dated local handoff files; state lives in the forge and `ddf status`; a handoff is one templated comment on a tracking task.
- Docs are in English. Roles documentation links to `prompts/` instead of duplicating it.

## 12. DDF's own profile

DDF is a pilot of itself (D1). Its profile marks every security-relevant area as `sensitive` (R1 + R2, R2 at `deep`): `internal/core/policy/**`, `internal/core/lifecycle/**`, `internal/core/workspace/**` (hook neutralisation, patch apply), `internal/core/store/migrations/**`, `internal/daemon/**` (it drives the gate, the merge and the intents), `cmd/ddfd/**` (the daemon's entry point: it wires the adapters, the secrets and the lock), the Publisher `internal/core/publisher/**` (sanitizer, path checks), the token broker, the review-signature code, `internal/adapters/agent/**` (settings isolation, read-only tools), `internal/adapters/sandbox/**` (firewall), the merge code in `internal/adapters/forge/**`, `prompts/**`, `schemas/**` and every forbidden path. Its `ciEntrypoints` are `scripts/docs-check.mjs` and, when they exist, `go.mod`, `go.sum` and `test/boundaries/**`. Until DDF has its own project file, the working copy of this profile is in [manual mode](../orchestrator/manual-mode.md#ddfs-own-profile-until-ddfprojectyaml-exists). Combined with §4.4 (the daemon runs only from a maintainer-promoted ref), an autonomously merged change to DDF cannot alter the running factory until a human promotes it.

## 13. Migration

**Harvested from PR #9 (rewritten, not merged):** scoped task key; intent before every forge write; uncertain outcome blocks retries until reconciled; idempotent publication; review dedup by head; reviewer independence by principal, not by vendor.

**Dropped from PR #9:** per-stage human grants and 120 s grant freshness; per-call USD reservations; hard-coded human merge; optional single review.

**Moved from the personal workflow repository:** role contracts, tiers, subagent rules, review and correction rules, change/handoff templates, prompts, documentation contract (without personal style). Personal items (profile sync, local paths, owner logins, task history) stay out of DDF.

**Moved from the private pilot design:** state machine, merge gate with head pinning, signed markers, severity and follow-up routing, follow-up loop guard, zones and waiting, CI flake policy, escalation, container runner, single-instance lock, dry-run. Dropped: running agents on a production host, a runner watchdog with sudo, a scoped exception in the old workflow repository.

**This repository (first PR, M1):**
- `AGENTS.md`: DDF is developed by its own docs (manual mode until M5); the managed routing block of the old workflow repository is removed; branch roles stay.
- `README.md`, `ARCHITECTURE.md`: human merge stays the default; autonomous merge is a per-project opt-in with operator consent; link this spec.
- `.ai/decisions.md`: research-only gate lifted by the maintainer (2026-11-03); D1–D15.
- Close #1 (completed, outcome = this spec), #8 and PR #9 (superseded), #10 (no archived code or dependencies adopted).

**Target repositories:** remove the old managed routing block; `AGENTS.md` keeps project rules and one line pointing to `.ddf/project.yaml`.

**Old workflow repository:** archived in M6 after every managed repository has dropped its routing block.

## 14. Milestones

| Milestone | Content | Exit criteria | Merges |
|---|---|---|---|
| M0 Prep | Local workspace cleanup (separate track). Fact check (§16). | Answers recorded in `.ai/decisions.md` | maintainer |
| M1 Docs | This spec, `docs/` (concepts, orchestrator, operator skeleton), `prompts/`, `schemas/`, new `AGENTS.md`, issue closures | An interactive session can run a DDF task by `manual-mode` alone | maintainer |
| M2 Offline core | `model`, `store`, `lifecycle`, `policy`, `scheduler`, `workspace`, `forge-local`, `sandbox-fake`, fake agent; import-boundary test; hosted CI | Full lifecycle (intake → merge, fixing, sync, gate refusals, revert, restart recovery) passes in tests with fakes | maintainer |
| M3 Real execution | `sandbox-docker`, base image, isolation rules, `context`, `agent-claude-code`, `agent-opencode` | One real task on a fixture repository through `forge-local` with a real model, both adapters; isolation probes pass | maintainer |
| M4 GitHub + channel | `forge-github` (`apps create`, token broker, intake actors, PRs, reviews, pinned merge, labels), CLI (`notify-cli`); `notify-telegram` deferred to V1.1 | Dry-run against a sandbox repository: correct reads, intended writes logged, no writes performed | maintainer |
| M5 Install + pilot | Linux container profile (control-plane container, `ddf` host user, three mounts), operator config, secrets; macOS colima profile (`ddf` user, launchd) as the second profile; 24 h dry run on DDF; DDF at `merge-integration`; pilot product profile PR and CI prerequisites; Telegram deferred to V1.1 | §14.1 acceptance on a small always-on arm64 Linux host | maintainer until DDF autonomy is on |
| M6 Retire | Archive the old workflow repository; remove routing blocks elsewhere; replace standalone agent role configs | No repository references the old canon | DDF / maintainer |

Each milestone gets its own implementation plan.

### 14.1 V1 acceptance

- On the private pilot product, three real tasks go from intake to merge into the integration branch with no human action; at least one goes through a correction batch; at least one `needs-owner` question/answer round trip via the CLI (Telegram deferred to V1.1).
- M5 acceptance runs on a small always-on arm64 Linux host in the Linux container profile (reference deployment). Development and probes imitate this weak host in a Linux arm64 VM limited to 1 vCPU and 1–2 GB. The second runtime profile passes the same probes.
- Probes, each must hold:
  1. a forged or replayed review marker is rejected;
  2. a reviewer cannot be made to approve through workspace agent config (planted hooks/settings are ignored);
  3. a change touching a forbidden path or a symlink is not published;
  4. planted Git hooks/config in the Executor copy never run on the host;
  5. an intake label or answer from a non-allow-listed account is ignored;
  6. a run container has the isolation properties of §8.4 (no route to the host, the control plane or private address ranges, DNS excepted; all capabilities dropped; no new privileges; non-root user; pids and memory limits; no runtime socket), checked by the one probe set in [security-model.md](../operator/security-model.md#probes) that every runtime profile must pass;
  7. a daemon restart mid-run produces no duplicate task or change;
  8. a head pushed between gate evaluation and merge makes the merge fail;
  9. a Claude rate limit falls back to the opencode entry.

## 15. Accepted risks (V1)

- A run container holds only the credential of its routed adapter, received through the credential source (§8.4). A real pay-per-token provider key never enters a run container: the per-run token implementation of the credential source is in place before the first pay-per-token key is loaded. Agent CLI subscription tokens that cannot be proxied stay visible inside their routed adapter's container; this is an accepted risk, on a dedicated account. Publishing any of them is not accepted (§8.4 redaction).
- Run-container egress is restricted per adapter from M3 (§8.4): an agent run reaches only its routed adapter's provider endpoint, and Executor runs, the local gate and `forge-local` CI also reach the project's declared registries. Before M3 this restriction is not yet enforced.
- Even with the M3 egress restriction, egress is allowed per host, not per method, so the reachable endpoints remain an exfiltration channel for the credential inside the container: a declared registry that accepts uploads (for example `npm publish` with an auth token the run supplies itself) is reachable from Executor runs, the local gate and `forge-local` CI, and the routed provider endpoint accepts requests under any account's key. A credential usable from outside the run (an agent CLI subscription token) can leave through either. The §8.4 redaction covers only DDF's own forge writes and notifications, not these channels. This is an accepted risk, on the dedicated account: a pay-per-token provider key never enters a run container and a per-run token is valid only against the control-plane endpoint, and a registry is added only by a human-merged change to `.ddf/project.yaml` (§6.7).
- IPv6 egress is not filtered in V1; the isolation probe fails if a container gets a global IPv6 route.
- The host is a workstation: availability depends on it being on.
- Private repositories on plans without rulesets have no forge-side protection; there the merge gate is the only enforcement.
- Project CI executes agent-written code; mitigated only by the prerequisites in §8.5.

## 16. Facts to verify in M0

Answers, evidence and probe definitions: `docs/research/2026-11-m0-facts.md`; decisions in `.ai/decisions.md` ("M0 facts (2026-11)"). Item 5 concerns the macOS colima profile; the Linux container profile (reference deployment) is checked by the probe spike, which also decides rootful or rootless.

1. Whether an approval submitted by a GitHub App counts toward a ruleset's required approvals. If not: machine users for reviewers, and GitHub's terms on how many machine accounts one person may hold. **Answered:** unverified (terms documented): M4 probe `app-approval-counts` (reviewer App with `pull_requests: write`; also try `contents: read`/`write` variants); fallback machine users (≤ 1 free machine account per person per GitHub terms, per research).
2. Installation token narrowing (repository and permission subset) for all three Apps. **Answered:** documented, supported as §8.2 assumes (repository IDs + permission subset, ≤ 1 h); M4 probe `token-narrowing`.
3. Whether `update-branch` or a pushed base merge is rejected when the base contains workflow-file changes and the App lacks `workflows`. **Answered:** unverified, rejected with 403 per a third-party report (pushed merge inferred); outcome in §5.2 Sync with base and §6.6 rule 4; M4 probe `sync-with-workflow-change`.
4. `claude -p` and `opencode run` inside a Linux container as non-root with a long-lived token: non-interactive behaviour, exact flags to disable workspace settings/hooks/MCP and use only DDF's bundle, rate-limit error shapes, read-only tool restriction for reviewers. **Answered:** Claude Code verified; opencode verified with free models only (no provider credential available); rate-limit shapes unverified; outcome in §7.3, §8.3 and §8.4; open: `opencode-executor-output`, `opencode-reviewer-readonly` (paid provider); M3 probes `cli-version-pin`, `agent-noninteractive`, `agent-home-state`, `claude-config-isolation`, `opencode-config-isolation`, `claude-reviewer-readonly`, `opencode-reviewer-readonly`, `claude-executor-output`, `opencode-executor-output`, `claude-auth-error-shape`, `opencode-error-shape`, `claude-rate-limit-shape`, `opencode-rate-limit-shape`, `secret-exfil-redaction`.
5. colima under a non-login service user via launchd: start-up, resource limits, VM firewall blocking the host gateway, coexistence with other users' services on the host. **Answered:** verified (start-up, limits, firewall, rules lost on VM restart, no launchd restart of the VM); outcome in §8.1, §8.4 and §9; unverified: start after a host reboot without login, launchd respawn when the colima process exits, provision hooks elevating with `sudo -n`, IPv6, coexistence with a second colima instance (the owner's own); M3 probes `vm-firewall-reapply`, `runtime-recovery` (with `ddfd` under the LaunchDaemon), `coexistence`; M5 probes `colima-boot`, `coexistence`.
6. `node:sqlite` stability and warnings on the pinned Node 24 release. **Answered:** `node:sqlite` works without warning on 24.21.0; stability 1.2 (release candidate); pinned to 24.21.0, minimum 24.15.0 (§4.4).

## Changelog

- 2026-11-04 M0: §4.4 — stack pins Node ≥ 24.15.0 (facts: docs/research/2026-11-m0-facts.md#nodesqlite-16-item-6)
- 2026-11-04 M0: §5.2 Sync with base — forge-refused sync on base workflow-file changes → `needs-owner` without consuming a sync batch; human branch update → full re-review (facts: docs/research/2026-11-m0-facts.md#sync-when-the-base-changed-workflow-files-16-item-3)
- 2026-11-04 M0: §6.6 rule 4 — a base merge not created by the daemon never qualifies for the ancestor exception (facts: docs/research/2026-11-m0-facts.md#sync-when-the-base-changed-workflow-files-16-item-3)
- 2026-11-04 M0: §7.3 — opencode pinned to npm `opencode-ai` 1.x, 2.x unsupported; version check (facts: docs/research/2026-11-m0-facts.md#versions)
- 2026-11-04 M0: §7.3 — isolation switches named; `--tools` not isolation; `--bare` unusable; brief on stdin (facts: docs/research/2026-11-m0-facts.md#claude-code-making-ddfs-bundle-the-only-configuration-source, #opencode-making-ddfs-bundle-the-only-configuration-source)
- 2026-11-04 M0: §7.3 — explicit model flag and wall-clock timeout on every CLI call (facts: docs/research/2026-11-m0-facts.md#opencode-auth-and-rate-limit-error-shapes, #opencode-executor-and-json-output)
- 2026-11-04 M0: §7.3 — error classification from `is_error`/`api_error_status`/`error`, not `subtype`; auth → credential failure, rate limit → next entry (facts: docs/research/2026-11-m0-facts.md#claude-code-auth-and-rate-limit-error-shapes, #opencode-auth-and-rate-limit-error-shapes)
- 2026-11-04 M0: §8.1 — launchd supervises `ddfd`, not the VM; `ddfd` restarts the VM (facts: docs/research/2026-11-m0-facts.md#restart-supervision-by-launchd)
- 2026-11-04 M0: §8.3 — host never runs an agent CLI against a repository copy; `.opencode/`, `.claude/`, `node_modules/` additions suspicious (facts: docs/research/2026-11-m0-facts.md#opencode-making-ddfs-bundle-the-only-configuration-source, #state-written-to-the-home-directory)
- 2026-11-04 M0: §8.4 — fresh empty container home (facts: docs/research/2026-11-m0-facts.md#state-written-to-the-home-directory)
- 2026-11-04 M0: §8.4 — daemon applies VM firewall after every VM start, verifies and probes before every container start, re-applies once; firewall also blocks the VM except DNS; daemon restarts the VM (facts: docs/research/2026-11-m0-facts.md#rule-persistence-across-vm-restarts, #vm-firewall-rules, #what-84-and-9-should-say-forced-changes)
- 2026-11-04 M0: §9 — runtime unavailable only after the daemon's restart/re-apply fails; missing firewall counts; pause lifts automatically with a notification; launchd never restarts the VM (facts: docs/research/2026-11-m0-facts.md#what-84-and-9-should-say-forced-changes)
- 2026-11-04 M0: §16 — items 1–6 marked answered with pointers (facts: docs/research/2026-11-m0-facts.md)
- 2026-11-04 M0 review: §4.1, §5 — global pause is a set of reasons (facts: docs/research/2026-11-m0-facts.md#what-84-and-9-should-say-forced-changes)
- 2026-11-04 M0 review: §4.4 — Node pinned to 24.21.0, `node:sqlite` needs ≥ 24.15.0 (facts: docs/research/2026-11-m0-facts.md#nodesqlite-16-item-6)
- 2026-11-04 M0 review: §5.2 Sync with base — workflow-file check is deterministic before any sync or publish of base changes, incl. Executor sync batches; 403 only a fallback (facts: docs/research/2026-11-m0-facts.md#sync-when-the-base-changed-workflow-files-16-item-3)
- 2026-11-04 M0 review: §5.2 Revert — pause reason `revert-red`, lifted only by resume (facts: docs/research/2026-11-m0-facts.md#what-84-and-9-should-say-forced-changes)
- 2026-11-04 M0 review: §7.3 — residual Claude Code config sources; dedicated account; remote-settings check fails closed (facts: docs/research/2026-11-m0-facts.md#claude-code-making-ddfs-bundle-the-only-configuration-source)
- 2026-11-04 M0 review: §7.3 — no run-time registry installs; pinned opencode plugin SDK in the base image (facts: docs/research/2026-11-m0-facts.md#state-written-to-the-home-directory)
- 2026-11-04 M0 review: §7.3 — one credential per container; agent output redacted before forge writes and notifications (facts: docs/research/2026-11-m0-facts.md#credentials-inside-the-container)
- 2026-11-04 M0 review: §7.3 — deterministic credential/rate-limit/backoff rules (facts: docs/research/2026-11-m0-facts.md#claude-code-auth-and-rate-limit-error-shapes, #opencode-auth-and-rate-limit-error-shapes)
- 2026-11-04 M0 review: §8.1 — boot-time colima starter has no `KeepAlive` (facts: docs/research/2026-11-m0-facts.md#recipe-for-m5)
- 2026-11-04 M0 review: §8.3 — Publisher refuses `.opencode/`, `.claude/` and unallowed `node_modules/` additions (facts: docs/research/2026-11-m0-facts.md#state-written-to-the-home-directory)
- 2026-11-04 M0 review: §8.4 — missing rules without an observed VM restart → pause reason `security` (facts: docs/research/2026-11-m0-facts.md#rule-persistence-across-vm-restarts)
- 2026-11-04 M0 review: §8.4 — redaction by pattern and loaded secret values before every forge write and notification; patch with a loaded secret refused (facts: docs/research/2026-11-m0-facts.md#credentials-inside-the-container)
- 2026-11-04 M0 review: §8.6, §9 — pause reasons, lift rule, `security` and `credential` rows, run-log redaction by secret value (facts: docs/research/2026-11-m0-facts.md#what-84-and-9-should-say-forced-changes, #credentials-inside-the-container)
- 2026-11-04 M0 review: §14.1 probe 6 — also covers container→VM access (facts: docs/research/2026-11-m0-facts.md#host-reachability-without-a-firewall)
- 2026-11-04 M0 review: §15 — publication of container tokens not accepted; IPv6 egress unfiltered, probe fails on a global IPv6 route (facts: docs/research/2026-11-m0-facts.md#credentials-inside-the-container, #vm-firewall-rules)
- 2026-11-04 M0 review: §16.1 — wording `unverified (terms documented)` (facts: docs/research/2026-11-m0-facts.md#app-approvals-and-required-reviews-16-item-1)
- 2026-11-04 M0 review: §16.4 — opencode verified with free models only; open probes and full probe list (facts: docs/research/2026-11-m0-facts.md#recommended-adapter-command-lines)
- 2026-11-04 M0 review: §16.5 — unverified items listed; `coexistence` probe (facts: docs/research/2026-11-m0-facts.md#container-limits)
- 2026-11-04 M0 review: §7.3 — adapter checks the routed credential is present before starting a CLI; missing → credential failure, not a failed run (facts: docs/research/2026-11-m0-facts.md#agent-clis-16-item-4)
- 2026-11-05 Issue #20: §5.3 — "revision" defined (initial execution or one correction batch, CI-fix pushes counted inside) (see docs/concepts/lifecycle.md#revision)
- 2026-11-05 Issue #20: §4.4 — no separate release branch; the daemon runs from a release tag on `master` (owner decision 2026-11-05, see .ai/decisions.md)
- 2026-11-05 Issue #22: §6.2, §12 — wording: on `sensitive` only R2 runs at `deep`; R1 stays at `standard` as in §7.1; no behaviour change (see docs/concepts/roles.md)
- 2026-11-05 Issue #22: §7.1 — Planner tier predicted from the paths the task names; a `sensitive` proposal from a pass below `deep` re-runs the Planner once at `deep`, counted in `maxRunsPerTask` (see docs/concepts/roles.md#planner)
- 2026-11-05 Issue #22: §6.7 — `change.branch` placeholders `{type}`, `{n}`, `{slug}` defined (see docs/project/project-yaml-reference.md#change)
- 2026-11-06 Issue #31: §4.1, §4.4 — stack changed from TypeScript on Node 24 to Go (static binaries, cgo-free SQLite driver); §16 item 6 stays as a historical answer (see docs/research/2026-11-runtime-and-language.md, .ai/decisions.md ADR 2)
- 2026-11-06 Issue #31: §4.4 — runtime model recorded as hybrid: long-lived control plane, disposable per-task execution side; follow-ups #33, #34, #35 (see docs/research/2026-11-runtime-and-language.md, .ai/decisions.md ADR 1)
- 2026-11-06 Issue #32: §4.4 — Go layout `internal/` + `cmd/` and a standard-library boundary test (owner choices, see docs/plans/2026-11-05-m2-offline-core.md OD-15, OD-16)
- 2026-11-06 Issue #32: §12 — Go paths; aligned with the manual-mode profile: migrations, daemon and Publisher globs and the `ciEntrypoints` added (closes #30; see docs/orchestrator/manual-mode.md#ddfs-own-profile-until-ddfprojectyaml-exists)
- 2026-11-10 Issue #45: §2 D2, D9, D10 — reference deployment is the Linux container profile, macOS colima profile second; Telegram deferred to V1.1; isolation by properties (owner decisions of #43, see .ai/decisions.md)
- 2026-11-10 Issue #45: §3, §4.2 — secrets in the read-only `secrets` mount; Sandbox runtime per profile; Notify `notify-cli` in V1, `notify-telegram` in V1.1
- 2026-11-10 Issue #45: §7.2 — container limits fit the container runtime's resources
- 2026-11-10 Issue #45: §8.1 — two runtime profiles; control-plane container with `config`, `secrets`, `data` mounts; rootful or rootless open; weak host and project self-hosted runners
- 2026-11-10 Issue #45: §8.4 — run-container isolation properties, one probe set, per-profile provision; colima firewall as the macOS profile's "no route"; profile-neutral `security`/`runtime` precedence from a runtime instance identifier and a rule fingerprint recorded at apply time
- 2026-11-10 Issue #45: §9 — pause reasons `runtime` and `security` defined by isolation-rule properties; host sleep row profile-neutral
- 2026-11-10 Issue #45: §14, §14.1 — M3 isolation rules; M5 on a small always-on arm64 Linux host, probes in a 1 vCPU, 1–2 GB Linux arm64 VM; probe 6 names the properties and links the single list; `needs-owner` round trip via the CLI
- 2026-11-10 Issue #45: §15, §16 — IPv6 line names the isolation probe; §16 item 5 scoped to the macOS colima profile
- 2026-11-10 Issue #46: §4.1 — scheduler admits runs against host capacity reported by the Sandbox port with an operator override, per-run limits from Issue #34; workspace hands per-run copies to the port as content
- 2026-11-10 Issue #46: §4.2 — Sandbox port operations: labelled run cell, content in, run, files out, kill, list by label, destroy by task key and verify, report capacity; no host path arguments
- 2026-11-10 Issue #46: §4.4 — control-plane image carries `git` (ADR 2 amendment); runtime-model bullet points to the teardown record and recovery
- 2026-11-10 Issue #46: §4.5 — per-task teardown record (destroyed, verified, time), indicative
- 2026-11-10 Issue #46: §7.2 — fixed `maxConcurrentContainers: 2` replaced by host capacity with operator override; `maxConcurrentContainers` an optional cap
- 2026-11-10 Issue #46: §8.3 — per-run copy rule reconciled: never shared, never a host or control-plane bind mount, transport is the adapter's matter
- 2026-11-10 Issue #46: §8.4 — containers labelled with task key and run ID; fixed 3 GB RAM, 2 CPU defaults removed, limits from the sizing rule (Issue #34)
- 2026-11-10 Issue #46: §9 — single recovery rule by label: terminal or unknown task → destroy, verify, record; non-terminal → V1 kill and discard
- 2026-11-10 Issue #47: §1 — the egress item removed from the Out of scope list; per-run egress is specified in §8.4
- 2026-11-10 Issue #47: §4.2 — Agent port signature `run(role, entry, brief, workspace, limits, credentialSource) → (result, patch, usage)` with four failure classes; Forge port takes one or more reviewer identities, the core does not fix their number
- 2026-11-10 Issue #47: §6.7 — `registries` example key; project dependencies come from `image` by default, declared registries join the egress allow-list of Executor, local-gate and `forge-local` CI containers
- 2026-11-10 Issue #47: §7.2 — routing entries carry a required `provider`; `reviewers.r2DifferentProvider` compares only `provider`
- 2026-11-10 Issue #47: §7.3 — split into Port level (signature, inputs, outputs, failure classes `credential`, `rate-limited`, `retryable`, `fatal`, credential source) and CLI-in-container profile (CLI rules, credential delivery, CLI error mapping); no-run-time-installs scoped to the agent CLI and its own packages
- 2026-11-10 Issue #47: §8.4 — per-run egress rule enforced from M3; credentials reach a run only through the credential source, first and later implementations
- 2026-11-10 Issue #47: §15 — credential and egress risks replaced by the credential-source schedule and the per-adapter egress restriction from M3 (owner decisions 3 and 4 of #43, see .ai/decisions.md)
- 2026-11-11 Issue #54: §15 — registries and the routed provider endpoint named as a residual exfiltration channel for the in-container credential, accepted on the dedicated account; no method-level registry enforcement planned (see docs/operator/security-model.md#accepted-risks)
- 2026-11-11 Issue #49: §3, §8.1 — host-side `secrets` and `data` locations owned by `ddf`, directories 0700, secret files 0600, rootful and rootless
- 2026-11-11 Issue #49: §8.3, §9 — daemon clones and run logs in the `data` mount; `~ddf/work`, `~ddf/runs` are macOS colima profile defaults
- 2026-11-11 Issue #50: §14 — M4 row: `notify-telegram` deferred to V1.1, as in D9 and §4.2 (minor from PR #48)
- 2026-11-12 Issue #39: §12 — `cmd/ddfd/**` added to the `sensitive` paths (owner choice, see docs/orchestrator/manual-mode.md#ddfs-own-profile-until-ddfprojectyaml-exists)
