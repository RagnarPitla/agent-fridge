// SPDX-License-Identifier: Apache-2.0
// A card that falls off the door must be swept safely, exactly once, even when
// several housemates notice at the same moment.
import test from 'node:test';
import assert from 'node:assert/strict';
import { bootstrap, cleanup, fridge, notes } from '../helpers.mjs';
import { race } from './race.mjs';

const wait = (ms) => Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);

test('a live card is never swept, no matter how many processes look at it', async () => {
  const names = Array.from({ length: 6 }, (_, i) => `watcher-${i}`);
  const root = bootstrap('stale-live', ['holder', ...names]);
  try {
    const id = fridge(root, ['claim', 'src/**', '--task', 'long job', '--ttl', '2h', '--json'], { actor: 'holder' }).json.data.claimId;
    const results = await race(root, 'race-claim.mjs', names.map((a) => ({ FRIDGE_ACTOR: a, RACE_TARGET: 'src/api/routes.ts' })));
    assert.equal(results.filter((r) => r.report?.code === 10).length, 6, 'everyone is refused while the card is live');
    assert.equal(fridge(root, ['status', '--json'], { actor: 'holder' }).json.data.claims[0].id, id);
  } finally { cleanup(root); }
});

test('when the lease runs out, exactly one of six racing processes takes over', async () => {
  const names = Array.from({ length: 6 }, (_, i) => `taker-${i}`);
  const root = bootstrap('stale-takeover', ['holder', ...names]);
  try {
    fridge(root, ['claim', 'src/api/**', '--task', 'went to lunch', '--ttl', '1s'], { actor: 'holder' });
    wait(1500);
    const results = await race(root, 'race-claim.mjs', names.map((a) => ({ FRIDGE_ACTOR: a, RACE_TARGET: 'src/api/**' })));
    const winners = results.filter((r) => r.report?.code === 0);
    assert.equal(winners.length, 1, `exactly one takeover expected, got ${winners.length}`);
    assert.equal(results.filter((r) => r.report?.code === 10).length, 5);

    const expired = notes(root).filter((n) => n.type === 'claim.expired');
    assert.equal(expired.length, 1, 'the expiry is recorded exactly once, not six times');
    assert.equal(expired[0].data.owner, 'holder');
    const live = fridge(root, ['status', '--json'], { actor: 'holder' }).json.data.claims;
    assert.equal(live.length, 1);
    assert.equal(live[0].actorName, winners[0].report.actor);
  } finally { cleanup(root); }
});

test('an expired owner cannot pretend nothing happened', () => {
  const root = bootstrap('stale-owner', ['holder', 'other']);
  try {
    const id = fridge(root, ['claim', 'src/**', '--task', 'slow', '--ttl', '1s', '--json'], { actor: 'holder' }).json.data.claimId;
    wait(1500);
    assert.equal(fridge(root, ['heartbeat', '--json'], { actor: 'holder' }).code, 13, 'E_LEASE_EXPIRED');
    fridge(root, ['reap'], { actor: 'other' });
    assert.equal(fridge(root, ['release', id], { actor: 'holder' }).code, 11, 'the card is gone: E_NOT_FOUND');
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'again'], { actor: 'holder' }).code, 0, 'and it can simply be claimed again');
  } finally { cleanup(root); }
});

test('heartbeats hold a card open past its original expiry', () => {
  const root = bootstrap('stale-heartbeat', ['holder', 'other']);
  try {
    fridge(root, ['claim', 'src/**', '--task', 'long', '--ttl', '2s'], { actor: 'holder' });
    for (let i = 0; i < 3; i++) {
      wait(700);
      assert.equal(fridge(root, ['heartbeat'], { actor: 'holder' }).code, 0, `heartbeat ${i} should succeed`);
    }
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'steal'], { actor: 'other' }).code, 10, 'still held after 2.1s of a 2s lease');
  } finally { cleanup(root); }
});

test('any command from the owner counts as a heartbeat', () => {
  const root = bootstrap('stale-piggyback', ['holder', 'other']);
  try {
    fridge(root, ['claim', 'src/**', '--task', 'long', '--ttl', '2s'], { actor: 'holder' });
    wait(1200);
    fridge(root, ['pin', 'still working on it'], { actor: 'holder' });
    wait(1200);
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'steal'], { actor: 'other' }).code, 10, 'the pin renewed the lease');
  } finally { cleanup(root); }
});
