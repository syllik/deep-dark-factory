## DDF handoff

<!-- One comment on the tracking Issue. Facts with links only. Write "none" in an empty section; keep every section. Never name local paths or temp directories: PR, head SHA and state only. Rules: docs/orchestrator/handoff.md -->

Mode: <manual | supervisor>

### Done

- <PR or Issue link>: <one line>

### In flight (PR, head SHA, state)

- <PR link> · head `<full SHA>` · state `<ci | review | fixing | merge>` · reviews: <r1/r2 for this head, or none> · correction batches: <n>/3

### Waiting on owner

- <task link> · `needs-owner` · <category>: <one-line question> (<link to the asking comment>)
- <PR link> · owner action · <start reviewer session r1 | r2 at head `<full SHA>` | post comment <heading> on <target>>
- <PR link> · ready for human merge at head `<full SHA>`

### Decided

- <decision, one line> (<link to where it is recorded: decision log entry, planner result comment, PR or Issue comment>)

### Failed

- <task or PR link>: <approach tried> · dropped: <reason> (<link to the trace, if any: PR, commit, review or comment>)

### Next

1. <first concrete step for the next session>

### Worktrees

- `<branch>` · PR <link> · <keep | delete after merge | safe to delete (PR MERGED)>
