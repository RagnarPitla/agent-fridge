// SPDX-License-Identifier: Apache-2.0
import { createHash, randomBytes } from 'node:crypto';
import os from 'node:os';
import { AppError } from './errors.mjs';

const B32 = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
export const ID_RE = /^(wsp|act|ses|clm|evt|msg|que)_[0-9A-HJKMNP-TV-Z]{26}$/;

let lastMs = -1;
let lastRand = null;

function encodeTime(ms) {
  let out = '';
  for (let i = 9; i >= 0; i--) {
    const mod = ms % 32;
    out = B32[mod] + out;
    ms = (ms - mod) / 32;
  }
  return out;
}

function randomChars() {
  const bytes = randomBytes(16);
  return Array.from(bytes, (b) => B32[b % 32]);
}

function increment(chars) {
  const out = chars.slice();
  for (let i = out.length - 1; i >= 0; i--) {
    const idx = B32.indexOf(out[i]);
    if (idx < 31) {
      out[i] = B32[idx + 1];
      return out;
    }
    out[i] = B32[0];
  }
  return randomChars();
}

export function ulid(now = Date.now()) {
  if (now === lastMs && lastRand) lastRand = increment(lastRand);
  else lastRand = randomChars();
  lastMs = now;
  return encodeTime(now) + lastRand.join('');
}

export const newId = (prefix) => `${prefix}_${ulid()}`;
export const isId = (value) => typeof value === 'string' && ID_RE.test(value);

export const nowIso = (d = new Date()) => new Date(d).toISOString();
export const compactTs = (iso) => iso.replace(/[-:]/g, '').replace(/\.(\d{3})Z$/, '$1Z');

const DURATION_RE = /^(\d+)(ms|s|m|h|d)$/;
/** Numbers are milliseconds (that is how config stores them). Strings must carry a
 *  unit, because "--ttl 90" is exactly the kind of ambiguity that ruins a night. */
export function parseDuration(input, flag = 'duration') {
  if (input === undefined || input === null) return null;
  if (typeof input === 'number') {
    if (!Number.isFinite(input) || input < 0) throw new AppError('E_USAGE', `Invalid ${flag}: ${input}`);
    return Math.round(input);
  }
  const raw = String(input).trim();
  const m = DURATION_RE.exec(raw);
  if (!m) {
    throw new AppError('E_USAGE', `Invalid ${flag}: '${raw}'.`, {
      hint: /^\d+$/.test(raw) ? `Durations need a unit: ${raw}s, ${raw}m, ${raw}h.` : 'Use 500ms, 30s, 15m, 2h, or 1d.',
    });
  }
  return Number(m[1]) * { ms: 1, s: 1000, m: 60000, h: 3600000, d: 86400000 }[m[2]];
}

export function humanMs(ms) {
  if (ms < 0) return 'expired';
  const abs = Math.abs(ms);
  const s = Math.floor(abs / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rs = s % 60;
  if (m < 60) return rs ? `${m}m ${rs}s` : `${m}m`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  return rm ? `${h}h ${rm}m` : `${h}h`;
}

export const sha256 = (text) => 'sha256:' + createHash('sha256').update(text).digest('hex');
export const shortHash = (text) => createHash('sha256').update(text).digest('hex').slice(0, 12);
export const randomToken = () => randomBytes(24).toString('base64url');
export const hostId = () => sha256(os.hostname()).slice(0, 23);

export function slug(name, max = 24) {
  const s = String(name).toLowerCase().normalize('NFC').replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '');
  return (s || 'anon').slice(0, max);
}

export function processAlive(pid) {
  if (!pid || !Number.isInteger(pid)) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (e) {
    return e.code === 'EPERM';
  }
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// A blocking sleep, for the paths that cannot await: releasing the mutex runs
// from process exit and signal handlers, where the event loop will not turn
// again. Atomics.wait on a buffer nobody notifies is the only real one.
export const sleepSync = (ms) => {
  try {
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
  } catch {
    const until = Date.now() + ms;
    while (Date.now() < until) { /* SharedArrayBuffer unavailable; spin */ }
  }
};
export const jitter = (ms, ratio = 0.3) => Math.max(1, Math.round(ms * (1 + (Math.random() * 2 - 1) * ratio)));

// Deterministic JSON: sorted keys, 2-space indent, trailing newline.
export function stableStringify(value, indent = 2) {
  const seen = new WeakSet();
  const norm = (v) => {
    if (v === null || typeof v !== 'object') return v;
    if (seen.has(v)) return null;
    seen.add(v);
    if (Array.isArray(v)) return v.map(norm);
    const out = {};
    for (const k of Object.keys(v).sort()) out[k] = norm(v[k]);
    return out;
  };
  return JSON.stringify(norm(value), null, indent) + '\n';
}

export function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
