// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { BODY, bodyHash, block, install, splice, statusFor, VENDORS } from '../../src/adapters/templates.mjs';
import { makeRepo, cleanup } from '../helpers.mjs';

test('every vendor points at a real, distinct instruction file', () => {
  const files = Object.values(VENDORS).map((v) => v.file);
  assert.equal(new Set(files).size, files.length);
  assert.ok(files.includes('AGENTS.md'));
  assert.ok(files.includes('CLAUDE.md'));
  assert.ok(files.includes(path.join('.github', 'copilot-instructions.md')));
});

test('the rule text is ASCII only, so no vendor renders weird symbols', () => {
  const offenders = [...BODY].filter((ch) => ch.charCodeAt(0) > 126);
  assert.deepEqual(offenders, [], `non-ASCII characters found: ${offenders.join(' ')}`);
});

test('the rule text stays short enough that agents actually follow it', () => {
  assert.ok(BODY.split('\n').length < 45, 'keep the canonical block under 45 lines');
});

test('the block names the exit code that matters', () => {
  assert.match(BODY, /exits \*\*10\*\*/);
  assert.match(BODY, /fridge claim/);
});

test('splice inserts once and updates in place afterwards', () => {
  const doc = '# My rules\n\nAlways run the tests.\n';
  const once = splice(doc, block());
  assert.ok(once.startsWith('# My rules'), 'existing content is preserved');
  assert.ok(once.includes('Always run the tests.'));
  const twice = splice(once, block());
  assert.equal(twice, once, 'splicing the same block twice is a no-op');
  const changed = splice(once, block().replace(bodyHash(), 'deadbeef0000'));
  assert.ok(changed.startsWith('# My rules'));
  assert.equal(changed.match(/BEGIN WCP-ADAPTER/g).length, 1, 'never duplicates the block');
});

test('a half-deleted block is an explicit error, not a silent mess', () => {
  const broken = `intro\n<!-- BEGIN WCP-ADAPTER v0.1 hash:${bodyHash()} -->\nsome text\n`;
  assert.throws(() => splice(broken, block()), (e) => e.code === 'E_STATE_CORRUPT');
});

test('install then check reports current; hand-edited text reports drift', () => {
  const root = makeRepo('adapters');
  try {
    fs.writeFileSync(path.join(root, 'CLAUDE.md'), '# Existing house rules\n\nBe careful.\n');
    const res = install(root, ['claude', 'copilot'], { tmpDir: path.join(root, '.tmp') });
    assert.equal(res.length, 2);
    assert.equal(statusFor(root, 'claude').state, 'current');
    assert.equal(statusFor(root, 'copilot').state, 'current');
    const claude = fs.readFileSync(path.join(root, 'CLAUDE.md'), 'utf8');
    assert.ok(claude.includes('Be careful.'), 'the user\'s own rules survive');

    fs.writeFileSync(path.join(root, 'CLAUDE.md'), claude.replace(bodyHash(), '0123456789ab'));
    assert.equal(statusFor(root, 'claude').state, 'drifted');
    const checked = install(root, ['claude'], { check: true, tmpDir: path.join(root, '.tmp') });
    assert.equal(checked[0].state, 'drifted');
    assert.equal(fs.readFileSync(path.join(root, 'CLAUDE.md'), 'utf8').includes('0123456789ab'), true, '--check must not write');
  } finally {
    cleanup(root);
  }
});

test('an unknown vendor is a usage error, not a silent skip', () => {
  const root = makeRepo('adapters-bad');
  try {
    assert.throws(() => install(root, ['emacs-telepathy'], { tmpDir: path.join(root, '.tmp') }), (e) => e.code === 'E_USAGE');
  } finally {
    cleanup(root);
  }
});
