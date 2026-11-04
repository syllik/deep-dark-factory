# Policy

Purpose: the deterministic rules that decide autonomy, risk class, reviewer set, blocking findings, path policy, hard stops and the merge gate.

Status: normative

Rationale and full design: [spec §6](../specs/2026-11-03-ddf-v1-design.md#6-policy). Policy functions are pure and deterministic; no LLM call decides a policy outcome.

## Autonomy levels

- `propose` (default): DDF drives the task to a ready change; a human merges.
- `merge-integration`: the [merge-gate](roles.md#merge-gate) merges into the integration branch.

Effective autonomy = min(operator maximum for that project, project request) ([config-layers.md](config-layers.md#rules)). No level promotes to production.

Rationale: [spec §6.1](../specs/2026-11-03-ddf-v1-design.md#61-autonomy-levels).

## Risk classes

Rationale: [spec §6.2](../specs/2026-11-03-ddf-v1-design.md#62-risk-classes-and-reviewers).

| Class | Condition (deterministic) | Reviewers |
|---|---|---|
| `docs` | only `risk.docs` paths; agent-instruction files are never `docs` | R1 |
| `low` | code, changed lines ≤ `risk.lowMaxLines` (default 200, lockfiles and pure renames excluded from the count), no `risk.sensitive` or hard-stop path | R1 |
| `standard` | other code | R1 + R2 |
| `sensitive` | any `risk.sensitive` path (money, keys, auth, data, migrations) or default hard-stop globs (deploy, infrastructure, container files, IaC) | R1 + R2 at `deep` |
| `revert` | an exact revert of one DDF merge commit, produced by DDF | R1 |

Rules:

1. The class is computed from the diff and the profile.
2. The Planner may raise it; R1 may raise it (adds R2). Nothing lowers it.
3. When R2 is not in the set, R1 also runs the hard-stop checklist.
4. Paths in the project's `ciEntrypoints` force `sensitive` (see [Forbidden paths](#forbidden-paths)).

R1 = [Reviewer-correctness](roles.md#reviewer-correctness), R2 = [Reviewer-security](roles.md#reviewer-security).

## Findings

Shape: `{id, severity: P0..P3, category: correctness|acceptance|security|data|hard-stop|docs, securityLevel?, evidence, location?, fixHint?}`.

Blocking: P0, P1, security High/Medium, category `hard-stop`.

Routing of non-blocking findings (P2 follow-up, P3 minors) is in [lifecycle.md](lifecycle.md#review). Rationale: [spec §6.3](../specs/2026-11-03-ddf-v1-design.md#63-findings).

## Hard stops

Always `needs-owner`: secrets and credentials, deploy and infrastructure, real money, destructive data operations, forge or repository settings, other repositories, purchases.

- Projects add categories and path globs (`hardStops` in the project profile).
- A change that hit a hard stop or path policy is never merged autonomously, even after an owner answer: it continues at `propose`.

Rationale: [spec §6.4](../specs/2026-11-03-ddf-v1-design.md#64-hard-stops).

## Forbidden paths

Forbidden by default (agents may not change them; owner-only):

<!-- sync:forbidden -->
- `.ddf/**`;
- forge and CI configuration: `.github/**`, `.gitlab-ci.yml`, equivalents;
- Git behaviour: `.gitmodules`, `.gitattributes`, `.gitignore` of the repository root;
- agent configuration and instructions: `AGENTS.md`, `CLAUDE.md`, `.claude/**`, `.mcp.json`, `opencode.json`, `.opencode/**`, `.codex/**`, `.cursor/**`;
- `CODEOWNERS`, `LICENSE`, `.env*`;
- symbolic links (any added or changed symlink is refused in V1).
<!-- /sync:forbidden -->

Rules:

1. The project's `ciEntrypoints` (scripts and manifests CI relies on, e.g. `package.json`, guard scripts) are not forbidden but force the `sensitive` class.
2. Path policy is evaluated from the integration branch copy of the profile, never from the change itself.
3. Projects add more (`forbidden` in the project profile); they cannot remove defaults.
4. The Publisher evaluates path policy on the applied tree before any forge write ([identity.md](identity.md#workspaces-and-publication)).

Rationale: [spec §6.5](../specs/2026-11-03-ddf-v1-design.md#65-path-policy).

## Merge gate

`evaluateMerge` returns `MERGE` only if every condition holds; each refusal reason is an enumerated value with its own test:

1. effective autonomy is `merge-integration`; no global pause;
2. task state is `merge`; no `needs-owner`/`hold` flag; the change never hit a hard stop or path policy;
3. change open, not draft;
4. every reviewer required by the risk class has a valid signed review with verdict APPROVE and zero blocking findings, for the current head **or** for an ancestor head where every later commit is a base merge the daemon itself created (its SHA recorded in the store from the daemon's own intent; author metadata does not count) and `git patch-id --verbatim` of the change is unchanged; a base merge made by anyone else (e.g. a human "Update branch") never qualifies;
5. every check in the profile's `checks.required` is present and successful on the current head (with no list: at least one check run, all successful);
6. path policy passes;
7. change not behind base and mergeable;
8. any forge read error → refusal (fail-closed).

Rules:

- The merge is pinned to the evaluated head (GitHub: merge with the `sha` parameter; `forge-local`: compare-and-swap ref update). A moved head fails the merge.
- Review signatures are HMAC-SHA256 with a key only the daemon holds; the gate reads reviews from the store and verifies signatures; forge comments are a projection.
- Each refusal maps to a transition: [lifecycle.md](lifecycle.md#merge-refusals).

Rationale: [spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate).

## Project profile

Every policy input above (`risk.*`, `ciEntrypoints`, `forbidden`, `hardStops`, `checks.required`, `autonomy`, `zones`, `planning.maxAutoChildren`, `reviewers.r2DifferentProvider`) comes from `.ddf/project.yaml`, always read from the integration branch. Full example: [spec §6.7](../specs/2026-11-03-ddf-v1-design.md#67-project-profile).
