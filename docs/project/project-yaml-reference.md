# project.yaml reference

Purpose: every key of a project's `.ddf/project.yaml`, with type, default and example, matching `schemas/project.json`.

Status: normative

The profile is always read from the integration branch, never from a change. It can only tighten product invariants ([config-layers.md](../concepts/config-layers.md#rules)). How each key is used by policy: [policy.md](../concepts/policy.md#project-profile). Rationale: [spec §6.7](../specs/2026-11-03-ddf-v1-design.md#67-project-profile).

Schema (canonical): [`schemas/project.json`](../../schemas/project.json) (`ddf.project/v1`). Full example: [`schemas/examples/project.yaml`](../../schemas/examples/project.yaml). Starter: [`templates/project/.ddf/project.yaml`](../../templates/project/.ddf/project.yaml). Unknown keys are rejected at every level.

## Summary

| Key | Type | Required | Default |
|---|---|---|---|
| `version` | constant `ddf.project/v1` | yes | none |
| `integrationBranch` | string, non-empty | yes | none |
| `autonomy` | `propose` \| `merge-integration` | no | `propose` |
| `verify` | list of non-empty strings, min 1 | yes | none |
| `checks.required` | list of non-empty strings, min 1 | no | none |
| `image` | string, non-empty | no | none (default base image) |
| `risk.lowMaxLines` | integer ≥ 0 | no | 200 |
| `risk.docs` | list of globs | no | none |
| `risk.sensitive` | list of globs | no | none |
| `ciEntrypoints` | list of globs | no | none |
| `forbidden` | list of globs | no | none |
| `hardStops` | list of strings matching `^[a-z][a-z0-9-]*$` | no | none |
| `zones.<name>` | list of globs; name matches `^[A-Za-z0-9][A-Za-z0-9_-]*$` | no | none |
| `planning.maxAutoChildren` | integer ≥ 0 | no | 3 |
| `change.branch` | string, non-empty | no | none |
| `change.merge` | `squash` | no | `squash` |
| `change.template` | string, non-empty | no | none |
| `reviewers.r2DifferentProvider` | boolean | no | `false` |

`checks`, `risk`, `zones`, `planning`, `change` and `reviewers` are objects; all are optional.

## Keys

### `version`

Schema identifier. Must be exactly `ddf.project/v1`.

```yaml
version: ddf.project/v1
```

### `integrationBranch`

The dev/staging branch DDF targets and reads the profile from. DDF never merges into any other branch.

```yaml
integrationBranch: master
```

### `autonomy`

Requested level. Effective autonomy = min(operator `maxAutonomy` for this project, this value) ([policy.md](../concepts/policy.md#autonomy-levels)). `propose`: a human merges. `merge-integration`: the merge gate merges into the integration branch; requires the CI prerequisites ([onboarding.md](onboarding.md#ci-prerequisites-for-autonomy)).

```yaml
autonomy: propose
```

### `verify`

Commands run inside containers only: by the Executor, by the local gate and as CI for `forge-local`. Never run on the host.

```yaml
verify:
  - npm ci
  - npm run verify
```

### `checks.required`

Forge check names the merge gate requires on the current head; the list needs at least one name (an empty list is rejected). Omit the key to mean: at least one check run, all successful ([policy.md](../concepts/policy.md#merge-gate)).

```yaml
checks:
  required: [verify]
```

### `image`

Optional container build file for runs of this project. Without it the default base image is used ([sandbox-docker.md](../adapters/sandbox-docker.md)).

```yaml
image: .ddf/Dockerfile
```

### `risk`

Inputs to the risk class ([policy.md](../concepts/policy.md#risk-classes)).

- `lowMaxLines` (default 200): changed-line limit for `low`; lockfiles and pure renames are excluded from the count.
- `docs`: paths that alone make a change `docs`. Agent-instruction files are never `docs`.
- `sensitive`: any match makes the change `sensitive` (R1 + R2, R2 at `deep`).

```yaml
risk:
  lowMaxLines: 200
  docs: ["docs/**", "**/*.md"]
  sensitive: ["src/payments/**", "src/auth/**", "migrations/**"]
```

### `ciEntrypoints`

Scripts and manifests CI relies on. Not forbidden, but any change to them forces the `sensitive` class.

```yaml
ciEntrypoints: ["package.json", "scripts/ci/**"]
```

### `forbidden`

Additional forbidden path globs on top of the product defaults ([policy.md](../concepts/policy.md#forbidden-paths)). Product defaults always apply and cannot be removed.

```yaml
forbidden: ["ops/**"]
```

### `hardStops`

Hard-stop categories, e.g. `secrets`, `deploy`, `real-money`, `data-destructive`. Projects may add categories; the product hard stops always apply ([policy.md](../concepts/policy.md#hard-stops)).

```yaml
hardStops: [secrets, deploy, real-money, data-destructive]
```

### `zones`

Named path sets. A zone is held from `running` until the holding task is merged or leaves the conveyor; other tasks touching it wait ([lifecycle.md](../concepts/lifecycle.md#states)).

```yaml
zones:
  migrations: ["migrations/**"]
  deps: ["package.json", "package-lock.json"]
```

### `planning.maxAutoChildren`

Default 3. A request that decomposes into more children goes to `needs-owner` with the plan ([lifecycle.md](../concepts/lifecycle.md#intake)).

```yaml
planning:
  maxAutoChildren: 3
```

### `change`

- `branch`: branch name pattern for changes. `{type}` is the Conventional Commits type of the change (`feat`, `fix`, `docs`, `refactor`, `test`, `ci`, `chore`), `{n}` the task number, `{slug}` a short kebab-case slug.
- `merge`: merge method; only `squash` (default).
- `template`: optional change (PR/MR) body template, e.g. `.ddf/change-template.md`. The Publisher fills it; agent text goes only into the sanitized section.

```yaml
change:
  branch: "{type}/issue-{n}-{slug}"
  merge: squash
  template: .ddf/change-template.md
```

### `reviewers.r2DifferentProvider`

Default `false`. When `true`, R2 must run on a different provider than the Executor ([routing.md](../concepts/routing.md#selection-and-fallback)).

```yaml
reviewers:
  r2DifferentProvider: false
```

## What a project cannot set

- Model names (operator layer only), tier lowering, operator `maxAutonomy`, the allow-list, or removal of product forbidden paths and hard stops ([config-layers.md](../concepts/config-layers.md#rules)).
