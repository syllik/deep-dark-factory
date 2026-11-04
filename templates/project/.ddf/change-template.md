Task: {task}
Authorization: {authorization}

## Summary

{summary}

## Verify

{verify}

## Agent notes (sanitized)

{agentNotes}

<!--
Filled by the DDF Publisher (trusted code), not by agents.
- {task}: reference to the task this change implements. DDF adds the closing reference only for this task.
- {authorization}: optional; only for an owner-authorized forbidden-path edit: the owner's quoted authorization and the exact authorized paths. Omit the whole line otherwise. Reviewers do not read the PR body; the orchestrator also passes the authorization to them directly.
- {summary}: what changed and why, from the task spec and the Executor summary.
- {verify}: verify commands run and their results, plus the required checks.
- {agentNotes}: agent-written text; closing keywords, CI-skip directives and mentions are stripped before it is inserted.
-->
