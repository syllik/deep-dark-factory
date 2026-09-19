# Research

Primary tracker: [Issue #1 — Define v0 architecture and migration boundary](https://github.com/syllik/deep-dark-factory/issues/1).

## Purpose

Determine the smallest architecture required for the v0 vertical slice before production implementation begins.

The research must distinguish reusable product concepts from legacy implementation complexity.

## Sources

Audit at minimum:

- `syllik/ai-workflow`;
- `syllik/codex-local-runner`;
- Fabro as an architecture reference only;
- 3–5 other relevant open-source workflow/agent systems;
- native Git and GitHub primitives;
- mature libraries for generic infrastructure candidates.

## Decision vocabulary

Every legacy capability or proposed subsystem receives one disposition:

- **KEEP** — invariant/concept remains materially unchanged.
- **MOVE** — concept belongs in Deep Dark Factory but implementation may change.
- **REDESIGN** — requirement remains; old architecture must not be carried forward.
- **DROP** — not needed in the target architecture.
- **EXTERNALIZE** — use an external primitive/library/adapter instead of owning it.

## Capability matrix

| Capability | Legacy source | Preliminary disposition | Research question | Target boundary |
| --- | --- | --- | --- | --- |
| Task ingestion | GitHub Issues / legacy queue logic | REDESIGN | What is the smallest forge-neutral task contract? | Task adapter |
| Workflow graph/lifecycle | runner + documented workflow | REDESIGN | How little orchestration is required for v0? | Execution |
| Persistent execution state | runner canonical state + task state files | REDESIGN | What must persist to resume safely? | Persistence |
| Resume/checkpoints | runner recovery + task state | REDESIGN | Can a simple persisted run record cover v0? | Persistence |
| Locking/concurrency | runner locks/reservations | REDESIGN | What is required for one-task v0 and later workers? | Execution |
| Context discovery | ai-workflow + repo context | MOVE | How is relevant context found without a central registry? | Context |
| Context budget | ai-workflow/runner | KEEP | What hard limits and measurements are needed? | Context |
| Token/agent-call budget | workflow conventions | KEEP | Which budgets are first-class run policy? | Policy |
| Agent abstraction | Sol/Luna/Codex roles | REDESIGN | What provider-neutral contract is sufficient? | Agent |
| Model routing | role conventions | REDESIGN | How are capability/cost policies expressed without vendor names? | Agent/Policy |
| Sandboxing | macOS/Codex boundary | REDESIGN | Local process, container, or library primitives? | Sandbox |
| Git isolation | retained worktrees | REDESIGN | Worktree, clone, container copy, or abstraction? | Git/Sandbox |
| Deterministic verification | repository checks | KEEP | How does each target repo declare verification? | Verification |
| Independent model review | managed Codex workflow | REDESIGN/EXTERNALIZE | What minimal read-only reviewer contract works across local CLIs, hosted reviewers, and APIs? | Review |
| Retry/fix loops | runner/review loop | REDESIGN | What bounded retry policy is sufficient? | Execution |
| Human gates | explicit approvals | KEEP | Which operations require explicit authority? | Policy |
| Secrets/credentials | runner-local auth/App tokens | REDESIGN | How to support local and server execution safely? | Credentials |
| Forge authentication | GitHub-specific auth | REDESIGN | What generic forge interface and GitHub implementation? | Forge |
| Branch/PR publication | publisher state machine | REDESIGN | What minimum safe publication contract is needed? | Forge |
| Review ingestion | managed Codex workflow | REDESIGN | How are structured findings accepted without giving the reviewer write authority? | Review |
| Observability/audit | logs/state/fingerprints | REDESIGN | Which events are actually needed for debugging/audit? | Observability |
| Scheduling/webhooks | Actions/manual dispatch | REDESIGN | What belongs in v0 versus later server mode? | Server |
| GitHub Projects | project state | EXTERNALIZE | Optional dashboard only? | Adapter |
| GitHub Actions | CI/bridge | EXTERNALIZE | Keep only as project CI/integration option? | CI |
| Local Mac runner | self-hosted execution host | DROP as dependency | Is any v0 capability genuinely macOS-specific? | Optional backend |

## Reviewer dogfood decision

For the first real workflow, use Google Antigravity CLI as an experimental independent reviewer after deterministic verification and before PR publication.

Reasons:

- it supports Google OAuth for local CLI authentication;
- Google AI Pro provides higher Antigravity usage limits, allowing the existing subscription to be used without making paid API usage a product requirement;
- Google's documented code-review workflow demonstrates diff-scoped, read-only reviewing;
- it gives provider diversity when the implementation agent is Codex/OpenAI;
- it can be replaced later because only the generic structured-finding contract is part of the core architecture.

Do not implement the reviewer as a GitHub Action with a repository `GEMINI_API_KEY` during the research phase. Google's automated Antigravity SDK example uses an API key (or Vertex credentials), which creates a different credential/billing path and would couple the initial design to GitHub Actions.

Do not use Jules as the default independent reviewer for this role. Jules remains a useful coding/task agent, but the first reviewer requirement is specifically read-only diff analysis with no code mutation.

Target dogfood flow:

```text
Issue/spec
  -> implementation agent
  -> deterministic verification
  -> Antigravity read-only diff review
  -> P0/P1?
       yes -> implementation agent correction -> deterministic verification
       no  -> continue
  -> publish PR
  -> human merge
```

Default v0 budget: one independent review and at most one correction pass.

Official references checked during this research:

- Google AI Pro benefits / Antigravity quota: https://support.google.com/googleone/answer/14534406
- Antigravity CLI OAuth example: https://codelabs.developers.google.com/sdd-agy-cli
- Antigravity code-review CLI/SDK example: https://codelabs.developers.google.com/agy-cli-sdk-code-review

## Legacy concepts expected to survive

These are hypotheses to validate, not code to copy:

- human merge authority;
- exact task and target identity;
- frozen/verified base revision before mutation;
- least-privilege credentials;
- bounded context;
- bounded agent/retry budget;
- deterministic local verification;
- independent read-only model review;
- explicit separation between execution, review, correction, and publication;
- durable enough state to recover safely.

## Legacy implementation expected not to migrate directly

Audit before finalizing, but default to not carrying forward:

- the existing canonical state machine;
- runner-specific locks/reservations;
- Mac-specific host proof architecture;
- GitHub Actions execution bridges;
- central `workspace.yaml` as a required project registry;
- generated project index as runtime routing;
- duplicate Markdown task state as an execution engine;
- ChipIn-specific repository assumptions;
- hard-coded agent/provider roles.

## Required research output

Before creating the production source tree, Issue #1 should produce:

1. completed capability matrix with evidence;
2. final v0 logical architecture;
3. selected implementation language/runtime;
4. dependency decisions with alternatives considered;
5. persistence/checkpoint decision;
6. sandbox decision;
7. forge/provider/reviewer interfaces;
8. security and credential boundary;
9. token/context/review budget model;
10. one executable implementation plan for:
   `Issue -> context -> isolated agent -> deterministic verify -> independent review -> bounded correction -> PR -> human merge`.

## Stop condition

Do not start a general-purpose workflow engine, distributed scheduler, UI, GitHub Projects integration, or Mac runner integration merely because it may be useful later. Add them only if the v0 vertical slice or evidence from research requires them.
