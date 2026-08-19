// SPDX-License-Identifier: Apache-2.0
// Cross-platform test runner. cmd.exe does not expand globs and older Node
// versions do not accept directories, so we discover the files ourselves.
// Concurrency suites run one file at a time: they measure OS-level contention,
// so running them in parallel would have the tests fight each other for cores
// instead of measuring the thing under test.
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const repo = fileURLToPath(new URL('..', import.meta.url));
const dirs = process.argv.slice(2).filter((a) => !a.startsWith('-'));
const passthrough = process.argv.slice(2).filter((a) => a.startsWith('-'));
if (!dirs.length) dirs.push('test/unit', 'test/integration');

const files = [];
const walk = (dir) => {
  let entries = [];
  try { entries = fs.readdirSync(dir, { withFileTypes: true }); } catch { return; }
  for (const e of entries.sort((a, b) => a.name.localeCompare(b.name))) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p);
    else if (/\.test\.mjs$/.test(e.name)) files.push(p);
  }
};
for (const d of dirs) walk(path.resolve(repo, d));

if (!files.length) {
  process.stderr.write(`no test files found in: ${dirs.join(', ')}\n`);
  process.exit(1);
}

const serial = files.some((f) => f.includes(`${path.sep}concurrency${path.sep}`))
  && !passthrough.some((a) => a.startsWith('--test-concurrency'));
const args = ['--test', ...(serial ? ['--test-concurrency=1'] : []), ...passthrough, ...files];
const r = spawnSync(process.execPath, args, { cwd: repo, stdio: 'inherit' });
process.exit(r.status === null ? 1 : r.status);
