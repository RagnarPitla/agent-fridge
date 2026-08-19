// SPDX-License-Identifier: Apache-2.0
// Terminals get closed. Laptops sleep. Agents are killed mid-thought.
// None of that may leave the fridge in a state a human has to repair by hand.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { REPO, bootstrap, cleanup, fridge, notes } from '../helpers.mjs';

const wait = (ms) => Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
const crash = (root, actor, env) => spawnSync(process.execPath, [path.join(REPO, 'test', 'fixtures', 'crash.mjs')], {
  cwd: root, encoding: 'utf8', env: { ...process.env, FRIDGE_ACTOR: actor, NO_COLOR: '1', ...env },
});

test('a SIGKILLed agent leaves readable state and its card expires on schedule', () => {
  const root = bootstrap('crash-basic', ['ghost', 'survivor']);
  try {
    const r = crash(root, 'ghost', { CRASH_TARGET: 'src/api/**', CRASH_TTL: '1s' });
    // Windows has no signals; Node emulates SIGKILL with TerminateProcess, so the
    // child reports a non-zero status and no signal. Either way it died mid-work
    // without releasing anything, which is the condition under test.
    const diedAbruptly = r.signal === 'SIGKILL' || (process.platform === 'win32' && r.status !== 0);
    assert.ok(diedAbruptly, `the child really was killed, not asked politely (signal=${r.signal} status=${r.status})`);
    assert.equal(fridge(root, ['board'], { actor: 'survivor' }).code, 0, 'the board still reads');
    assert.equal(fridge(root, ['claim', 'src/api/**', '--task', 'take over'], { actor: 'survivor' }).code, 10, 'still held while the lease runs');
    wait(1400);
    assert.equal(fridge(root, ['claim', 'src/api/**', '--task', 'take over'], { actor: 'survivor' }).code, 0, 'and released by time, with nobody to ask');
    assert.ok(notes(root).some((n) => n.type === 'claim.expired'), 'the fridge says what happened');
  } finally { cleanup(root); }
});

test('a lock left behind by a dead process is broken, not waited on forever', () => {
  const root = bootstrap('crash-mutex', ['ghost', 'survivor']);
  try {
    crash(root, 'ghost', { CRASH_TARGET: 'docs/**', CRASH_TTL: '1s', CRASH_MODE: 'mutex' });
    const lock = path.join(root, '.fridge', 'locks', 'registry.lock.d');
    assert.ok(fs.existsSync(lock), 'the crash left the registry lock behind');
    const started = Date.now();
    const r = fridge(root, ['claim', 'src/**', '--task', 'work anyway'], { actor: 'survivor' });
    assert.equal(r.code, 0, 'the stale lock was broken');
    assert.ok(Date.now() - started < 20000, 'and it did not take forever');
  } finally { cleanup(root); }
});

test('a torn temp file never becomes a record', () => {
  const root = bootstrap('crash-tmp', ['alice']);
  try {
    fridge(root, ['claim', 'src/**', '--task', 'x'], { actor: 'alice' });
    const tmp = path.join(root, '.fridge', 'tmp');
    fs.writeFileSync(path.join(tmp, 'claim-half-written.json.tmp'), '{"schema":"wcp/0.1/claim","id":"clm_TR');
    assert.equal(fridge(root, ['status', '--json'], { actor: 'alice' }).json.data.claims.length, 1, 'a half-written temp file is not a claim');
    assert.equal(fridge(root, ['doctor', '--json'], { actor: 'alice' }).code, 0, 'and it is not reported as corruption');
    fs.utimesSync(path.join(tmp, 'claim-half-written.json.tmp'), new Date(Date.now() - 7200000), new Date(Date.now() - 7200000));
    const d = fridge(root, ['doctor', '--json'], { actor: 'alice' });
    assert.ok(d.json.data.findings.some((f) => f.id === 'tmp-junk'), 'but old debris is reported');
    fridge(root, ['doctor', '--fix'], { actor: 'alice' });
    assert.equal(fs.readdirSync(tmp).filter((f) => f.includes('half-written')).length, 0, 'and cleaned up');
  } finally { cleanup(root); }
});

test('a corrupted record is quarantined, never silently dropped', () => {
  const root = bootstrap('crash-corrupt', ['alice']);
  try {
    const id = fridge(root, ['claim', 'src/**', '--task', 'x', '--json'], { actor: 'alice' }).json.data.claimId;
    const file = path.join(root, '.fridge', 'claims', `${id}.json`);
    fs.writeFileSync(file, '{"schema":"wcp/0.1/claim", "id": trunc');
    assert.equal(fridge(root, ['status', '--json'], { actor: 'alice' }).code, 0, 'one bad record does not break the board');
    assert.equal(fridge(root, ['doctor', '--check'], { actor: 'alice' }).code, 30);
    assert.equal(fridge(root, ['doctor', '--fix'], { actor: 'alice' }).code, 0);
    assert.equal(fs.existsSync(file), false);
    const quarantined = fs.readdirSync(path.join(root, '.fridge', 'quarantine'));
    assert.equal(quarantined.length, 1, 'the damaged bytes are kept for a human to look at');
    assert.equal(fridge(root, ['doctor', '--check'], { actor: 'alice' }).code, 0);
  } finally { cleanup(root); }
});

test('losing the whole live directory is survivable: history is still there', () => {
  const root = bootstrap('crash-nuke', ['alice']);
  try {
    fridge(root, ['claim', 'src/**', '--task', 'x'], { actor: 'alice' });
    fridge(root, ['pin', 'important finding about the parser'], { actor: 'alice' });
    const before = notes(root).length;
    fs.rmSync(path.join(root, '.fridge', 'claims'), { recursive: true, force: true });
    fs.rmSync(path.join(root, '.fridge', 'leases'), { recursive: true, force: true });
    assert.equal(fridge(root, ['status', '--json'], { actor: 'alice' }).json.data.claims.length, 0);
    assert.equal(notes(root).length, before, 'the notes wall is untouched');
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'fresh start'], { actor: 'alice' }).code, 0);
    assert.match(fridge(root, ['log'], { actor: 'alice' }).stdout, /important finding about the parser/);
  } finally { cleanup(root); }
});
