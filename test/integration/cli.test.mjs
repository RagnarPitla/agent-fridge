// SPDX-License-Identifier: Apache-2.0
// These run the real binary in real child processes: exit codes here are the
// exact contract that AGENTS.md promises to every vendor.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { bootstrap, cleanup, fridge, makeRepo, notes, readDoor } from '../helpers.mjs';

test('init creates a workspace that is git-friendly and self-describing', () => {
  const root = makeRepo('init');
  try {
    const r = fridge(root, ['init']);
    assert.equal(r.code, 0);
    const state = path.join(root, '.fridge');
    for (const f of ['VERSION', 'config.json', 'workspace.json', '.gitignore', 'DOOR.md']) {
      assert.ok(fs.existsSync(path.join(state, f)), `${f} should exist`);
    }
    assert.equal(fs.readFileSync(path.join(state, 'VERSION'), 'utf8').trim(), 'wcp/0.1');
    const ignore = fs.readFileSync(path.join(state, '.gitignore'), 'utf8');
    assert.match(ignore, /!\/notes\//, 'shared history is committed');
    assert.match(ignore, /^\/\*$/m, 'live state is ignored by default');
    assert.match(fs.readFileSync(path.join(root, 'AGENTS.md'), 'utf8'), /fridge claim/);
    assert.match(fs.readFileSync(path.join(root, '.gitattributes'), 'utf8'), /notes/);
    assert.equal(fridge(root, ['init']).code, 15, 'second init is E_ALREADY_EXISTS');
  } finally { cleanup(root); }
});

test('commands refuse to guess: no workspace, no session', () => {
  const root = makeRepo('bare');
  try {
    assert.equal(fridge(root, ['board']).code, 3, 'E_NOT_INITIALIZED');
    fridge(root, ['init', '--no-adapters']);
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'x']).code, 7, 'E_NO_SESSION');
    fridge(root, ['join', '--agent', 'alice']);
    fridge(root, ['join', '--agent', 'bob']);
    const ambiguous = fridge(root, ['claim', 'src/**', '--task', 'x'], { actor: '' });
    assert.equal(ambiguous.code, 7, 'two housemates and no --agent is an error, not a coin flip');
    assert.match(ambiguous.stderr, /--agent/);
  } finally { cleanup(root); }
});

test('the full happy path: claim, note, read the door, release', () => {
  const root = bootstrap('happy', ['alice']);
  try {
    const c = fridge(root, ['claim', 'src/api/**', '--task', 'refactor routes', '--json'], { actor: 'alice' });
    assert.equal(c.code, 0);
    assert.equal(c.json.ok, true);
    assert.equal(c.json.protocol, 'wcp/0.1');
    const id = c.json.data.claimId;
    assert.match(id, /^clm_[0-9A-HJKMNP-TV-Z]{26}$/);
    assert.equal(c.json.data.scope.materialized.length, 2, 'two files under src/api');

    assert.equal(fridge(root, ['pin', 'rewrote the retry loop'], { actor: 'alice' }).code, 0);
    assert.match(readDoor(root), /refactor routes/);
    assert.match(fridge(root, ['log', '--limit', '20'], { actor: 'alice' }).stdout, /rewrote the retry loop/);

    const rel = fridge(root, ['release', id, '--outcome', 'done', '--note', 'green', '--json'], { actor: 'alice' });
    assert.equal(rel.code, 0);
    assert.equal(rel.json.data.released.length, 1);
    assert.equal(fridge(root, ['status', '--json'], { actor: 'alice' }).json.data.claims.length, 0);
    assert.ok(fs.existsSync(path.join(root, '.fridge', 'archive', 'claims', `${id}.json`)), 'released claims are archived, not deleted');
  } finally { cleanup(root); }
});

test('exit 10 is the whole point: overlapping scopes are refused', () => {
  const root = bootstrap('conflict', ['alice', 'bob']);
  try {
    assert.equal(fridge(root, ['claim', 'src/api/**', '--task', 'a'], { actor: 'alice' }).code, 0);
    const cases = [
      ['src/api/routes.ts', 'a single file inside a claimed tree'],
      ['src/api/**', 'the identical tree'],
      ['src/**', 'a parent tree'],
      ['src/api', 'the directory itself'],
    ];
    for (const [p, why] of cases) {
      const r = fridge(root, ['claim', p, '--task', 'b', '--json'], { actor: 'bob' });
      assert.equal(r.code, 10, `should refuse ${why}`);
      assert.equal(r.json.error.code, 'E_CONFLICT');
      assert.equal(r.json.error.details.conflicts[0].actorName, 'alice');
    }
    assert.equal(fridge(root, ['claim', 'src/ui/**', '--task', 'b'], { actor: 'bob' }).code, 0, 'a sibling tree is fine');
    assert.equal(fridge(root, ['claim', 'docs/**', '--task', 'c'], { actor: 'bob' }).code, 0);
  } finally { cleanup(root); }
});

test('check and guard answer the only question an agent needs', () => {
  const root = bootstrap('check', ['alice', 'bob']);
  try {
    fridge(root, ['claim', 'src/api/**', '--task', 'a'], { actor: 'alice' });
    assert.equal(fridge(root, ['check', 'src/api/routes.ts'], { actor: 'alice' }).code, 0, 'mine');
    assert.equal(fridge(root, ['check', 'src/api/routes.ts'], { actor: 'bob' }).code, 10, 'theirs');
    assert.equal(fridge(root, ['check', 'docs/guide.md'], { actor: 'bob' }).code, 0, 'unclaimed is allowed by default');
    assert.equal(fridge(root, ['check', 'docs/guide.md', '--for-write'], { actor: 'bob' }).code, 14, 'E_OUT_OF_SCOPE under --for-write');
    const g = fridge(root, ['guard', 'src/api/db.ts', '--json'], { actor: 'bob' });
    assert.equal(g.code, 10);
    assert.equal(g.json.error.details.paths[0].status, 'theirs');
  } finally { cleanup(root); }
});

test('shared and advisory modes coexist; exclusive does not', () => {
  const root = bootstrap('modes', ['alice', 'bob']);
  try {
    fridge(root, ['claim', 'docs/**', '--task', 'read', '--mode', 'shared'], { actor: 'alice' });
    assert.equal(fridge(root, ['claim', 'docs/**', '--task', 'read too', '--mode', 'shared'], { actor: 'bob' }).code, 0);
    assert.equal(fridge(root, ['claim', 'docs/**', '--task', 'rewrite', '--mode', 'exclusive'], { actor: 'bob' }).code, 10);
    fridge(root, ['claim', 'src/ui/**', '--task', 'watching', '--mode', 'advisory'], { actor: 'alice' });
    assert.equal(fridge(root, ['claim', 'src/ui/**', '--task', 'editing', '--mode', 'exclusive'], { actor: 'bob' }).code, 0, 'advisory never blocks');
  } finally { cleanup(root); }
});

test('you cannot release, extend or hand off somebody else\'s card', () => {
  const root = bootstrap('ownership', ['alice', 'bob']);
  try {
    const id = fridge(root, ['claim', 'src/api/**', '--task', 'a', '--json'], { actor: 'alice' }).json.data.claimId;
    assert.equal(fridge(root, ['release', id], { actor: 'bob' }).code, 12, 'E_NOT_OWNER');
    assert.equal(fridge(root, ['extend', id, '--ttl', '1h'], { actor: 'bob' }).code, 12);
    assert.equal(fridge(root, ['handoff', id, '--to', 'alice'], { actor: 'bob' }).code, 12);
    assert.equal(fridge(root, ['release', id, '--force'], { actor: 'bob' }).code, 0, '--force is the documented human override');
    assert.ok(notes(root).some((n) => n.type === 'claim.released' && n.data.forced === true), 'a forced release is recorded');
  } finally { cleanup(root); }
});

test('handoff keeps the card owned at every moment', () => {
  const root = bootstrap('handoff', ['alice', 'bob']);
  try {
    const id = fridge(root, ['claim', 'src/api/**', '--task', 'a', '--json'], { actor: 'alice' }).json.data.claimId;
    const h = fridge(root, ['handoff', id, '--to', 'bob', '--note', 'tests are red', '--json'], { actor: 'alice' });
    assert.equal(h.code, 0);
    assert.equal(fridge(root, ['claim', 'src/api/**', '--task', 'sneak'], { actor: 'bob' }).code, 10, 'an offered card is still held');
    const inbox = fridge(root, ['inbox', '--json'], { actor: 'bob' });
    assert.equal(inbox.json.data.messages[0].note, 'tests are red');
    assert.equal(fridge(root, ['accept', h.json.data.messageId, '--json'], { actor: 'bob' }).code, 0);
    const after = fridge(root, ['status', '--json'], { actor: 'bob' }).json.data.claims[0];
    assert.equal(after.actorName, 'bob');
    assert.equal(fridge(root, ['release', id], { actor: 'alice' }).code, 12, 'alice no longer owns it');
    assert.equal(fridge(root, ['release', id], { actor: 'bob' }).code, 0);
  } finally { cleanup(root); }
});

test('decline leaves the card with the original owner', () => {
  const root = bootstrap('decline', ['alice', 'bob']);
  try {
    const id = fridge(root, ['claim', 'src/api/**', '--task', 'a', '--json'], { actor: 'alice' }).json.data.claimId;
    const h = fridge(root, ['handoff', id, '--to', 'bob', '--json'], { actor: 'alice' });
    assert.equal(fridge(root, ['decline', h.json.data.messageId, '--reason', 'busy'], { actor: 'bob' }).code, 0);
    assert.equal(fridge(root, ['status', '--json'], { actor: 'alice' }).json.data.claims[0].actorName, 'alice');
    assert.equal(fridge(root, ['release', id], { actor: 'alice' }).code, 0);
  } finally { cleanup(root); }
});

test('a lease that runs out is swept, and the work can be taken over', () => {
  const root = bootstrap('lease', ['alice', 'bob']);
  try {
    fridge(root, ['claim', 'src/api/**', '--task', 'slow', '--ttl', '1s'], { actor: 'alice' });
    assert.equal(fridge(root, ['claim', 'src/api/**', '--task', 'takeover'], { actor: 'bob' }).code, 10, 'still live');
    const sleep = new SharedArrayBuffer(4);
    Atomics.wait(new Int32Array(sleep), 0, 0, 1400);
    assert.equal(fridge(root, ['reap', '--json'], { actor: 'bob' }).json.data.reaped.length, 1);
    assert.equal(fridge(root, ['claim', 'src/api/**', '--task', 'takeover'], { actor: 'bob' }).code, 0);
    assert.ok(notes(root).some((n) => n.type === 'claim.expired'), 'expiry is recorded on the wall');
  } finally { cleanup(root); }
});

test('heartbeat and extend keep a long job alive', () => {
  const root = bootstrap('heartbeat', ['alice']);
  try {
    const id = fridge(root, ['claim', 'src/api/**', '--task', 'long', '--ttl', '30s', '--json'], { actor: 'alice' }).json.data.claimId;
    const before = fridge(root, ['status', '--json'], { actor: 'alice' }).json.data.claims[0].expiresAt;
    const hb = fridge(root, ['heartbeat', '--json'], { actor: 'alice' });
    assert.equal(hb.code, 0);
    assert.equal(hb.json.data.renewed[0].claimId, id);
    assert.ok(Date.parse(hb.json.data.renewed[0].expiresAt) >= Date.parse(before));
    const ex = fridge(root, ['extend', id, '--ttl', '2h', '--json'], { actor: 'alice' });
    assert.equal(ex.code, 0);
    assert.ok(Date.parse(ex.json.data.expiresAt) - Date.now() > 3600000);
  } finally { cleanup(root); }
});

test('notes are write-once: nothing an agent does can rewrite history', () => {
  const root = bootstrap('notes', ['alice', 'bob']);
  try {
    for (let i = 0; i < 10; i++) fridge(root, ['pin', `alice note ${i}`], { actor: 'alice' });
    for (let i = 0; i < 10; i++) fridge(root, ['pin', `bob note ${i}`], { actor: 'bob' });
    const all = notes(root);
    const pinned = all.filter((n) => n.type === 'note.note');
    assert.equal(pinned.length, 20);
    assert.equal(new Set(all.map((n) => n.id)).size, all.length, 'every note has a unique id');
    for (const a of ['alice', 'bob']) {
      for (let i = 0; i < 10; i++) {
        assert.equal(pinned.filter((n) => n.actorName === a && n.summary === `${a} note ${i}`).length, 1);
      }
    }
  } finally { cleanup(root); }
});

test('notes refuse to become a secret store', () => {
  const root = bootstrap('secrets', ['alice']);
  try {
    const r = fridge(root, ['pin', 'deployed with AKIAIOSFODNN7EXAMPLE'], { actor: 'alice' });
    assert.equal(r.code, 2);
    assert.match(r.stderr, /AWS access key/);
    assert.equal(fridge(root, ['pin', 'deployed with AKIAIOSFODNN7EXAMPLE', '--allow-secret-like'], { actor: 'alice' }).code, 0);
  } finally { cleanup(root); }
});

test('the door is generated, and drift is detectable', () => {
  const root = bootstrap('door', ['alice']);
  try {
    fridge(root, ['claim', 'src/**', '--task', 'work'], { actor: 'alice' });
    assert.equal(fridge(root, ['board', '--check'], { actor: 'alice' }).code, 0);
    assert.match(readDoor(root), /DO NOT EDIT/);
    fs.writeFileSync(path.join(root, '.fridge', 'DOOR.md'), '# I edited the door by hand\n');
    assert.equal(fridge(root, ['board', '--check'], { actor: 'alice' }).code, 30, 'E_DRIFT');
    assert.equal(fridge(root, ['render'], { actor: 'alice' }).code, 0);
    assert.equal(fridge(root, ['board', '--check'], { actor: 'alice' }).code, 0);
    assert.match(readDoor(root), /Claimed right now/, 'the generated view came back');
  } finally { cleanup(root); }
});

test('run claims, executes, and always tidies up', () => {
  const root = bootstrap('run', ['alice']);
  try {
    const ok = fridge(root, ['run', '--claim', 'src/**', '--task', 'tests', '--', process.execPath, '-e', 'console.log("child ran")'], { actor: 'alice' });
    assert.equal(ok.code, 0);
    assert.match(ok.stdout, /child ran/);
    assert.equal(fridge(root, ['status', '--json'], { actor: 'alice' }).json.data.claims.length, 0, 'the card came down');

    const bad = fridge(root, ['run', '--claim', 'src/**', '--task', 'tests', '--', process.execPath, '-e', 'process.exit(7)'], { actor: 'alice' });
    assert.equal(bad.code, 7, 'the child exit code is passed straight through');
    assert.equal(fridge(root, ['status', '--json'], { actor: 'alice' }).json.data.claims.length, 0);

    fridge(root, ['claim', 'src/**', '--task', 'blocker'], { actor: 'alice' });
    fridge(root, ['join', '--agent', 'bob']);
    const blocked = fridge(root, ['run', '--claim', 'src/**', '--task', 'x', '--', process.execPath, '-e', 'console.log("must not run")'], { actor: 'bob' });
    assert.equal(blocked.code, 10);
    assert.doesNotMatch(blocked.stdout, /must not run/, 'the command must not run when the claim fails');
  } finally { cleanup(root); }
});

test('wait returns as soon as the card comes down, and times out honestly', () => {
  const root = bootstrap('wait', ['alice', 'bob']);
  try {
    const id = fridge(root, ['claim', 'src/**', '--task', 'a', '--json'], { actor: 'alice' }).json.data.claimId;
    const timedOut = fridge(root, ['wait', id, '--timeout', '1s'], { actor: 'bob' });
    assert.equal(timedOut.code, 21, 'E_WAIT_TIMEOUT');
    fridge(root, ['release', id], { actor: 'alice' });
    assert.equal(fridge(root, ['wait', id, '--timeout', '5s'], { actor: 'bob' }).code, 11, 'a card that is already gone is E_NOT_FOUND');
  } finally { cleanup(root); }
});

test('paths are validated before anything is written', () => {
  const root = bootstrap('paths', ['alice']);
  try {
    for (const bad of ['../outside', '~/secrets', '.git/config', '.fridge/claims']) {
      const r = fridge(root, ['claim', bad, '--task', 'x'], { actor: 'alice' });
      assert.equal(r.code, 40, `E_PATH_INVALID for ${bad}`);
    }
    assert.equal(fridge(root, ['claim', '**', '--task', 'everything'], { actor: 'alice' }).code, 2, 'a whole-repo claim needs --confirm-global');
    assert.equal(fridge(root, ['claim', '**', '--task', 'everything', '--confirm-global'], { actor: 'alice' }).code, 0);
  } finally { cleanup(root); }
});

test('usage errors are explicit, never silent', () => {
  const root = bootstrap('usage', ['alice']);
  try {
    assert.equal(fridge(root, ['claim', 'src/**'], { actor: 'alice' }).code, 2, '--task is required by default');
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'x', '--bogus'], { actor: 'alice' }).code, 2);
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'x', '--ttl', 'soon'], { actor: 'alice' }).code, 2);
    assert.equal(fridge(root, ['claim', 'src/**', '--task', 'x', '--mode', 'maybe'], { actor: 'alice' }).code, 2);
    assert.equal(fridge(root, ['teleport'], { actor: 'alice' }).code, 2);
    assert.equal(fridge(root, ['release', 'clm_00000000000000000000000000'], { actor: 'alice' }).code, 11);
    const help = fridge(root, ['claim', '--help'], { actor: 'alice' });
    assert.equal(help.code, 0);
    assert.match(help.stdout, /exit codes:/);
    assert.match(help.stdout, /10\s+E_CONFLICT/);
  } finally { cleanup(root); }
});

test('every JSON response uses the same envelope', () => {
  const root = bootstrap('json', ['alice']);
  try {
    for (const args of [['version'], ['whoami'], ['status'], ['board'], ['log'], ['inbox'], ['doctor']]) {
      const r = fridge(root, [...args, '--json'], { actor: 'alice' });
      assert.equal(r.code, 0, `${args[0]} should succeed`);
      assert.notEqual(r.json, null, `${args[0]} must emit parseable JSON`);
      assert.deepEqual(Object.keys(r.json).sort(), ['command', 'data', 'error', 'exitCode', 'ok', 'protocol', 'ts']);
      assert.equal(r.json.ok, true);
      assert.equal(r.stdout.trimEnd().split('\n').at(-1).endsWith('}'), true, 'stdout is exactly one JSON object');
    }
    const err = fridge(root, ['claim', '../nope', '--task', 'x', '--json'], { actor: 'alice' });
    assert.equal(err.json.ok, false);
    assert.equal(err.json.exitCode, 40);
    assert.equal(err.json.error.code, 'E_PATH_INVALID');
    assert.ok(err.json.error.hint, 'errors carry a next step');
  } finally { cleanup(root); }
});

test('output is plain ASCII with no escape codes, so PowerShell stays readable', () => {
  const root = bootstrap('ascii', ['alice']);
  try {
    fridge(root, ['claim', 'src/**', '--task', 'unicode check'], { actor: 'alice' });
    for (const args of [['board'], ['status'], ['log'], ['whoami'], ['doctor'], ['version']]) {
      const r = fridge(root, args, { actor: 'alice' });
      assert.doesNotMatch(r.stdout, /\u001b\[/, `${args[0]} must not emit ANSI escapes`);
      const nonAscii = [...r.stdout].filter((ch) => ch.charCodeAt(0) > 126);
      assert.deepEqual(nonAscii, [], `${args[0]} emitted non-ASCII: ${nonAscii.join(' ')}`);
    }
  } finally { cleanup(root); }
});

test('doctor finds damage and --fix repairs it', () => {
  const root = bootstrap('doctor', ['alice']);
  try {
    fridge(root, ['claim', 'src/**', '--task', 'x'], { actor: 'alice' });
    fs.writeFileSync(path.join(root, '.fridge', 'leases', 'clm_ORPHAN.json'), '{"schema":"wcp/0.1/lease","claimId":"clm_ORPHAN"}\n');
    fs.writeFileSync(path.join(root, '.fridge', 'actors', 'broken.json'), '{ this is not json');
    fs.rmSync(path.join(root, '.fridge', '.gitignore'));
    const found = fridge(root, ['doctor', '--check', '--json'], { actor: 'alice' });
    assert.equal(found.code, 30, 'E_DRIFT');
    const ids = found.json.error.details.findings.map((f) => f.id);
    assert.ok(ids.includes('gitignore-missing'));
    assert.ok(ids.some((i) => i.startsWith('orphan-lease:')));
    assert.ok(ids.some((i) => i.startsWith('corrupt:')));
    assert.equal(fridge(root, ['doctor', '--fix'], { actor: 'alice' }).code, 0);
    assert.equal(fridge(root, ['doctor', '--check'], { actor: 'alice' }).code, 0, 'clean after --fix');
    assert.ok(fs.existsSync(path.join(root, '.fridge', '.gitignore')));
    assert.equal(fs.readdirSync(path.join(root, '.fridge', 'quarantine')).length, 1, 'corrupt records are quarantined, never deleted');
  } finally { cleanup(root); }
});

test('adapters write one canonical block into every vendor surface', () => {
  const root = bootstrap('adapters-cli', ['alice']);
  try {
    fs.writeFileSync(path.join(root, 'CLAUDE.md'), '# House rules\n\nRun the tests.\n');
    assert.equal(fridge(root, ['adapters', 'check'], { actor: 'alice' }).code, 30);
    assert.equal(fridge(root, ['adapters', 'install', '--vendor', 'all'], { actor: 'alice' }).code, 0);
    assert.equal(fridge(root, ['adapters', 'check'], { actor: 'alice' }).code, 0);
    const claude = fs.readFileSync(path.join(root, 'CLAUDE.md'), 'utf8');
    assert.match(claude, /Run the tests\./, 'existing instructions are preserved');
    assert.match(claude, /BEGIN WCP-ADAPTER/);
    for (const f of ['AGENTS.md', 'CLAUDE.md', '.github/copilot-instructions.md', '.codex/instructions.md', 'docs/AGENT-COORDINATION.md']) {
      assert.match(fs.readFileSync(path.join(root, f), 'utf8'), /fridge claim/, `${f} carries the rules`);
    }
    assert.equal(fridge(root, ['adapters', 'install', '--vendor', 'all'], { actor: 'alice' }).code, 0, 'install is idempotent');
    assert.equal(fridge(root, ['adapters', 'check'], { actor: 'alice' }).code, 0);
  } finally { cleanup(root); }
});

test('migrate imports the legacy shared Markdown files without losing a line', () => {
  const root = bootstrap('migrate', ['alice']);
  try {
    fs.writeFileSync(path.join(root, 'To-do.done.md'), '# Done\n\n- shipped the parser\n- fixed the retry loop\n');
    fs.writeFileSync(path.join(root, 'shared-development-updates.md'), '## agent-a\n\n- owns src/api\n- 128 lines rewritten\n');
    const dry = fridge(root, ['migrate', '--dry-run', '--json'], { actor: 'alice' });
    assert.equal(dry.code, 0);
    assert.equal(dry.json.data.count, 4);
    assert.equal(notes(root).filter((n) => n.type.startsWith('legacy.')).length, 0, '--dry-run writes nothing');
    assert.equal(fridge(root, ['migrate', '--freeze'], { actor: 'alice' }).code, 0);
    const imported = notes(root).filter((n) => n.type.startsWith('legacy.'));
    assert.equal(imported.length, 4);
    assert.ok(imported.some((n) => n.summary === '128 lines rewritten'));
    assert.match(fs.readFileSync(path.join(root, 'To-do.done.md'), 'utf8'), /^<!-- FROZEN/);
  } finally { cleanup(root); }
});

test('config is readable and writable, and rejects nonsense', () => {
  const root = bootstrap('config', ['alice']);
  try {
    assert.equal(fridge(root, ['config', 'lease.defaultTtlMs'], { actor: 'alice' }).stdout.trim(), '900000');
    assert.equal(fridge(root, ['config', 'policy.requireTaskOnClaim', 'false'], { actor: 'alice' }).code, 0);
    assert.equal(fridge(root, ['claim', 'src/**'], { actor: 'alice' }).code, 0, '--task is optional once policy says so');
    assert.equal(fridge(root, ['config', 'lease.defaultTtlMs', 'ages'], { actor: 'alice' }).code, 2);
    assert.equal(fridge(root, ['config', 'nope.nope'], { actor: 'alice' }).code, 11);
  } finally { cleanup(root); }
});

test('a claim above the maximum ttl is capped with a warning, not silently honoured', () => {
  const root = bootstrap('ttl-cap', ['alice']);
  try {
    const r = fridge(root, ['claim', 'src/**', '--task', 'forever', '--ttl', '7d', '--json'], { actor: 'alice' });
    assert.equal(r.code, 0);
    assert.equal(r.json.data.ttlMs, 14400000, 'capped at lease.maxTtlMs');
    assert.match(r.stderr, /capped/);
  } finally { cleanup(root); }
});

test('a workspace from a future protocol is refused, not guessed at', () => {
  const root = bootstrap('protocol', ['alice']);
  try {
    fs.writeFileSync(path.join(root, '.fridge', 'VERSION'), 'wcp/9.9\n');
    const r = fridge(root, ['board'], { actor: 'alice' });
    assert.equal(r.code, 4, 'E_PROTOCOL_VERSION');
    assert.match(r.stderr, /wcp\/9\.9/);
  } finally { cleanup(root); }
});

test('--queue puts you on the waiting list instead of just saying no', () => {
  const root = bootstrap('queue', ['alice', 'bob']);
  try {
    const held = fridge(root, ['claim', 'src/api/**', '--task', 'refactor', '--json'], { actor: 'alice' });
    assert.equal(held.code, 0);
    const claimId = held.json.data.claimId;

    const denied = fridge(root, ['claim', 'src/api/routes.ts', '--task', 'typo', '--queue', '--json'], { actor: 'bob' });
    assert.equal(denied.code, 10, 'still an honest refusal');
    assert.equal(denied.json.error.details.queued.length, 1, 'and a place in the line');

    const queueDir = path.join(root, '.fridge', 'queue');
    assert.equal(fs.readdirSync(queueDir).filter((f) => f.endsWith('.json')).length, 1);
    assert.equal(fridge(root, ['status', '--json'], { actor: 'bob' }).json.data.waiting, 1, 'the board shows the line');
    assert.equal(notes(root).filter((n) => n.type === 'queue.joined').length, 1);

    assert.equal(fridge(root, ['release', claimId, '--outcome', 'done'], { actor: 'alice' }).code, 0);
    assert.equal(fs.readdirSync(queueDir).filter((f) => f.endsWith('.json')).length, 0, 'releasing clears the line');
    assert.equal(fridge(root, ['claim', 'src/api/routes.ts', '--task', 'typo'], { actor: 'bob' }).code, 0);
  } finally { cleanup(root); }
});

test('reap --force sweeps an expired card whose owner is still running', () => {
  const root = bootstrap('reap-force', ['alice', 'bob']);
  try {
    fridge(root, ['config', 'lease.graceMs', '3600000'], { actor: 'alice' });
    const held = fridge(root, ['claim', 'src/api/**', '--task', 'slow', '--ttl', '1s', '--json'], { actor: 'alice' });
    assert.equal(held.code, 0);
    const claimId = held.json.data.claimId;

    // Pin the claim to a process that really is alive (this test), so the only
    // thing that has run out is the lease. That is the case grace exists for.
    const file = path.join(root, '.fridge', 'claims', `${claimId}.json`);
    const claim = JSON.parse(fs.readFileSync(file, 'utf8'));
    claim.process.pid = process.pid;
    fs.writeFileSync(file, JSON.stringify(claim, null, 2));
    const until = Date.now() + 1400;
    while (Date.now() < until) { /* let the lease run out for real */ }

    assert.equal(fridge(root, ['reap', '--json'], { actor: 'bob' }).json.data.reaped.length, 0, 'grace protects a live owner');
    const forced = fridge(root, ['reap', '--force', '--json'], { actor: 'bob' });
    assert.equal(forced.code, 0);
    assert.equal(forced.json.data.reaped.length, 1, 'force sweeps it anyway');
    assert.equal(notes(root).filter((n) => n.type === 'claim.expired' && n.data.forced === true).length, 1, 'and says so on the wall');
  } finally { cleanup(root); }
});

test('render --output writes a committable copy of the door', () => {
  const root = bootstrap('render-out', ['alice']);
  try {
    fridge(root, ['claim', 'docs/**', '--task', 'write the guide'], { actor: 'alice' });
    const r = fridge(root, ['render', '--output', 'TEAM-BOARD.md'], { actor: 'alice' });
    assert.equal(r.code, 0);
    const copy = fs.readFileSync(path.join(root, 'TEAM-BOARD.md'), 'utf8');
    assert.match(copy, /DO NOT EDIT/);
    assert.match(copy, /write the guide/);
  } finally { cleanup(root); }
});

test('migrate credits the original author when it can be sure who that was', () => {
  const root = bootstrap('migrate-authors', ['alice', 'copilot']);
  try {
    fs.writeFileSync(path.join(root, 'shared-development-updates.md'),
      '## updates\n\n- copilot: owns src/ui this afternoon\n- legacy-bot: rewrote the parser\n- nobody in particular wrote this line\n');
    const r = fridge(root, ['migrate', '--updates', 'shared-development-updates.md', '--author-map', 'legacy-bot=alice', '--json'], { actor: 'alice' });
    assert.equal(r.code, 0);
    const imported = notes(root).filter((n) => n.type === 'legacy.update');
    const by = (needle) => imported.find((n) => n.data.body.includes(needle));
    assert.equal(by('owns src/ui').actorName, 'copilot', 'a known actor keeps their name');
    assert.equal(by('rewrote the parser').actorName, 'alice', '--author-map is honoured');
    assert.equal(by('nobody in particular').actorName, 'alice', 'an unknown author falls back to the importer');
    assert.equal(by('nobody in particular').data.detectedAuthor, null, 'and it says it did not detect one');
  } finally { cleanup(root); }
});

test('a card taken on another machine is not force-released by accident', () => {
  const root = bootstrap('foreign', ['alice', 'bob']);
  try {
    const held = fridge(root, ['claim', 'src/api/**', '--task', 'remote work', '--json'], { actor: 'alice' });
    const claimId = held.json.data.claimId;
    const file = path.join(root, '.fridge', 'claims', `${claimId}.json`);
    const claim = JSON.parse(fs.readFileSync(file, 'utf8'));
    claim.host = 'sha256:some-other-machine';
    fs.writeFileSync(file, JSON.stringify(claim, null, 2));

    const r = fridge(root, ['release', claimId, '--force', '--outcome', 'abandoned'], { actor: 'bob' });
    assert.equal(r.code, 41, 'E_FOREIGN_HOST');
    assert.match(r.stderr, /another machine/);
    assert.equal(fridge(root, ['release', claimId, '--force', '--allow-multihost', '--outcome', 'abandoned'], { actor: 'bob' }).code, 0);
  } finally { cleanup(root); }
});
