// SPDX-License-Identifier: Apache-2.0
//
// Windows fails a rename, an unlink and an rmdir of a lock directory while
// another process has owner.json open, and waiters open it constantly to read
// who is holding the lock. The failure clears in microseconds.
//
// A release that tried once and gave up turned that microsecond into a
// permanent outage: the lock directory survived a release nobody was holding,
// every later acquirer sat there until its timeout, and the workspace was
// stuck until the stale window expired. In CI it cost one goroutine in sixteen
// to strand the other fifteen.
//
// So a release retries. Pinned here on every platform rather than only on the
// one that caught it.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { withMutex, seams } from '../../src/core/mutex.mjs';
import { TMP } from '../helpers.mjs';

let n = 0;

function fakeWs(label, { staleMs = 600000, acquireTimeoutMs = 30000 } = {}) {
  const root = path.join(TMP, `mutex-rel-${label}-${process.pid}-${n++}`);
  fs.mkdirSync(path.join(root, 'tmp'), { recursive: true });
  return {
    root,
    paths: { mutex: path.join(root, 'registry.lock.d'), tmp: path.join(root, 'tmp') },
    config: { mutex: { acquireTimeoutMs, staleMs, maxHoldMs: 5000 } },
    sessionId: 'ses_test',
  };
}

const cleanup = (ws) => { try { fs.rmSync(ws.root, { recursive: true, force: true }); } catch { /* best effort */ } };

/**
 * Blocks every way of removing the lock for a while, the way one sharing
 * violation would. Both paths go together: a test that only blocks the rename
 * is not modelling Windows, because the in-place fallback then succeeds.
 */
function blockRemovalFor(t, ms) {
  const originals = { drop: seams.dropLockOnce, dismantle: seams.dismantleLockInPlace };
  const until = Date.now() + ms;
  const stats = { attempts: 0 };
  seams.dropLockOnce = (...a) => {
    stats.attempts += 1;
    return Date.now() < until ? false : originals.drop(...a);
  };
  seams.dismantleLockInPlace = (...a) => (Date.now() < until ? false : originals.dismantle(...a));
  t.after(() => {
    seams.dropLockOnce = originals.drop;
    seams.dismantleLockInPlace = originals.dismantle;
  });
  return stats;
}

test('a release keeps trying when the filesystem says no', async (t) => {
  const ws = fakeWs('retry');
  t.after(() => cleanup(ws));
  const stats = blockRemovalFor(t, 200);

  await withMutex(ws, 'holder', async () => {});

  assert.equal(fs.existsSync(ws.paths.mutex), false, 'the lock survived a release that was only transiently blocked');
  assert.ok(stats.attempts >= 2, `release gave up after ${stats.attempts} attempt(s); it must retry`);
});

// The point of retrying is that the next acquirer does not have to wait out
// the stale window. staleMs is far beyond this test's patience on purpose: if
// it passes by breaking a stale lock rather than by a clean release, it fails.
test('a blocked release does not stall the next acquirer', async (t) => {
  const ws = fakeWs('next');
  t.after(() => cleanup(ws));
  blockRemovalFor(t, 200);

  await withMutex(ws, 'holder', async () => {});
  let entered = false;
  await withMutex(ws, 'next', async () => { entered = true; });
  assert.ok(entered, 'the next acquirer could not get in after a transiently blocked release');
});

// Giving up is allowed. Giving up quietly and leaving a lock that stale
// detection cannot reclaim is not.
test('a release that cannot succeed leaves a reclaimable lock', async (t) => {
  const ws = fakeWs('corpse', { acquireTimeoutMs: 30000 });
  t.after(() => cleanup(ws));

  const originals = { drop: seams.dropLockOnce, dismantle: seams.dismantleLockInPlace };
  seams.dropLockOnce = () => false;
  seams.dismantleLockInPlace = () => false;
  t.after(() => {
    seams.dropLockOnce = originals.drop;
    seams.dismantleLockInPlace = originals.dismantle;
  });

  await withMutex(ws, 'holder', async () => {});
  assert.equal(fs.existsSync(ws.paths.mutex), true, 'this test is meaningless if the lock did get removed');
  seams.dropLockOnce = originals.drop;
  seams.dismantleLockInPlace = originals.dismantle;

  await new Promise((r) => { setTimeout(r, 120); });
  let entered = false;
  await withMutex(ws, 'next', async () => { entered = true; }, { staleMs: 100 });
  assert.ok(entered, 'an abandoned lock was not reclaimable');
});
