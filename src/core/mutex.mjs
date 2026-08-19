// SPDX-License-Identifier: Apache-2.0
// The one pen on the string. mkdir is the mutex. Held for milliseconds, never for a chore.
import fs from 'node:fs';
import path from 'node:path';
import { AppError } from './errors.mjs';
import { readJsonSafe, writeJsonAtomic, rmrf, unlinkQuiet } from './fsx.mjs';
import { hostId, jitter, nowIso, processAlive, sleep } from './util.mjs';
import { WRITER } from '../brand.mjs';

export async function withMutex(ws, op, fn, { timeoutMs, staleMs, onBreak } = {}) {
  const lockDir = ws.paths.mutex;
  const ownerFile = path.join(lockDir, 'owner.json');
  const limit = timeoutMs ?? ws.config.mutex.acquireTimeoutMs;
  const stale = staleMs ?? ws.config.mutex.staleMs;
  const deadline = Date.now() + limit;
  let delay = 5;
  let held = false;

  const release = () => {
    if (!held) return;
    held = false;
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
      if (!owner.ok) {
        let mtime = 0;
        try { mtime = fs.statSync(lockDir).mtimeMs; } catch { mtime = 0; }
        if (Date.now() - mtime > stale) { breakIt = true; why = 'unreadable-owner'; }
      } else {
        const o = owner.value;
        const age = Date.now() - Date.parse(o.acquiredAt || 0);
        if (o.host === hostId() && !processAlive(o.pid)) { breakIt = true; why = 'owner-process-gone'; }
        else if (age > stale) { breakIt = true; why = 'owner-stale'; }
      }
      if (breakIt) {
        if (onBreak) await onBreak({ why, owner: owner.ok ? owner.value : null });
        rmrf(lockDir);
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
    writeJsonAtomic(ownerFile, {
      acquiredAt: nowIso(), host: hostId(), op, pid: process.pid,
      schema: 'wcp/0.1/mutex-owner', sessionId: ws.sessionId || null, writer: WRITER,
    }, ws.paths.tmp);
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
