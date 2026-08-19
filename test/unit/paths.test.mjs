// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import fs from 'node:fs';
import {
  expandBraces, isRootGlobal, literalPrefix, matchesAny, materialize, normalizePattern,
  patternToRegExp, scopesOverlap,
} from '../../src/core/paths.mjs';
import { makeRepo, cleanup } from '../helpers.mjs';

const root = makeRepo('paths-unit');
test.after(() => cleanup(root));
const norm = (input, cwd = root) => normalizePattern(input, { root, cwd }).pattern;

test('normalizePattern: repo-relative results', () => {
  assert.equal(norm('src/api/routes.ts'), 'src/api/routes.ts');
  assert.equal(norm('./src/api'), 'src/api');
  assert.equal(norm('src//api///routes.ts'), 'src/api/routes.ts');
  assert.equal(norm('src/api/'), 'src/api');
  assert.equal(normalizePattern('src/api/', { root, cwd: root }).dirIntent, true);
  assert.equal(norm('src/**/*.ts'), 'src/**/*.ts');
});

test('normalizePattern: resolves relative to cwd, not the repo root', () => {
  const cwd = path.join(root, 'src');
  assert.equal(normalizePattern('api/routes.ts', { root, cwd }).pattern, 'src/api/routes.ts');
  assert.equal(normalizePattern('*.ts', { root, cwd: path.join(root, 'src', 'api') }).pattern, 'src/api/*.ts');
});

test('normalizePattern: rejects everything that could escape or confuse', () => {
  const bad = [
    ['../outside', 'traversal up'],
    ['src/../../etc/passwd', 'traversal through'],
    ['~/secrets', 'home relative'],
    ['//server/share', 'UNC'],
    ['src/\u0000evil', 'NUL byte'],
    ['src/two\nlines', 'newline'],
    ['C:/Windows', 'drive letter'],
    ['.git/config', 'reserved root'],
    ['.fridge/claims', 'reserved root'],
    ['src/con', 'reserved windows name'],
    ['src/name.', 'trailing dot'],
    ['src/name ', 'trailing space'],
    ['', 'empty'],
    ['x'.repeat(5000), 'too long'],
  ];
  for (const [input, why] of bad) {
    assert.throws(() => normalizePattern(input, { root, cwd: root }), (e) => e.code === 'E_PATH_INVALID', `should reject ${why}: ${JSON.stringify(input).slice(0, 40)}`);
  }
});

test('normalizePattern: unicode is normalized to NFC so the same name is the same key', () => {
  const nfd = 'src/cafe\u0301.ts';
  const nfc = 'src/caf\u00e9.ts';
  assert.equal(norm(nfd), norm(nfc));
});

test('normalizePattern: a symlink that escapes the workspace is refused', { skip: process.platform === 'win32' }, () => {
  const outside = path.join(root, '..', `escape-${process.pid}`);
  fs.mkdirSync(outside, { recursive: true });
  fs.symlinkSync(outside, path.join(root, 'jump'));
  try {
    assert.throws(() => normalizePattern('jump/file.txt', { root, cwd: root }), (e) => e.code === 'E_PATH_INVALID');
  } finally {
    fs.rmSync(path.join(root, 'jump'), { force: true });
    fs.rmSync(outside, { recursive: true, force: true });
  }
});

test('glob subset: * stops at a slash, ** crosses segments', () => {
  const m = (p, f) => patternToRegExp(p).test(f);
  assert.equal(m('src/*.ts', 'src/a.ts'), true);
  assert.equal(m('src/*.ts', 'src/api/a.ts'), false);
  assert.equal(m('src/**', 'src/api/deep/a.ts'), true);
  assert.equal(m('src/**/*.ts', 'src/api/a.ts'), true);
  assert.equal(m('src/**/*.ts', 'src/a.ts'), true);
  assert.equal(m('src/?.ts', 'src/a.ts'), true);
  assert.equal(m('src/?.ts', 'src/ab.ts'), false);
  assert.equal(m('src/[ab].ts', 'src/b.ts'), true);
  assert.equal(m('src/[!ab].ts', 'src/c.ts'), true);
  assert.equal(m('src/[!ab].ts', 'src/a.ts'), false);
});

test('glob subset: unsupported syntax is an explicit error, never a silent mismatch', () => {
  for (const p of ['!(src)', 'src/+(a|b)', '@(x)', '!src/**']) {
    assert.throws(() => patternToRegExp(p), (e) => e.code === 'E_PATH_INVALID', `should reject ${p}`);
  }
});

test('brace expansion', () => {
  assert.deepEqual(expandBraces('src/{a,b}.ts').sort(), ['src/a.ts', 'src/b.ts']);
  assert.deepEqual(expandBraces('src/{a,b}/{x,y}.ts').length, 4);
  assert.deepEqual(expandBraces('src/a.ts'), ['src/a.ts']);
});

test('literalPrefix and isRootGlobal', () => {
  assert.equal(literalPrefix('src/api/**'), 'src/api');
  assert.equal(literalPrefix('src/api/routes.ts'), 'src/api/routes.ts');
  assert.equal(literalPrefix('*.md'), '');
  assert.equal(isRootGlobal('**'), true);
  assert.equal(isRootGlobal('**/*.ts'), true);
  assert.equal(isRootGlobal('src/**'), false);
});

const scope = (include) => {
  const m = materialize(root, include, { limit: 5000 });
  return { include, exclude: [], ...m };
};

test('overlap: the cases that caused the original 128-line loss', () => {
  assert.equal(scopesOverlap(scope(['src/api/**']), scope(['src/api/routes.ts'])).overlap, true);
  assert.equal(scopesOverlap(scope(['src/api/routes.ts']), scope(['src/api/**'])).overlap, true);
  assert.equal(scopesOverlap(scope(['src/**']), scope(['src/api/deep/**'])).overlap, true);
  assert.equal(scopesOverlap(scope(['src/api/**']), scope(['src/ui/**'])).overlap, false);
  assert.equal(scopesOverlap(scope(['docs/**']), scope(['src/**'])).overlap, false);
});

test('overlap: identical patterns collide even when no file exists yet', () => {
  const empty = { include: ['future/dir/**'], exclude: [], materialized: [], matchers: ['future/dir/**'] };
  assert.equal(scopesOverlap(empty, empty).overlap, true);
  assert.equal(scopesOverlap(empty, { ...empty, include: ['future/dir/deep/**'], matchers: ['future/dir/deep/**'] }).overlap, true);
});

test('overlap: a root-global pattern collides with everything', () => {
  const all = { include: ['**'], exclude: [], materialized: [], matchers: ['**'] };
  assert.equal(scopesOverlap(all, scope(['docs/**'])).overlap, true);
  assert.equal(scopesOverlap(scope(['docs/**']), all).overlap, true);
});

test('overlap: a bare glob does not falsely collide with a nested tree', () => {
  assert.equal(scopesOverlap(scope(['*.md']), scope(['src/**'])).overlap, false);
});

test('overlap: truncated materialization fails safe (may over-report, never under-report)', () => {
  const a = { include: ['src/**'], exclude: [], materialized: [], matchers: ['src/**'], materializedTruncated: true };
  const b = { include: ['src/api/x.ts'], exclude: [], materialized: [], matchers: ['src/api/x.ts'] };
  assert.equal(scopesOverlap(a, b).overlap, true);
});

test('materialize honours the limit and reports truncation', () => {
  const m = materialize(root, ['**/*'], { limit: 2 });
  assert.equal(m.materialized.length, 2);
  assert.equal(m.materializedTruncated, true);
});

test('matchesAny with case folding', () => {
  assert.equal(matchesAny(['src/API/**'], 'src/api/a.ts', false), false);
  assert.equal(matchesAny(['src/API/**'], 'src/api/a.ts', true), true);
});
