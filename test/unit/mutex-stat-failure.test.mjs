// SPDX-License-Identifier: Apache-2.0
//
// Windows fails a stat of a directory that is pending deletion. The first
// version of this mutex answered a failed stat with "modified at the epoch",
// which made a lock somebody was actively holding look infinitely stale. A
// waiter would delete the live lock directory, a third process would then
// mkdir successfully, and two processes were inside the critical section at
// once. That is the exact failure this whole project exists to prevent, so it
// is pinned here on every platform rather than only on the one that caught it.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { withMutex, seams } from '../../src/core/mutex.mjs';
import { TMP } from '../helpers.mjs';

let n = 0;

/** The slice of a workspace the mutex actually touches. */
function fakeWs(label, { staleMs = 15000, acquireTimeoutMs = 10000 } = {}) {
  const root = path.join(TMP, `mutex-${label}-${process.pid}-${n++}`);
  const dir = path.join(root, 'registry.lock.d');
  fs.mkdirSync(path.join(root, 'tmp'), { recursive: true });
  return {
    root,
    paths: { mutex: dir, tmp: path.join(root, 'tmp') },
    config: { mutex: { acquireTimeoutMs, staleMs, maxHoldMs: 5000 } },
    sessionId: 'ses_test',
  };
}

const cleanup = (ws) => { try { fs.rmSync(ws.root, { recursive: true, force: true }); } catch { /* best effort */ } };

test('a failed stat must not look like an ancient lock', async (t) => {
  const ws = fakeWs('stat-fail', { staleMs: 5000, acquireTimeoutMs: 300 });
  t.after(() => cleanup(ws));

  const original = seams.statLock;
  seams.statLock = () => { throw new Error('simulated windows delete-pending stat failure'); };
  t.after(() => { seams.statLock = original; });

  // A holder that has taken the lock but has not written owner.json yet. This
  // is a real window in every acquisition, not a contrived state.
  fs.mkdirSync(ws.paths.mutex, { recursive: true });

  let entered = false;
  await assert.rejects(
    () => withMutex(ws, 'waiter', async () => { entered = true; }),
    (e) => e.code === 'E_MUTEX_TIMEOUT',
    'the waiter should have given up rather than break in',
  );
  assert.equal(entered, false, 'a waiter broke into a lock it could not prove was dead');
  assert.ok(fs.existsSync(ws.paths.mutex), 'the waiter deleted a live lock directory');
});

test('an unreadable lock is still broken once it is genuinely stale', async (t) => {
  const ws = fakeWs('stale-unreadable', { staleMs: 60, acquireTimeoutMs: 4000 });
  t.after(() => cleanup(ws));

  const original = seams.statLock;
  seams.statLock = () => { throw new Error('simulated stat failure'); };
  t.after(() => { seams.statLock = original; });

  fs.mkdirSync(ws.paths.mutex, { recursive: true });

  let entered = false;
  await withMutex(ws, 'waiter', async () => { entered = true; });
  assert.equal(entered, true, 'a genuinely stale lock was never broken');
});

test('breaking a stale lock still admits one holder at a time', async (t) => {
  // Long enough that a busy event loop is never mistaken for a dead process.
  // The corpse below is dated 1999, so it is stale under any window.
  const ws = fakeWs('storm', { staleMs: 5000, acquireTimeoutMs: 8000 });
  t.after(() => cleanup(ws));

  fs.mkdirSync(ws.paths.mutex, { recursive: true });
  fs.writeFileSync(path.join(ws.paths.mutex, 'owner.json'), JSON.stringify({
    acquiredAt: '1999-01-01T00:00:00.000Z', host: 'somewhere-else', op: 'abandoned',
    pid: 1, schema: 'wcp/0.1/mutex-owner', writer: 'test',
  }));

  let inside = 0;
  let maxSeen = 0;
  await Promise.all(Array.from({ length: 12 }, () => withMutex(ws, 'storm', async () => {
    inside += 1;
    maxSeen = Math.max(maxSeen, inside);
    await new Promise((r) => setTimeout(r, 2));
    inside -= 1;
  })));

  assert.equal(maxSeen, 1, `saw ${maxSeen} concurrent holders while breaking a stale lock`);
  assert.equal(fs.existsSync(ws.paths.mutex), false, 'lock directory survived the last release');
});

test('release leaves no half dismantled lock behind', async (t) => {
  const ws = fakeWs('churn');
  t.after(() => cleanup(ws));

  for (let i = 0; i < 40; i += 1) {
    await withMutex(ws, 'churn', async () => {});
  }

  assert.equal(fs.existsSync(ws.paths.mutex), false, 'lock directory survived the last release');
  const leftovers = fs.readdirSync(ws.root).filter((f) => f.startsWith('registry.lock.d.'));
  assert.deepEqual(leftovers, [], 'release left temporary lock corpses behind');
});
