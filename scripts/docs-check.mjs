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

// (d) forbidden-paths block in policy.md equals the spec block.
function syncBlock(r) {
  const abs = join(root, r);
  if (!existsSync(abs)) { report(`SYNC_DRIFT ${r}:0 file missing`); return null; }
  const all = lines(read(abs));
  const start = all.findIndex((l) => l.trim() === '<!-- sync:forbidden -->');
  const end = all.findIndex((l, i) => i > start && l.trim() === '<!-- /sync:forbidden -->');
  if (start === -1 || end === -1) { report(`SYNC_DRIFT ${r}:0 missing sync:forbidden markers`); return null; }
  return { start, items: all.slice(start + 1, end).map((l) => l.trim()) };
}
const policyPath = 'docs/concepts/policy.md';
const specPath = 'docs/specs/2026-11-03-ddf-v1-design.md';
const policy = syncBlock(policyPath);
const spec = syncBlock(specPath);
if (policy && spec) {
  const n = Math.max(policy.items.length, spec.items.length);
  for (let i = 0; i < n; i++) {
    if (policy.items[i] !== spec.items[i]) {
      report(`SYNC_DRIFT ${policyPath}:${policy.start + 2 + i} expected ${JSON.stringify(spec.items[i] ?? '<end of block>')}`);
      break;
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
