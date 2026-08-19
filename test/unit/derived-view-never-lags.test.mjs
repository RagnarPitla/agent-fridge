// SPDX-License-Identifier: Apache-2.0
//
// The overview is derived, which is only a useful promise if it is derived
// *eagerly*. If a command writes a note and forgets to re-render, the door
// silently lags behind the truth and `doctor --check` starts reporting drift
// that no human caused. This test walks a workspace through every mutating
// command and asserts the derived view is consistent after each one.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { makeRepo, cleanup, fridge } from '../helpers.mjs';

test('every mutating command leaves the derived view consistent', (t) => {
  const root = makeRepo('derived');
  t.after(() => cleanup(root));

  assert.equal(fridge(root, ['init', '--no-adapters']).code, 0);
  assert.equal(fridge(root, ['doctor', '--check']).code, 0, 'a freshly initialised fridge is already tidy');

  /** Run one command, then assert the derived view kept up with it. */
  const step = (label, args, expected = 0) => {
    const r = fridge(root, args);
    assert.equal(r.code, expected, `${label}: expected exit ${expected}, got ${r.code}\n${r.stderr}`);

    const check = fridge(root, ['doctor', '--check']);
    assert.equal(
      check.code,
      0,
      `${label} left the door out of date (doctor exit ${check.code}).\n` +
      `Whatever ${label} writes, it must re-render the derived view.\n${check.stdout}${check.stderr}`,
    );
    return r;
  };

  step('join alice', ['join', '--agent', 'alice', '--vendor', 'human']);
  step('join bob', ['join', '--agent', 'bob', '--vendor', 'human']);

  const claimed = step('claim', ['claim', 'src/**', '--task', 'refactor', '--ttl', '10m', '--agent', 'alice']);
  const claimId = JSON.parse(fridge(root, ['status', '--json', '--agent', 'alice']).stdout).data.claims[0].id;
  assert.ok(claimId, `expected a claim id, got: ${claimed.stdout}`);

  step('denied claim', ['claim', 'src/api/routes.ts', '--task', 'clash', '--agent', 'bob'], 10);
  step('denied claim, queued', ['claim', 'src/api/db.ts', '--task', 'clash', '--queue', '--agent', 'bob'], 10);
  step('pin', ['pin', 'left a note on the door', '--agent', 'bob']);
  step('heartbeat', ['heartbeat', '--agent', 'alice']);
  step('extend', ['extend', claimId, '--ttl', '20m', '--agent', 'alice']);
  step('handoff', ['handoff', claimId, '--to', 'bob', '--note', 'yours now', '--agent', 'alice']);
  step('accept', ['accept', claimId, '--agent', 'bob']);
  step('release', ['release', claimId, '--outcome', 'done', '--agent', 'bob']);
  step('reap', ['reap']);
});

test('init alone leaves a workspace that passes its own health check', (t) => {
  const root = makeRepo('derived-init');
  t.after(() => cleanup(root));

  fridge(root, ['init', '--no-adapters']);
  assert.equal(fridge(root, ['doctor', '--check']).code, 0);

  // The first thing any agent does is join. That must not be enough to make a
  // brand new workspace fail its own health check.
  fridge(root, ['join', '--agent', 'solo', '--vendor', 'human']);
  const check = fridge(root, ['doctor', '--check']);
  assert.equal(check.code, 0, `join made a pristine workspace unhealthy:\n${check.stdout}${check.stderr}`);
});

test('the rendered door reflects the join that just happened', (t) => {
  const root = makeRepo('derived-content');
  t.after(() => cleanup(root));

  fridge(root, ['init', '--no-adapters']);
  fridge(root, ['join', '--agent', 'zephyr', '--vendor', 'claude']);

  const door = fs.readFileSync(path.join(root, '.fridge', 'DOOR.md'), 'utf8');
  assert.match(door, /zephyr/, 'the door should already show the actor who just joined');
});
