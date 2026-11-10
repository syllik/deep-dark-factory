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

1. **Status line** (documented): its stdin JSON carries `session_id`, `context_window.total_input_tokens` (the input + cache-write + cache-read tokens of the latest API response) and `context_window.context_window_size` (the model's context window). The status line runs only in the terminal UI, and only if configured.
2. **Transcript** (fallback, observed and not documented): the latest entry of the session transcript with `type: "assistant"`, `isSidechain` not `true` and a `message.usage` object; the size is `input_tokens + cache_creation_input_tokens + cache_read_input_tokens`. The transcript is written asynchronously and can lag one turn. It carries no window size.

Limits follow the [handoff rules](../orchestrator/handoff.md#context-budget): shares of the room above the session's baseline, no fixed token numbers. The baseline is the first size measured for the session by either source; the script writes it once and never overwrites it. The window is `context_window.context_window_size`; only when the harness reports none (no status line, for example) does the script use the operator's `DDF_CONTEXT_WINDOW`. If neither is available, or the room is zero or less, the hook stays silent and the status line prints `ctx -`: the script never assumes a window. Operator settings, all optional, as environment variables of the Claude Code process:

- `DDF_CONTEXT_SOFT_SHARE`, `DDF_CONTEXT_HARD_SHARE`, `DDF_CONTEXT_STEP_SHARE`: the soft and hard shares of the room and the step past hard (ratio defaults 0.6, 0.85 and 0.1, the example from the handoff rules);
- `DDF_CONTEXT_CAP`: a cap on the total context in tokens, for cost; it acts as the effective window, min(window, cap);
- `DDF_CONTEXT_WINDOW`: the window size, used only when the harness reports none.

The hook runs on `UserPromptSubmit` and `PostToolUse` and only adds context through `hookSpecificOutput.additionalContext`. On either event it warns once when the working context crosses soft, once when it crosses hard, and then once per step share of the room past hard; never on every prompt. The last warned level and size are kept in state, so a drop below a limit (after compaction, for example) re-arms the warning. Hook events from subagents (the input carries `agent_id`) are ignored: they neither receive the warning nor count as warned for the main session. Warning texts name no project, repository or path; the hard text names the closing actions. State is kept in a per-user directory, `$XDG_RUNTIME_DIR/ddf-context-budget` or else `~/.cache/ddf-context-budget`; the script uses it only if it is a real directory (not a symlink) owned by the current user with no group or other permissions, and replaces state files by writing a new file and renaming it, so it never writes through a symlink. It never blocks, and on any error it exits 0 silently.

Save as `ddf-context-budget.mjs` (Node 18 or later):

```js
#!/usr/bin/env node
// Context budget for interactive Claude Code sessions.
// Limits are shares of the room = effective window - baseline; the baseline is
// the context size at the session's first measured turn. No fixed token numbers.
// `statusline` mode records the live size and window; `hook` mode warns.
import { readFileSync, writeFileSync, mkdirSync, statSync, lstatSync, renameSync, unlinkSync, openSync, readSync, closeSync } from 'node:fs';
import { join } from 'node:path';
import { homedir } from 'node:os';
import { randomUUID } from 'node:crypto';

const share = (name, def) => {
  const v = Number(process.env[name]);
  return v > 0 && v <= 1 ? v : def;
};
const SOFT_SHARE = share('DDF_CONTEXT_SOFT_SHARE', 0.6);
const HARD_SHARE = share('DDF_CONTEXT_HARD_SHARE', 0.85);
const STEP_SHARE = share('DDF_CONTEXT_STEP_SHARE', 0.1);
const ENV_WINDOW = Number(process.env.DDF_CONTEXT_WINDOW) > 0 ? Number(process.env.DDF_CONTEXT_WINDOW) : null;
const CAP = Number(process.env.DDF_CONTEXT_CAP) > 0 ? Number(process.env.DDF_CONTEXT_CAP) : null;
// Per-user state directory, never the shared temp directory.
const STATE = process.env.XDG_RUNTIME_DIR
  ? join(process.env.XDG_RUNTIME_DIR, 'ddf-context-budget')
  : join(homedir(), '.cache', 'ddf-context-budget');
const FRESH_MS = 10 * 60 * 1000;

let input = {};
try { input = JSON.parse(readFileSync(0, 'utf8')); } catch { process.exit(0); }
const id = String(input.session_id ?? '').replace(/[^A-Za-z0-9_-]/g, '');
const file = (ext) => join(STATE, `${id}.${ext}`);
const k = (n) => `${Math.round(n / 1000)}k`;

// Use STATE only if it is a real directory, owned by this user, with no group/other access.
function stateIsPrivate() {
  mkdirSync(STATE, { recursive: true, mode: 0o700 });
  const s = lstatSync(STATE);
  if (!s.isDirectory()) return false;
  if (typeof process.getuid === 'function' && s.uid !== process.getuid()) return false;
  return (s.mode & 0o077) === 0;
}

// Write a fresh file ('wx' fails on any existing path, symlinks included), then rename it into place.
function put(ext, value) {
  const tmp = join(STATE, `${id}.${ext}.${randomUUID()}.tmp`);
  try {
    writeFileSync(tmp, value, { flag: 'wx', mode: 0o600 });
    renameSync(tmp, file(ext));
  } catch {
    try { unlinkSync(tmp); } catch {}
  }
}
function get(ext) {
  try { return readFileSync(file(ext), 'utf8'); } catch { return null; }
}
function fresh(ext) {
  try {
    if (Date.now() - statSync(file(ext)).mtimeMs > FRESH_MS) return null;
    return Number(readFileSync(file(ext), 'utf8')) || null;
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

// Baseline: the first size measured for this session by either source; written once, never overwritten.
function baseline(tokens) {
  const b = Number(get('base'));
  if (b > 0) return b;
  put('base', String(tokens));
  return Number(get('base')) || tokens;
}

// Limits in tokens for this session, or null when the room cannot be measured.
function limits(tokens, window) {
  const base = baseline(tokens); // recorded even before the window is known
  if (!window) return null; // never assume a window
  const effective = CAP ? Math.min(window, CAP) : window; // the operator cap acts as the window
  const room = effective - base;
  if (room <= 0) return null;
  return { base, room, effective, soft: base + SOFT_SHARE * room, hard: base + HARD_SHARE * room, step: STEP_SHARE * room };
}

try {
  if (!id) process.exit(0);
  if (!stateIsPrivate()) process.exit(0);

  if (process.argv[2] === 'statusline') {
    const tokens = input.context_window?.total_input_tokens;
    const reported = Number(input.context_window?.context_window_size) || null;
    if (!Number.isFinite(tokens)) { process.stdout.write('ctx -'); process.exit(0); }
    put('tokens', String(tokens));
    if (reported) put('window', String(reported));
    const L = limits(tokens, reported ?? ENV_WINDOW);
    if (!L) { process.stdout.write('ctx -'); process.exit(0); }
    const mark = tokens >= L.hard ? ' HARD' : tokens >= L.soft ? ' soft' : '';
    process.stdout.write(`ctx ${k(tokens - L.base)}/${k(L.room)}${mark}`);
    process.exit(0);
  }

  if (input.agent_id) process.exit(0); // subagent event: no warning, no warned state
  const tokens = fresh('tokens') ?? fromTranscript(input.transcript_path);
  if (!tokens) process.exit(0);
  const L = limits(tokens, fresh('window') ?? ENV_WINDOW);
  if (!L) process.exit(0); // cannot measure the room: stay silent

  const level = tokens >= L.hard ? 2 : tokens >= L.soft ? 1 : 0;
  const [wLevel, wTokens] = (get('warned') ?? '0 0').split(' ').map((n) => Number(n) || 0);
  if (level < wLevel) put('warned', `${level} ${tokens}`); // dropped below a limit: re-arm
  const crossed = level > wLevel;
  const grew = level === 2 && wLevel === 2 && tokens - wTokens >= L.step;
  if (!crossed && !grew) process.exit(0);
  put('warned', `${level} ${tokens}`);

  const used = `${k(tokens - L.base)} of ${k(L.room)} working room used`;
  const text = level === 2
    ? `Context budget: hard limit, ${used}. Start no new step. Finish the current step, then only: post the result, post the handoff, answer the owner's questions about the result. Then stop.`
    : `Context budget: soft limit, ${used}. Advice: at the next checkpoint, if the remaining work is long or a pause will outlast the prompt cache, post a handoff and continue in a new session.`;
  process.stdout.write(JSON.stringify({ hookSpecificOutput: { hookEventName: input.hook_event_name, additionalContext: text } }));
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

Facts used: the Claude Code [hooks](https://code.claude.com/docs/en/hooks) page (common input fields, `agent_id` on events fired inside a subagent, `additionalContext`, exit 0 never blocks) and the [status line](https://code.claude.com/docs/en/statusline) page (`session_id`, `context_window.total_input_tokens`, `context_window.context_window_size`). The transcript entry shape is observed, not documented: if it changes, the fallback returns nothing and the hook stays silent.
