// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import { EXIT, EXIT_DOC, AppError } from '../../src/core/errors.mjs';
import { humanMs, mulberry32, parseDuration, slug, stableStringify, ulid } from '../../src/core/util.mjs';

test('exit codes are a stable, documented, collision-free contract', () => {
  const seen = new Map();
  for (const [name, code] of Object.entries(EXIT)) {
    assert.equal(typeof code, 'number', `${name} must be a number`);
    assert.ok(Number.isInteger(code) && code >= 0 && code < 126, `${name}=${code} must be an integer in 0..125`);
    assert.ok(EXIT_DOC[name], `${name} must be documented`);
    assert.equal(seen.has(code), false, `${name} collides with ${seen.get(code)} on ${code}`);
    seen.set(code, name);
  }
  assert.equal(EXIT.OK, 0);
  assert.equal(EXIT.E_USAGE, 2);
  assert.equal(EXIT.E_CONFLICT, 10);
  assert.equal(EXIT.E_NOT_OWNER, 12);
  assert.equal(EXIT.E_LEASE_EXPIRED, 13);
  assert.equal(EXIT.E_OUT_OF_SCOPE, 14);
  assert.equal(EXIT.E_MUTEX_TIMEOUT, 20);
  assert.equal(EXIT.E_WAIT_TIMEOUT, 21);
  assert.equal(EXIT.E_DRIFT, 30);
  assert.equal(EXIT.E_PATH_INVALID, 40);
});

test('AppError carries its exit code and hint', () => {
  const e = new AppError('E_CONFLICT', 'taken', { hint: 'try later' });
  assert.equal(e.exitCode, 10);
  assert.equal(e.hint, 'try later');
  assert.ok(e instanceof Error);
  assert.throws(() => new AppError('E_NOT_A_REAL_CODE', 'x'), /unknown exit code/i);
});

test('parseDuration accepts the documented units and rejects the rest', () => {
  assert.equal(parseDuration('500ms'), 500);
  assert.equal(parseDuration('30s'), 30000);
  assert.equal(parseDuration('15m'), 900000);
  assert.equal(parseDuration('2h'), 7200000);
  assert.equal(parseDuration('1d'), 86400000);
  assert.equal(parseDuration(1234), 1234, 'numbers are milliseconds, as stored in config');
  for (const bad of ['', 'soon', '5 weeks', '-3m', 'NaNs', '1.5.2h', '90']) {
    assert.throws(() => parseDuration(bad), (e) => e.code === 'E_USAGE', `should reject ${bad}`);
  }
});

test('humanMs is short and readable', () => {
  assert.equal(humanMs(0), '0s');
  assert.equal(humanMs(1500), '1s');
  assert.equal(humanMs(90000), '1m 30s');
  assert.equal(humanMs(-5000), 'expired');
});

test('ulid ids sort in creation order even inside the same millisecond', () => {
  const ids = Array.from({ length: 500 }, () => ulid());
  assert.deepEqual([...ids].sort(), ids, 'lexical order must equal creation order');
  assert.equal(new Set(ids).size, ids.length, 'ids must be unique');
  assert.equal(ids[0].length, 26);
});

test('stableStringify is byte-identical regardless of key order', () => {
  const a = stableStringify({ b: 1, a: { d: 4, c: 3 } });
  const b = stableStringify({ a: { c: 3, d: 4 }, b: 1 });
  assert.equal(a, b);
  assert.ok(a.endsWith('\n'), 'records end with a newline so git diffs stay clean');
});

test('slug is filesystem safe', () => {
  assert.equal(slug('Claude Code A'), 'claude-code-a');
  assert.equal(slug('../../etc/passwd'), 'etc-passwd');
  assert.equal(slug('CON'), 'con');
  assert.ok(slug('x'.repeat(200)).length <= 24);
});

test('mulberry32 is deterministic, so simulations are reproducible', () => {
  const a = mulberry32(42); const b = mulberry32(42);
  assert.deepEqual([a(), a(), a()], [b(), b(), b()]);
});
