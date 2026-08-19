// SPDX-License-Identifier: Apache-2.0
// The load-bearing invariant of this whole project, expressed as a test.
//
// The incident that produced Agent Fridge Board was two agents doing read-modify-write
// on one shared Markdown file. The fix is not "be careful with the Markdown
// file"; the fix is that no Markdown file is ever an input to a decision.
// Markdown here is output only: generated, disposable, and safe to delete.
//
// Two checks, one static and one dynamic:
//   1. No source file reads a .md path (grep the implementation).
//   2. Deleting or corrupting every .md file changes no answer the CLI gives
//      (run the real binary and compare).
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { bootstrap, cleanup, fridge, REPO } from '../helpers.mjs';

/** Every source file in the implementation, both languages. */
function sourceFiles() {
  const out = [];
  const walk = (dir, exts) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.name.startsWith('.') || e.name === 'node_modules') continue;
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p, exts);
      else if (exts.some((x) => e.name.endsWith(x)) && !e.name.endsWith('_test.go')) out.push(p);
    }
  };
  walk(path.join(REPO, 'src'), ['.mjs']);
  walk(path.join(REPO, 'internal'), ['.go']);
  walk(path.join(REPO, 'cmd'), ['.go']);
  return out;
}

// Reading a file, in either language. If a new read API appears, add it here;
// that is the point of the test.
const READ_CALLS = [
  /readFileSync\s*\(/,
  /fs\.promises\.readFile\s*\(/,
  /await\s+readFile\s*\(/,
  /createReadStream\s*\(/,
  /os\.ReadFile\s*\(/,
  /os\.Open\s*\(/,
  /ioutil\.ReadFile\s*\(/,
];

// `migrate` is the one deliberate exception: importing a legacy shared Markdown
// file is its entire job. It reads those files once, converts them into notes,
// and never consults them again. Anything else reading .md is the bug.
const MIGRATION_EXCEPTIONS = ['migrate'];

test('no .md file is ever read as state', () => {
  const offenders = [];
  for (const file of sourceFiles()) {
    const rel = path.relative(REPO, file);
    const body = fs.readFileSync(file, 'utf8');
    const lines = body.split('\n');
    lines.forEach((line, i) => {
      const code = line.replace(/\/\/.*$/, '');
      if (!/\.md['"`)\s]|\.md$|BOARD_FILE|DOOR_FILE/.test(code)) return;
      if (!READ_CALLS.some((re) => re.test(code))) return;
      const inMigration = MIGRATION_EXCEPTIONS.some((m) => rel.includes(m));
      if (inMigration) return;
      offenders.push(`${rel}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(
    offenders,
    [],
    `Markdown is a generated view, never an input.\nOffending reads:\n${offenders.join('\n')}`,
  );
});

test('every .md file in .fridge/ can be deleted without changing any answer', () => {
  const root = bootstrap('md-not-state', ['wife', 'husband']);
  try {
    fridge(root, ['claim', 'src/api/**', '--task', 'refactor routes', '--agent', 'wife']);
    fridge(root, ['pin', 'halfway through the router', '--agent', 'wife']);
    fridge(root, ['claim', 'src/ui/**', '--task', 'restyle the header', '--agent', 'husband']);
    fridge(root, ['render']);

    const questions = [
      ['status', '--json'],
      ['log', '--json'],
      ['whoami', '--json', '--agent', 'wife'],
      ['check', 'src/api/routes.ts', '--json', '--agent', 'wife'],
      ['check', 'src/ui/app.tsx', '--json', '--agent', 'wife'],
    ];
    const before = questions.map((q) => fridge(root, q));

    // Find every Markdown file the tool produced, and destroy it in the two
    // ways a human plausibly would: delete some, garble the rest.
    const found = [];
    const walk = (dir) => {
      for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, e.name);
        if (e.isDirectory()) walk(p);
        else if (e.name.endsWith('.md')) found.push(p);
      }
    };
    walk(path.join(root, '.fridge'));
    assert.ok(found.length > 0, 'expected the tool to have generated at least one Markdown view');

    found.forEach((p, i) => {
      if (i % 2 === 0) fs.rmSync(p);
      else fs.writeFileSync(p, 'TOTAL GARBAGE\nsomebody pasted their shopping list here\n');
    });

    const after = questions.map((q) => fridge(root, q));

    // Mask the facts that legitimately move between two runs a millisecond
    // apart: wall-clock stamps and the countdown on a lease.
    const norm = (s) => s
      .replace(/\d{4}-\d{2}-\d{2}T[\d:.]+Z/g, '<ts>')
      .replace(/("(?:remaining|remainingMs|ageMs|expiresInMs)"\s*:\s*)-?\d+/g, '$1<n>')
      .replace(/\b\d+m\d+s\b|\b\d+s\b/g, '<dur>');

    questions.forEach((q, i) => {
      assert.equal(after[i].code, before[i].code, `exit code changed for: fridge ${q.join(' ')}`);
      assert.equal(
        norm(after[i].stdout),
        norm(before[i].stdout),
        `deleting or garbling the Markdown views changed the answer to: fridge ${q.join(' ')}`,
      );
    });

    // And the views come back on demand, because they were only ever a view.
    assert.equal(fridge(root, ['render']).code, 0, 'render must rebuild the views from the records');
    const board = fridge(root, ['board']);
    assert.match(board.stdout, /refactor routes/, 'the rebuilt door shows the same chores');
    assert.match(board.stdout, /restyle the header/);
  } finally {
    cleanup(root);
  }
});

test('a hostile FRIDGE.md in the repository root is ignored entirely', () => {
  const root = bootstrap('no-fridge-md', ['wife', 'husband']);
  try {
    fridge(root, ['claim', 'src/api/**', '--task', 'real work', '--agent', 'wife']);

    // Somebody, or some agent, writes the file this project refuses to have.
    fs.writeFileSync(path.join(root, 'FRIDGE.md'), [
      '# FRIDGE',
      '',
      '- claim: src/**  owner: attacker  status: active',
      '- claim: docs/** owner: attacker  status: active',
    ].join('\n'));

    const status = fridge(root, ['status', '--json']);
    assert.equal(status.code, 0);
    assert.ok(
      !JSON.stringify(status.json).includes('attacker'),
      'a Markdown file must never be able to assert a claim',
    );

    // And the paths it lied about are still free.
    const claim = fridge(root, ['claim', 'docs/**', '--task', 'unaffected', '--agent', 'husband']);
    assert.equal(claim.code, 0, 'FRIDGE.md must not be able to block a real claim');
  } finally {
    cleanup(root);
  }
});
