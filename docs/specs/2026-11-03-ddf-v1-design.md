# Deep Dark Factory V1 — design

Status: **draft for owner review** (2026-11-03).
Supersedes as the V1 architecture: `ARCHITECTURE.md` "Open decisions", `docs/controller-v0-decision.md`, research Issue #1, Issue #8 and draft PR #9.
Inputs: Issue #12 (decision to consolidate and carried-over requirements, agent identity requirements), the owner-approved autonomous-flow design of a private pilot project (2026-10-28), `codex-local-runner-archive/docs/DDF-LESSONS.md`, an audit of the existing agent workflow repositories (2026-11-03).

## 1. Goal and scope

DDF is a self-hosted factory that takes a software task and drives it, without a human in the loop, from intake to a merge into the project's **integration branch** (dev/staging). It works with any Git repository: through a forge adapter (GitHub first) or with a plain Git remote and no forge at all.

V1 is done when one operator runs DDF on one host and two pilot projects (DDF itself and one private product) merge real tasks autonomously into their integration branches (acceptance in §13).

**In scope (V1):**
- intake from free-form requests and from existing tasks;
- planning, implementation, independent review, bounded correction, merge into the integration branch;
- per-project policy (autonomy, risk, paths, hard stops);
- per-operator routing of roles to agent CLIs and models;
- GitHub adapter with three GitHub Apps; local Git adapter;
- Telegram and CLI as the operator channel;
- documentation that any LLM orchestrator session can follow, with or without the daemon.

**Out of scope (V1):** promotion to production and deploys; dashboards and GitHub Projects sync; webhooks; multi-host workers; egress allow-listing; GitLab/Gitea/Forgejo adapters; Codex CLI adapter; an Auditor role; free-form tasks via Telegram.

## 2. Owner decisions (2026-11-03)

| # | Decision |
|---|---|
| D1 | V1 autonomous merge into the integration branch only for DDF itself and the owner's private pilot product. Every other project starts at `propose` (human merge). |
| D2 | V1 host: the owner's Mac under a separate non-admin user; later a VPS or own hardware. The core is host-independent. |
| D3 | Agent adapters in V1: Claude Code CLI and opencode CLI. |
| D4 | Agent roles: Planner, Executor, Reviewer-correctness (R1), Reviewer-security (R2). Trusted non-AI roles: Publisher, Merge-gate. The daemon is the orchestrator. |
| D5 | Reviewer count is a policy outcome of the risk class (1 or 2), never a hard-coded constant. |
| D6 | Three GitHub Apps: `worker`, `review-correctness`, `review-security`. Fallback if GitHub does not count App approvals: machine users (subject to GitHub terms, see §15). |
| D7 | Two intake modes: free-form request (Planner creates the tasks) and an existing task marked for DDF. |
| D8 | No GitHub Projects board in the core. DDF must work on a plain Git remote. |
| D9 | Operator channel: CLI and Telegram. |
| D10 | Isolation: one container per run (colima on macOS), no forge credentials inside. |
| D11 | Operator config lives in a private per-operator repository (for the owner: a private `ddf-config`); secrets never in Git. |
| D12 | The old personal workflow repository is archived after migration; DDF `docs/` becomes the single canon for autonomous and interactive (manual) orchestration. |
| D13 | Local workspace cleanup is a separate track before the pilot. |
| D14 | Build approach: new core generalized from the 2026-10-28 autonomous-flow design; harvest selected ideas from PR #9; close PR #9 as superseded. |
| D15 | Model tiers follow the owner's standard: `frontier` (never selected automatically), `deep`, `standard`, `routine`, `fast`. Subagents per run: at most 3 by default. |

## 3. Configuration layers

| Layer | Location | Owner | Contents |
|---|---|---|---|
| Product | this repository | DDF maintainers | defaults, role prompts, schemas, templates |
| Operator | a private repo or folder chosen by the operator (`ddf setup` scaffolds it) | the person running DDF | `routing.yaml` (tiers → adapter/model), `projects.yaml` (connected projects, forge, host paths), limits, notification settings, App IDs |
| Project | `.ddf/project.yaml` in the target repository, always read from the integration branch | the project | branches, verify commands, risk rules, sensitive/forbidden paths, hard stops, zones, autonomy level |
| Secrets | `~ddf/.config/ddf/secrets/` mode 0600 on the host | the operator | App private keys, review-signing key, Telegram token, agent CLI tokens and provider keys |

Precedence: product defaults < operator < project for policy that only tightens (more reviewers, higher tier, more forbidden paths). A project can never loosen a product invariant, and an operator can never loosen a project's policy. Model names appear only in the operator layer.

## 4. Architecture

### 4.1 Core (no network, no forge, no provider)

| Module | Responsibility |
|---|---|
| `model` | Types and versioned schemas: Task, Run, Change, Review, Finding, Question, Decision. Schema IDs `ddf.<name>/v1`. |
| `store` | SQLite (Node `node:sqlite`, no dependency) as the canonical state; append-only event journal; run leases; intents. |
| `lifecycle` | Pure transition functions for the task state machine (§5). |
| `scheduler` | Run slots, zones (no two concurrent runs in one zone), priority order, global pause. |
| `policy` | Risk classification, reviewer set, blocking rules, path policy, hard-stop detection, `evaluateMerge` (§6). Pure functions. |
| `workspace` | Daemon-owned clones, per-run isolated copies, patch-id, local verify using profile commands. |
| `context` | Builds bounded per-role briefs from the task spec, project profile, `AGENTS.md` and referenced files. |

### 4.2 Ports and V1 adapters

| Port | V1 adapters | Contract |
|---|---|---|
| Forge | `forge-local`, `forge-github` | list/claim tasks, create tasks, read checks, publish change, post review, merge, close task, post comment, set projection labels |
| Agent | `agent-claude-code`, `agent-opencode` | `run(role, brief, workspace, limits) → result` validated against the role schema |
| Sandbox | `sandbox-docker` (colima on macOS, Docker on Linux), `sandbox-fake` | create/run/kill a container with mounts and limits |
| Notify | `notify-telegram`, `notify-cli` | send event, receive owner answers and commands |
| Secrets | `secrets-file` | read a named secret |

### 4.3 Entry points

- `ddfd`: the daemon. One tick per minute: poll forges, advance tasks, start/stop runs.
- `ddf`: CLI. `setup`, `init <repo>`, `ask <project> "<text>"`, `status`, `show <task>`, `answer <task> "<text>"`, `pause`, `resume`, `apps create`.

### 4.4 Boundaries

- Core never imports adapters; adapters never import each other; the daemon wires them. A CI test fails on a forbidden import.
- Trusted code = daemon + adapters on the host. Agents run only inside containers, receive a brief and a workspace copy, and return `result.json`. They never hold forge credentials or the review-signing key.
- V1 ships as one npm package (`src/core`, `src/adapters`, `src/cli`). Split into packages only when a second consumer exists.
- Stack: TypeScript on Node 24, Node built-ins first (`node:sqlite`, `node:test`, `fetch`); every dependency needs a recorded reason.

### 4.5 Store contents

Tables (indicative): `tasks`, `runs`, `changes`, `reviews`, `findings`, `questions`, `intents`, `leases`, `events`. Every externally visible action (open change, post review, merge, close task, model invocation) writes an **intent** before the call and its outcome after. On restart, unresolved intents are reconciled against the forge before anything is retried (§9).

## 5. Task lifecycle

```text
inbox → planning → ready → running → ci → review → merge → done
                                      ↑            │
                                      └─ fixing ←──┘
flags (off the conveyor): needs-owner, blocked, failed, hold; global: paused
```

**Intake.**
- *Request*: `ddf ask`, or a forge task created with the "DDF request" form. The Planner decomposes it into child tasks. More children than `planning.maxAutoChildren` (default 3) → `needs-owner` with the plan; otherwise children go to `ready`. The request closes when all children close.
- *Existing task*: marked for DDF (GitHub: label `ddf` or comment `/ddf`) → `planning`. Tasks with `deferred*`, `needs-owner` or `hold` are never picked.

**Planner verdicts:** `READY` (spec, acceptance criteria, risk class proposal, zones, dependencies), `DECOMPOSE`, `ASK` (question → `needs-owner`), `REJECT` (not actionable → `needs-owner` with reason). If the spec is ambiguous but product intent is clear, the Planner chooses and records an `AGENT-DEFAULT` decision in the project's decision log. Owner-written tasks may be in any language; the Planner writes the spec in English.

**Executor.** Runs in a container on a workspace copy, may use up to 3 subagents, runs the profile's local verify. Verdicts: `COMPLETE` → Publisher; `BLOCKED` → `needs-owner`. Missing/invalid result or timeout → one automatic retry, then `needs-owner`.

**Publisher (trusted code).** Checks path policy (from the integration branch), runs the local gate, makes one commit and one push per revision (no force-push, rebase or amend, no AI attribution), opens or updates the change with the project's change template.

**CI.** All required checks green on the current head. For `forge-local` the daemon re-runs verify in a fresh container as CI. One automatic rerun per head; pass-on-rerun is recorded as a flake; a test flaking in two changes becomes a quarantine task. At most two CI-fix attempts per revision, then `needs-owner`.

**Review.** The reviewer set comes from policy (§6.2). Reviewers run in parallel on the same head, each in a fresh container with a read-only workspace and without the executor transcript. Findings: blocking (P0, P1, security High/Medium, any hard stop) → `fixing`; P2 → one follow-up task per change (`ddf:followup`); P3 → appended to the project's standing minors task, which is never executed.

**Fixing.** At most 3 correction batches, then `needs-owner`. After each batch: CI, then full re-review of the new head with prior findings. The Executor may answer "won't fix + reason"; the same finding confirmed blocking twice → `needs-owner`.

**Sync with base.** Behind and clean → the daemon merges the base into the branch; reviews survive when `git patch-id --stable` is unchanged. Conflict → an Executor sync batch (budget 2), then full re-review.

**Merge.** `evaluateMerge` passes → squash merge into the integration branch → DDF closes the task. If the integration branch turns red after a DDF merge, DDF opens a revert change (needs green CI and R1), and reopens the task. A second red in a row → global pause and Telegram alert.

**Owner answers** (Telegram reply, `ddf answer`, or a forge comment) are attached to the task, which resumes where it stopped.

**Timeouts (defaults):** Planner 30 min, Executor 3 h, reviewer 1 h.

## 6. Policy

### 6.1 Autonomy levels (project)

- `propose` (default): DDF drives the task to a ready change; a human merges.
- `merge-integration`: the merge-gate merges into the integration branch.

No level promotes to production.

### 6.2 Risk classes and reviewers

| Class | Condition (deterministic) | Reviewers |
|---|---|---|
| `docs` | only paths matching `risk.docs` | R1 |
| `low` | code, changed lines ≤ `risk.lowMaxLines` (default 200), no `risk.sensitive` path | R1 |
| `standard` | other code | R1 + R2 |
| `sensitive` | any `risk.sensitive` path (money, keys, auth, data, migrations) | R1 + R2 at `deep` |

The class is computed from the diff and the profile. The Planner may raise it; R1 may raise it (adds R2). Nothing lowers it.

### 6.3 Findings

`{id, severity: P0..P3, category: correctness|acceptance|security|data|hard-stop|docs, securityLevel?, evidence, location?, fixHint?}`. Blocking: P0, P1, security High/Medium, category `hard-stop`.

### 6.4 Hard stops

Always `needs-owner`: secrets and credentials, deploy and infrastructure, real money, destructive data operations, forge or repository settings, other repositories, purchases. Projects add categories and path globs.

### 6.5 Path policy

Forbidden by default: `.ddf/**`, CI configuration (`.github/workflows/**` and equivalents), `CODEOWNERS`, `LICENSE`, `.env*`. Projects add more. Evaluated from the integration branch copy of the profile, never from the change itself.

### 6.6 Merge gate

`evaluateMerge` returns `MERGE` only if every condition holds; each refusal reason is an enumerated value with its own test:

1. project autonomy is `merge-integration`; no global pause;
2. task state is `merge`; no `needs-owner`/`hold` flag;
3. change open, not draft;
4. every reviewer required by the risk class has a valid signed review for the current head (or a head with equal patch-id) with verdict APPROVE and zero blocking findings;
5. all required checks green on the current head;
6. path policy passes;
7. change not behind base and mergeable;
8. any forge read error → refusal (fail-closed).

Review signatures: HMAC-SHA256 over `{changeRef, head, role, verdict, blockingCount, findingsDigest}` with a key only the daemon holds. On GitHub, reviewer Apps also submit native reviews (APPROVE / REQUEST_CHANGES) for visibility and rulesets; the gate trusts only the signature.

### 6.7 Project profile

```yaml
version: ddf.project/v1
integrationBranch: master
autonomy: merge-integration          # or propose
verify:                              # local gate and forge-local CI
  - npm ci
  - npm run verify
image: .ddf/Dockerfile               # optional; default base image otherwise
risk:
  lowMaxLines: 200
  docs: ["docs/**", "**/*.md"]
  sensitive: ["src/payments/**", "src/auth/**", "migrations/**"]
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

## 7. Roles and routing

### 7.1 Roles and default tiers

| Role | Tier | Output |
|---|---|---|
| Planner | `standard`; `deep` for `sensitive` | verdict + spec + acceptance + risk proposal + zones + deps |
| Executor | `standard` | `COMPLETE` / `BLOCKED` + summary + verify evidence |
| R1 correctness | `standard` | findings package, verdict |
| R2 security/data | `standard`; `deep` for `sensitive` | findings package + hard-stop checklist, verdict |
| Helpers (subagents) | `fast` for search/read, never write code | — |
| Reports, digests, mechanical tasks with an exact spec | `routine` | — |

`frontier` is never selected by DDF. Projects may only raise tiers or set `r2DifferentProvider`.

### 7.2 Operator routing

```yaml
# <operator-config>/routing.yaml — model names are examples
tiers:
  deep:     [{adapter: claude-code, model: opus-5.5, effort: high}]
  standard: [{adapter: claude-code, model: opus-5.5, effort: medium},
             {adapter: opencode, model: <provider/model>}]   # fallback on rate limit
  routine:  [{adapter: claude-code, model: sonnet-5.5, effort: medium}]
  fast:     [{adapter: claude-code, model: haiku-5.5, effort: low}]
roles:
  r2: {prefer: [{adapter: opencode, model: <other-provider/model>}]}   # optional
limits:
  maxConcurrentRuns: 2
  maxSubagentsPerRun: 3
```

Entries are tried in order. A rate-limited entry is marked cooling until its reset time; the next entry of the tier is used; with no entry left the task waits and one notification is sent.

### 7.3 Agent adapter contract

- Non-interactive CLI inside the container (`claude -p --output-format json …`, `opencode run --agent … --model …`). Permission prompts are disabled inside the container; the container is the security boundary.
- Inputs mounted read-only: brief, role prompt bundle. Output: `/out/result.json`, validated against `schemas/<role>.result.json`; missing or invalid output fails the run.
- The adapter records model ID, token usage and cost from the CLI output into the journal.
- Role prompts live once in `prompts/`; each adapter renders them into the CLI's native form (Claude Code system prompt and `.claude/agents/*.md`; opencode agent config) inside the container.
- Optional per-task `maxCostUsd` for API-key-billed entries. No per-stage human grants and no per-call USD reservations.

## 8. Identity, secrets and isolation

**Host.** A dedicated non-admin OS user `ddf` with a private home (mode 700). `ddfd` runs from launchd (macOS) or systemd (Linux) as `ddf`. The operator's own home directory and personal forge login are not accessible.

**GitHub identity.** Three Apps created by `ddf apps create` (GitHub App manifest flow), owned by the operator, installed only on selected repositories:

| App | Permissions | Not granted |
|---|---|---|
| `worker` | contents: write, pull_requests: write, issues: write, checks: read, actions: read, metadata: read | administration, workflows |
| `review-correctness` | contents: read, pull_requests: write, metadata: read | contents write |
| `review-security` | same as `review-correctness` | contents write |

The daemon's token broker mints an installation token per operation, narrowed to one repository and the permissions that operation needs (lifetime ≤ 1 h). Tokens never enter containers. Missing `workflows` permission makes GitHub itself reject pushes that touch workflow files.

**Workspaces.** Daemon clones live in `~ddf/work/<owner>/<repo>`. Each run gets an isolated local copy without remotes or credentials, mounted read-write for the Executor and read-only for reviewers. Only the Publisher commits and pushes.

**Containers.** One per run; base image from `images/` or the project's `image`; default limits 3 GB RAM, 2 CPU, pids limit; no Docker socket; no access to host services; services (databases, browsers) run inside. Network egress is open in V1 (model APIs, package registries) — accepted risk.

**Audit.** The journal records per action: role, task, App and permission set and token expiry (never the token), commits, model, usage.

**Kill switch.** `ddf pause` or Telegram `/pause` stops new runs; uninstalling the Apps revokes forge access immediately.

**Rulesets.** On public repositories DDF recommends a ruleset requiring a pull request, required checks and one approval with no bypass actors. Reviewer-count-by-risk stays enforced by the gate.

## 9. Notifications, failures and recovery

**Telegram (immediate):** `needs-owner` (task, category, one-line question; a reply to the message is the answer), global pause with reason, rate limit (once per reset window), credential failures (→ global pause). **Daily digest:** merged, running, waiting for the owner, usage. Commands: `/status`, `/pause`, `/resume`. Messages are accepted only from the configured chat ID.

**CLI:** `ddf status`, `ddf show <task>`, `ddf answer`, `ddf pause`, `ddf resume`.

**Tick:** every 60 s; forge polling with conditional requests (ETag). No webhooks in V1.

**Recovery:** on daemon start, containers from a previous daemon are killed and their runs rolled back to the last durable state; unresolved intents are reconciled against the forge (e.g. an open change for the branch already exists → adopt it). Unreconcilable intents → `needs-owner`; never a blind retry.

| Failure | Reaction |
|---|---|
| forge error | fail-closed, exponential backoff |
| free disk below floor (default 15 GB) | no new runs, notify |
| container runtime unavailable | global pause, notify |
| host sleep | launchd restarts the daemon; expired leases are released |

**Run logs:** `~ddf/runs/<task>/<run>/` (brief, result, sanitized CLI output), kept 30 days; secrets are redacted by pattern before writing.

## 10. Forge adapters

### 10.1 `forge-local`

- Remote: any Git URL (`file://` bare repository or `ssh://`).
- Tasks and requests: created with `ddf ask` or task files; stored in the DDF store.
- Change: a branch; review results and findings in the store; merge by the gate with Git commands.
- CI: verify in a fresh container.
- Used for fully offline operation and as the forge in DDF's own end-to-end tests.

### 10.2 `forge-github`

| DDF concept | GitHub |
|---|---|
| task | Issue; children are sub-issues |
| request | Issue from the "DDF request" form |
| intake mark | label `ddf` or comment `/ddf` (from a repository writer) |
| state projection | labels `ddf:planning`, `ddf:ready`, `ddf:running`, `ddf:ci`, `ddf:review`, `ddf:fixing`, `ddf:merge`; flags `needs-owner`, `ddf:hold`, `ddf:followup`, `ddf:minors` |
| change | pull request from the `worker` App |
| review | native PR review from the reviewer App + signed marker comment |
| checks | check runs and commit statuses on the head |
| status | pinned "Factory status" Issue, updated at most every 10 minutes |

Labels are a projection for humans; the store is canonical. DDF never reads or writes GitHub Projects.

## 11. Documentation set (the orchestrator canon)

```text
docs/
  concepts/      lifecycle, policy, roles, routing, identity, config-layers
  orchestrator/  manual-mode, supervisor-mode, handoff
  operator/      setup, config-reference, security-model (incl. accepted risks)
  project/       onboarding (ddf init), project-yaml-reference
  adapters/      forge-github (Apps), forge-local, agent-claude-code, agent-opencode, sandbox-docker
  specs/         design documents (this file)
prompts/         planner, executor, reviewer-correctness, reviewer-security
schemas/         role results, project.yaml, routing.yaml
templates/project/  .ddf/project.yaml, issue forms (task, request, bug), AGENTS.md skeleton
```

- **manual-mode**: the procedure for an interactive LLM session without the daemon — same lifecycle and roles, executor subagents with exact briefs, publication, CI, reviews in separate sessions, ≤3 correction batches. In manual mode the human always merges. DDF itself is built this way until M5.
- **supervisor-mode**: an interactive session that operates the factory through `ddf` (requests, answers, unblocking); it does not write code.
- **handoff**: replaces dated local handoff files; state lives in the forge and `ddf status`; a handoff is one templated comment on a tracking task.
- Docs are in English. Roles documentation links to `prompts/` instead of duplicating it.

## 12. Migration

**Harvested from PR #9 (rewritten, not merged):** scoped task key from project/source/ref/id; intent recorded before every external call; uncertain outcome blocks retries until reconciled; idempotent publication; review dedup by head; reviewer independence by principal, not by vendor.

**Dropped from PR #9:** per-stage human grants and 120 s grant freshness; per-call USD reservations; hard-coded human merge; optional single review.

**Moved from the personal workflow repository:** role contracts, tiers, subagent rules, review and correction rules, change/handoff templates, prompts, documentation contract (without personal style). Personal items (profile sync, local paths, owner logins, task history) stay out of DDF.

**Moved from the private pilot design:** state machine, merge gate, signed markers, severity and follow-up routing, CI flake policy, escalation, container runner. Dropped: running agents on the production host, runner watchdog with sudo, a scoped exception in the old workflow repository.

**This repository (first PR, M1):**
- `AGENTS.md`: DDF is developed by its own docs (manual mode until M5); the managed routing block of the old workflow repository is removed; branch roles stay (`dungeon-master` production, `master` integration, `slave/<feature>`).
- `README.md`, `ARCHITECTURE.md`: human merge stays the default; autonomous merge is a per-project opt-in; link this spec.
- `.ai/decisions.md`: research-only gate lifted by the owner (2026-11-03); D1–D15.
- Close #1 (completed, outcome = this spec), #8 and PR #9 (superseded), #10 (no archived code or dependencies adopted).

**Target repositories:** remove the old managed routing block; `AGENTS.md` keeps project rules and one line pointing to `.ddf/project.yaml`.

**Old workflow repository:** archived in M6 after every managed repository has dropped its routing block.

## 13. Milestones

| Milestone | Content | Exit criteria | Merges |
|---|---|---|---|
| M0 Prep | Local workspace cleanup (separate track). Fact check (§15). | Clean layout; answers recorded in `.ai/decisions.md` | owner |
| M1 Docs | This spec, `docs/` (concepts, orchestrator, operator skeleton), `prompts/`, `schemas/`, new `AGENTS.md`, issue closures | An interactive session can run a DDF task by `manual-mode` alone | owner |
| M2 Offline core | `model`, `store`, `lifecycle`, `policy`, `scheduler`, `workspace`, `forge-local`, `sandbox-fake`, fake agent; import-boundary test; hosted CI | Full lifecycle (intake → merge, fixing, sync, revert, restart recovery) passes in tests with fakes | owner |
| M3 Real execution | `sandbox-docker`, base image, `context`, `agent-claude-code`, `agent-opencode` | One real task on a fixture repository through `forge-local` with a real model, both adapters | owner |
| M4 GitHub + channel | `forge-github` (`apps create`, token broker, issues, PRs, reviews, merge, labels, status issue), `notify-telegram`, CLI | Dry-run against a sandbox repository: correct reads, intended writes logged, no writes performed | owner |
| M5 Install + pilot | `ddf` user, launchd, operator config, secrets; 24 h dry run on DDF; DDF at `merge-integration`; pilot product profile PR | §13.1 acceptance | owner until DDF autonomy is on |
| M6 Retire | Archive the old workflow repository; remove routing blocks elsewhere; replace standalone opencode role config | No repository references the old canon | DDF / owner |

Each milestone gets its own implementation plan.

### 13.1 V1 acceptance

- On the private pilot product, three real tasks go from intake to merge into the integration branch with no human action.
- At least one of them goes through a correction batch.
- At least one `needs-owner` question and answer round trip via Telegram.
- Probes: a forged review marker is rejected; a change touching a forbidden path is not published; a daemon restart mid-run produces no duplicate change; a Claude rate limit falls back to the opencode entry.

## 14. Accepted risks (V1)

- Agent CLI tokens and provider keys are visible inside containers.
- Network egress from containers is not filtered.
- The host is a personal laptop: availability depends on it being on.
- Private repositories on a free GitHub plan have no rulesets; there the merge gate is the only enforcement.

## 15. Facts to verify in M0

1. Whether an approval submitted by a GitHub App counts toward a ruleset's required approvals. If not: machine users for reviewers, and check GitHub's terms on how many machine accounts one person may hold.
2. Installation token narrowing (repository and permission subset) for all three Apps.
3. `claude -p` and `opencode run` non-interactive behaviour inside a Linux container with a long-lived token (`claude setup-token`), including rate-limit error shapes.
4. colima resource limits on the Mac and coexistence with the existing CI runner user.
