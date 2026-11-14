# forge-github adapter

Purpose: how DDF maps its forge port onto GitHub: the three Apps and their permissions, the token broker, intake actors, labels, pull requests, reviews and the pinned merge.

Status: implemented in M4

GitHub is the first forge adapter, not the core abstraction. Rationale: [spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters), [spec §8.2](../specs/2026-11-03-ddf-v1-design.md#82-github-identity), [spec §10.2](../specs/2026-11-03-ddf-v1-design.md#102-forge-github).

## Forge port contract

Every forge adapter implements these operations ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters)):

- list candidate tasks; read intake actor; create task (with task-key marker); find change by task key;
- publish change; update change body; update branch from base;
- read checks for a head; rerun checks;
- post review; read answers and their authors;
- merge pinned to a head SHA; revert; close/reopen task; set projection labels.

Every write supports **dry-run** (logged, not performed); `ddfd --dry-run` routes all writes through it. Every write is preceded by an intent in the store and followed by its outcome; every object DDF creates carries the hidden marker `ddf:task-key=<key>` ([spec §4.5](../specs/2026-11-03-ddf-v1-design.md#45-store-and-intents)).

## Concept mapping

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

## App permissions

Three Apps created by `ddf apps create` (GitHub App manifest flow), owned by the operator, installed only on selected repositories:

| App | Permissions | Not granted |
|---|---|---|
| `worker` | contents: write, pull_requests: write, issues: write, checks: read, statuses: read, actions: write (rerun), metadata: read | administration, workflows |
| `review-correctness` | contents: read, pull_requests: write, metadata: read | contents write |
| `review-security` | same as `review-correctness` | contents write |

The token broker mints an installation token per operation, narrowed to one repository and the permissions that operation needs (lifetime ≤ 1 h). Tokens never enter containers. The missing `workflows` permission makes GitHub reject pushes that touch workflow files.

App private keys live in the secrets directory ([setup.md](../operator/setup.md#secrets-layout)). The journal records App, permission set and token expiry per action, never the token.

## Intake and answers

- The adapter reads **who** applied the `ddf` label or wrote `/ddf` from the Issue timeline events. Intake is accepted only if that login is in the project's `allowedActors`, or is the `worker` App creating a child or follow-up task ([lifecycle.md](../concepts/lifecycle.md#intake)).
- Issue forms never apply `ddf`. The request form applies `ddf:request`, which is inert until an allow-listed actor adds `ddf`.
- Owner answers are read from Issue/PR comments and accepted only from allow-listed humans. An answer never authorizes an autonomous merge of a hard-stop or path-policy change.
- Polling uses conditional requests (ETag) every tick (60 s). No webhooks in V1.

## Changes

- The `worker` App pushes one commit per revision to the change branch (no force-push, rebase or amend) and opens or updates the pull request.
- The body comes from the project's `change.template`; agent text appears only in the sanitized section; `Closes <task>` is added by DDF only for the task itself ([roles.md](../concepts/roles.md#publisher)).
- Update from base: a daemon-authored merge of the base into the branch; its SHA is recorded from the daemon's own intent.
- **Workflow files in the base:** before any sync or publish that brings base changes into the branch (the clean merge, or the push of an Executor sync batch), the daemon runs `git diff --name-only <merge-base> <base> -- .github/workflows`. A non-empty result means the `worker` App (no `workflows` permission) may not write them, so no GitHub call is made: → `needs-owner` with the reason "base changed workflow files; update the branch manually", and the sync batch is not consumed. A 403 "… without `workflows` permission" is only a fallback signal with the same outcome ([lifecycle.md](../concepts/lifecycle.md#sync-with-base)).
- A human "Update branch" is not a daemon-created base merge: the new head needs a full re-review ([policy.md](../concepts/policy.md#merge-gate) condition 4).

## Reviews

- Each reviewer App submits a native review (`APPROVE` / `REQUEST_CHANGES`) and a signed marker comment. Reviewer Apps re-approve after a sync.
- The merge gate reads reviews from the store and verifies Ed25519 signatures (key ID and version); GitHub reviews and comments are a projection ([policy.md](../concepts/policy.md#merge-gate)).

## Merge

- Squash merge with the `sha` parameter set to the evaluated head; a moved head fails the merge.
- Any GitHub read error or unknown mergeable state → refusal (fail-closed) and backoff ([lifecycle.md](../concepts/lifecycle.md#merge-refusals)).
- Revert: a revert pull request of class `revert`, opened by the `worker` App, and the task is reopened.

## Facts verified in M0

Answers recorded in M0 ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0); evidence in [M0 facts](../research/2026-11-m0-facts.md#github)):

1. whether an App approval counts toward a ruleset's required approvals: unverified (terms documented); M4 probe `app-approval-counts` (reviewer App with `pull_requests: write`, also `contents: read`/`write` variants); fallback machine users (≤ 1 free machine account per person under GitHub's terms);
2. installation token narrowing (repository and permission subset) for all three Apps: documented, supported as above (repository IDs + permission subset, ≤ 1 h); M4 probe `token-narrowing`;
3. `update-branch` or a pushed base merge when the base contains workflow-file changes and the App lacks `workflows`: unverified, rejected with 403 per a third-party report (pushed merge inferred); handled by the deterministic check under [Changes](#changes); M4 probe `sync-with-workflow-change`.
