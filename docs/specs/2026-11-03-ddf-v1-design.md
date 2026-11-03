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

**Out of scope (V1):** promotion to production and deploys; dashboards and GitHub Projects sync; pinned status issue (V1.1); webhooks; multi-host workers; egress allow-listing; GitLab/Gitea/Forgejo adapters; Codex CLI adapter; an Auditor role; free-form tasks via Telegram.

## 2. Maintainer decisions (2026-11-03)

| # | Decision |
|---|---|
| D1 | V1 autonomous merge into the integration branch only for DDF itself and one private pilot product. Every other project starts at `propose` (human merge). |
| D2 | V1 host: a macOS workstation under a separate non-admin user; later a server. The core is host-independent. |
| D3 | Agent adapters in V1: Claude Code CLI and opencode CLI. |
| D4 | Agent roles: Planner, Executor, Reviewer-correctness (R1), Reviewer-security (R2). Trusted non-AI roles: Publisher, Merge-gate. The daemon is the orchestrator. |
| D5 | Reviewer count is a policy outcome of the risk class (1 or 2), never a hard-coded constant. |
| D6 | Three GitHub Apps: `worker`, `review-correctness`, `review-security`. Fallback if GitHub does not count App approvals: machine users (subject to GitHub terms, §16). |
| D7 | Two intake modes: free-form request (Planner creates the tasks) and an existing task marked for DDF. |
| D8 | No GitHub Projects board in the core. DDF must work on a plain Git remote. |
| D9 | Operator channel: CLI and Telegram. |
| D10 | Isolation: one container per run (colima on macOS), no forge credentials inside. |
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
| Secrets | `~ddf/.config/ddf/secrets/` mode 0600 on the host | the operator | App private keys, review-signing key, Telegram token, agent CLI tokens and provider keys |

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
| `store` | SQLite (Node `node:sqlite`, no dependency) as the canonical state; append-only event journal; run leases; intents. |
| `lifecycle` | Pure transition functions for the task state machine (§5). |
| `scheduler` | Run slots, zones, dependencies, priority order, global pause. |
| `policy` | Risk classification, reviewer set, blocking rules, path policy, hard-stop detection, `evaluateMerge` (§6). Pure functions. |
| `workspace` | Daemon-owned clean clones, per-run copies, patch export/apply, patch-id. Never executes repository content on the host (§8.3). |
| `context` | Builds bounded per-role briefs from the task spec, the project profile, `AGENTS.md` and referenced files, all read from the integration branch. |

### 4.2 Ports and V1 adapters

| Port | V1 adapters | Contract (operations) |
|---|---|---|
| Forge | `forge-local`, `forge-github` | list candidate tasks; read intake actor; create task (with task-key marker); find change by task key; publish change; update change body; update branch from base; read checks for a head; rerun checks; post review; read answers and their authors; merge pinned to a head SHA; revert; close/reopen task; set projection labels. Every write supports **dry-run** (logged, not performed). |
| Agent | `agent-claude-code`, `agent-opencode` | `run(role, brief, workspace, limits) → result` validated against the role schema (§7.3) |
| Sandbox | `sandbox-docker` (colima on macOS, Docker on Linux), `sandbox-fake` | create/run/kill a container with mounts, limits and network rules |
| Notify | `notify-telegram`, `notify-cli` | send event; receive owner answers and commands with sender identity |
| Secrets | `secrets-file` | read a named secret |

### 4.3 Entry points

- `ddfd`: the daemon. Single instance per host (exclusive file lock). One tick per minute: poll forges, advance tasks, start/stop runs. `--dry-run` routes every forge write through dry-run.
- `ddf`: CLI. `setup`, `init <repo>`, `ask <project> "<text>"`, `status`, `show <task>`, `answer <task> "<text>"`, `pause`, `resume`, `apps create`.

### 4.4 Boundaries

- Core never imports adapters; adapters never import each other; the daemon wires them. A CI test fails on a forbidden import.
- Trusted code = daemon + adapters on the host. Agents run only inside containers, receive a brief and a workspace copy, and return files in `/out`. They never hold forge credentials or the review-signing key.
- **The daemon runs only from a maintainer-promoted ref** of this repository (the production branch or a release tag), never from the integration branch. Autonomous merges into DDF's integration branch therefore take effect only after a human promotion.
- V1 ships as one npm package (`src/core`, `src/adapters`, `src/cli`). Split into packages only when a second consumer exists.
- Stack: TypeScript on Node 24, Node built-ins first (`node:sqlite`, `node:test`, `fetch`); every dependency needs a recorded reason.

### 4.5 Store and intents

Tables (indicative): `tasks`, `runs`, `changes`, `reviews`, `findings`, `questions`, `intents`, `leases`, `events`.

- Every **forge write** (create task, push, open/update change, post review, update branch, merge, revert, close/reopen task) writes an intent before the call and its outcome after.
- Every forge object DDF creates carries a hidden task-key marker (`ddf:task-key=<key>`), so reconciliation finds it by key, not by branch name or title.
- **Model runs are not intents.** A run interrupted by a restart is discarded and retried within the task's run budget (§5.3).
- Task key: a digest of `{forge, repository, source kind, source id}`; every store row, lease and signature is scoped by it.

## 5. Task lifecycle

```text
inbox → planning → ready → running → publishing → ci → review → merge → done
                     ↑                              ↑            │
                  waiting                           └─ fixing ←──┘
flags (off the conveyor): needs-owner, hold, failed; global: paused
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

**Sync with base.** Behind and clean → the daemon merges the base into the branch (a daemon-authored merge commit). Conflict → an Executor sync batch, then full re-review.

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

**Revert.** If the integration branch's CI is red on the DDF merge commit itself (after one rerun), DDF opens a revert change of risk class `revert` (§6.2) and reopens the task. A second DDF-attributed red within 24 h, across the project → global pause and Telegram alert. Red caused by a human merge is not attributed to DDF.

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
| `sensitive` | any `risk.sensitive` path (money, keys, auth, data, migrations) or default hard-stop globs (deploy, infrastructure, container files, IaC) | R1 + R2 at `deep` |
| `revert` | an exact revert of one DDF merge commit, produced by DDF | R1 |

The class is computed from the diff and the profile. The Planner may raise it; R1 may raise it (adds R2). Nothing lowers it. When R2 is not in the set, R1 also runs the hard-stop checklist.

### 6.3 Findings

`{id, severity: P0..P3, category: correctness|acceptance|security|data|hard-stop|docs, securityLevel?, evidence, location?, fixHint?}`. Blocking: P0, P1, security High/Medium, category `hard-stop`.

### 6.4 Hard stops

Always `needs-owner`: secrets and credentials, deploy and infrastructure, real money, destructive data operations, forge or repository settings, other repositories, purchases. Projects add categories and path globs. A change that hit a hard stop or path policy is never merged autonomously, even after an owner answer: it continues at `propose`.

### 6.5 Path policy

Forbidden by default (agents may not change them; owner-only):
- `.ddf/**`;
- forge and CI configuration: `.github/**`, `.gitlab-ci.yml`, equivalents;
- Git behaviour: `.gitmodules`, `.gitattributes`, `.gitignore` of the repository root;
- agent configuration and instructions: `AGENTS.md`, `CLAUDE.md`, `.claude/**`, `.mcp.json`, `opencode.json`, `.opencode/**`, `.codex/**`, `.cursor/**`;
- `CODEOWNERS`, `LICENSE`, `.env*`;
- symbolic links (any added or changed symlink is refused in V1).

The project's `ciEntrypoints` (scripts and manifests CI relies on, e.g. `package.json`, guard scripts) are not forbidden but force the `sensitive` class, so dependency changes stay possible while CI weakening gets R2 at `deep`.

Evaluated from the integration branch copy of the profile, never from the change itself. Projects add more.

### 6.6 Merge gate

`evaluateMerge` returns `MERGE` only if every condition holds; each refusal reason is an enumerated value with its own test:

1. effective autonomy is `merge-integration`; no global pause;
2. task state is `merge`; no `needs-owner`/`hold` flag; the change never hit a hard stop or path policy;
3. change open, not draft;
4. every reviewer required by the risk class has a valid signed review with verdict APPROVE and zero blocking findings, for the current head **or** for an ancestor head where every later commit is a base merge the daemon itself created (its SHA recorded in the store from the daemon's own intent; author metadata does not count) and `git patch-id --verbatim` of the change is unchanged;
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

Reviewers judge the diff, the spec and the CI results; they do not execute repository code. `frontier` is never selected by DDF. Projects may only raise tiers or set `r2DifferentProvider`.

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
  maxConcurrentContainers: 2     # every container counts: Executor, each reviewer, local gate, forge-local CI
  maxSubagentsPerRun: 3          # advisory, passed to the agent; not enforceable by the CLIs
```

Entries are tried in order. A rate-limited entry is marked cooling until its reset time; the next entry of the tier is used; with no entry left the task waits and one notification is sent. Container limits must fit the container VM size; `ddf status` reports a misfit.

### 7.3 Agent adapter contract

- Non-interactive CLI inside the container, as a non-root user (`claude -p --output-format json …`, `opencode run --agent … --model …`). Permission prompts are disabled inside the container; the container is the security boundary.
- **Configuration comes only from DDF.** The adapter starts the CLI with DDF's rendered role bundle as the only settings source: project and user settings, hooks, MCP servers and plugins from the workspace are disabled. Project instructions (`AGENTS.md`) reach agents only through the brief, read from the integration branch. M0 verifies the exact flags per CLI (§16).
- Inputs mounted read-only: brief, role bundle. Outputs: `/out/result.json` (validated against `schemas/<role>.result.json`; missing or invalid → failed run) and, for the Executor, `/out/change.patch`.
- The adapter records model ID, token usage and cost from the CLI output into the journal.
- Role prompts live once in `prompts/`; each adapter renders them into the CLI's native form (Claude Code system prompt and agent files; opencode agent config) in a directory outside the workspace.
- Optional per-task `maxCostUsd` for API-key-billed entries. No per-stage human grants and no per-call USD reservations.

## 8. Identity, secrets and isolation

### 8.1 Host

A dedicated non-admin OS user `ddf` with a private home (mode 700). `ddfd` runs from launchd (macOS) or systemd (Linux) as `ddf`, from a maintainer-promoted DDF ref (§4.4). The operator's own home directory and personal forge login are not accessible.

### 8.2 GitHub identity

Three Apps created by `ddf apps create` (GitHub App manifest flow), owned by the operator, installed only on selected repositories:

| App | Permissions | Not granted |
|---|---|---|
| `worker` | contents: write, pull_requests: write, issues: write, checks: read, statuses: read, actions: write (rerun), metadata: read | administration, workflows |
| `review-correctness` | contents: read, pull_requests: write, metadata: read | contents write |
| `review-security` | same as `review-correctness` | contents write |

The token broker mints an installation token per operation, narrowed to one repository and the permissions that operation needs (lifetime ≤ 1 h). Tokens never enter containers. The missing `workflows` permission makes GitHub reject pushes that touch workflow files.

### 8.3 Workspaces and publication

- Daemon clones live in `~ddf/work/<forge>/<owner>/<repo>`, used only by trusted code, with hooks disabled (`core.hooksPath` to an empty directory, no fsmonitor, no filter drivers).
- Each run gets a fresh copy (its own `.git`, no remotes, no credentials) that exists only inside the container.
- After the Executor finishes, the adapter's trusted wrapper (not the agent) runs `git add -A && git diff --cached --binary <base>` in the container and writes `/out/change.patch`. The Publisher applies it to the daemon's clean clone with hooks and config neutralised, refuses symlinks, paths outside the repository and any path under `.git/`, and evaluates path policy on the applied tree.
- **The host never executes repository content.** Verify, the local gate and `forge-local` CI always run in fresh containers. The local gate runs on the applied tree **before** the commit is pushed, so a failing gate produces no forge write.

### 8.4 Containers

- One per run; base image from `images/` or the project's `image`; non-root user; default limits 3 GB RAM, 2 CPU, pids limit; no Docker socket.
- **No access to host services.** On colima the VM's gateway to the host loopback is dropped by firewall rules inside the VM, re-applied by the daemon on every VM start or recreation; containers reach only the internet. A probe verifies this at daemon start and in §14.1.
- Services (databases, browsers) run inside the container.
- Network egress to the internet is open in V1 (model APIs, package registries) — accepted risk.

### 8.5 CI prerequisites for autonomous projects

Agent code runs in the project's CI. A project may be set to `merge-integration` only if its change-triggered CI workflows run with read-only repository permissions and no deployment or production secrets, and any self-hosted CI runner holds no secrets beyond what tests need. `ddf init` checks what it can and records the rest as an onboarding checklist item.

### 8.6 Audit, kill switch, rulesets

- The journal records per action: role, task, App and permission set and token expiry (never the token), commits, model, usage.
- `ddf pause` or Telegram `/pause` stops new runs; uninstalling the Apps revokes forge access immediately.
- On repositories that support rulesets, DDF recommends: pull request required, required checks, one approval, no bypass actors. Reviewer count by risk class stays enforced by the gate.

## 9. Notifications, failures and recovery

**Telegram (immediate):** `needs-owner` (task, category, one-line question; a reply to the message is the answer), global pause with reason, rate limit (once per reset window), credential failures (→ global pause). **Daily digest:** merged, running, waiting for the owner, usage. Commands: `/status`, `/pause`, `/resume`. Accepted only from the configured Telegram user ID in the configured chat.

**CLI:** `ddf status`, `ddf show <task>`, `ddf answer`, `ddf pause`, `ddf resume`.

**Tick:** every 60 s; forge polling with conditional requests (ETag). No webhooks in V1.

**Recovery:** on start the daemon takes the instance lock, kills containers from a previous instance, discards their runs (retried within budget), and reconciles every unresolved forge intent by task-key marker (e.g. a change for the task already exists → adopt it). Unreconcilable intents → `needs-owner`; never a blind retry of a forge write.

| Failure | Reaction |
|---|---|
| forge error | fail-closed, exponential backoff |
| free disk below floor (default 15 GB) | no new runs, notify |
| container runtime unavailable | global pause, notify |
| host sleep | launchd restarts the daemon; expired leases are released |

**Run logs:** `~ddf/runs/<task>/<run>/` (brief, result, sanitized CLI output), kept 30 days; secrets redacted by pattern before writing.

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

DDF is a pilot of itself (D1). Its profile marks every security-relevant area as `sensitive` (R1 + R2 at `deep`): `src/core/policy/**`, `src/core/lifecycle/**`, `src/core/workspace/**` (hook neutralisation, patch apply), the Publisher (sanitizer, path checks), the token broker, the review-signature code, `src/adapters/agent-*/**` (settings isolation, read-only tools), `src/adapters/sandbox-*/**` (firewall), the merge code in `src/adapters/forge-*/**`, `prompts/**` and `schemas/**`. Combined with §4.4 (the daemon runs only from a maintainer-promoted ref), an autonomously merged change to DDF cannot alter the running factory until a human promotes it.

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
| M3 Real execution | `sandbox-docker`, base image, VM firewall, `context`, `agent-claude-code`, `agent-opencode` | One real task on a fixture repository through `forge-local` with a real model, both adapters; isolation probes pass | maintainer |
| M4 GitHub + channel | `forge-github` (`apps create`, token broker, intake actors, PRs, reviews, pinned merge, labels), `notify-telegram`, CLI | Dry-run against a sandbox repository: correct reads, intended writes logged, no writes performed | maintainer |
| M5 Install + pilot | `ddf` user, launchd, operator config, secrets; 24 h dry run on DDF; DDF at `merge-integration`; pilot product profile PR and CI prerequisites | §14.1 acceptance | maintainer until DDF autonomy is on |
| M6 Retire | Archive the old workflow repository; remove routing blocks elsewhere; replace standalone agent role configs | No repository references the old canon | DDF / maintainer |

Each milestone gets its own implementation plan.

### 14.1 V1 acceptance

- On the private pilot product, three real tasks go from intake to merge into the integration branch with no human action; at least one goes through a correction batch; at least one `needs-owner` question/answer round trip via Telegram.
- Probes, each must hold:
  1. a forged or replayed review marker is rejected;
  2. a reviewer cannot be made to approve through workspace agent config (planted hooks/settings are ignored);
  3. a change touching a forbidden path or a symlink is not published;
  4. planted Git hooks/config in the Executor copy never run on the host;
  5. an intake label or answer from a non-allow-listed account is ignored;
  6. a container cannot reach host loopback services;
  7. a daemon restart mid-run produces no duplicate task or change;
  8. a head pushed between gate evaluation and merge makes the merge fail;
  9. a Claude rate limit falls back to the opencode entry.

## 15. Accepted risks (V1)

- Agent CLI tokens and provider keys are visible inside containers.
- Network egress from containers is not filtered.
- The host is a workstation: availability depends on it being on.
- Private repositories on plans without rulesets have no forge-side protection; there the merge gate is the only enforcement.
- Project CI executes agent-written code; mitigated only by the prerequisites in §8.5.

## 16. Facts to verify in M0

1. Whether an approval submitted by a GitHub App counts toward a ruleset's required approvals. If not: machine users for reviewers, and GitHub's terms on how many machine accounts one person may hold.
2. Installation token narrowing (repository and permission subset) for all three Apps.
3. Whether `update-branch` or a pushed base merge is rejected when the base contains workflow-file changes and the App lacks `workflows`.
4. `claude -p` and `opencode run` inside a Linux container as non-root with a long-lived token: non-interactive behaviour, exact flags to disable workspace settings/hooks/MCP and use only DDF's bundle, rate-limit error shapes, read-only tool restriction for reviewers.
5. colima under a non-login service user via launchd: start-up, resource limits, VM firewall blocking the host gateway, coexistence with other users' services on the host.
6. `node:sqlite` stability and warnings on the pinned Node 24 release.
