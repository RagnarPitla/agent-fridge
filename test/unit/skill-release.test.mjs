// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { REPO } from '../helpers.mjs';

const read = (rel) => fs.readFileSync(path.join(REPO, rel), 'utf8');

test('the Agent Skill uses portable front matter', () => {
  const match = /^---\n([\s\S]*?)\n---/.exec(read('skill/SKILL.md'));
  assert.ok(match, 'skill/SKILL.md has no front matter');
  const keys = match[1]
    .split('\n')
    .filter((line) => /^[a-z][a-z0-9-]*:/.test(line))
    .map((line) => line.split(':', 1)[0]);
  assert.deepEqual(keys, ['name', 'description']);
});

test('release assets and install docs agree on SKILL.md', () => {
  const workflow = read('.github/workflows/release.yml');
  const docs = read('skill/README.md');

  assert.match(workflow, /dist\/SKILL\.md dist\/SKILL\.md\.sha256/);
  assert.match(docs, /releases\/latest\/download\/SKILL\.md/);
  assert.match(docs, /releases\/latest\/download\/SKILL\.md\.sha256/);
  assert.doesNotMatch(docs, /skillPath/);
});
