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
const MAX_BRACE_EXPANSIONS = 256;

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
  const out = [];
  const visit = (value) => {
    const open = value.indexOf('{');
    if (open === -1) {
      if (out.length >= MAX_BRACE_EXPANSIONS) {
        throw new AppError('E_PATH_INVALID', `Pattern expands to more than ${MAX_BRACE_EXPANSIONS} alternatives: ${pattern}`, {
          hint: 'Split it into separate claims.',
        });
      }
      out.push(value);
      return;
    }
    let depth = 0;
    let close = -1;
    for (let i = open; i < value.length; i++) {
      if (value[i] === '{') depth++;
      else if (value[i] === '}') {
        depth--;
        if (depth === 0) { close = i; break; }
      }
    }
    if (close === -1) throw new AppError('E_PATH_INVALID', `Unterminated brace in pattern: ${pattern}`);
    const head = value.slice(0, open);
    const tail = value.slice(close + 1);
    const body = value.slice(open + 1, close);
    let nested = 0;
    let start = 0;
    for (let i = 0; i < body.length; i++) {
      if (body[i] === '{') nested++;
      else if (body[i] === '}') nested--;
      else if (body[i] === ',' && nested === 0) {
        visit(head + body.slice(start, i) + tail);
        start = i + 1;
      }
    }
    visit(head + body.slice(start) + tail);
  };
  visit(pattern);
  return out;
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
  try {
    return new RegExp(re + '$', insensitive ? 'iu' : 'u');
  } catch {
    throw new AppError('E_PATH_INVALID', `Unsupported pattern: ${pattern}`);
  }
}

// ------------------------------------------------------- pattern intersection
//
// Two claims collide if some path could satisfy both scopes, whether or not
// that path exists yet. Comparing materialized file lists cannot see a file
// that nobody has created, and comparing literal prefixes cannot see that
// `a*/x.ts` and `*b/x.ts` both match `ab/x.ts`. So the patterns themselves are
// intersected, symbolically, before anything on disk is consulted.
//
// The supported subset (literals, `*`, `?`, `[...]`, `{...}` after expansion,
// and `**` as a whole segment) is a regular language, so "can these two match
// the same string" is decidable. It is decided here by two dynamic programs:
// one over path segments for `**`, one over characters inside a segment.

const expandBracesBounded = (pattern) => expandBraces(pattern);

function classRanges(set, pattern) {
  const chars = Array.from(set);
  const ranges = [];
  for (let i = 0; i < chars.length;) {
    if (i + 2 < chars.length && chars[i + 1] === '-') {
      const lo = chars[i].codePointAt(0);
      const hi = chars[i + 2].codePointAt(0);
      if (lo > hi) throw new AppError('E_PATH_INVALID', `Descending character range in: ${pattern}`);
      ranges.push([lo, hi]);
      i += 3;
    } else {
      const cp = chars[i].codePointAt(0);
      ranges.push([cp, cp]);
      i++;
    }
  }
  ranges.sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const merged = [];
  for (const range of ranges) {
    const last = merged[merged.length - 1];
    if (!last || range[0] > last[1] + 1) merged.push([...range]);
    else last[1] = Math.max(last[1], range[1]);
  }
  return merged;
}

/** One path segment becomes a list of single-character matchers plus stars. */
function segTokens(seg, pattern) {
  const chars = Array.from(seg);
  const out = [];
  for (let i = 0; i < chars.length; i++) {
    const c = chars[i];
    if (c === '*') {
      if (!out.length || out[out.length - 1].t !== 'star') out.push({ t: 'star' });
    } else if (c === '?') out.push({ t: 'any' });
    else if (c === '[') {
      let j = i + 1;
      let neg = false;
      if (chars[j] === '!' || chars[j] === '^') { neg = true; j++; }
      let set = '';
      while (j < chars.length && chars[j] !== ']') { set += chars[j]; j++; }
      if (j >= chars.length) throw new AppError('E_PATH_INVALID', `Unterminated character class in: ${pattern}`);
      if (set === '') throw new AppError('E_PATH_INVALID', `Empty character class in: ${pattern}`);
      out.push({ t: 'class', neg, ranges: classRanges(set, pattern) });
      i = j;
    } else out.push({ t: 'lit', c });
  }
  return out;
}

const rangeHas = (ranges, cp) => ranges.some(([lo, hi]) => cp >= lo && cp <= hi);
const normalizeRanges = (ranges) => {
  const sorted = ranges.map(([lo, hi]) => [lo, hi]).sort((a, b) => a[0] - b[0] || a[1] - b[1]);
  const out = [];
  for (const range of sorted) {
    const last = out[out.length - 1];
    if (!last || range[0] > last[1] + 1) out.push(range);
    else last[1] = Math.max(last[1], range[1]);
  }
  return out;
};

/**
 * Canonicalise ASCII classes to lowercase for case-insensitive comparison.
 * Unicode case mappings are not monotonic over arbitrary ranges, so a class
 * containing non-ASCII code points returns null and the caller fails safe.
 */
const foldedRanges = (ranges, insensitive) => {
  if (!insensitive) return normalizeRanges(ranges);
  const out = [];
  const add = (lo, hi) => { if (lo <= hi) out.push([lo, hi]); };
  for (const [lo, hi] of ranges) {
    if (lo < 0 || hi > 0x7f) return null;
    add(lo, Math.min(hi, 0x40));
    add(Math.max(lo, 0x41) + 0x20, Math.min(hi, 0x5a) + 0x20);
    add(Math.max(lo, 0x5b), hi);
  }
  return normalizeRanges(out);
};

/** Does a character class contain c, including inclusive ranges such as a-z? */
const classHas = (tok, c, insensitive) => {
  if (insensitive) {
    const escaped = tok.ranges.map(([lo, hi]) => {
      const start = `\\u{${lo.toString(16)}}`;
      const end = `\\u{${hi.toString(16)}}`;
      return lo === hi ? start : `${start}-${end}`;
    }).join('');
    return new RegExp(`^[${tok.neg ? '^' : ''}${escaped}]$`, 'iu').test(c);
  }
  const has = rangeHas(tok.ranges, c.codePointAt(0));
  return tok.neg ? !has : has;
};

function rangesIntersect(a, b) {
  for (const [alo, ahi] of a) {
    for (const [blo, bhi] of b) {
      if (Math.max(alo, blo) <= Math.min(ahi, bhi)) return true;
    }
  }
  return false;
}

function hasOutside(positive, excluded) {
  for (const [lo, hi] of positive) {
    let cursor = lo;
    for (const [xlo, xhi] of excluded) {
      if (xhi < cursor) continue;
      if (xlo > hi) break;
      if (xlo > cursor) return true;
      cursor = Math.max(cursor, xhi + 1);
      if (cursor > hi) break;
    }
    if (cursor <= hi) return true;
  }
  return false;
}

const sameChar = (a, b, insensitive) => {
  if (a === b) return true;
  if (!insensitive) return false;
  return new RegExp(`^\\u{${a.codePointAt(0).toString(16)}}$`, 'iu').test(b);
};

/** Can two single-character matchers accept the same character? */
function charsIntersect(x, y, insensitive) {
  if (x.t === 'any' || y.t === 'any') return true;
  if (x.t === 'lit' && y.t === 'lit') return sameChar(x.c, y.c, insensitive);
  if (x.t === 'lit') return classHas(y, x.c, insensitive);
  if (y.t === 'lit') return classHas(x, y.c, insensitive);
  const xr = foldedRanges(x.ranges, insensitive);
  const yr = foldedRanges(y.ranges, insensitive);
  if (!xr || !yr) return true;
  if (x.neg && y.neg) return true;
  if (x.neg) return hasOutside(yr, xr);
  if (y.neg) return hasOutside(xr, yr);
  return rangesIntersect(xr, yr);
}

/** Can these two brace-free, separator-free segment patterns match a common string? */
function segmentsIntersect(sa, sb, insensitive, pattern) {
  const a = segTokens(sa, pattern);
  const b = segTokens(sb, pattern);
  const memo = new Map();
  const go = (i, j) => {
    const key = i * (b.length + 1) + j;
    const hit = memo.get(key);
    if (hit !== undefined) return hit;
    let res;
    if (i === a.length && j === b.length) res = true;
    else if (i === a.length) res = b.slice(j).every((t) => t.t === 'star');
    else if (j === b.length) res = a.slice(i).every((t) => t.t === 'star');
    else if (a[i].t === 'star') res = go(i + 1, j) || go(i, j + 1);
    else if (b[j].t === 'star') res = go(i, j + 1) || go(i + 1, j);
    else res = charsIntersect(a[i], b[j], insensitive) && go(i + 1, j + 1);
    memo.set(key, res);
    return res;
  };
  return go(0, 0);
}

/** Can these two brace-free patterns match a common path? `**` spans segments. */
function globsIntersect(pa, pb, insensitive) {
  const a = pa.split('/');
  const b = pb.split('/');
  const memo = new Map();
  const go = (i, j) => {
    const key = i * (b.length + 1) + j;
    const hit = memo.get(key);
    if (hit !== undefined) return hit;
    let res;
    if (i === a.length && j === b.length) res = true;
    else if (i === a.length) res = b.slice(j).every((s) => s === '**');
    else if (j === b.length) res = a.slice(i).every((s) => s === '**');
    else if (a[i] === '**' && b[j] === '**') res = go(i + 1, j + 1) || go(i + 1, j) || go(i, j + 1);
    else if (a[i] === '**') res = go(i + 1, j) || go(i, j + 1);
    else if (b[j] === '**') res = go(i, j + 1) || go(i + 1, j);
    else res = segmentsIntersect(a[i], b[j], insensitive, `${pa} vs ${pb}`) && go(i + 1, j + 1);
    memo.set(key, res);
    return res;
  };
  return go(0, 0);
}

/**
 * Could any path, existing or not, match both patterns?
 *
 * Never answers no when a common path exists. It may answer yes for a pair
 * that would not have collided in practice, which costs a claim and protects
 * the file.
 */
export function patternsCanIntersect(pa, pb, insensitive = false) {
  for (const ea of expandBracesBounded(pa)) {
    for (const eb of expandBracesBounded(pb)) {
      if (globsIntersect(ea, eb, insensitive)) return true;
    }
  }
  return false;
}

/**
 * Does every path matching `inner` also match `outer`? Sufficient, not
 * complete: it recognises the shapes an --exclude actually takes, and says no
 * whenever it is unsure, which keeps the caller failing closed.
 */
export function patternCovers(outer, inner, insensitive = false) {
  const covers1 = (o, i) => {
    if (caseFold(o, insensitive) === caseFold(i, insensitive)) return true;
    if (!o.endsWith('/**')) return false;
    const base = o.slice(0, -3);
    if (META.test(base)) return false;
    const foldedBase = caseFold(base, insensitive);
    const foldedInner = caseFold(i, insensitive);
    return foldedInner === foldedBase || foldedInner.startsWith(foldedBase + '/');
  };
  for (const ei of expandBracesBounded(inner)) {
    if (!expandBracesBounded(outer).some((eo) => covers1(eo, ei))) return false;
  }
  return true;
}

/** A claim on a path also owns everything under it, once that path is a directory. */
const withSubtree = (pattern) => (pattern.endsWith('/**') ? [pattern] : [pattern, `${pattern}/**`]);

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

/**
 * Resolve a user-supplied output path and prove it lands inside the workspace.
 *
 * Generated views and reports are written by whoever runs the command, from
 * paths that arrive in flags and in config. `path.resolve` alone is happy to
 * produce `/etc/anything`, and a symlinked ancestor turns an innocent-looking
 * relative path into the same thing, so both are checked.
 */
export function resolveInsideWorkspace(root, input, what = 'output path') {
  if (input === undefined || input === null || String(input).trim() === '') {
    throw new AppError('E_PATH_INVALID', `Empty ${what}.`);
  }
  const raw = String(input);
  if (raw.includes('\0')) throw new AppError('E_PATH_INVALID', `${what} contains a NUL byte.`);
  const abs = path.resolve(root, raw);
  const inside = (real, realRoot) => {
    const rel = path.relative(realRoot, real);
    return rel === '' || (!rel.startsWith('..' + path.sep) && rel !== '..' && !path.isAbsolute(rel));
  };
  if (!inside(abs, path.resolve(root))) {
    throw new AppError('E_PATH_INVALID', `${what} escapes the workspace: ${input}`, {
      hint: 'Generated views must stay inside the repository.',
    });
  }
  let realRoot;
  try { realRoot = fs.realpathSync(root); } catch { realRoot = path.resolve(root); }
  // Walk up to the deepest ancestor that exists: the file itself usually does
  // not yet, but a symlinked directory on the way there decides where it lands.
  let probe = abs;
  while (probe !== path.dirname(probe)) {
    try { fs.lstatSync(probe); break; } catch { probe = path.dirname(probe); }
  }
  let realProbe;
  try { realProbe = fs.realpathSync(probe); } catch { realProbe = probe; }
  const tail = path.relative(probe, abs);
  const realTarget = tail ? path.join(realProbe, tail) : realProbe;
  if (!inside(realTarget, realRoot)) {
    throw new AppError('E_PATH_INVALID', `${what} resolves through a symlink to outside the workspace: ${input}`, {
      hint: 'Write generated views to a real path inside the repository.',
    });
  }
  return abs;
}

/** Drop anything whose real path leaves the workspace, however it got matched. */
export function containedFiles(root, files) {
  let realRoot;
  try { realRoot = fs.realpathSync(root); } catch { realRoot = path.resolve(root); }
  const kept = [];
  const escaped = [];
  for (const f of files) {
    let real;
    try { real = fs.realpathSync(path.join(root, f)); } catch { kept.push(f); continue; }
    const rel = path.relative(realRoot, real);
    if (rel === '..' || rel.startsWith('..' + path.sep) || path.isAbsolute(rel)) escaped.push(f);
    else kept.push(f);
  }
  return { kept, escaped };
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
  // A glob can walk through a symlinked directory that the literal-prefix
  // check never saw, so the results are filtered too, not just the pattern.
  const { kept, escaped } = containedFiles(root, out);
  return {
    materialized: kept.sort(),
    materializedTruncated: truncated,
    matchers,
    materializer: listing.materializer,
    escaped,
  };
}

/** A pattern that can reach any depth from the repository root. */
export const isRootGlobal = (p) => p === '**' || p === '*' || p === '**/*' || p.startsWith('**/');

/**
 * Conservative overlap between two scopes.
 *
 * Decided on the patterns first, so a collision on a file that does not exist
 * yet is caught. Materialization is only consulted afterwards, to name the
 * files in the error message.
 */
export function scopesOverlap(a, b, { insensitive = false } = {}) {
  const fold = (s) => caseFold(s, insensitive);
  const excludedBy = (scope, pattern) => (scope.exclude || []).some((e) => patternCovers(e, pattern, insensitive));

  for (const pa of a.include) {
    for (const pb of b.include) {
      if (isRootGlobal(pa) || isRootGlobal(pb)) {
        if (excludedBy(a, pb) || excludedBy(b, pa)) continue;
        return { overlap: true, reason: 'global-pattern', paths: [pa, pb] };
      }
    }
  }
  for (const pa of a.include) {
    for (const pb of b.include) {
      // An exclude only rules a pair out when it swallows the other side
      // whole. Anything less and some path could still satisfy both.
      if (excludedBy(a, pb) || excludedBy(b, pa)) continue;
      for (const wa of withSubtree(pa)) {
        for (const wb of withSubtree(pb)) {
          if (!patternsCanIntersect(wa, wb, insensitive)) continue;
          const setB = new Set((b.materialized || []).map(fold));
          const hits = (a.materialized || []).filter((f) => setB.has(fold(f)));
          if (hits.length) return { overlap: true, reason: 'materialized-intersection', paths: hits.slice(0, 25) };
          if (fold(pa) === fold(pb)) return { overlap: true, reason: 'same-pattern', paths: [pa] };
          const la = fold(literalPrefix(pa));
          const lb = fold(literalPrefix(pb));
          if (la && lb && (isPrefixPath(la, lb) || isPrefixPath(lb, la))) {
            return { overlap: true, reason: 'literal-prefix-nesting', paths: [pa, pb] };
          }
          return { overlap: true, reason: 'pattern-intersection', paths: [pa, pb] };
        }
      }
    }
  }
  return { overlap: false };
}
