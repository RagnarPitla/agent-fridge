// SPDX-License-Identifier: Apache-2.0
//
// One command that runs what CI runs, in the order CI runs it.
//
// This exists because "it passed locally" kept meaning "I ran the subset I
// happened to remember". Every check below has failed CI at least once after a
// change its author was sure was safe.
//
//   node tools/check.mjs           everything
//   node tools/check.mjs --fast    skip the slow suites, for a tight loop
import { spawnSync } from 'node:child_process';
import process from 'node:process';

const fast = process.argv.includes('--fast');

const steps = [
  ['lint', 'node', ['tools/lint.mjs']],
  ['generated docs', 'node', ['tools/gen-exit-codes.mjs', '--check']],
  ['gofmt', 'node', ['tools/go.mjs', 'fmt']],
  ['go vet', 'node', ['tools/go.mjs', 'vet', './...']],
  ['node tests', 'node', ['tools/run-tests.mjs', ...(fast ? ['test/unit'] : ['test/unit', 'test/integration', 'test/concurrency'])]],
  ['go tests', 'node', ['tools/go.mjs', 'test', ...(fast ? ['./internal/...'] : ['./...'])]],
  ['conformance, go', 'node', ['tools/go.mjs', 'run', './cmd/fridge', '--', 'conform']],
  ['conformance, node', 'node', ['bin/fridge.mjs', 'conform']],
];
if (!fast) steps.push(['parity', 'node', ['tools/parity.mjs']]);

const failed = [];
for (const [name, cmd, args] of steps) {
  process.stdout.write(`\n=== ${name} ===\n`);
  const started = Date.now();
  const r = spawnSync(cmd, args, { stdio: 'inherit' });
  const secs = ((Date.now() - started) / 1000).toFixed(1);
  if (r.status !== 0) {
    failed.push(name);
    process.stdout.write(`--- ${name} FAILED after ${secs}s\n`);
  } else {
    process.stdout.write(`--- ${name} ok (${secs}s)\n`);
  }
}

process.stdout.write('\n');
if (failed.length) {
  process.stdout.write(`FAILED: ${failed.join(', ')}\n`);
  process.exit(1);
}
process.stdout.write(`all ${steps.length} checks passed${fast ? ' (fast subset)' : ''}\n`);
