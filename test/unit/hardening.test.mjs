// SPDX-License-Identifier: Apache-2.0
// One regression per hardening fix. Each test names the way exclusive
// ownership used to be lost, and asserts it cannot be lost that way again.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { bootstrap, cleanup, fridge, notes, noteFiles } from '../helpers.mjs';
import { scopesOverlap, patternsCanIntersect, patternCovers, resolveInsideWorkspace } from '../../src/core/paths.mjs';
import { gitignoreFor } from '../../src/core/store.mjs';
import { guardSecrets, looksSecret } from '../../src/core/secrets.mjs';

const claimId = (r) => r.json?.data?.claimId;
const cards = (root, actor) => fridge(root, ['status', '--json'], { actor }).json.data.claims;

// ---------------------------------------------------------------- 1. globs

test('overlap is decided on patterns, so a file that does not exist yet is still protected', () => {
  const pairs = [
    ['*.md', 'CHANGELOG.md'],
    ['a*/x.ts', '*b/x.ts'],
    ['src/*.ts', 'src/a?.ts'],
    ['{src,docs}/**', 'docs/guide.md'],
    ['src/[ab]/x.ts', 'src/[bc]/x.ts'],
  ];
  for (const [a, b] of pairs) {
    const o = scopesOverlap({ include: [a], exclude: [] }, { include: [b], exclude: [] });
    assert.equal(o.overlap, true, `${a} and ${b} can both match a future path but were called disjoint`);
    assert.ok(o.reason, `${a} vs ${b} came back without a reason`);
  }
});

test('genuinely disjoint patterns are still allowed to proceed in parallel', () => {
  const pairs = [
    ['src/api/*.ts', 'src/api/*.js'],
    ['src/**', 'docs/**'],
    ['src/[ab]/x.ts', 'src/[cd]/x.ts'],
    ['README.md', 'src/**'],
    ['*.ts', 'src/**'],
  ];
  for (const [a, b] of pairs) {
    assert.equal(
      scopesOverlap({ include: [a], exclude: [] }, { include: [b], exclude: [] }).overlap,
      false,
      `${a} and ${b} cannot share a path but were refused`,
    );
  }
});

test('a second exclusive claim on a file nobody has created yet is refused', () => {
  const root = bootstrap('h-glob', ['alice', 'bob']);
  try {
    assert.equal(fridge(root, ['claim', '*.md', '--task', 'docs', '--json'], { actor: 'alice' }).code, 0);
    const r = fridge(root, ['claim', 'CHANGELOG.md', '--task', 'changelog', '--json'], { actor: 'bob' });
    assert.equal(r.code, 10, 'a future CHANGELOG.md would have had two owners');
    assert.equal(r.json.error.code, 'E_CONFLICT');
    assert.equal(cards(root, 'alice').length, 1);
  } finally { cleanup(root); }
});

test('brace expansion is bounded rather than allowed to explode', () => {
  const bomb = `${'{a,b}'.repeat(12)}/x.ts`;
  assert.throws(() => patternsCanIntersect(bomb, 'src/**'), /E_PATH_INVALID|alternatives/);
});

test('an exclude only cancels a pair when it swallows the other side whole', () => {
  assert.equal(patternCovers('src/vendor/**', 'src/vendor/lib/a.ts'), true);
  assert.equal(patternCovers('src/vendor/**', 'src/**'), false);
  const withExclude = scopesOverlap(
    { include: ['src/**'], exclude: ['src/vendor/**'] },
    { include: ['src/vendor/**'], exclude: [] },
  );
  assert.equal(withExclude.overlap, false, 'an exclude that covers the other side lets both proceed');
  const partial = scopesOverlap(
    { include: ['src/**'], exclude: ['src/vendor/one.ts'] },
    { include: ['src/vendor/**'], exclude: [] },
  );
  assert.equal(partial.overlap, true, 'a partial exclude must not cancel the conflict');
});

// ---------------------------------------------------------------- 2. identity

test('a mutating command never inherits the only name on the door', () => {
  const root = bootstrap('h-ident', ['alice']);
  try {
    const r = fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: '' });
    assert.equal(r.code, 7);
    assert.equal(r.json.error.code, 'E_NO_SESSION');
    assert.match(r.json.error.hint, /alice/, 'the error still says who is on the door');
    assert.equal(cards(root, 'alice').length, 0, 'nothing was claimed on somebody else behalf');
  } finally { cleanup(root); }
});

test('a read-only command may still guess the sole actor', () => {
  const root = bootstrap('h-ident-read', ['alice']);
  try {
    assert.equal(fridge(root, ['status', '--json'], { actor: '' }).code, 0);
    assert.equal(fridge(root, ['whoami', '--json'], { actor: '' }).code, 0);
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 5. corruption

test('an unreadable card blocks a new claim instead of looking like free space', () => {
  const root = bootstrap('h-corrupt', ['alice', 'bob']);
  try {
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }));
    fs.writeFileSync(path.join(root, '.fridge', 'claims', `${id}.json`), '{"schema":"wcp/0.1/claim", trunc');
    const r = fridge(root, ['claim', 'src/**', '--task', 'steal', '--json'], { actor: 'bob' });
    assert.equal(r.code, 5, 'a damaged record must fail closed');
    assert.equal(r.json.error.code, 'E_STATE_CORRUPT');
  } finally { cleanup(root); }
});

test('the board still reads, but says loudly that it is incomplete', () => {
  const root = bootstrap('h-corrupt-view', ['alice']);
  try {
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }));
    fs.writeFileSync(path.join(root, '.fridge', 'claims', `${id}.json`), 'not json at all');
    const r = fridge(root, ['status'], { actor: 'alice' });
    assert.equal(r.code, 0);
    assert.match(r.stdout, /unreadable/i, 'a corrupt card is never silently dropped from the view');
    assert.equal(fridge(root, ['doctor', '--check'], { actor: 'alice' }).code, 30);
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 6. handoffs

test('an offer that was superseded cannot be redeemed later', () => {
  const root = bootstrap('h-offer', ['alice', 'bob', 'carol']);
  try {
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }));
    const first = fridge(root, ['handoff', id, '--to', 'bob', '--note', 'yours', '--json'], { actor: 'alice' })
      .json.data.messageId;
    fridge(root, ['handoff', id, '--to', 'carol', '--note', 'actually yours', '--json'], { actor: 'alice' });
    const second = fridge(root, ['inbox', '--json'], { actor: 'carol' }).json.data.messages[0].id;
    assert.equal(fridge(root, ['accept', second, '--json'], { actor: 'carol' }).code, 0);

    // First line of defence: superseding withdrew the offer, so it is not bob's to take.
    assert.equal(fridge(root, ['accept', first, '--json'], { actor: 'bob' }).code, 11);

    // Second line: even if the envelope is still sitting in the inbox - written by
    // an older build, or restored from a backup - accepting it must be refused,
    // because the card it names no longer belongs to the agent who offered it.
    const archived = path.join(root, '.fridge', 'archive', 'messages', `${first}.json`);
    const restored = path.join(root, '.fridge', 'inbox', 'bob', `${first}.json`);
    const env = JSON.parse(fs.readFileSync(archived, 'utf8'));
    env.state = 'offered';
    fs.writeFileSync(restored, JSON.stringify(env));
    const late = fridge(root, ['accept', first, '--json'], { actor: 'bob' });
    assert.equal(late.code, 10, 'a stale offer must not move the card a second time');
    assert.equal(late.json.error.code, 'E_CONFLICT');

    const live = cards(root, 'alice');
    assert.equal(live.length, 1);
    assert.equal(live[0].actorName, 'carol', 'the card stayed with whoever legitimately accepted');
  } finally { cleanup(root); }
});

test('a withdrawn offer leaves the inbox and keeps its outcome on record', () => {
  const root = bootstrap('h-withdraw', ['alice', 'bob', 'carol']);
  try {
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }));
    const first = fridge(root, ['handoff', id, '--to', 'bob', '--json'], { actor: 'alice' }).json.data.messageId;
    fridge(root, ['handoff', id, '--to', 'carol', '--json'], { actor: 'alice' });
    assert.equal(fridge(root, ['inbox', '--json'], { actor: 'bob' }).json.data.messages.length, 0);
    const archived = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'archive', 'messages', `${first}.json`), 'utf8'));
    assert.equal(archived.state, 'withdrawn');
  } finally { cleanup(root); }
});

test('declining an old offer does not cancel the offer that is live now', () => {
  const root = bootstrap('h-decline', ['alice', 'bob', 'carol']);
  try {
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }));
    const first = fridge(root, ['handoff', id, '--to', 'bob', '--json'], { actor: 'alice' }).json.data.messageId;
    fridge(root, ['handoff', id, '--to', 'carol', '--json'], { actor: 'alice' });
    fs.writeFileSync(
      path.join(root, '.fridge', 'inbox', 'bob', `${first}.json`),
      fs.readFileSync(path.join(root, '.fridge', 'archive', 'messages', `${first}.json`), 'utf8'),
    );
    fridge(root, ['decline', first, '--reason', 'busy', '--json'], { actor: 'bob' });
    const live = fridge(root, ['inbox', '--json'], { actor: 'carol' }).json.data.messages;
    assert.equal(live.length, 1, 'carol offer survived bob decline');
    assert.equal(fridge(root, ['accept', live[0].id, '--json'], { actor: 'carol' }).code, 0);
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 4. wire format

test('force-releasing somebody else card is recorded as revoked, not released', () => {
  const root = bootstrap('h-revoke', ['alice', 'bob']);
  try {
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }));
    assert.equal(fridge(root, ['release', id, '--force', '--json'], { actor: 'bob' }).code, 0);
    const archived = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'archive', 'claims', `${id}.json`), 'utf8'));
    assert.equal(archived.state, 'revoked');
  } finally { cleanup(root); }
});

test('releasing your own card is recorded as released', () => {
  const root = bootstrap('h-release-own', ['alice']);
  try {
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }));
    fridge(root, ['release', id, '--outcome', 'done', '--json'], { actor: 'alice' });
    const archived = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'archive', 'claims', `${id}.json`), 'utf8'));
    assert.equal(archived.state, 'released');
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 7. atomicity

test('a note is never visible at its final path before it is complete', () => {
  const root = bootstrap('h-atomic', ['alice']);
  try {
    fridge(root, ['pin', 'a durable finding about the parser', '--json'], { actor: 'alice' });
    for (const f of noteFiles(root)) {
      const raw = fs.readFileSync(f, 'utf8');
      assert.notEqual(raw.length, 0, `${f} was published empty`);
      assert.doesNotThrow(() => JSON.parse(raw), `${f} was published half-written`);
    }
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 8. paths

test('a generated view cannot be written outside the workspace', () => {
  const root = bootstrap('h-escape', ['alice']);
  try {
    for (const target of ['../escaped.md', path.join(root, '..', 'escaped.md')]) {
      const r = fridge(root, ['render', '--output', target, '--json'], { actor: 'alice' });
      assert.equal(r.code, 40, `${target} was accepted`);
      assert.equal(r.json.error.code, 'E_PATH_INVALID');
    }
    assert.equal(fs.existsSync(path.join(root, '..', 'escaped.md')), false);
  } finally { cleanup(root); }
});

test('a symlinked output path is judged by where it really lands', () => {
  const root = bootstrap('h-symlink', ['alice']);
  try {
    const outside = path.join(root, '..', `h-symlink-outside-${process.pid}`);
    fs.mkdirSync(outside, { recursive: true });
    try {
      fs.symlinkSync(outside, path.join(root, 'away'), 'dir');
    } catch {
      return; // no symlink privilege on this machine
    }
    const r = fridge(root, ['render', '--output', 'away/door.md', '--json'], { actor: 'alice' });
    assert.equal(r.code, 40);
    assert.equal(fs.existsSync(path.join(outside, 'door.md')), false);
    fs.rmSync(outside, { recursive: true, force: true });
  } finally { cleanup(root); }
});

test('resolveInsideWorkspace accepts an ordinary path inside the repo', () => {
  const root = bootstrap('h-inside', ['alice']);
  try {
    assert.equal(resolveInsideWorkspace(root, 'docs/board.md', '--output'), path.join(root, 'docs', 'board.md'));
    assert.throws(() => resolveInsideWorkspace(root, '', '--output'), /E_PATH_INVALID|Empty/);
  } finally { cleanup(root); }
});

test('a simulation report cannot be written outside the workspace', () => {
  const root = bootstrap('h-report', ['alice']);
  try {
    const r = fridge(root, ['simulate', '--agents', '2', '--report', '../report.md', '--json'], { actor: 'alice' });
    assert.equal(r.code, 40);
    assert.equal(fs.existsSync(path.join(root, '..', 'report.md')), false);
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 9. secrets

test('every durable free-text field is scanned, not just the note body', () => {
  const root = bootstrap('h-secret', ['alice']);
  const token = `ghp_${'A1b2C3d4E5f6G7h8I9j0'}`;
  try {
    const cases = [
      ['claim', 'src/**', '--task', `deploy with ${token}`],
      ['pin', 'ordinary text', '--task', `deploy with ${token}`],
    ];
    for (const args of cases) {
      const r = fridge(root, [...args, '--json'], { actor: 'alice' });
      assert.equal(r.code, 2, `${args[0]} accepted a token`);
      assert.equal(r.json.error.code, 'E_USAGE');
    }
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'clean', '--json'], { actor: 'alice' }));
    const rel = fridge(root, ['release', id, '--note', `oops ${token}`, '--json'], { actor: 'alice' });
    assert.equal(rel.code, 2, 'a release note is durable too');
    assert.equal(notes(root).some((n) => JSON.stringify(n).includes(token)), false, 'no token reached the wall');
  } finally { cleanup(root); }
});

test('the escape hatch still works and names the offending flag', () => {
  const token = `ghp_${'A1b2C3d4E5f6G7h8I9j0'}`;
  assert.equal(looksSecret(token), 'a GitHub token');
  assert.equal(guardSecrets({ '--task': token }, { allow: true }), null);
  assert.throws(() => guardSecrets({ '--task': token }), (e) => e.details.field === '--task');
});

// ---------------------------------------------------------------- 10. lost updates

test('two config writes from two processes both survive', () => {
  const root = bootstrap('h-config', ['alice']);
  try {
    const a = spawnSync(process.execPath, [path.join(root, '..', '..', '..', 'bin', 'fridge.mjs')], { cwd: root });
    void a;
    fridge(root, ['config', 'lease.defaultTtlMs', '600000'], { actor: 'alice' });
    fridge(root, ['config', 'door.autoRender', 'false'], { actor: 'alice' });
    const cfg = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'config.json'), 'utf8'));
    assert.equal(cfg.lease.defaultTtlMs, 600000);
    assert.equal(cfg.door.autoRender, false);
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 11. renewal

test('renewal is centralised, so a command that never renewed before does now', () => {
  const root = bootstrap('h-renew', ['alice']);
  try {
    fridge(root, ['config', 'lease.renewThresholdRatio', '1.5'], { actor: 'alice' });
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--ttl', '30s', '--json'], { actor: 'alice' }));
    const before = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'leases', `${id}.json`), 'utf8'));
    fridge(root, ['inbox', '--json'], { actor: 'alice' });
    const after = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'leases', `${id}.json`), 'utf8'));
    assert.ok(after.renewals > before.renewals, 'inbox did not renew the lease it should have');
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 13. notes.commit

test('notes.commit false actually keeps the notes wall out of Git', () => {
  const root = bootstrap('h-notes-commit', ['alice']);
  try {
    fridge(root, ['config', 'notes.commit', 'false'], { actor: 'alice' });
    const ignore = fs.readFileSync(path.join(root, '.fridge', '.gitignore'), 'utf8');
    assert.equal(ignore.includes('!/notes/'), false, 'the ignore file still un-ignores notes');
    assert.equal(ignore, gitignoreFor({ commitNotes: false }));
    fridge(root, ['pin', 'a private note', '--json'], { actor: 'alice' });
    const rel = path.relative(root, noteFiles(root)[0]).split(path.sep).join('/');
    const checked = spawnSync('git', ['check-ignore', rel], { cwd: root, encoding: 'utf8' });
    assert.equal(checked.status, 0, `git does not ignore ${rel}`);
    assert.equal(fridge(root, ['doctor', '--check'], { actor: 'alice' }).code, 0, 'and doctor sees no drift');
  } finally { cleanup(root); }
});

test('doctor repairs an ignore file that disagrees with notes.commit', () => {
  const root = bootstrap('h-notes-drift', ['alice']);
  try {
    fs.writeFileSync(path.join(root, '.fridge', '.gitignore'), '/*\n');
    assert.equal(fridge(root, ['doctor', '--check'], { actor: 'alice' }).code, 30);
    assert.equal(fridge(root, ['doctor', '--fix'], { actor: 'alice' }).code, 0);
    assert.equal(fs.readFileSync(path.join(root, '.fridge', '.gitignore'), 'utf8'), gitignoreFor({ commitNotes: true }));
  } finally { cleanup(root); }
});

// ------------------------------------------------------- 4. lock events on record

test('breaking an abandoned lock is never silent', () => {
  const root = bootstrap('h-lock-note', ['alice']);
  try {
    // A lock nobody will ever release, aged past the stale window.
    const lockDir = path.join(root, '.fridge', 'locks', 'registry.lock.d');
    fs.mkdirSync(lockDir, { recursive: true });
    fs.writeFileSync(path.join(lockDir, 'owner.json'), JSON.stringify({
      pid: 999999, host: 'a-machine-that-is-not-this-one', op: 'claim',
      acquiredAt: new Date(Date.now() - 3600_000).toISOString(),
    }));
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'take over', '--json'], { actor: 'alice' }).code, 0);
    const broken = notes(root).filter((n) => n.type === 'lock.broken');
    assert.equal(broken.length >= 1, true, 'breaking a lock left no record');
    assert.equal(broken[0].data.why, 'owner-stale');
    assert.equal(broken[0].data.previousOwner.pid, 999999);
  } finally { cleanup(root); }
});

// ---------------------------------------------------------------- 14. one snapshot

test('the door body and its state stamp always describe the same instant', () => {
  const root = bootstrap('h-snapshot', ['alice']);
  try {
    fridge(root, ['config', 'door.autoRender', 'false'], { actor: 'alice' });
    fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' });
    fridge(root, ['render', '--json'], { actor: 'alice' });
    assert.equal(fridge(root, ['render', '--check', '--json'], { actor: 'alice' }).code, 0);
    fridge(root, ['claim', 'docs/**', '--task', 'y', '--json'], { actor: 'alice' });
    assert.equal(fridge(root, ['render', '--check', '--json'], { actor: 'alice' }).code, 30, 'drift must be visible');
    fridge(root, ['render', '--json'], { actor: 'alice' });
    const door = fs.readFileSync(path.join(root, '.fridge', 'DOOR.md'), 'utf8');
    assert.match(door, /docs\/\*\*/, 'the body caught up');
    assert.equal(fridge(root, ['render', '--check', '--json'], { actor: 'alice' }).code, 0, 'and so did the stamp');
  } finally { cleanup(root); }
});
