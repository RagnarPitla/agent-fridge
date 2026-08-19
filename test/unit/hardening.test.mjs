// SPDX-License-Identifier: Apache-2.0
// One regression per hardening fix. Each test names the way exclusive
// ownership used to be lost, and asserts it cannot be lost that way again.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { bootstrap, cleanup, fridge, notes, noteFiles, CLI } from '../helpers.mjs';
import { scopesOverlap, patternsCanIntersect, patternCovers, resolveInsideWorkspace } from '../../src/core/paths.mjs';
import { gitignoreFor, openWorkspace } from '../../src/core/store.mjs';
import { guardSecrets, looksSecret } from '../../src/core/secrets.mjs';
import { createExclusive, seams as fsxSeams } from '../../src/core/fsx.mjs';
import { autoRender, doorDrift, renderDoorFrom, seams as renderSeams, snapshot } from '../../src/core/render.mjs';
import { withMutex } from '../../src/core/mutex.mjs';

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
    ['[a-z].md', 'b.md'],
    ['[a-m].md', '[h-z].md'],
    ['[!a-z].md', '7.md'],
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
    ['[a-f].md', '[x-z].md'],
    ['[!a-z].md', 'b.md'],
    ['[a-z].md', '[!a-z].md'],
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

test('sole-actor inference never renews leases or creates a misspelled actor', () => {
  const root = bootstrap('h-ident-read-only', ['alice']);
  try {
    const typo = fridge(root, ['status', '--agent', 'alcie', '--json']);
    assert.equal(typo.code, 7);
    assert.equal(fs.existsSync(path.join(root, '.fridge', 'actors', 'alcie.json')), false, 'a read typo silently joined');

    const held = fridge(root, ['claim', 'src/**', '--task', 'near expiry', '--json'], { actor: 'alice' });
    const leaseFile = path.join(root, '.fridge', 'leases', `${claimId(held)}.json`);
    const lease = JSON.parse(fs.readFileSync(leaseFile, 'utf8'));
    lease.expiresAt = new Date(Date.now() + 5000).toISOString();
    lease.renewals = 0;
    fs.writeFileSync(leaseFile, JSON.stringify(lease, null, 2));
    const before = fs.readFileSync(leaseFile, 'utf8');
    assert.equal(fridge(root, ['status', '--json']).code, 0);
    assert.equal(fs.readFileSync(leaseFile, 'utf8'), before, 'inferred read-only identity renewed a lease');
  } finally { cleanup(root); }
});

test('a stale environment identity does not block identity-free administration', () => {
  const root = bootstrap('h-ident-stale-env', ['alice']);
  try {
    for (const args of [
      ['status', '--json'],
      ['board', '--json'],
      ['config', '--json'],
      ['doctor', '--check', '--json'],
    ]) {
      const result = fridge(root, args, { actor: 'departed' });
      assert.equal(result.code, 0, `${args[0]} was blocked by a stale FRIDGE_ACTOR: ${result.stdout || result.stderr}`);
    }
    const held = claimId(fridge(root, ['claim', 'src/**', '--task', 'held', '--json'], { actor: 'alice' }));
    assert.equal(fridge(root, ['reap', '--dry-run', '--json'], { actor: 'departed' }).code, 0);
    assert.equal(fridge(root, ['wait', held, '--timeout', '1ms', '--json'], { actor: 'departed' }).code, 21);
    assert.equal(fridge(root, ['reap', '--dry-run', '--agent', 'departed', '--json']).code, 7);
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

test('corrupt claims block lease and ownership mutations', () => {
  const root = bootstrap('h-corrupt-mutations', ['alice']);
  try {
    const id = claimId(fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }));
    const file = path.join(root, '.fridge', 'claims', `${id}.json`);
    fs.writeFileSync(file, '{"schema":"wcp/0.1/claim", trunc');
    for (const args of [
      ['heartbeat', '--json'],
      ['release', '--all', '--json'],
      ['reap', '--dry-run', '--json'],
    ]) {
      const result = fridge(root, args, { actor: 'alice' });
      assert.equal(result.code, 5, `${args[0]} ignored a corrupt claim`);
      assert.equal(result.json.error.code, 'E_STATE_CORRUPT');
    }
    assert.equal(fs.existsSync(path.join(root, '.fridge', 'leases', `${id}.json`)), true, 'a failed mutation changed lease state');
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
    const finalPath = path.join(root, '.fridge', 'notes', 'atomic.json');
    const complete = JSON.stringify({ schema: 'wcp/0.1/note', summary: 'complete before publish' });
    let observed = false;
    fsxSeams.beforeExclusivePublish = (tmp, final) => {
      observed = true;
      assert.equal(final, finalPath);
      assert.equal(fs.existsSync(final), false, 'the final name existed before publication');
      assert.equal(fs.readFileSync(tmp, 'utf8'), complete, 'the staged note was not complete');
    };
    createExclusive(finalPath, complete, path.join(root, '.fridge', 'tmp'));
    assert.equal(observed, true, 'the pre-publication seam was not reached');
    assert.equal(fs.readFileSync(finalPath, 'utf8'), complete);
  } finally {
    fsxSeams.beforeExclusivePublish = () => {};
    cleanup(root);
  }
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

test('door.path is confined to the workspace and drives automatic rendering', () => {
  const root = bootstrap('h-door-path', ['alice']);
  try {
    const escaped = fridge(root, ['config', 'door.path', '../escaped-door.md', '--json'], { actor: 'alice' });
    assert.equal(escaped.code, 40);
    assert.equal(escaped.json.error.code, 'E_PATH_INVALID');
    assert.match(escaped.json.error.message, /door\.path/);
    assert.doesNotMatch(escaped.json.error.message, /\[object Object\]/);

    assert.equal(fridge(root, ['config', 'door.path', 'docs/AGENT-FRIDGE.md', '--json'], { actor: 'alice' }).code, 0);
    fs.rmSync(path.join(root, '.fridge', 'DOOR.md'), { force: true });
    assert.equal(fridge(root, ['pin', 'render the configured door', '--json'], { actor: 'alice' }).code, 0);
    assert.equal(fs.existsSync(path.join(root, 'docs', 'AGENT-FRIDGE.md')), true);
    assert.equal(fs.existsSync(path.join(root, '.fridge', 'DOOR.md')), false, 'auto-render ignored door.path');
  } finally { cleanup(root); }
});

test('malformed door configuration is rejected before it can redirect a view', () => {
  const root = bootstrap('h-door-shape', ['alice']);
  const configPath = path.join(root, '.fridge', 'config.json');
  try {
    const original = JSON.parse(fs.readFileSync(configPath, 'utf8'));
    for (const door of [
      { ...original.door, path: 42 },
      { ...original.door, path: '' },
      { ...original.door, extraTargets: 'docs/door.md' },
      { ...original.door, extraTargets: ['docs/door.md', 42] },
    ]) {
      fs.writeFileSync(configPath, `${JSON.stringify({ ...original, door }, null, 2)}\n`);
      const result = fridge(root, ['status', '--json'], { actor: 'alice' });
      assert.equal(result.code, 5);
      assert.equal(result.json.error.code, 'E_STATE_CORRUPT');
    }
    fs.writeFileSync(configPath, `${JSON.stringify(original, null, 2)}\n`);
    const invalid = fridge(root, ['config', 'door.extraTargets', 'docs/door.md', '--json'], { actor: 'alice' });
    assert.equal(invalid.code, 2);
    assert.equal(invalid.json.error.code, 'E_USAGE');
    assert.equal(fridge(root, ['config', 'door.extraTargets', '["docs/door.md"]', '--json'], { actor: 'alice' }).code, 0);
    assert.deepEqual(JSON.parse(fs.readFileSync(configPath, 'utf8')).door.extraTargets, ['docs/door.md']);
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

test('migration is explicit, confined, and secret-scanned before it writes', () => {
  const root = bootstrap('h-migrate-safety', ['alice']);
  const legacy = path.join(root, 'shared-development-updates.md');
  const outside = path.join(root, '..', `outside-ledger-${process.pid}.md`);
  try {
    fs.writeFileSync(legacy, '- ghp_abcdefghijklmnopqrstuvwxyz1234567890\n');
    fs.writeFileSync(outside, '- harmless outside line\n');
    const before = notes(root).length;

    const inferredWrite = fridge(root, ['migrate', '--updates', 'shared-development-updates.md', '--json']);
    assert.equal(inferredWrite.code, 7, 'a non-dry migration inherited the sole actor');
    assert.equal(notes(root).length, before);

    const dry = fridge(root, ['migrate', '--updates', 'shared-development-updates.md', '--dry-run', '--json']);
    assert.equal(dry.code, 2, 'dry-run did not report the secret that blocks import');
    assert.equal(notes(root).length, before);

    const secret = fridge(root, ['migrate', '--updates', 'shared-development-updates.md', '--freeze', '--json'], { actor: 'alice' });
    assert.equal(secret.code, 2);
    assert.equal(secret.json.error.code, 'E_USAGE');
    assert.equal(notes(root).length, before, 'migration wrote notes before validation finished');
    assert.doesNotMatch(fs.readFileSync(legacy, 'utf8'), /^<!-- FROZEN/, 'migration froze the source before validation passed');

    const escaped = fridge(root, ['migrate', '--updates', `../${path.basename(outside)}`, '--json'], { actor: 'alice' });
    assert.equal(escaped.code, 40);
    assert.equal(escaped.json.error.code, 'E_PATH_INVALID');

    const emptyDone = path.join(root, 'To-do.done.md');
    fs.writeFileSync(emptyDone, '');
    const preview = fridge(root, [
      'migrate',
      '--updates', 'shared-development-updates.md',
      '--todo-done', 'To-do.done.md',
      '--allow-secret-like',
      '--dry-run',
      '--json',
    ], { actor: 'alice' });
    assert.equal(preview.code, 0);
    assert.deepEqual(
      preview.json.data.files.sort(),
      ['To-do.done.md', 'shared-development-updates.md'],
      'zero-entry migration sources must still appear in the report',
    );

    const allowed = fridge(root, [
      'migrate', '--updates', 'shared-development-updates.md', '--allow-secret-like', '--json',
    ], { actor: 'alice' });
    assert.equal(allowed.code, 0);
    assert.equal(notes(root).filter((n) => n.type === 'legacy.update').length, 1);
  } finally {
    fs.rmSync(outside, { force: true });
    cleanup(root);
  }
});

test('migration refuses to freeze a source that changed after preflight', async () => {
  const root = bootstrap('h-migrate-edited', ['alice']);
  const legacy = path.join(root, 'shared-development-updates.md');
  const marker = path.join(root, '.fridge', 'tmp', 'migrate-preflight-ready');
  const proceed = path.join(root, '.fridge', 'tmp', 'migrate-preflight-continue');
  try {
    fs.writeFileSync(legacy, '- original entry\n');
    const before = notes(root).length;
    const child = spawn(process.execPath, [
      CLI, 'migrate', '--updates', 'shared-development-updates.md', '--freeze', '--json',
    ], {
      cwd: root,
      env: {
        ...process.env,
        FRIDGE_ACTOR: 'alice',
        FRIDGE_TEST: '1',
        FRIDGE_FAULT: 'delay-migrate-after-preflight',
        NO_COLOR: '1',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = ''; let stderr = '';
    child.stdout.on('data', (chunk) => { stdout += chunk; });
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    const deadline = Date.now() + 5000;
    while (!fs.existsSync(marker) && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    if (!fs.existsSync(marker)) {
      child.kill();
      assert.fail(`migration never reached its preflight seam: ${stdout || stderr}`);
    }
    fs.appendFileSync(legacy, '- concurrent edit\n');
    fs.writeFileSync(proceed, 'continue\n');
    const code = await new Promise((resolve) => child.on('close', resolve));
    assert.equal(code, 10, stdout || stderr);
    const current = fs.readFileSync(legacy, 'utf8');
    assert.match(current, /concurrent edit/);
    assert.doesNotMatch(current, /^<!-- FROZEN/);
    assert.equal(notes(root).length, before, 'migration wrote notes before detecting the changed source');
  } finally { cleanup(root); }
});

test('migration rejects a source larger than its bounded mutex budget', () => {
  const root = bootstrap('h-migrate-size', ['alice']);
  try {
    const legacy = path.join(root, 'shared-development-updates.md');
    fs.writeFileSync(legacy, '');
    fs.truncateSync(legacy, 10 * 1024 * 1024 + 1);
    const result = fridge(root, ['migrate', '--updates', 'shared-development-updates.md', '--dry-run', '--json']);
    assert.equal(result.code, 2);
    assert.match(result.json.error.message, /10 MiB/);
  } finally { cleanup(root); }
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

test('run waits for an in-flight heartbeat before releasing its card', () => {
  const root = bootstrap('h-run-heartbeat', ['alice']);
  try {
    const result = fridge(root, [
      'run', '--claim', 'src/**', '--task', 'heartbeat shutdown', '--ttl', '3s',
      '--', process.execPath, '-e', 'setTimeout(() => {}, 1100)',
    ], {
      actor: 'alice',
      env: { FRIDGE_TEST: '1', FRIDGE_FAULT: 'delay-run-heartbeat' },
    });
    assert.equal(result.code, 0, result.stderr);
    assert.equal(fs.existsSync(path.join(root, '.fridge', 'tmp', 'run-heartbeat-entered')), true, 'the regression seam never ran');
    assert.deepEqual(fs.readdirSync(path.join(root, '.fridge', 'claims')).filter((f) => f.endsWith('.json')), []);
    assert.deepEqual(fs.readdirSync(path.join(root, '.fridge', 'leases')).filter((f) => f.endsWith('.json')), []);
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
    const ws = openWorkspace({ repo: root, cwd: root });
    const captured = snapshot(ws);
    fridge(root, ['claim', 'docs/**', '--task', 'y', '--json'], { actor: 'alice' });
    const oldDoor = renderDoorFrom(captured);
    assert.doesNotMatch(oldDoor, /docs\/\*\*/, 'the injected mutation leaked into the old snapshot');
    assert.equal(doorDrift(ws, oldDoor).drift, true, 'an old snapshot was certified as current');
    fridge(root, ['render', '--json'], { actor: 'alice' });
    const door = fs.readFileSync(path.join(root, '.fridge', 'DOOR.md'), 'utf8');
    assert.match(door, /docs\/\*\*/, 'the body caught up');
    assert.equal(fridge(root, ['render', '--check', '--json'], { actor: 'alice' }).code, 0, 'and so did the stamp');
  } finally { cleanup(root); }
});

test('auto-render reports failure when state never converges', () => {
  const root = bootstrap('h-render-convergence', ['alice']);
  try {
    const ws = openWorkspace({ repo: root, cwd: root });
    renderSeams.afterAutoWrite = (current, attempt) => {
      fs.writeFileSync(path.join(current.paths.queue, `render-race-${attempt}.json`), '{}\n');
    };
    assert.equal(autoRender(ws), false);
    const door = fs.readFileSync(ws.paths.door, 'utf8');
    assert.equal(doorDrift(ws, door).drift, true, 'a non-converged render claimed success');
  } finally {
    renderSeams.afterAutoWrite = () => {};
    cleanup(root);
  }
});

test('a broken old holder cannot remove its replacement lock', async () => {
  const root = bootstrap('h-replacement-lock', ['alice']);
  const marker = path.join(root, 'old-holder-ready');
  try {
    fridge(root, ['config', 'mutex.staleMs', '40'], { actor: 'alice' });
    fridge(root, ['config', 'mutex.acquireTimeoutMs', '1500'], { actor: 'alice' });
    const storeUrl = new URL('../../src/core/store.mjs', import.meta.url).href;
    const mutexUrl = new URL('../../src/core/mutex.mjs', import.meta.url).href;
    const code = `
      import fs from 'node:fs';
      import { openWorkspace } from ${JSON.stringify(storeUrl)};
      import { withMutex } from ${JSON.stringify(mutexUrl)};
      const [root, marker] = process.argv.slice(1);
      const ws = openWorkspace({ repo: root, cwd: root });
      await withMutex(ws, 'old-holder', async () => {
        fs.writeFileSync(marker, 'ready');
        await new Promise((resolve) => setTimeout(resolve, 180));
      });
    `;
    const child = spawn(process.execPath, ['--input-type=module', '-e', code, root, marker], {
      cwd: root,
      stdio: ['ignore', 'ignore', 'pipe'],
    });
    const exited = new Promise((resolve, reject) => {
      let stderr = '';
      child.stderr.on('data', (chunk) => { stderr += chunk; });
      child.on('error', reject);
      child.on('close', (exitCode) => exitCode === 0 ? resolve() : reject(new Error(stderr || `old holder exited ${exitCode}`)));
    });
    for (let i = 0; i < 100 && !fs.existsSync(marker); i += 1) {
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    assert.equal(fs.existsSync(marker), true, 'old holder never acquired the lock');

    const ws = openWorkspace({ repo: root, cwd: root });
    await withMutex(ws, 'replacement', async () => {
      await new Promise((resolve) => setTimeout(resolve, 240));
      const owner = JSON.parse(fs.readFileSync(path.join(root, '.fridge', 'locks', 'registry.lock.d', 'owner.json'), 'utf8'));
      assert.equal(owner.op, 'replacement', 'the old holder removed the replacement generation');
    });
    await exited;
  } finally { cleanup(root); }
});

test('a renewable mutex is not broken while a long bounded operation is still active', async () => {
  const root = bootstrap('h-mutex-renew', ['alice']);
  try {
    const ws = openWorkspace({ repo: root, cwd: root });
    ws.config.mutex.staleMs = 500;
    ws.config.mutex.acquireTimeoutMs = 2000;
    let firstDone = false;
    let secondEnteredEarly = false;
    let requestRefresh;
    const refreshRequested = new Promise((resolve) => { requestRefresh = resolve; });
    let markRefreshed;
    const refreshed = new Promise((resolve) => { markRefreshed = resolve; });
    let releaseFirst;
    const mayRelease = new Promise((resolve) => { releaseFirst = resolve; });
    const first = withMutex(ws, 'renewable-holder', async (refresh) => {
      await refreshRequested;
      refresh();
      markRefreshed();
      await mayRelease;
      firstDone = true;
    });
    await new Promise((resolve) => setTimeout(resolve, 600));
    requestRefresh();
    await refreshed;
    setTimeout(releaseFirst, 100);
    const second = withMutex(ws, 'contender', () => {
      secondEnteredEarly = !firstDone;
    });
    await Promise.all([first, second]);
    assert.equal(secondEnteredEarly, false, 'a live holder was replaced after staleMs despite refreshing');
  } finally { cleanup(root); }
});
