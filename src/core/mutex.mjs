// SPDX-License-Identifier: Apache-2.0
// The one pen on the string. mkdir is the mutex. Held for milliseconds, never for a chore.
import fs from 'node:fs';
import path from 'node:path';
import { AppError } from './errors.mjs';
import { readJsonSafe, writeJsonAtomic, rmrf, unlinkQuiet } from './fsx.mjs';
import { hostId, jitter, nowIso, processAlive, randomToken, sleep, sleepSync } from './util.mjs';
import { WRITER } from '../brand.mjs';
import { pin } from './store.mjs';

// Breaking a lock, and holding one too long, are both safety-relevant events
// that belong on the wall rather than in a terminal somebody has closed. A
// failure to record one must never take down the operation that caused it:
// the note is evidence, not a step in the protocol.
function recordLockEvent(ws, type, summary, data) {
  try { pin(ws, { type, actor: null, session: null, subject: 'registry.lock.d', summary, data }); }
  catch { /* evidence is best effort; the lock decision already stands */ }
}

// A seam. Windows fails a stat of a directory that is pending deletion, and
// the only honest answer to a failed stat is "I cannot tell how old this lock
// is", so tests need a way to produce that failure anywhere.
export const seams = {
  statLock: (p) => fs.statSync(p),

  // One attempt to take the lock directory away, reporting whether it is gone.
  //
  // A seam for the same reason. On Windows every operation here fails while
  // another process has owner.json open, and waiters open it constantly:
  // rename hits a sharing violation, the unlink hits a sharing violation, and
  // the rmdir then fails because the directory is not empty.
  //
  // Renaming aside is tried first because it is atomic: no other process ever
  // sees a lock directory whose owner file has gone missing.
  dropLockOnce: (lockDir) => {
    try {
      const dead = `${lockDir}.rel-${randomToken().slice(0, 8)}`;
      fs.renameSync(lockDir, dead);
      rmrf(dead);
      return true;
    } catch { /* blocked, or already gone */ }
    return !fs.existsSync(lockDir);
  },

  // The last resort when renaming aside keeps failing. Blocked by the same
  // sharing violation, so a test that only models the rename is not modelling
  // Windows. This does briefly leave a lock directory with no owner file,
  // which is safe but costs any waiter a full stale window.
  dismantleLockInPlace: (lockDir, ownerFile) => {
    unlinkQuiet(ownerFile);
    try { fs.rmdirSync(lockDir); return true; } catch { /* not empty, or blocked */ }
    rmrf(lockDir);
    return !fs.existsSync(lockDir);
  },
};

// How long a release keeps trying. A release that gives up leaves a lock that
// nobody holds and nobody can take, which stops the whole workspace until the
// stale window expires, so it is worth being patient. In practice the handle
// that blocks it is open for microseconds.
const RELEASE_TIMEOUT_MS = 2000;

/** Identifies one specific tenancy of the lock, so a waiter can tell "the holder I judged" from "whoever holds it now". */
const ownerKey = (o) => `${o.host}|${o.pid}|${o.acquiredAt}|${o.nonce || ''}`;

/**
 * Serialises every removal of the lock directory behind a second directory.
 *
 * Both breaking somebody else's lock and releasing our own are check-then-act:
 * read the owner file, decide it is removable, remove it. Between the check
 * and the act the lock can change hands, and removing a lock that has changed
 * hands admits a second holder. Holding this while doing both closes that
 * window, because a lock can only change hands by first being removed.
 */
function withBreakLock(lockDir, stale, fn) {
  const breakDir = `${lockDir}.break`;
  try {
    fs.mkdirSync(breakDir, { recursive: false });
  } catch {
    // Somebody else is removing, or a remover died holding this. Removal
    // takes microseconds, so a break lock older than the stale window is
    // unambiguously abandoned. A stat we cannot take proves nothing.
    try {
      if (Date.now() - seams.statLock(breakDir).mtimeMs > stale) rmrf(breakDir);
    } catch { /* cannot judge it, so leave it alone */ }
    return { entered: false, value: undefined };
  }
  try {
    return { entered: true, value: fn() };
  } finally {
    try { fs.rmdirSync(breakDir); } catch { rmrf(breakDir); }
  }
}

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
  return withBreakLock(lockDir, stale, () => {
    const again = readJsonSafe(ownerFile);
    if (again.ok !== ownerOK) return false;
    if (again.ok && ownerKey(again.value) !== key) return false;
    if (!again.ok && !fs.existsSync(lockDir)) return false;
    const dead = `${lockDir}.dead-${randomToken().slice(0, 8)}`;
    try { fs.renameSync(lockDir, dead); } catch { return false; }
    rmrf(dead);
    return true;
  }).value === true;
}

export async function withMutex(ws, op, fn, { timeoutMs, staleMs, onBreak } = {}) {
  const lockDir = ws.paths.mutex;
  const ownerFile = path.join(lockDir, 'owner.json');
  const limit = timeoutMs ?? ws.config.mutex.acquireTimeoutMs;
  const stale = staleMs ?? ws.config.mutex.staleMs;
  const deadline = Date.now() + limit;
  let delay = 5;
  let held = false;
  // Our fencing token. It is written into owner.json as part of taking the
  // lock, and checked again before we remove it. A holder that was judged dead
  // and broken finds somebody else's nonce and keeps its hands off, so a
  // replacement lock is never removed by the process it replaced.
  const nonce = randomToken();
  // What the lock looked like the first time we suspected it was dead, and
  // when we first suspected it. A lock whose owner file cannot be read yet is
  // not evidence of a dead process: it is the normal window between mkdir and
  // the owner write. We only break on a suspicion that has held steady for the
  // whole stale window.
  let suspectKey = '';
  let suspectSince = 0;

  const dropIfStillOurs = () => {
    const o = readJsonSafe(ownerFile);
    // Not provably ours. Either we were broken and somebody else holds it, or
    // a replacement is mid-acquire. Removing it in either case admits a second
    // holder, so leave it: stale detection reclaims a genuine orphan.
    if (!o.ok) return !fs.existsSync(lockDir);
    if (o.value.nonce !== nonce) return true;
    if (seams.dropLockOnce(lockDir, ownerFile)) return true;
    return seams.dismantleLockInPlace(lockDir, ownerFile);
  };

  const release = () => {
    if (!held) return;
    held = false;
    // Keep trying. On Windows any of these operations fails while a waiter has
    // owner.json open, and that failure clears in microseconds, but a
    // single-shot release turns it into a lock nobody holds and nobody can
    // take. Busy-waiting here is deliberate: release runs from process exit
    // and signal handlers, where nothing can be awaited.
    const until = Date.now() + RELEASE_TIMEOUT_MS;
    for (;;) {
      const attempt = withBreakLock(lockDir, stale, dropIfStillOurs);
      if (attempt.entered && attempt.value) return;
      if (Date.now() >= until) return;
      sleepSync(2);
    }
  };
  const onExit = () => release();
  const onSignal = (sig) => { release(); process.exit(sig === 'SIGINT' ? 130 : 143); };

  while (!held) {
    try {
      fs.mkdirSync(lockDir, { recursive: false });
      // Stamp identity immediately. Until the nonce is on disk a release
      // cannot prove the lock is ours, so this happens here rather than after
      // the handlers are installed, and a failure is cleaned up on the spot
      // while no other process can yet have judged this lock stale.
      try {
        writeJsonAtomic(ownerFile, {
          acquiredAt: nowIso(), host: hostId(), nonce, op, pid: process.pid,
          schema: 'wcp/0.1/mutex-owner', sessionId: ws.sessionId || null, writer: WRITER,
        }, ws.paths.tmp);
      } catch (e) {
        seams.dropLockOnce(lockDir, ownerFile) || seams.dismantleLockInPlace(lockDir, ownerFile);
        throw new AppError('E_STATE_CORRUPT', `Could not stamp the registry lock: ${e.code || e.message}`, {
          hint: 'Check permissions on .fridge/locks and .fridge/tmp.',
        });
      }
      held = true;
      break;
    } catch (e) {
      if (e instanceof AppError) throw e;
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
        if (breakLock(lockDir, ownerFile, key, owner.ok, stale)) {
          const evidence = owner.ok ? owner.value : null;
          recordLockEvent(ws, 'lock.broken', `broke an abandoned registry lock (${why})`, {
            why, op, previousOwner: evidence && { pid: evidence.pid ?? null, host: evidence.host ?? null, op: evidence.op ?? null, acquiredAt: evidence.acquiredAt ?? null },
          });
          if (onBreak) await onBreak({ why, owner: evidence });
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
    // Between the stamp and here, a waiter that judged us stale could have
    // broken the lock. Detect that instead of running fn unprotected.
    const mine = readJsonSafe(ownerFile);
    if (!mine.ok || mine.value.nonce !== nonce) {
      held = false;
      throw new AppError('E_MUTEX_TIMEOUT', 'Another process broke this lock while we were taking it.', {
        hint: 'Retry. If it keeps happening, raise mutex.staleMs in .fridge/config.json',
      });
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
      recordLockEvent(ws, 'lock.slow', `held the registry mutex for ${heldMs}ms during '${op}'`, {
        op, heldMs, maxHoldMs: ws.config.mutex.maxHoldMs,
      });
    }
  }
}
