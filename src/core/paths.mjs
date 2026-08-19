// SPDX-License-Identifier: Apache-2.0
// Path normalization, a dependency-free glob subset, and conservative overlap.
// Invariant I4: overlap() may say yes when a real conflict would not have happened.
// It must never say no when the materialized sets intersect or the prefixes nest.
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { AppError } from './errors.mjs';

const IS_WIN = process.platform === 'win32';
const META = /[*?[{]/;
const WIN_RESERVED = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)/i;
const RESERVED_ROOTS = ['.fridge', '.git'];

export const caseFold = (s, insensitive) => (insensitive ? s.toLowerCase() : s);

export function defaultCaseInsensitive(setting = 'auto') {
  if (setting === 'sensitive') return false;
  if (setting === 'insensitive') return true;
  return process.platform === 'win32' || process.platform === 'darwin';
}

export function literalPrefix(pattern) {
  const i = pattern.search(META);
  if (i === -1) return pattern.replace(/\/+$/, '');
  const head = pattern.slice(0, i);
  const cut = head.lastIndexOf('/');
  return cut === -1 ? '' : head.slice(0, cut);
}

export function isPrefixPath(a, b) {
  if (a === '' || b === '') return true;
  if (a === b) return true;
  return b.startsWith(a + '/');
}

export function expandBraces(pattern) {
  const open = pattern.indexOf('{');
  if (open === -1) return [pattern];
  let depth = 0;
  let close = -1;
  for (let i = open; i < pattern.length; i++) {
    if (pattern[i] === '{') depth++;
    else if (pattern[i] === '}') {
      depth--;
      if (depth === 0) { close = i; break; }
    }
  }
  if (close === -1) throw new AppError('E_PATH_INVALID', `Unterminated brace in pattern: ${pattern}`);
  const head = pattern.slice(0, open);
  const tail = pattern.slice(close + 1);
  const body = pattern.slice(open + 1, close);
  const parts = [];
  let depth2 = 0;
  let cur = '';
  for (const ch of body) {
    if (ch === '{') depth2++;
    if (ch === '}') depth2--;
    if (ch === ',' && depth2 === 0) { parts.push(cur); cur = ''; } else cur += ch;
  }
  parts.push(cur);
  return parts.flatMap((p) => expandBraces(head + p + tail));
}

function segmentToRegex(seg, pattern) {
  let re = '';
  for (let i = 0; i < seg.length; i++) {
    const c = seg[i];
    if (c === '*') re += '[^/]*';
    else if (c === '?') re += '[^/]';
    else if (c === '[') {
      let j = i + 1;
      let neg = false;
      if (seg[j] === '!' || seg[j] === '^') { neg = true; j++; }
      let cls = '';
      while (j < seg.length && seg[j] !== ']') { cls += seg[j].replace(/[\\^\]]/g, '\\$&'); j++; }
      if (j >= seg.length) throw new AppError('E_PATH_INVALID', `Unterminated character class in: ${pattern}`);
      if (cls === '') throw new AppError('E_PATH_INVALID', `Empty character class in: ${pattern}`);
      re += `[${neg ? '^' : ''}${cls}]`;
      i = j;
    } else re += c.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }
  return re;
}

export function patternToRegExp(pattern, insensitive = false) {
  if (/[!@+?*]\(/.test(pattern)) {
    throw new AppError('E_PATH_INVALID', `Extended globs are not supported: ${pattern}`, {
      hint: 'Supported: * ** ? [abc] {a,b}. Use --exclude instead of negation.',
    });
  }
  if (pattern.startsWith('!')) {
    throw new AppError('E_PATH_INVALID', `Negated patterns are not supported: ${pattern}`, { hint: 'Use --exclude.' });
  }
  const segs = pattern.split('/');
  let re = '^';
  segs.forEach((seg, idx) => {
    const last = idx === segs.length - 1;
    if (seg === '**') re += last ? '.*' : '(?:.*/)?';
    else {
      if (seg.includes('**')) {
        throw new AppError('E_PATH_INVALID', `'**' must be a whole path segment: ${pattern}`);
      }
      re += segmentToRegex(seg, pattern) + (last ? '' : '/');
    }
  });
  return new RegExp(re + '$', insensitive ? 'i' : '');
}

export function matchesAny(patterns, file, insensitive = false) {
  for (const p of patterns) {
    for (const expanded of expandBraces(p)) {
      if (patternToRegExp(expanded, insensitive).test(file)) return true;
    }
  }
  return false;
}

/** Normalize one user-supplied path or glob into a repo-relative POSIX pattern. */
export function normalizePattern(input, { root, cwd = process.cwd() }) {
  if (input === undefined || input === null || String(input).trim() === '') {
    throw new AppError('E_PATH_INVALID', 'Empty path.');
  }
  let raw = String(input);
  if (raw.includes('\0')) throw new AppError('E_PATH_INVALID', 'Path contains a NUL byte.');
  if (/[\n\r]/.test(raw)) throw new AppError('E_PATH_INVALID', 'Path contains a newline.');
  if (raw.length > 4096) throw new AppError('E_PATH_INVALID', 'Path is longer than 4096 characters.');
  if (IS_WIN) raw = raw.replace(/\\/g, '/');
  if (raw.startsWith('~')) throw new AppError('E_PATH_INVALID', 'Home-relative paths are not accepted.');
  if (raw.startsWith('//') || /^\\\\/.test(raw)) throw new AppError('E_PATH_INVALID', 'UNC paths are not accepted.');
  raw = raw.normalize('NFC').replace(/\/{2,}/g, '/');
  const dirIntent = raw.length > 1 && raw.endsWith('/');
  raw = raw.replace(/\/+$/, '') || '/';

  const metaIdx = raw.search(META);
  const head = metaIdx === -1 ? raw : raw.slice(0, metaIdx);
  const tail = metaIdx === -1 ? '' : raw.slice(metaIdx);
  const cut = head.lastIndexOf('/');
  const headDir = cut === -1 ? '' : head.slice(0, cut) || '/';
  const headRest = cut === -1 ? head : head.slice(cut + 1);

  const absDir = path.resolve(cwd, headDir === '' ? '.' : headDir);
  let relDir = path.relative(root, absDir).split(path.sep).join('/');
  if (relDir === '..' || relDir.startsWith('../')) {
    throw new AppError('E_PATH_INVALID', `Path escapes the workspace: ${input}`, { hint: 'Claim paths inside the repository only.' });
  }
  const pattern = [relDir, headRest + tail].filter((p) => p !== '' && p !== '.').join('/');
  if (pattern === '' || pattern === '.') {
    throw new AppError('E_PATH_INVALID', 'Whole-workspace claims need --confirm-global.', { hint: 'fridge claim "src/**" is usually what you want.' });
  }
  for (const seg of pattern.split('/')) {
    if (seg === '.' || seg === '..') throw new AppError('E_PATH_INVALID', `Path traversal is not allowed: ${input}`);
    if (WIN_RESERVED.test(seg)) throw new AppError('E_PATH_INVALID', `Reserved Windows name: ${seg}`);
    if (seg.includes(':')) throw new AppError('E_PATH_INVALID', `':' is not allowed in a path segment: ${seg}`);
    if (/[. ]$/.test(seg)) throw new AppError('E_PATH_INVALID', `Segment must not end with a dot or space: ${seg}`);
  }
  const firstSeg = pattern.split('/')[0];
  if (RESERVED_ROOTS.includes(firstSeg)) {
    throw new AppError('E_PATH_INVALID', `${firstSeg}/ is reserved and cannot be claimed.`);
  }
  // Symlink containment: check the deepest ancestor that actually exists, not just
  // the full literal prefix. A symlinked *directory* half way down is the real risk.
  const lp = literalPrefix(pattern) || pattern.split('/')[0];
  const lexists = (p) => { try { fs.lstatSync(p); return true; } catch { return false; } };
  let probe = path.join(root, lp);
  while (probe !== root && !lexists(probe)) probe = path.dirname(probe);
  if (probe !== root && lexists(probe)) {
    let real;
    try {
      real = fs.realpathSync(probe);
    } catch {
      // A dangling symlink: resolve one hop by hand rather than trusting it.
      try { real = path.resolve(path.dirname(probe), fs.readlinkSync(probe)); } catch { real = probe; }
    }
    const realRoot = fs.realpathSync(root);
    const rel = path.relative(realRoot, real);
    if (rel === '..' || rel.startsWith('..' + path.sep) || path.isAbsolute(rel)) {
      throw new AppError('E_PATH_INVALID', `Symlink escapes the workspace: ${input}`, {
        hint: 'Claims must stay inside the repository. Resolve the real path and claim that.',
      });
    }
  }
  return { pattern, dirIntent, isGlob: META.test(pattern) };
}

export function isGlobal(pattern) {
  return pattern === '**' || pattern === '*' || pattern === '.' || pattern === '/';
}

export function listWorkspaceFiles(root) {
  const git = spawnSync('git', ['-C', root, 'ls-files', '-co', '--exclude-standard', '-z'], {
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  });
  if (git.status === 0 && typeof git.stdout === 'string') {
    return { files: git.stdout.split('\0').filter(Boolean), materializer: 'git' };
  }
  const out = [];
  const skip = new Set(['.git', '.fridge', 'node_modules', '.venv', 'dist', 'build', '.next']);
  const walk = (dir, rel) => {
    let entries;
    try { entries = fs.readdirSync(dir, { withFileTypes: true }); } catch { return; }
    for (const e of entries) {
      if (skip.has(e.name)) continue;
      const child = path.join(dir, e.name);
      const childRel = rel ? `${rel}/${e.name}` : e.name;
      if (e.isDirectory()) walk(child, childRel);
      else if (e.isFile()) out.push(childRel);
    }
  };
  walk(root, '');
  return { files: out, materializer: 'walk' };
}

/** Expand patterns into concrete repo-relative files, plus the matchers used later. */
export function materialize(root, patterns, { limit = 5000, files = null, insensitive = false } = {}) {
  const listing = files ? { files, materializer: 'given' } : listWorkspaceFiles(root);
  const matchers = [];
  for (const p of patterns) {
    matchers.push(p);
    const abs = path.join(root, literalPrefix(p) === p ? p : '');
    if (!META.test(p) && fs.existsSync(abs) && fs.statSync(abs).isDirectory()) matchers.push(`${p}/**`);
  }
  const out = [];
  let truncated = false;
  for (const f of listing.files) {
    if (matchesAny(matchers, f, insensitive)) {
      if (out.length >= limit) { truncated = true; break; }
      out.push(f);
    }
  }
  return { materialized: out.sort(), materializedTruncated: truncated, matchers, materializer: listing.materializer };
}

/** A pattern that can reach any depth from the repository root. */
export const isRootGlobal = (p) => p === '**' || p === '*' || p === '**/*' || p.startsWith('**/');

/** Conservative overlap between two scopes. */
export function scopesOverlap(a, b, { insensitive = false } = {}) {
  const fold = (s) => caseFold(s, insensitive);
  for (const pa of a.include) {
    for (const pb of b.include) {
      if (isRootGlobal(pa) || isRootGlobal(pb)) {
        return { overlap: true, reason: 'global-pattern', paths: [pa, pb] };
      }
      const la = fold(literalPrefix(pa));
      const lb = fold(literalPrefix(pb));
      // Empty prefixes are handled by materialization below; treating them as
      // nesting here would flag `*.md` against `src/**`, which cannot collide.
      if (la && lb && (isPrefixPath(la, lb) || isPrefixPath(lb, la))) {
        return { overlap: true, reason: 'literal-prefix-nesting', paths: [pa, pb] };
      }
    }
  }
  const setB = new Set((b.materialized || []).map(fold));
  const hits = (a.materialized || []).filter((f) => setB.has(fold(f)));
  if (hits.length) return { overlap: true, reason: 'materialized-intersection', paths: hits.slice(0, 25) };
  const crossA = (a.materialized || []).filter((f) => matchesAny(b.matchers || b.include, f, insensitive));
  if (crossA.length) return { overlap: true, reason: 'cross-pattern-match', paths: crossA.slice(0, 25) };
  const crossB = (b.materialized || []).filter((f) => matchesAny(a.matchers || a.include, f, insensitive));
  if (crossB.length) return { overlap: true, reason: 'cross-pattern-match', paths: crossB.slice(0, 25) };
  if (a.materializedTruncated || b.materializedTruncated) {
    for (const pa of a.include) {
      for (const pb of b.include) {
        const la = fold(literalPrefix(pa));
        const lb = fold(literalPrefix(pb));
        if (isPrefixPath(la, lb) || isPrefixPath(lb, la)) {
          return { overlap: true, reason: 'truncated-scope-fallback', paths: [pa, pb] };
        }
      }
    }
  }
  return { overlap: false };
}
