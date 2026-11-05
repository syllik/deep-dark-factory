# Glossary

Purpose: short definitions of the core DDF terms, each linked to the concept doc or spec section that defines it.

Status: normative

Definitions here only summarise their sources; when a definition and its linked source disagree, the source wins.

**change**
The forge change DDF opens for a task: on GitHub a pull request opened by the `worker` App, on `forge-local` a branch. The Publisher opens or updates it and writes its body from a template.
See [spec §10.2](specs/2026-11-03-ddf-v1-design.md#102-forge-github), [spec §10.1](specs/2026-11-03-ddf-v1-design.md#101-forge-local), [roles.md#publisher](concepts/roles.md#publisher).

**forbidden path**
A path agents may not change; it is owner-only. Projects add more with `forbidden` but cannot remove the defaults, and path policy is evaluated from the integration branch copy of the profile, never from the change itself.
See [policy.md#forbidden-paths](concepts/policy.md#forbidden-paths), [spec §6.5](specs/2026-11-03-ddf-v1-design.md#65-path-policy).

**hard stop**
A category that always sends a task to `needs-owner`: secrets and credentials, deploy and infrastructure, real money, destructive data operations, forge or repository settings, other repositories, purchases (projects add more with `hardStops`). A change that hit a hard stop is never merged autonomously, even after an owner answer: it continues at `propose`.
See [policy.md#hard-stops](concepts/policy.md#hard-stops), [spec §6.4](specs/2026-11-03-ddf-v1-design.md#64-hard-stops).

**head**
The current head commit of a change. Reviewers run on the same head, required checks must pass on the current head, and the merge is pinned to the evaluated head, so a moved head fails the merge.
See [policy.md#merge-gate](concepts/policy.md#merge-gate), [lifecycle.md#review](concepts/lifecycle.md#review), [spec §6.6](specs/2026-11-03-ddf-v1-design.md#66-merge-gate).

**intent**
A store record written before every forge write and completed with its outcome after the call. On start the daemon reconciles unresolved intents by task-key marker and never blindly retries a forge write; model runs are not intents.
See [spec §4.5](specs/2026-11-03-ddf-v1-design.md#45-store-and-intents), [spec §9](specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery).

**lease**
A run lease held in the store, scoped by task key like every store row. When the daemon is restarted after host sleep, expired leases are released.
See [lifecycle.md#store-intents-and-leases](concepts/lifecycle.md#store-intents-and-leases), [spec §4.1](specs/2026-11-03-ddf-v1-design.md#41-core-no-network-no-forge-no-provider), [spec §4.5](specs/2026-11-03-ddf-v1-design.md#45-store-and-intents), [spec §9](specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery).

**manual mode**
The procedure an interactive LLM session follows to run one DDF task without the daemon, keeping the same lifecycle, roles, bounds and policy; a human merges. DDF itself is built this way until M5.
See [manual-mode.md](orchestrator/manual-mode.md), [spec §11](specs/2026-11-03-ddf-v1-design.md#11-documentation-set-the-orchestrator-canon).

**pause reason**
One member of the global pause's set of reasons: `owner`, `credential`, `revert-red`, `runtime` or `security`. The daemon is paused while the set is non-empty; only `runtime` lifts automatically, every other reason only by `ddf resume` or `/resume`.
See [lifecycle.md#global-pause](concepts/lifecycle.md#global-pause), [spec §9](specs/2026-11-03-ddf-v1-design.md#9-notifications-failures-and-recovery), [lifecycle.md#revert](concepts/lifecycle.md#revert).

**projection**
Forge labels and comments that mirror DDF state for humans; the store is canonical. The merge gate reads reviews from the store, not from forge comments.
See [lifecycle.md#conveyor](concepts/lifecycle.md#conveyor), [spec §10.2](specs/2026-11-03-ddf-v1-design.md#102-forge-github), [policy.md#merge-gate](concepts/policy.md#merge-gate).

**reviewer set**
The reviewers a change needs, taken from its risk class: R1 for `docs`, `low` and `revert`; R1 + R2 for `standard`; R1 + R2 at `deep` for `sensitive`. When R2 is not in the set, R1 also runs the hard-stop checklist.
See [policy.md#risk-classes](concepts/policy.md#risk-classes), [spec §6.2](specs/2026-11-03-ddf-v1-design.md#62-risk-classes-and-reviewers).

**revision**
The initial execution or one correction batch as published by the Publisher (one commit and one push, no force-push, rebase or amend); CI-fix pushes made within it are counted inside it, so the CI-fix bound (default 2) applies per revision.
See [lifecycle.md#revision](concepts/lifecycle.md#revision), [roles.md#publisher](concepts/roles.md#publisher), [lifecycle.md#bounds](concepts/lifecycle.md#bounds).

**risk class**
The class (`docs`, `low`, `standard`, `sensitive`, `revert`) computed deterministically from the diff and the project profile; it decides the reviewer set. The Planner or R1 may raise it; nothing lowers it.
See [policy.md#risk-classes](concepts/policy.md#risk-classes), [spec §6.2](specs/2026-11-03-ddf-v1-design.md#62-risk-classes-and-reviewers).

**run**
One agent execution of a role for a task, inside a container. Every run counts toward `maxRunsPerTask` (default 20), and a run interrupted by a daemon restart is discarded and retried within that budget.
See [lifecycle.md#bounds](concepts/lifecycle.md#bounds), [spec §4.5](specs/2026-11-03-ddf-v1-design.md#45-store-and-intents).

**supervisor mode**
How an interactive session operates a running DDF factory through the `ddf` CLI (`status`, `show`, `ask`, `answer`, `pause`, `resume`). The session is an operator console, not a worker: it never writes code or replaces a role.
See [supervisor-mode.md](orchestrator/supervisor-mode.md).

**task**
The unit of work DDF moves along the conveyor from intake to merge; on GitHub an Issue, with children as sub-issues. It is identified by its task key, a digest of `{forge, repository, source kind, source id}`.
See [lifecycle.md#conveyor](concepts/lifecycle.md#conveyor), [spec §4.5](specs/2026-11-03-ddf-v1-design.md#45-store-and-intents), [spec §10.2](specs/2026-11-03-ddf-v1-design.md#102-forge-github).

**zone**
A named path set declared under `zones` in the project profile. A zone is held from `running` until the holding task is merged or leaves the conveyor; other tasks touching it wait.
See [project-yaml-reference.md#zones](project/project-yaml-reference.md#zones), [lifecycle.md#ready-and-waiting](concepts/lifecycle.md#ready-and-waiting), [spec §6.7](specs/2026-11-03-ddf-v1-design.md#67-project-profile).
