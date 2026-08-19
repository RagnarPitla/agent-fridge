// SPDX-License-Identifier: Apache-2.0
// Atomic filesystem primitives. mkdir, open(O_EXCL), rename. Nothing else.
import fs from 'node:fs';
import path from 'node:path';
import { AppError } from './errors.mjs';
import { stableStringify, ulid } from './util.mjs';

const IS_WIN = process.platform === 'win32';
const RENAME_RETRY = ['EPERM', 'EACCES', 'EBUSY'];

export const seams = {
  beforeExclusivePublish: () => {},
};

export function ensureDir(dir) {
  try {
    fs.mkdirSync(dir, { recursive: true });
  } catch (e) {
    if (e.code === 'EACCES' || e.code === 'EROFS') throw new AppError('E_PERMISSION', `Cannot create ${dir}: ${e.code}`);
    throw e;
  }
}

function sleepSync(ms) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    /* deliberate spin: sync path, sub-350ms worst case */
  }
}

function fsyncDir(dir) {
  if (IS_WIN) return;
  let fd;
  try {
    fd = fs.openSync(dir, 'r');
    fs.fsyncSync(fd);
  } catch {
    /* directory fsync is best effort */
  } finally {
    if (fd !== undefined) try { fs.closeSync(fd); } catch { /* ignore */ }
  }
}

/** Stage in tmp, fsync, rename. The only way a mutable record is ever written. */
export function writeAtomic(finalPath, text, tmpDir) {
  const dir = path.dirname(finalPath);
  ensureDir(dir);
  ensureDir(tmpDir);
  const tmp = path.join(tmpDir, `${process.pid}-${ulid()}.tmp`);
  let fd;
  try {
    fd = fs.openSync(tmp, 'wx', 0o600);
    fs.writeFileSync(fd, text);
    fs.fsyncSync(fd);
  } catch (e) {
    if (fd !== undefined) try { fs.closeSync(fd); } catch { /* ignore */ }
    try { fs.unlinkSync(tmp); } catch { /* ignore */ }
    if (e.code === 'EACCES' || e.code === 'EROFS') throw new AppError('E_PERMISSION', `Cannot write ${finalPath}: ${e.code}`);
    throw new AppError('E_STATE_CORRUPT', `Failed staging ${finalPath}: ${e.message}`);
  } finally {
    if (fd !== undefined) try { fs.closeSync(fd); } catch { /* ignore */ }
  }
  let renamed = false;
  for (let attempt = 1; attempt <= 6 && !renamed; attempt++) {
    try {
      fs.renameSync(tmp, finalPath);
      renamed = true;
    } catch (e) {
      if (IS_WIN && RENAME_RETRY.includes(e.code) && attempt < 6) {
        sleepSync(10 * 2 ** (attempt - 1));
        continue;
      }
      try { fs.unlinkSync(tmp); } catch { /* ignore */ }
      throw new AppError('E_STATE_CORRUPT', `Failed replacing ${finalPath}: ${e.code || e.message}`);
    }
  }
  fsyncDir(dir);
  return finalPath;
}

/**
 * Write-once create, with the content complete before the name exists.
 *
 * Opening the final path and then writing publishes an empty file first, and a
 * concurrent reader that catches that window sees a note with no note in it.
 * Staging in tmp and hard-linking into place makes the name appear only when
 * the bytes are already on disk, and `link` still fails with EEXIST, so this
 * keeps the exclusivity the caller relies on to detect id collisions.
 */
export function createExclusive(finalPath, text, tmpDir) {
  const dir = path.dirname(finalPath);
  ensureDir(dir);
  const staging = tmpDir || path.join(dir, '.tmp');
  ensureDir(staging);
  const tmp = path.join(staging, `${process.pid}-${ulid()}.tmp`);
  let fd;
  try {
    fd = fs.openSync(tmp, 'wx', 0o644);
    fs.writeFileSync(fd, text);
    fs.fsyncSync(fd);
  } catch (e) {
    if (fd !== undefined) try { fs.closeSync(fd); } catch { /* ignore */ }
    try { fs.unlinkSync(tmp); } catch { /* ignore */ }
    throw new AppError('E_STATE_CORRUPT', `Failed staging ${finalPath}: ${e.message}`);
  } finally {
    if (fd !== undefined) try { fs.closeSync(fd); } catch { /* ignore */ }
  }
  seams.beforeExclusivePublish(tmp, finalPath);
  try {
    fs.linkSync(tmp, finalPath);
  } catch (e) {
    if (e.code === 'EEXIST') { try { fs.unlinkSync(tmp); } catch { /* ignore */ } throw e; }
    // Filesystems without hard links (some SMB shares, FAT). Rename is still
    // atomic in content, and the caller's names carry a random id, so losing
    // link's exclusivity here costs nothing that matters.
    if (fs.existsSync(finalPath)) {
      try { fs.unlinkSync(tmp); } catch { /* ignore */ }
      const err = new Error(`EEXIST: ${finalPath}`);
      err.code = 'EEXIST';
      throw err;
    }
    fs.renameSync(tmp, finalPath);
    fsyncDir(dir);
    return finalPath;
  }
  try { fs.unlinkSync(tmp); } catch { /* ignore */ }
  fsyncDir(dir);
  return finalPath;
}

export const writeJsonAtomic = (p, obj, tmpDir) => writeAtomic(p, stableStringify(obj), tmpDir);
export const createJsonExclusive = (p, obj, tmpDir) => createExclusive(p, stableStringify(obj), tmpDir);

export function readJson(file) {
  const raw = fs.readFileSync(file, 'utf8');
  try {
    return JSON.parse(raw);
  } catch (e) {
    throw new AppError('E_STATE_CORRUPT', `Unparseable record: ${file}`, { hint: 'fridge doctor --fix' });
  }
}

export function readJsonSafe(file) {
  try {
    return { ok: true, value: readJson(file) };
  } catch (e) {
    return { ok: false, error: e.message, file };
  }
}

export function listJson(dir) {
  try {
    return fs.readdirSync(dir).filter((f) => f.endsWith('.json')).sort().map((f) => path.join(dir, f));
  } catch (e) {
    if (e.code === 'ENOENT') return [];
    throw e;
  }
}

export function walkJson(dir, out = []) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch (e) {
    if (e.code === 'ENOENT') return out;
    throw e;
  }
  for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walkJson(full, out);
    else if (entry.name.endsWith('.json')) out.push(full);
  }
  return out;
}

export const exists = (p) => fs.existsSync(p);
export const rmrf = (p) => fs.rmSync(p, { recursive: true, force: true });
export const unlinkQuiet = (p) => { try { fs.unlinkSync(p); } catch { /* ignore */ } };
