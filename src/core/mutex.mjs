// SPDX-License-Identifier: Apache-2.0
// The one pen on the string. mkdir is the mutex. Held for milliseconds, never for a chore.
import fs from 'node:fs';
import path from 'node:path';
import { AppError } from './errors.mjs';
import { readJsonSafe, writeJsonAtomic, rmrf, unlinkQuiet } from './fsx.mjs';
import { hostId, jitter, nowIso, processAlive, randomToken, sleep } from './util.mjs';
import { WRITER } from '../brand.mjs';

// A seam. Windows fails a stat of a directory that is pending deletion, and
// the only honest answer to a failed stat is "I cannot tell how old this lock
// is", so tests need a way to produce that failure anywhere.
export const seams = { statLock: (p) => fs.statSync(p) };

/** Identifies one specific tenancy of the lock, so a waiter can tell "the holder I judged" from "whoever holds it now". */
const ownerKey = (o) => `${o.host}|${o.pid}|${o.acquiredAt}`;

/**
 * Removes a lock that a waiter has judged to be dead.
 *
 * Breaking is the one operation that can violate mutual exclusion, because a
 * waiter that deletes a lock somebody is still holding lets a second process
 * in. Two things make it safe. Breaking is serialised behind a second lock
 * directory, so two waiters can never be in here at once: without that, two
 * waiters can both judge the same corpse, the first deletes it, a third
 * process legitimately takes the lock, and the second waiter then deletes a
 * live lock it judged in a previous era. And the evidence is re-read here,
 * under that exclusion, immediately before acting.
 *
 * The corpse is claimed by renaming it aside rather than deleted in place, so
 * the removal is one atomic step from any other process's point of view.
 */
function breakLock(lockDir, ownerFile, key, ownerOK, stale) {
  const breakDir = `${lockDir}.break`;
  try {
    fs.mkdirSync(breakDir, { recursive: false });
  } catch {
    // Somebody else is breaking, or a breaker died holding this. Breaking
    // takes microseconds, so a break lock older than the stale window is
    // unambiguously abandoned. A stat we cannot take proves nothing.
    try {
      if (Date.now() - seams.statLock(breakDir).mtimeMs > stale) rmrf(breakDir);
    } catch { /* cannot judge it, so leave it alone */ }
    return false;
  }
  try {
    const again = readJsonSafe(ownerFile);
    if (again.ok !== ownerOK) return false;
    if (again.ok && ownerKey(again.value) !== key) return false;
    if (!again.ok && !fs.existsSync(lockDir)) return false;
    const dead = `${lockDir}.dead-${randomToken().slice(0, 8)}`;
    try { fs.renameSync(lockDir, dead); } catch { return false; }
    rmrf(dead);
    return true;
  } finally {
    try { fs.rmdirSync(breakDir); } catch { rmrf(breakDir); }
  }
}

export async function withMutex(ws, op, fn, { timeoutMs, staleMs, onBreak } = {}) {
  const lockDir = ws.paths.mutex;
  const ownerFile = path.join(lockDir, 'owner.json');
  const limit = timeoutMs ?? ws.config.mutex.acquireTimeoutMs;
  const stale = staleMs ?? ws.config.mutex.staleMs;
  const deadline = Date.now() + limit;
  let delay = 5;
  let held = false;
  // What the lock looked like the first time we suspected it was dead, and
  // when we first suspected it. A lock whose owner file cannot be read yet is
  // not evidence of a dead process: it is the normal window between mkdir and
  // the owner write. We only break on a suspicion that has held steady for the
  // whole stale window.
  let suspectKey = '';
  let suspectSince = 0;

  const release = () => {
    if (!held) return;
    held = false;
    // Move the whole lock out of the way in one step, so a waiter never sees a
    // live lock directory with a missing owner file.
    try {
      const dead = `${lockDir}.rel-${randomToken().slice(0, 8)}`;
      fs.renameSync(lockDir, dead);
      rmrf(dead);
      return;
    } catch { /* fall back to taking it apart in place */ }
    unlinkQuiet(ownerFile);
    try { fs.rmdirSync(lockDir); } catch { rmrf(lockDir); }
  };
  const onExit = () => release();
  const onSignal = (sig) => { release(); process.exit(sig === 'SIGINT' ? 130 : 143); };

  while (!held) {
    try {
      fs.mkdirSync(lockDir, { recursive: false });
      held = true;
      break;
    } catch (e) {
      if (e.code !== 'EEXIST') {
        if (e.code === 'ENOENT') { fs.mkdirSync(path.dirname(lockDir), { recursive: true }); continue; }
        if (e.code === 'EACCES' || e.code === 'EROFS') throw new AppError('E_PERMISSION', `Cannot lock ${lockDir}: ${e.code}`);
        throw e;
      }
      const owner = readJsonSafe(ownerFile);
      let breakIt = false;
      let why = null;
      let key = '';
      if (!owner.ok) {
        // No readable owner. Do not guess an age from a stat we may not be
        // able to take: on Windows, stat of a directory pending deletion
        // fails, and treating that as "modified at the epoch" would break a
        // lock somebody is actively holding.
        key = 'unreadable';
        try {
          const mtime = seams.statLock(lockDir).mtimeMs;
          key = `unreadable@${mtime}`;
          if (Date.now() - mtime > stale) { breakIt = true; why = 'unreadable-owner'; }
        } catch { /* cannot judge its age, so fall back to watching it */ }
        if (!breakIt) {
          if (key !== suspectKey) { suspectKey = key; suspectSince = Date.now(); }
          else if (Date.now() - suspectSince > stale) { breakIt = true; why = 'unreadable-owner'; }
        }
      } else {
        const o = owner.value;
        const age = Date.now() - Date.parse(o.acquiredAt || 0);
        key = ownerKey(o);
        if (o.host === hostId() && !processAlive(o.pid)) { breakIt = true; why = 'owner-process-gone'; }
        else if (age > stale) { breakIt = true; why = 'owner-stale'; }
        if (suspectKey !== key) { suspectKey = key; suspectSince = Date.now(); }
      }
      if (breakIt) {
        if (breakLock(lockDir, ownerFile, key, owner.ok, stale) && onBreak) {
          await onBreak({ why, owner: owner.ok ? owner.value : null });
        }
        suspectKey = '';
        suspectSince = 0;
        continue;
      }
      if (Date.now() >= deadline) {
        throw new AppError('E_MUTEX_TIMEOUT', `Another process is holding the registry mutex (${limit}ms).`, {
          hint: 'Retry, or run: fridge doctor --fix',
        });
      }
      await sleep(jitter(Math.min(delay, 250)));
      delay = Math.min(Math.round(delay * 1.6), 250);
    }
  }

  process.on('exit', onExit);
  process.on('SIGINT', onSignal);
  process.on('SIGTERM', onSignal);
  const startedAt = Date.now();
  try {
    try {
      writeJsonAtomic(ownerFile, {
        acquiredAt: nowIso(), host: hostId(), op, pid: process.pid,
        schema: 'wcp/0.1/mutex-owner', sessionId: ws.sessionId || null, writer: WRITER,
      }, ws.paths.tmp);
    } catch (e) {
      // The usual reason this fails is that another process judged our lock
      // dead and broke it while we were still setting up. Say so, and do not
      // run fn: whatever we would have done is no longer protected.
      if (!fs.existsSync(lockDir)) {
        throw new AppError('E_MUTEX_TIMEOUT', 'Another process broke this lock while we were taking it.', {
          hint: 'Retry. If it keeps happening, raise mutex.staleMs in .fridge/config.json',
        });
      }
      throw e;
    }
    return await fn();
  } finally {
    const heldMs = Date.now() - startedAt;
    release();
    process.off('exit', onExit);
    process.off('SIGINT', onSignal);
    process.off('SIGTERM', onSignal);
    if (heldMs > ws.config.mutex.maxHoldMs) {
      process.stderr.write(`warning: held the registry mutex for ${heldMs}ms during '${op}'\n`);
    }
  }
}
