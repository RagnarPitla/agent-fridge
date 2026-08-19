// SPDX-License-Identifier: Apache-2.0
// The failure issue #1 was opened about, reproduced at OS level: two agents
// reach for glob patterns that share no existing file, but that could both
// match a file somebody is about to create. Before the fix both processes were
// granted an exclusive claim and the first `git add` would decide the winner.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { bootstrap, cleanup, fridge, notes } from '../helpers.mjs';
import { scopesOverlap } from '../../src/core/paths.mjs';
import { race } from './race.mjs';

const outcome = (results) => results.map((r) => r.report).sort((a, b) => String(a?.actor).localeCompare(String(b?.actor)));

// Each pair can both match at least one path that does not exist yet, so at
// most one of the two may ever hold an exclusive claim.
const ambiguousPairs = [
  { label: 'star-md-vs-changelog', targets: ['*.md', 'CHANGELOG.md'], witness: 'CHANGELOG.md' },
  { label: 'prefix-vs-suffix', targets: ['a*/x.ts', '*b/x.ts'], witness: 'ab/x.ts' },
  { label: 'star-vs-question', targets: ['src/*.ts', 'src/a?.ts'], witness: 'src/ab.ts' },
  { label: 'brace-vs-literal', targets: ['{src,docs}/**', 'docs/adr-0003.md'], witness: 'docs/adr-0003.md' },
  { label: 'overlapping-classes', targets: ['src/[ab]/x.ts', 'src/[bc]/x.ts'], witness: 'src/b/x.ts' },
];

for (const { label, targets, witness } of ambiguousPairs) {
  test(`two processes, ambiguous globs (${label}): exactly one winner`, async () => {
    const names = ['left', 'right'];
    const root = bootstrap(`race-ambiguous-${label}`, names);
    try {
      // Nothing on disk matches both patterns, so a materialization-only
      // overlap test sees two disjoint scopes and grants both.
      assert.equal(fs.existsSync(path.join(root, witness)), false, `${witness} must not exist yet`);

      const results = await race(root, 'race-claim.mjs', names.map((a, i) => ({ FRIDGE_ACTOR: a, RACE_TARGET: targets[i] })));
      const detail = JSON.stringify(outcome(results));
      assert.equal(results.every((r) => r.report !== null), true, `both children reported: ${detail}`);

      const winners = results.filter((r) => r.report.code === 0);
      const refused = results.filter((r) => r.report.code === 10);
      assert.equal(winners.length, 1, `exactly one winner for ${targets.join(' vs ')}: ${detail}`);
      assert.equal(refused.length, 1, `and exactly one honest refusal: ${detail}`);
      assert.equal(refused[0].report.error, 'E_CONFLICT');

      const live = fridge(root, ['status', '--json'], { actor: names[0] }).json.data.claims;
      assert.equal(live.length, 1, `the board agrees there is one owner: ${detail}`);
      assert.equal(live[0].actorName, winners[0].report.actor);

      // And the file they were really fighting over has exactly one owner.
      fs.mkdirSync(path.dirname(path.join(root, witness)), { recursive: true });
      fs.writeFileSync(path.join(root, witness), 'created after the race\n');
      const owners = live.filter((c) => scopesOverlap(
        { include: c.include, exclude: c.exclude },
        { include: [witness], exclude: [] },
      ).overlap);
      assert.equal(owners.length, 1, `${witness} has exactly one owner once it exists: ${detail}`);

      assert.equal(notes(root).filter((n) => n.type === 'claim.acquired').length, 1);
      assert.equal(notes(root).filter((n) => n.type === 'claim.denied').length, 1, 'the refusal is on the wall too');
    } finally { cleanup(root); }
  });
}

test('eight processes, one ambiguous glob family: exactly one winner', async () => {
  const names = Array.from({ length: 8 }, (_, i) => `amb-${String(i).padStart(2, '0')}`);
  // Every one of these can match a future `CHANGELOG.md`, so they form a single
  // conflict family even though not one of them matches a file that exists.
  const targets = ['*.md', 'CHANGELOG.md', 'CHANGELOG.*', 'C*.md', '*.m?', '{CHANGELOG,LICENSE}.md', 'CHANGELO[G].md', '**/CHANGELOG.md'];
  const root = bootstrap('race-ambiguous-family', names);
  try {
    const results = await race(root, 'race-claim.mjs', names.map((a, i) => ({ FRIDGE_ACTOR: a, RACE_TARGET: targets[i] })));
    const detail = JSON.stringify(outcome(results));
    assert.equal(results.every((r) => r.report !== null), true, `every child reported: ${detail}`);
    assert.equal(results.filter((r) => r.report.code === 0).length, 1, `exactly one winner: ${detail}`);
    assert.equal(results.filter((r) => r.report.code === 10).length, 7, `seven honest refusals: ${detail}`);
    assert.equal(fridge(root, ['status', '--json'], { actor: names[0] }).json.data.claims.length, 1);
  } finally { cleanup(root); }
});

test('genuinely disjoint globs still run in parallel under contention', async () => {
  const names = ['ts-owner', 'js-owner', 'docs-owner', 'readme-owner'];
  const targets = ['src/api/*.ts', 'src/api/*.js', 'docs/**', 'README.md'];
  const root = bootstrap('race-disjoint', names);
  try {
    const results = await race(root, 'race-claim.mjs', names.map((a, i) => ({ FRIDGE_ACTOR: a, RACE_TARGET: targets[i] })));
    const detail = JSON.stringify(outcome(results));
    assert.equal(results.filter((r) => r.report?.code === 0).length, 4, `nobody is blocked by a phantom conflict: ${detail}`);
    assert.equal(fridge(root, ['status', '--json'], { actor: names[0] }).json.data.claims.length, 4);
  } finally { cleanup(root); }
});
