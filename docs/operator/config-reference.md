# Operator config reference

Purpose: every key of the operator's `routing.yaml` and `projects.yaml`, with type, default and allowed values, matching `schemas/routing.json` and `schemas/projects.json`.

Status: normative

The operator layer lives in a private repository or folder chosen by the operator ([config-layers.md](../concepts/config-layers.md)). It can only tighten project policy, never loosen it. Secrets never appear in these files ([setup.md](setup.md#secrets-layout)). Rationale: [spec §3](../specs/2026-11-03-ddf-v1-design.md#3-configuration-layers), [spec §7.2](../specs/2026-11-03-ddf-v1-design.md#72-operator-routing).

Schemas (canonical): [`schemas/routing.json`](../../schemas/routing.json) (`ddf.routing/v1`), [`schemas/projects.json`](../../schemas/projects.json) (`ddf.projects/v1`). Examples: [`schemas/examples/routing.yaml`](../../schemas/examples/routing.yaml), [`schemas/examples/projects.yaml`](../../schemas/examples/projects.yaml). Unknown keys are rejected at every level.

## routing.yaml

Maps tiers and roles to agent adapters and models. Selection and fallback rules: [routing.md](../concepts/routing.md#selection-and-fallback).

### Top level

| Key | Type | Required | Default | Notes |
|---|---|---|---|---|
| `tiers` | object | yes | none | keys `deep`, `standard`, `routine`, `fast`; `deep` and `standard` required |
| `roles` | object | no | none | per-role preferences, tried before the role's tier entries |
| `limits` | object | no | none | concurrency limits |

### `tiers`

| Key | Type | Required | Default | Notes |
|---|---|---|---|---|
| `tiers.deep` | list of entries (min 1) | yes | none | Planner and R2 on `sensitive` tasks |
| `tiers.standard` | list of entries (min 1) | yes | none | the default tier of every role |
| `tiers.routine` | list of entries (min 1) | no | none | reports and digests |
| `tiers.fast` | list of entries (min 1) | no | none | read-only helpers |

There is no `frontier` key: DDF never selects `frontier`, so it is not routable and the schema rejects it.

### Entry

Entries are tried in order; a rate-limited entry (HTTP 429 only) cools until its reset time. The adapter always passes `model` explicitly to the CLI (never a CLI default or a free fallback model; [routing.md](../concepts/routing.md#agent-adapter-contract)).

| Key | Type | Required | Default | Allowed values |
|---|---|---|---|---|
| `adapter` | string | yes | none | pattern `^[a-z][a-z0-9-]*$`; V1 adapters: `claude-code`, `opencode` |
| `model` | string | yes | none | non-empty; provider model identifier (`opencode`: `<provider>/<model>`) |
| `effort` | string | no | none | `low` \| `medium` \| `high` |

### `roles`

| Key | Type | Required | Default | Notes |
|---|---|---|---|---|
| `roles.planner` | object `{prefer}` | no | none | |
| `roles.executor` | object `{prefer}` | no | none | |
| `roles.r1` | object `{prefer}` | no | none | Reviewer-correctness |
| `roles.r2` | object `{prefer}` | no | none | Reviewer-security |
| `roles.<role>.prefer` | list of entries (min 1) | yes (inside a role) | none | tried before the tier entries |

### `limits`

| Key | Type | Required | Default | Notes |
|---|---|---|---|---|
| `limits.maxConcurrentContainers` | integer ≥ 1 | no | none (spec example: 2) | every container counts: Executor, each reviewer, local gate, forge-local CI |
| `limits.maxSubagentsPerRun` | integer ≥ 0 | no | 3 | advisory, passed to the agent; not enforceable by the CLIs |

### Example

```yaml
# Model names are EXAMPLES only.
tiers:
  deep:
    - {adapter: claude-code, model: example-model-a, effort: high}
  standard:
    - {adapter: claude-code, model: example-model-a, effort: medium}
    - {adapter: opencode, model: "<provider/model>"}   # fallback on rate limit
roles:
  r2: {prefer: [{adapter: opencode, model: "<other-provider/model>"}]}
limits:
  maxConcurrentContainers: 2
  maxSubagentsPerRun: 3
```

## projects.yaml

Connected projects with the operator's maximum autonomy and allow-listed humans, plus notification settings and limits.

### Top level

| Key | Type | Required | Default | Notes |
|---|---|---|---|---|
| `projects` | list of project | yes | none | may be empty |
| `notifications` | object | no | none | |
| `limits` | object | no | none | |

### Project

| Key | Type | Required | Default | Allowed values / notes |
|---|---|---|---|---|
| `id` | string | yes | none | pattern `^[a-z0-9][a-z0-9-]*$` |
| `forge` | string | yes | none | `github` \| `local` |
| `repository` | string | when `forge: github` | none | `<owner>/<repo>`, pattern `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`; forbidden when `forge: local` |
| `remote` | string | when `forge: local` | none | plain Git URL, pattern `^(file\|ssh)://`; forbidden when `forge: github` |
| `maxAutonomy` | string | yes | none | `propose` \| `merge-integration` |
| `allowedActors` | list of strings | when `forge: github` (min 1) | none | forge logins whose intake marks and answers are accepted |

Rules:

- **Effective autonomy = min(`maxAutonomy`, the project's `autonomy`).** A repository cannot grant itself autonomy ([policy.md](../concepts/policy.md#autonomy-levels)).
- Under decision D1 only DDF itself and one private pilot product get `maxAutonomy: merge-integration` in V1; every other project starts at `propose`.
- `merge-integration` additionally requires the project's CI prerequisites ([onboarding.md](../project/onboarding.md#ci-prerequisites-for-autonomy)).
- **Allow-list:** intake (label `ddf` or comment `/ddf`) and owner answers are accepted only from logins in `allowedActors`, or from the `worker` App creating a child or follow-up task ([lifecycle.md](../concepts/lifecycle.md#intake)). For `forge: local` the allow-list is the local operator.
- The operator config repository itself can never be listed as a project.

### `notifications`

| Key | Type | Required | Default | Notes |
|---|---|---|---|---|
| `notifications.telegram` | object | no | none | |
| `notifications.telegram.userId` | string | yes (inside `telegram`) | none | pattern `^-?[0-9]+$`; the only user whose answers and commands are accepted |
| `notifications.telegram.chatId` | string | yes (inside `telegram`) | none | pattern `^-?[0-9]+$`; the only chat accepted |

The Telegram bot token is a secret and lives in the secrets directory, not here.

### `limits`

| Key | Type | Required | Default | Notes |
|---|---|---|---|---|
| `limits.minFreeDiskGb` | integer ≥ 1 | no | 15 | operator-tunable floor (product default 15; may be raised or lowered): below this free disk space no new runs start; 0 is rejected because it would disable the guard |
| `limits.maxRunsPerTask` | integer 1–20 | no | 20 | agent runs per task, all roles ([lifecycle.md](../concepts/lifecycle.md#bounds)); the operator may only lower the product bound |

### Example

```yaml
projects:
  - id: example-app
    forge: github
    repository: example-org/example-app
    maxAutonomy: propose
    allowedActors: ["example-maintainer"]
  - id: offline-fixture
    forge: local
    remote: file:///srv/git/offline-fixture.git
    maxAutonomy: propose          # D1: only DDF itself and one pilot get merge-integration
notifications:
  telegram: {userId: "100000001", chatId: "100000001"}   # placeholders
limits:
  minFreeDiskGb: 15
  maxRunsPerTask: 20
```

## Not in these files

- GitHub App IDs belong to the operator layer ([spec §3](../specs/2026-11-03-ddf-v1-design.md#3-configuration-layers)) but are not part of the `ddf.projects/v1` or `ddf.routing/v1` schemas; their location is fixed with `ddf apps create` in M4.
- Run timeouts, CI wait, CI-fix, correction-batch and sync-batch bounds are product defaults ([lifecycle.md](../concepts/lifecycle.md#bounds)), not operator keys in V1. The only §5.3 lifecycle bound an operator sets is `limits.maxRunsPerTask` (lower only). The other limits (`limits.maxConcurrentContainers` and `limits.maxSubagentsPerRun` in `ddf.routing/v1`, `limits.minFreeDiskGb` in `ddf.projects/v1`) are operator-tunable resource settings, not lifecycle bounds.
