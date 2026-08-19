// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { bootstrap, cleanup, fridge } from '../helpers.mjs';
import { race } from './race.mjs';

test('concurrent config writes preserve every key', async () => {
  const root = bootstrap('race-config', ['alice']);
  try {
    const writes = [
      { RACE_KEY: 'paths.materializeLimit', RACE_VALUE: '1234' },
      { RACE_KEY: 'lease.graceMs', RACE_VALUE: '4321' },
    ];
    const results = await race(root, 'race-config.mjs', writes);
    assert.equal(results.every((r) => r.report?.code === 0), true, JSON.stringify(results));
    const config = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'config.json'), 'utf8'));
    assert.equal(config.paths.materializeLimit, 1234);
    assert.equal(config.lease.graceMs, 4321);
  } finally { cleanup(root); }
});

test('concurrent claims in one session preserve every ownership token', async () => {
  const root = bootstrap('race-session-tokens', ['alice']);
  try {
    const targets = Array.from({ length: 8 }, (_, i) => `parallel/file-${i}.ts`);
    const results = await race(root, 'race-claim.mjs', targets.map((target) => ({
      FRIDGE_ACTOR: 'alice',
      RACE_TARGET: target,
    })));
    assert.equal(results.every((r) => r.report?.code === 0), true, JSON.stringify(results.map((r) => r.report)));
    const actor = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'actors', 'alice.json'), 'utf8'));
    const session = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'sessions', `${actor.currentSessionId}.json`), 'utf8'));
    assert.equal(Object.keys(session.tokens).length, targets.length, 'a same-session read-modify-write lost a token');
    assert.deepEqual(new Set(Object.keys(session.tokens)), new Set(results.map((r) => r.report.claimId)));
    assert.equal(fridge(root, ['render', '--check', '--json'], { actor: 'alice' }).code, 0, 'auto-render left an older snapshot on the door');
  } finally { cleanup(root); }
});

test('concurrent heartbeats increment rather than overwrite renewals', async () => {
  const root = bootstrap('race-heartbeat', ['alice']);
  try {
    const claim = fridge(root, ['claim', 'src/**', '--task', 'heartbeat race', '--ttl', '30s', '--json'], { actor: 'alice' });
    assert.equal(claim.code, 0);
    const claimId = claim.json.data.claimId;
    const contenders = 8;
    const results = await race(root, 'race-heartbeat.mjs', Array.from({ length: contenders }, () => ({
      FRIDGE_ACTOR: 'alice',
      FRIDGE_NO_RENEW: '1',
    })));
    assert.equal(results.every((r) => r.report?.code === 0), true, JSON.stringify(results.map((r) => r.report)));
    const lease = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'leases', `${claimId}.json`), 'utf8'));
    assert.equal(lease.renewals, contenders, 'heartbeat writers overwrote one another');
  } finally { cleanup(root); }
});
