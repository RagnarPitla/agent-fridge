// SPDX-License-Identifier: Apache-2.0
// Genuine OS-level contention: N separate processes reach for the same chore
// at the same agreed instant. Exactly one may win.
import test from 'node:test';
import assert from 'node:assert/strict';
import { bootstrap, cleanup, fridge, notes } from '../helpers.mjs';
import { scopesOverlap } from '../../src/core/paths.mjs';
import { race } from './race.mjs';

const actors = (n, prefix = 'agent') => Array.from({ length: n }, (_, i) => `${prefix}-${String(i).padStart(2, '0')}`);

test('eight processes, one file: exactly one winner, seven honest refusals', async () => {
  const names = actors(8);
  const root = bootstrap('race-same', names);
  try {
    const results = await race(root, 'race-claim.mjs', names.map((a) => ({ FRIDGE_ACTOR: a, RACE_TARGET: 'src/api/routes.ts' })));
    const winners = results.filter((r) => r.report?.code === 0);
    const refused = results.filter((r) => r.report?.code === 10);
    assert.equal(results.length, 8);
    assert.equal(results.every((r) => r.report !== null), true, 'every child reported: none crashed');
    assert.equal(winners.length, 1, `expected exactly one winner, got ${winners.length}`);
    assert.equal(refused.length, 7, `expected seven E_CONFLICT, got ${refused.length}`);
    assert.equal(refused.every((r) => r.report.error === 'E_CONFLICT'), true);

    const live = fridge(root, ['status', '--json'], { actor: names[0] }).json.data.claims;
    assert.equal(live.length, 1, 'the board agrees: one card');
    assert.equal(live[0].actorName, winners[0].report.actor);
    assert.equal(notes(root).filter((n) => n.type === 'claim.acquired').length, 1);
    assert.equal(notes(root).filter((n) => n.type === 'claim.denied').length, 7, 'refusals are on the wall too');
  } finally { cleanup(root); }
});

test('eight processes, overlapping globs: winners never overlap each other', async () => {
  const names = actors(8, 'glob');
  const root = bootstrap('race-glob', names);
  try {
    const targets = ['src/**', 'src/api/**', 'src/api/routes.ts', 'src/api/*.ts', 'src/**/*.ts', 'src/api', 'src/api/db.ts', 'src/**'];
    const results = await race(root, 'race-claim.mjs', names.map((a, i) => ({ FRIDGE_ACTOR: a, RACE_TARGET: targets[i] })));
    const detail = JSON.stringify(results.map((r) => r.report));
    const winners = results.filter((r) => r.report?.code === 0);
    // Two of these targets (routes.ts, db.ts) are genuinely disjoint, so the
    // honest invariant is not "one winner" - it is "no two winners collide".
    assert.equal(results.length, 8);
    assert.equal(winners.length + results.filter((r) => r.report?.code === 10).length, 8, `every child either won or was refused: ${detail}`);
    assert.ok(winners.length >= 1, `someone must make progress: ${detail}`);
    const held = winners.map((w) => targets[names.indexOf(w.report.actor)]);
    for (let i = 0; i < held.length; i += 1) {
      for (let j = i + 1; j < held.length; j += 1) {
        assert.equal(scopesOverlap({ include: [held[i]], exclude: [] }, { include: [held[j]], exclude: [] }).overlap, false, `${held[i]} and ${held[j]} were both granted but overlap: ${detail}`);
      }
    }
    const live = fridge(root, ['status', '--json'], { actor: names[0] }).json.data.claims;
    assert.equal(live.length, winners.length, 'the board shows exactly the winners');
  } finally { cleanup(root); }
});

test('eight processes, one nested chore family: exactly one winner', async () => {
  const names = actors(8, 'nest');
  const root = bootstrap('race-nested', names);
  try {
    // Every target here contains, or is contained by, every other one.
    const targets = ['src/**', 'src/api/**', 'src/api/routes.ts', 'src/api/*.ts', 'src/**/*.ts', 'src/api', 'src/api/routes.ts', 'src/**'];
    const results = await race(root, 'race-claim.mjs', names.map((a, i) => ({ FRIDGE_ACTOR: a, RACE_TARGET: targets[i] })));
    const detail = JSON.stringify(results.map((r) => r.report));
    assert.equal(results.filter((r) => r.report?.code === 0).length, 1, `a fully nested family collapses to one winner: ${detail}`);
    assert.equal(results.filter((r) => r.report?.code === 10).length, 7, `expected seven E_CONFLICT: ${detail}`);
  } finally { cleanup(root); }
});

test('eight processes, eight separate chores: nobody is blocked', async () => {
  const names = actors(8, 'wide');
  const root = bootstrap('race-disjoint', names);
  try {
    const targets = ['src/api/routes.ts', 'src/api/db.ts', 'src/ui/app.tsx', 'docs/guide.md', 'README.md', 'src/new-a.ts', 'src/new-b.ts', 'docs/new-c.md'];
    const results = await race(root, 'race-claim.mjs', names.map((a, i) => ({ FRIDGE_ACTOR: a, RACE_TARGET: targets[i] })));
    const winners = results.filter((r) => r.report?.code === 0);
    assert.equal(winners.length, 8, `all eight disjoint claims should succeed, got ${winners.length}: ${JSON.stringify(results.map((r) => r.report))}`);
    assert.equal(new Set(winners.map((r) => r.report.claimId)).size, 8, 'eight distinct cards');
    assert.equal(fridge(root, ['status', '--json'], { actor: names[0] }).json.data.claims.length, 8);
  } finally { cleanup(root); }
});

test('six processes asking for shared access all get it', async () => {
  const names = actors(6, 'reader');
  const root = bootstrap('race-shared', names);
  try {
    const results = await race(root, 'race-claim.mjs', names.map((a) => ({ FRIDGE_ACTOR: a, RACE_TARGET: 'docs/**', RACE_MODE: 'shared' })));
    assert.equal(results.filter((r) => r.report?.code === 0).length, 6, 'shared readers never block each other');
    assert.equal(fridge(root, ['claim', 'docs/**', '--task', 'rewrite'], { actor: names[0] }).code, 10, 'but a writer still waits');
  } finally { cleanup(root); }
});

test('the registry never ends up in a state the CLI cannot read', async () => {
  const names = actors(10, 'stress');
  const root = bootstrap('race-integrity', names);
  try {
    await race(root, 'race-claim.mjs', names.map((a, i) => ({ FRIDGE_ACTOR: a, RACE_TARGET: i % 2 ? 'src/api/**' : 'src/ui/**' })));
    assert.equal(fridge(root, ['doctor', '--json'], { actor: names[0] }).code, 0);
    assert.equal(fridge(root, ['board'], { actor: names[0] }).code, 0);
    const claims = fridge(root, ['status', '--json'], { actor: names[0] }).json.data.claims;
    assert.equal(claims.length, 2, 'one card per contended scope');
    assert.equal(new Set(claims.map((c) => c.actorName)).size, 2);
  } finally { cleanup(root); }
});
