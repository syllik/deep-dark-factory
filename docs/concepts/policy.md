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
| `low` | code, [changed lines](#changed-lines) ≤ `risk.lowMaxLines` (default 200, lockfiles and pure renames excluded from the count), no `risk.sensitive` or hard-stop path | R1 |
| `standard` | other code | R1 + R2 |
| `sensitive` | any `risk.sensitive` path (money, keys, auth, data, migrations) or the default hard-stop globs (see [Hard stops](#hard-stops)) | R1 + R2, R2 at `deep` |
| `revert` | an exact revert of one DDF merge commit, produced by DDF | R1 |

Rules:

1. The class is computed from the diff and the profile.
2. The Planner may raise it; R1 may raise it (adds R2). Nothing lowers it.
3. When R2 is not in the set, R1 also runs the hard-stop checklist.
4. Paths in the project's `ciEntrypoints` force `sensitive` (see [Forbidden paths](#forbidden-paths)).

R1 = [Reviewer-correctness](roles.md#reviewer-correctness), R2 = [Reviewer-security](roles.md#reviewer-security).

## Changed lines

The count that `risk.lowMaxLines` is compared against:

1. The count comes from `git diff --numstat -M` between the merge base of the integration branch and the change head, and the head: the whole change, with daemon base merges excluded by the moved merge base.
2. Changed lines = added + deleted lines, summed over counted files.
3. [Lockfiles](#lockfiles) are not counted.
4. A pure rename (similarity 100%) counts 0; a rename with edits counts only its added and deleted lines.
5. Any binary file added, changed or deleted makes the change exceed `risk.lowMaxLines`.
6. A mode-only change counts 0.
7. Every path rule (docs, sensitive, `ciEntrypoints`, hard-stop globs, forbidden, agent-instruction files) matches both the old and the new path of a rename.

Rationale: [spec §6.2](../specs/2026-11-03-ddf-v1-design.md#62-risk-classes-and-reviewers).

## Changed paths

The set every path rule and path check matches: the [risk class](#risk-classes), [path policy](#forbidden-paths), [hard stops](#hard-stops), the [Publisher](roles.md#publisher)'s refusals and the [merge gate](#merge-gate).

1. The changed paths are the paths that differ between the merge base of the integration branch and the change, and the tree checked: the revision's applied tree (Publisher) or the change head (merge gate).
2. They include every added, modified, deleted or type-changed path, a mode-only change included, and both the old and the new path of a rename (`git diff --raw -M --no-abbrev`, which also gives each path's mode and blob ID).
3. Path rules never match the unchanged rest of the tree; a deletion matches like an addition. Example: deleting `deploy/prod.yaml` matches the hard-stop glob `deploy/**`.
4. The symlink rule is narrower: only an added or changed symlink is refused ([Forbidden paths](#forbidden-paths)).

Rationale: [spec §6.2](../specs/2026-11-03-ddf-v1-design.md#62-risk-classes-and-reviewers).

## Lockfiles

A fixed product list, not a profile key in V1:

<!-- sync:lockfiles -->
- `**/package-lock.json`
- `**/npm-shrinkwrap.json`
- `**/yarn.lock`
- `**/pnpm-lock.yaml`
- `**/bun.lock`
- `**/bun.lockb`
- `**/go.sum`
- `**/Cargo.lock`
- `**/poetry.lock`
- `**/Pipfile.lock`
- `**/uv.lock`
- `**/Gemfile.lock`
- `**/composer.lock`
- `**/mix.lock`
- `**/pubspec.lock`
- `**/Package.resolved`
- `**/packages.lock.json`
- `**/gradle.lockfile`
- `**/flake.lock`
<!-- /sync:lockfiles -->

## Agent-instruction files

Never `docs`; they are classed by the code rows. A fixed product list, not a profile key in V1:

<!-- sync:agent-instruction-files -->
- `**/AGENTS.md`
- `**/CLAUDE.md`
- `**/GEMINI.md`
- `**/SKILL.md`
- `**/.cursorrules`
- `**/.windsurfrules`
- `**/.github/copilot-instructions.md`
- `**/.agents/**`
- `**/.claude/**`
- `**/.codex/**`
- `**/.cursor/**`
- `**/.opencode/**`
- `**/.mcp.json`
- `**/opencode.json`
<!-- /sync:agent-instruction-files -->

## Findings

Shape: `{id, severity: P0..P3, category: correctness|acceptance|security|data|hard-stop|docs, securityLevel?, evidence, location?, fixHint?}`.

Blocking: P0, P1, security High/Medium, category `hard-stop`.

Routing of non-blocking findings (P2 follow-up, P3 minors) is in [lifecycle.md](lifecycle.md#review). Rationale: [spec §6.3](../specs/2026-11-03-ddf-v1-design.md#63-findings).

## Hard stops

Always `needs-owner`: secrets and credentials, deploy and infrastructure, real money, destructive data operations, forge or repository settings, other repositories, purchases.

Default hard-stop globs:

<!-- sync:hard-stop-globs -->
- `Dockerfile*`
- `**/Dockerfile*`
- `docker-compose*.y*ml`
- `**/*.tf`
- `**/*.tfvars`
- `k8s/**`
- `helm/**`
- `deploy/**`
- `infra/**`
- `.terraform/**`
- `ansible/**`
<!-- /sync:hard-stop-globs -->

Rules:

1. The **hard-stop entries** of a tree are its [changed paths](#changed-paths) that match a default hard-stop glob, each with its state in that tree (blob ID and mode, or deleted).
2. A revision with a hard-stop entry the owner has not approved is not published. The task goes to `needs-owner` (category and paths) before publishing, and `hit_stop` is set.
3. The owner's answer approves exactly the entries the question listed. The Publisher then publishes that revision (the owner-answered revision) and the task continues at `propose` (human merge); an owner who does not want it closes the task. A later revision or head whose hard-stop entries are all approved asks nothing new; any other entry (a new path, other content or mode, a deletion) is a new hard stop.
4. On a published head, an unapproved hard-stop entry, a forbidden path or an added or changed symlink fails [merge gate](#merge-gate) condition 6 and maps to the refusal row ([lifecycle.md](lifecycle.md#merge-refusals)), which asks the owner at most once per head. A head already asked about maps to the `hit_stop` row (condition 2), which asks nothing new; the owner-answered revision does not fail condition 6, since its entries are approved.
5. Projects add hard-stop categories (`hardStops` in the project profile), which the reviewers' hard-stop checklist covers. V1 has no project hard-stop glob key; a project uses `forbidden` or `risk.sensitive` for paths.
6. A change that hit a hard stop or path policy is never merged autonomously, even after an owner answer: it continues at `propose`. `hit_stop` is sticky: it is never cleared.

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
4. The Publisher evaluates path policy on the [changed paths](#changed-paths) of the applied tree before any forge write ([identity.md](identity.md#workspaces-and-publication)).
5. A revision whose changed paths touch a forbidden path or hold an added or changed symlink is refused by the Publisher and never published automatically (probe 3). The task goes to `needs-owner` and `hit_stop` is set. After the owner's answer it returns to `running` with the answer in the Executor brief; a revision that still touches a forbidden path is refused again. Forbidden paths change only in a human-merged change.
6. Agent configuration directories count at any depth: a changed path under `.opencode/` or `.claude/` anywhere in the tree (`**/.opencode/**`, `**/.claude/**`) is a forbidden path ([identity.md](identity.md#workspaces-and-publication)).

Rationale: [spec §6.5](../specs/2026-11-03-ddf-v1-design.md#65-path-policy).

## Merge gate

`evaluateMerge` returns `MERGE` only if every condition holds; each refusal reason is an enumerated value with its own test:

1. effective autonomy is `merge-integration`; no global pause;
2. task state is `merge`; no `needs-owner`/`hold` flag; the change never hit a hard stop or path policy;
3. change open, not draft;
4. every reviewer required by the risk class has a valid signed review with verdict APPROVE and zero blocking findings, for the current head **or** for an ancestor head where every later commit is a base merge the daemon itself created (its SHA recorded in the store from the daemon's own intent; author metadata does not count) and `git patch-id --verbatim` of the change is unchanged; a base merge made by anyone else (e.g. a human "Update branch") never qualifies;
5. every check in the profile's `checks.required` is present and successful on the current head (with no list: at least one check run, all successful);
6. path policy passes and no unapproved hard-stop entry is in the head's changed paths (§6.2, §6.4);
7. change not behind base and mergeable;
8. any forge read error → refusal (fail-closed).

Rules:

- The merge is pinned to the evaluated head (GitHub: merge with the `sha` parameter; `forge-local`: compare-and-swap ref update). A moved head fails the merge.
- Review signatures are Ed25519 with a key ID and a version (`ddf.review-signature/v1`), made with a private key only the daemon holds ([spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate)); the gate reads reviews from the store and rejects an unknown version, an unknown key ID or a bad signature (fail-closed); forge comments are a projection.
- Each refusal maps to a transition: [lifecycle.md](lifecycle.md#merge-refusals).

Rationale: [spec §6.6](../specs/2026-11-03-ddf-v1-design.md#66-merge-gate).

## Project profile

Every policy input above (`risk.*`, `ciEntrypoints`, `forbidden`, `hardStops`, `checks.required`, `autonomy`, `zones`, `planning.maxAutoChildren`, `reviewers.r2DifferentProvider`) comes from `.ddf/project.yaml`, always read from the integration branch. Full example: [spec §6.7](../specs/2026-11-03-ddf-v1-design.md#67-project-profile).
