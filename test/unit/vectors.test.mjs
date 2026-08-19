// SPDX-License-Identifier: Apache-2.0
// Conformance vectors, checked against this implementation. The vector files
// are language-neutral on purpose: a Go or Rust implementation of wcp/0.1 can
// load the same JSON and must produce the same answers.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { normalizePattern, scopesOverlap, expandBraces, patternToRegExp } from '../../src/core/paths.mjs';
import { AppError } from '../../src/core/errors.mjs';
import { makeRepo, cleanup } from '../helpers.mjs';

const dir = fileURLToPath(new URL('../../vectors/', import.meta.url));
const load = (name) => JSON.parse(fs.readFileSync(path.join(dir, name), 'utf8'));

test('vectors: path normalization', () => {
  const root = makeRepo('vectors');
  try {
    const { cases } = load('path-normalization.json');
    for (const c of cases) {
      const input = c.input.replace('<ROOT>', root);
      const cwd = path.join(root, c.cwd || '.');
      if (c.expect === 'E_PATH_INVALID') {
        assert.throws(
          () => normalizePattern(input, { root, cwd }),
          (e) => e instanceof AppError && e.code === 'E_PATH_INVALID',
          `${c.name}: ${JSON.stringify(c.input)} must be rejected`,
        );
      } else {
        assert.equal(normalizePattern(input, { root, cwd }).pattern, c.expect, c.name);
      }
    }
  } finally { cleanup(root); }
});

test('vectors: scope overlap', () => {
  const { cases } = load('scope-overlap.json');
  for (const c of cases) {
    const got = scopesOverlap({ include: c.a, exclude: [] }, { include: c.b, exclude: [] });
    assert.equal(got.overlap, c.overlap, `${c.name}: ${JSON.stringify(c.a)} vs ${JSON.stringify(c.b)}`);
    if (c.reason) assert.equal(got.reason, c.reason, `${c.name}: overlap reason`);
  }
});

test('vectors: glob matching', () => {
  const { cases } = load('glob-matching.json');
  const hits = (pattern, file) => expandBraces(pattern).some((p) => patternToRegExp(p).test(file));
  for (const c of cases) {
    for (const m of c.matches) assert.equal(hits(c.pattern, m), true, `${c.name}: ${c.pattern} should match ${m}`);
    for (const m of c.rejects) assert.equal(hits(c.pattern, m), false, `${c.name}: ${c.pattern} should not match ${m}`);
  }
});

test('vectors: brace expansion', () => {
  const { cases } = load('brace-expansion.json');
  for (const c of cases) {
    if (c.expect_error) {
      assert.throws(
        () => expandBraces(c.input),
        (e) => e instanceof AppError && e.code === c.expect_error,
        `${c.name}: ${c.input}`,
      );
    } else {
      assert.deepEqual(expandBraces(c.input).slice().sort(), c.expect.slice().sort(), c.name);
    }
  }
});

test('every vector file declares its protocol and names every case', () => {
  const files = fs.readdirSync(dir).filter((f) => f.endsWith('.json'));
  assert.ok(files.length >= 4, 'the spec promises conformance vectors');
  for (const f of files) {
    const doc = load(f);
    assert.equal(doc.protocol, 'wcp/0.1', `${f}: must declare the protocol it targets`);
    assert.ok(Array.isArray(doc.cases) && doc.cases.length > 0, `${f}: needs cases`);
    for (const c of doc.cases) assert.equal(typeof c.name, 'string', `${f}: every case needs a name`);
  }
});
