# agent-claude-code adapter

Purpose: how DDF runs a role through the Claude Code CLI inside a run container.

Status: implemented in M3

Routing name: `adapter: claude-code` in the operator's `routing.yaml` ([config-reference.md](../operator/config-reference.md#entry)). Rationale: [spec §7.3](../specs/2026-11-03-ddf-v1-design.md#73-agent-adapter-contract); the shared contract is in [routing.md](../concepts/routing.md#agent-adapter-contract).

## Contract

The adapter implements `run(role, brief, workspace, limits) → result` ([spec §4.2](../specs/2026-11-03-ddf-v1-design.md#42-ports-and-v1-adapters)):

- Non-interactive CLI inside the container, as a non-root user (`claude -p --output-format json …`). Permission prompts are disabled inside the container; the container is the security boundary.
- **Pinned CLI.** Claude Code from npm `@anthropic-ai/claude-code`; the base image records the exact version (M0 verified 2.1.317); the adapter refuses an unknown version. Containers set `DISABLE_AUTOUPDATER=1`.
- **Configuration comes only from DDF.** The adapter starts the CLI with DDF's rendered role bundle as the only settings source: project and user settings, hooks, MCP servers and plugins from the workspace are disabled. Project instructions (`AGENTS.md`) reach agents only through the brief, read from the integration branch.
- **Isolation switches:** `--setting-sources ''` with `--settings <bundle>` and `--strict-mcp-config --mcp-config <bundle>`, brief on stdin; `--tools` restricts built-in tools for reviewers but is not isolation; `--bare` is unusable with a subscription token.
- Residual Claude Code sources these flags do not disable: account-synced remote settings (applied regardless of local flags) and built-in plugins and agents; DDF therefore uses a dedicated account and token, and the M3 probe `claude-config-isolation` fails closed if the remote settings carry hooks, permissions or MCP servers.
- Every call passes an explicit `--model` from routing (never a CLI default; Claude Code's own `--fallback-model` stays unused) and runs under a wall-clock timeout, with `--no-session-persistence` in a fresh, empty home.
- The container receives only this adapter's credential (`CLAUDE_CODE_OAUTH_TOKEN` or one provider key). All agent-originated output is redacted before any forge write or notification ([roles.md](../concepts/roles.md#publisher)).
- Inputs mounted read-only: brief, role bundle. Outputs: `/out/result.json` (validated against `schemas/<role>.result.json`; missing or invalid → failed run) and, for the Executor, `/out/change.patch`.
- The adapter records model ID, token usage and cost from the CLI output into the journal (`--output-format json`: the `modelUsage` keys, `usage`, `total_cost_usd`; the cost is a list-price estimate, not a bill).
- Role prompts live once in `prompts/`; the adapter renders them into Claude Code's native form (system prompt and agent files) in a directory outside the workspace.
- Optional per-task `maxCostUsd` for API-key-billed entries.

## Role specifics

- Reviewers (R1, R2) get read/search tools only and no command execution ([roles.md](../concepts/roles.md#reviewer-correctness)): `--tools Read,Grep,Glob` with `--permission-mode dontAsk`, a bundle `permissions.deny` list as a second layer, cwd outside the workspace and the read-only copy added with `--add-dir`.
- The Executor gets full tools inside the container (`--permission-mode bypassPermissions`, cwd in the workspace copy); `limits.maxSubagentsPerRun` is passed as advice, not enforced ([routing.md](../concepts/routing.md#limits)).
- The patch is produced by the adapter's trusted wrapper, not by the agent ([identity.md](../concepts/identity.md#workspaces-and-publication)).

## Errors and rate limits

Failures are classified from `is_error`, `api_error_status` and the assistant `error` field, never from `subtype` (an auth failure still reports `subtype: "success"`):

| Signal | Classification |
|---|---|
| `authentication_failed`, a `result` starting with "Not logged in", or a non-zero exit with no `api_error_status` and authentication text | credential failure: global pause, reason `credential`; never retried |
| HTTP 429 (`rate_limit`) | rate limit: the entry cools until its reset time; the next entry of the tier is used ([routing.md](../concepts/routing.md#selection-and-fallback)) |
| other retryable errors (5xx: `server_error`, `overloaded`) | backoff on the same entry, not fallback |

Acceptance probe 9 checks the fallback to the opencode entry ([security-model.md](../operator/security-model.md#probes)). The rate-limit shape is unverified (M3 probe `claude-rate-limit-shape`).

## M0 facts

Verified in M0 with Claude Code 2.1.317 ([spec §16](../specs/2026-11-03-ddf-v1-design.md#16-facts-to-verify-in-m0), item 4): non-interactive runs as non-root with only a long-lived token and an empty home, the isolation switches, the reviewer tool restriction and the JSON output fields. Exact command lines per role, the bundle files and the evidence: [M0 facts](../research/2026-11-m0-facts.md#recommended-adapter-command-lines). M3 probes: `cli-version-pin`, `agent-noninteractive`, `agent-home-state`, `claude-config-isolation`, `claude-reviewer-readonly`, `claude-executor-output`, `claude-auth-error-shape`, `claude-rate-limit-shape`, `secret-exfil-redaction`.

## Interactive sessions

Manual-mode and supervisor-mode sessions run Claude Code interactively, outside DDF's containers. Their [context budget](../orchestrator/handoff.md#context-budget) is measured by a hook the operator installs in their own Claude Code settings; DDF installs nothing.

Sources of the context size, in order:

1. **Status line** (documented): its stdin JSON carries `session_id` and `context_window.total_input_tokens`, the input + cache-write + cache-read tokens of the latest API response. The status line runs only in the terminal UI, and only if configured.
2. **Transcript** (fallback, observed and not documented): the latest entry of the session transcript with `type: "assistant"`, `isSidechain` not `true` and a `message.usage` object; the size is `input_tokens + cache_creation_input_tokens + cache_read_input_tokens`. The transcript is written asynchronously and can lag one turn.

The hook runs on `UserPromptSubmit` (every prompt past a limit) and `PostToolUse` (once per limit crossed) and only adds context through `hookSpecificOutput.additionalContext`. It never blocks, and on any error it exits 0 silently. Limits: `DDF_CONTEXT_SOFT` and `DDF_CONTEXT_HARD` (defaults 120000 and 160000).

Save as `ddf-context-budget.mjs` (Node 18 or later):

```js
#!/usr/bin/env node
// DDF context budget for interactive Claude Code sessions.
// `statusline` mode records the live context size; `hook` mode warns past the limits.
import { readFileSync, writeFileSync, mkdirSync, statSync, openSync, readSync, closeSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

const SOFT = Number(process.env.DDF_CONTEXT_SOFT) || 120000;
const HARD = Number(process.env.DDF_CONTEXT_HARD) || 160000;
const STATE = join(tmpdir(), 'ddf-context-budget');
const FRESH_MS = 10 * 60 * 1000;

let input = {};
try { input = JSON.parse(readFileSync(0, 'utf8')); } catch { process.exit(0); }
const id = String(input.session_id ?? '').replace(/[^A-Za-z0-9_-]/g, '');
const file = (ext) => join(STATE, `${id}.${ext}`);
const k = (n) => `${Math.round(n / 1000)}k`;

function fromStatusLine() {
  try {
    const f = file('tokens');
    if (Date.now() - statSync(f).mtimeMs > FRESH_MS) return null;
    return Number(readFileSync(f, 'utf8')) || null;
  } catch { return null; }
}

function fromTranscript(path) {
  try {
    const size = statSync(path).size;
    const len = Math.min(size, 512 * 1024);
    const buf = Buffer.alloc(len);
    const fd = openSync(path, 'r');
    readSync(fd, buf, 0, len, size - len);
    closeSync(fd);
    const lines = buf.toString('utf8').split('\n');
    for (let i = lines.length - 1; i >= 0; i--) {
      let e;
      try { e = JSON.parse(lines[i]); } catch { continue; }
      const u = e?.message?.usage;
      if (e?.type !== 'assistant' || e.isSidechain === true || !u) continue;
      return (u.input_tokens ?? 0) + (u.cache_creation_input_tokens ?? 0) + (u.cache_read_input_tokens ?? 0);
    }
  } catch {}
  return null;
}

try {
  if (!id) process.exit(0);
  mkdirSync(STATE, { recursive: true, mode: 0o700 });

  if (process.argv[2] === 'statusline') {
    const tokens = input.context_window?.total_input_tokens;
    if (Number.isFinite(tokens)) writeFileSync(file('tokens'), String(tokens));
    const mark = tokens >= HARD ? ' HARD' : tokens >= SOFT ? ' soft' : '';
    process.stdout.write(Number.isFinite(tokens) ? `ctx ${k(tokens)}${mark}` : 'ctx -');
    process.exit(0);
  }

  const tokens = fromStatusLine() ?? fromTranscript(input.transcript_path);
  if (!tokens) process.exit(0);
  const level = tokens >= HARD ? 2 : tokens >= SOFT ? 1 : 0;
  let warned = 0;
  try { warned = Number(readFileSync(file('warned'), 'utf8')) || 0; } catch {}
  writeFileSync(file('warned'), String(level));
  const event = input.hook_event_name;
  if (level === 0 || (event === 'PostToolUse' && level <= warned)) process.exit(0);

  const text = level === 2
    ? `Context budget: ${k(tokens)} tokens, past the hard limit ${k(HARD)}. Finish the current step, post a handoff (DDF docs/orchestrator/handoff.md), and stop.`
    : `Context budget: ${k(tokens)} tokens, past the soft limit ${k(SOFT)}. At the next checkpoint, post a handoff and continue in a new session.`;
  process.stdout.write(JSON.stringify({ hookSpecificOutput: { hookEventName: event, additionalContext: text } }));
} catch {}
process.exit(0);
```

Settings snippet for the operator's user settings (replace the path; keep an existing status line by calling it from a wrapper instead):

```json
{
  "statusLine": { "type": "command", "command": "node <path>/ddf-context-budget.mjs statusline" },
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "node <path>/ddf-context-budget.mjs hook", "timeout": 10 }] }],
    "PostToolUse": [{ "hooks": [{ "type": "command", "command": "node <path>/ddf-context-budget.mjs hook", "timeout": 10 }] }]
  }
}
```

Facts used: the Claude Code [hooks](https://code.claude.com/docs/en/hooks) page (common input fields, `additionalContext`, exit 0 never blocks) and the [status line](https://code.claude.com/docs/en/statusline) page (`session_id`, `context_window.total_input_tokens`). The transcript entry shape is observed, not documented: if it changes, the fallback returns nothing and the hook stays silent.
