#!/usr/bin/env node
// Deterministic documentation consistency check. Node built-ins only.
// Usage: node scripts/docs-check.mjs [repo-root]   (default: parent of this script's directory)
// Exit 0 with "docs-check: OK" when clean; exit 1 after printing every finding.

import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { join, relative, dirname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(process.argv[2] ?? join(dirname(fileURLToPath(import.meta.url)), '..'));
const findings = [];
const report = (msg) => findings.push(msg);
const rel = (abs) => relative(root, abs).split(sep).join('/');
const SKIP_DIRS = new Set(['.git', 'node_modules']);

function walk(dir, out = []) {
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (SKIP_DIRS.has(entry.name)) continue;
    const abs = join(dir, entry.name);
    if (entry.isDirectory()) walk(abs, out);
    else if (entry.isFile()) out.push(abs);
  }
  return out;
}

const read = (abs) => readFileSync(abs, 'utf8');
const lines = (text) => text.split(/\r?\n/);

// Yields [lineNumber, line, inFence] for each line, tracking ``` and ~~~ fences.
function* scanLines(text) {
  let fence = null;
  const all = lines(text);
  for (let i = 0; i < all.length; i++) {
    const line = all[i];
    const m = line.match(/^\s{0,3}(`{3,}|~{3,})(.*)$/);
    if (m) {
      if (fence === null) { fence = m[1]; yield [i + 1, line, true]; continue; }
      if (m[1][0] === fence[0] && m[1].length >= fence.length && m[2].trim() === '') {
        fence = null; yield [i + 1, line, true]; continue;
      }
    }
    yield [i + 1, line, fence !== null];
  }
}

// GitHub-style heading slug (github-slugger semantics, simplified).
function slugify(heading) {
  const text = heading
    .replace(/<[^>]+>/g, '')                     // inline HTML
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')   // links/images -> text
    .replace(/[`*~]/g, '')                       // emphasis/code markers
    .trim()
    .toLowerCase();
  return text.replace(/[^\p{L}\p{M}\p{N}\p{Pc} -]/gu, '').replace(/ /g, '-');
}

const anchorCache = new Map();
function anchorsOf(abs) {
  if (anchorCache.has(abs)) return anchorCache.get(abs);
  const set = new Set();
  const seen = new Map();
  for (const [, line, inFence] of scanLines(read(abs))) {
    if (inFence) continue;
    const h = line.match(/^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/);
    if (h) {
      const base = slugify(h[1]);
      const n = seen.get(base) ?? 0;
      set.add(n === 0 ? base : `${base}-${n}`);
      seen.set(base, n + 1);
    }
    for (const m of line.matchAll(/<a\s+[^>]*(?:id|name)=["']([^"']+)["']/gi)) set.add(m[1]);
  }
  anchorCache.set(abs, set);
  return set;
}

// (a) relative Markdown links.
const allFiles = walk(root);
const mdFiles = allFiles.filter((f) => f.endsWith('.md'));
for (const file of mdFiles) {
  for (const [no, rawLine, inFence] of scanLines(read(file))) {
    if (inFence) continue;
    const line = rawLine.replace(/`[^`]*`/g, (s) => ' '.repeat(s.length)); // ignore inline code
    const targets = [];
    for (const m of line.matchAll(/!?\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+["'][^)]*["'])?\s*\)/g)) targets.push(m[1]);
    for (const m of line.matchAll(/^\s{0,3}\[[^\]]+\]:\s*<?(\S+?)>?(?:\s|$)/g)) targets.push(m[1]);
    for (const target of targets) {
      if (/^[a-z][a-z0-9+.-]*:/i.test(target)) continue; // http:, https:, mailto:, etc.
      const hashAt = target.indexOf('#');
      const pathPart = decodeURIComponent(hashAt === -1 ? target : target.slice(0, hashAt));
      const anchor = hashAt === -1 ? null : decodeURIComponent(target.slice(hashAt + 1));
      const dest = pathPart === '' ? file
        : pathPart.startsWith('/') ? join(root, pathPart) : resolve(dirname(file), pathPart);
      if (!existsSync(dest)) { report(`BROKEN_LINK ${rel(file)}:${no} ${target}`); continue; }
      if (anchor && statSync(dest).isFile() && dest.endsWith('.md') && !anchorsOf(dest).has(anchor.toLowerCase()) && !anchorsOf(dest).has(anchor)) {
        report(`BROKEN_LINK ${rel(file)}:${no} ${target}`);
      }
    }
  }
}

// (b) schemas parse and declare $schema/$id; examples parse.
for (const file of walk(join(root, 'schemas')).filter((f) => f.endsWith('.json'))) {
  const isExample = rel(file).startsWith('schemas/examples/');
  let doc;
  try { doc = JSON.parse(read(file)); } catch (e) { report(`BAD_SCHEMA ${rel(file)} invalid JSON: ${e.message}`); continue; }
  if (isExample || dirname(file) !== join(root, 'schemas')) continue;
  for (const key of ['$schema', '$id']) {
    if (typeof doc?.[key] !== 'string' || doc[key] === '') report(`BAD_SCHEMA ${rel(file)} missing ${key}`);
  }
}

// (c) placeholders in canon directories (plans/specs are working records, excluded).
const isExcludedDoc = (r) => r.startsWith('docs/plans/') || r.startsWith('docs/specs/');
const canon = ['docs', 'prompts', 'templates']
  .flatMap((d) => walk(join(root, d)))
  .filter((f) => !isExcludedDoc(rel(f)));
for (const file of canon) {
  lines(read(file)).forEach((line, i) => {
    if (/\b(TBD|TODO)\b/.test(line)) report(`PLACEHOLDER ${rel(file)}:${i + 1}`);
  });
}

// (d) every named sync block equals the spec block of the same name, byte for byte per line.
// Markers `<!-- sync:NAME -->` and `<!-- /sync:NAME -->`, NAME matching [a-z][a-z0-9-]*,
// must be alone on their line and start at column 1. The spec is the source and holds
// exactly one block per name (sync:forbidden required). docs/concepts/policy.md must hold
// sync:forbidden and every block the spec holds (hard-stop-globs, lockfiles,
// agent-instruction-files). Every other Markdown file under docs/, prompts/ and templates/
// that contains a sync marker is checked for the blocks it holds. SYNC_DRIFT for a missing
// block, a duplicate block in one file, a name the spec lacks, or a differing line.
const MARKER = /^<!-- (\/?)sync:([a-z][a-z0-9-]*) -->$/;
const isMarker = (l) => MARKER.test(l.trim());
function syncBlocks(r, required = []) {
  const abs = join(root, r);
  if (!existsSync(abs)) { report(`SYNC_DRIFT ${r}:0 file missing`); return null; }
  const all = lines(read(abs));
  let misplaced = false;
  all.forEach((l, i) => {
    const t = l.trim();
    if (isMarker(t) && l !== t) {
      misplaced = true;
      report(`SYNC_DRIFT ${r}:${i + 1} ${/^\s/.test(l) ? 'marker must start at column 1' : 'marker must not have trailing whitespace'}`);
    }
  });
  if (misplaced) return null;
  const blocks = new Map(); // name -> { start, items } of the first block with that name
  let open = null;          // { name, start } of the block being read
  const broken = new Set(); // names already reported as unbalanced
  all.forEach((l, i) => {
    const m = l.match(MARKER);
    if (!m) return;
    const [, slash, name] = m;
    if (!slash) {
      if (open) report(`SYNC_DRIFT ${r}:${i + 1} sync:${name} opened inside sync:${open.name}`);
      open = { name, start: i };
      return;
    }
    if (!open || open.name !== name) { broken.add(name); report(`SYNC_DRIFT ${r}:${i + 1} missing sync:${name} markers`); open = null; return; }
    if (blocks.has(name)) report(`SYNC_DRIFT ${r}:${open.start + 1} duplicate sync:${name} block`);
    else blocks.set(name, { start: open.start, items: all.slice(open.start + 1, i) });
    open = null;
  });
  if (open) { broken.add(open.name); report(`SYNC_DRIFT ${r}:${open.start + 1} missing sync:${open.name} markers`); }
  for (const name of required) {
    if (!blocks.has(name) && !broken.has(name)) report(`SYNC_DRIFT ${r}:0 missing sync:${name} markers`);
  }
  return blocks;
}
const policyPath = 'docs/concepts/policy.md';
const specPath = 'docs/specs/2026-11-03-ddf-v1-design.md';
const spec = syncBlocks(specPath, ['forbidden']);
const syncTargets = new Set([policyPath]);
for (const file of ['docs', 'prompts', 'templates'].flatMap((d) => walk(join(root, d)))) {
  const r = rel(file);
  if (r === specPath || !r.endsWith('.md')) continue;
  if (lines(read(file)).some(isMarker)) syncTargets.add(r);
}
const policyRequired = [...new Set(['forbidden', ...(spec?.keys() ?? [])])];
for (const target of [...syncTargets].sort()) {
  const blocks = syncBlocks(target, target === policyPath ? policyRequired : []);
  if (!blocks || !spec) continue;
  for (const [name, block] of [...blocks].sort((a, b) => a[1].start - b[1].start)) {
    const source = spec.get(name);
    if (!source) { report(`SYNC_DRIFT ${target}:${block.start + 1} sync:${name} not in spec`); continue; }
    const n = Math.max(block.items.length, source.items.length);
    for (let i = 0; i < n; i++) {
      if (block.items[i] !== source.items[i]) {
        report(`SYNC_DRIFT ${target}:${block.start + 2 + i} expected ${JSON.stringify(source.items[i] ?? '<end of block>')}`);
        break;
      }
    }
  }
}

// (e) doc size and Status line for operational docs (plans, specs, research excluded).
const operationalDocs = walk(join(root, 'docs')).filter((f) => {
  const r = rel(f);
  return r.endsWith('.md') && !isExcludedDoc(r) && !r.startsWith('docs/research/');
});
for (const file of operationalDocs) {
  const all = lines(read(file));
  const count = all.at(-1) === '' ? all.length - 1 : all.length;
  if (count > 250) report(`LONG_DOC ${rel(file)} ${count} lines (limit 250)`);
  if (!all.slice(0, 12).some((l) => /(^|[\s*_>])Status:/.test(l))) report(`NO_STATUS ${rel(file)}`);
}

if (findings.length) {
  for (const f of findings) console.log(f);
  console.log(`docs-check: FAILED (${findings.length} finding${findings.length === 1 ? '' : 's'})`);
  process.exit(1);
}
console.log('docs-check: OK');
