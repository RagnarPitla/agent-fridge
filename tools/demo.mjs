// SPDX-License-Identifier: Apache-2.0
// The 60-second demo, and the evidence behind the README's headline number.
// Runs the same workload twice in a throwaway repo:
//   A. the old way: N processes append to one shared Markdown file
//   B. the fridge way: N processes each write their own note
// Prints how many lines survived each. Nothing is mocked; these are real
// processes racing on the real filesystem.
//
// Usage: node tools/demo.mjs [--writers 8] [--lines 25] [--keep]
import fs from 'node:fs';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const repo = fileURLToPath(new URL('..', import.meta.url));
const bin = path.join(repo, 'bin', 'fridge.mjs');
const arg = (name, dflt) => {
  const i = process.argv.indexOf(`--${name}`);
  return i === -1 ? dflt : Number(process.argv[i + 1]);
};
const WRITERS = arg('writers', 8);
const LINES = arg('lines', 25);
const keep = process.argv.includes('--keep');
const root = path.join(repo, '.scratch', `demo-${Date.now()}`);

const say = (s = '') => process.stdout.write(`${s}\n`);
const rule = () => say('-'.repeat(64));
const fridge = (args, env = {}) => spawnSync(process.execPath, [bin, ...args], {
  cwd: root, encoding: 'utf8', env: { ...process.env, ...env },
});

fs.mkdirSync(path.join(root, 'src', 'api'), { recursive: true });
fs.writeFileSync(path.join(root, 'src', 'api', 'routes.ts'), '// routes\n');
fs.writeFileSync(path.join(root, 'README.md'), '# demo\n');
spawnSync('git', ['init', '-q'], { cwd: root });

const names = Array.from({ length: WRITERS }, (_, i) => `agent-${String(i + 1).padStart(2, '0')}`);

// ---------------------------------------------------------------- part A
say('');
say(`A. THE OLD WAY: ${WRITERS} processes, one shared Markdown file`);
rule();
const shared = path.join(root, 'shared-development-updates.md');
fs.writeFileSync(shared, '# Shared development updates\n\n');

const oldWay = names.map((name) => new Promise((resolve) => {
  const code = `
    import fs from 'node:fs';
    const file = ${JSON.stringify(shared)};
    for (let i = 0; i < ${LINES}; i += 1) {
      const text = fs.readFileSync(file, 'utf8');       // read
      const next = text + '- ${'${name}'} line ' + i + '\\n';  // edit in memory
      fs.writeFileSync(file, next);                     // write the whole file back
    }
  `.replace('${name}', name);
  const p = spawn(process.execPath, ['--input-type=module', '-e', code], { stdio: 'ignore' });
  p.on('exit', resolve);
}));
await Promise.all(oldWay);
const survivedA = fs.readFileSync(shared, 'utf8').split('\n').filter((l) => l.startsWith('- agent-')).length;
const wroteA = WRITERS * LINES;
say(`   wrote:    ${wroteA} lines`);
say(`   survived: ${survivedA} lines`);
say(`   LOST:     ${wroteA - survivedA} lines  <- this is the bug, reproduced`);

// ---------------------------------------------------------------- part B
say('');
say(`B. THE FRIDGE WAY: the same ${WRITERS} processes, same instant`);
rule();
fridge(['init', '--quiet']);
for (const n of names) fridge(['join', '--agent', n, '--quiet']);

const newWay = names.map((name) => new Promise((resolve) => {
  const code = `
    import { spawnSync } from 'node:child_process';
    for (let i = 0; i < ${LINES}; i += 1) {
      const r = spawnSync(process.execPath, [${JSON.stringify(bin)}, 'pin', 'line ' + i, '--quiet'], {
        cwd: ${JSON.stringify(root)},
        env: { ...process.env, FRIDGE_ACTOR: ${JSON.stringify(name)} },
        encoding: 'utf8',
      });
      if (r.status !== 0) { process.stderr.write('pin failed: ' + r.status + ' ' + (r.stderr || '')); process.exit(1); }
    }
  `;
  const p = spawn(process.execPath, ['--input-type=module', '-e', code], { stdio: ['ignore', 'ignore', 'pipe'] });
  let err = '';
  p.stderr.on('data', (d) => { err += d; });
  p.on('exit', (c) => { if (c !== 0 && process.env.DEMO_DEBUG) process.stderr.write(`child ${name} exit ${c}: ${err}\n`); resolve(c); });
}));
await Promise.all(newWay);

const log = JSON.parse(fridge(['log', '--json', '--limit', '10000'], { FRIDGE_ACTOR: names[0] }).stdout);
const pins = log.data.notes.filter((n) => n.type.startsWith('note.')).length;
say(`   wrote:    ${wroteA} notes`);
say(`   survived: ${pins} notes`);
say(`   LOST:     ${wroteA - pins} notes`);

// ---------------------------------------------------------------- part C
say('');
say('C. AND THE CHORES DO NOT COLLIDE');
rule();
const first = fridge(['claim', 'src/api/**', '--task', 'refactor the router', '--json'], { FRIDGE_ACTOR: names[0] });
const second = fridge(['claim', 'src/api/routes.ts', '--task', 'fix a typo', '--json'], { FRIDGE_ACTOR: names[1] });
say(`   ${names[0]} claims src/api/**        -> exit ${first.status} (${JSON.parse(first.stdout).ok ? 'granted' : 'refused'})`);
say(`   ${names[1]} claims src/api/routes.ts -> exit ${second.status} (${JSON.parse(second.stdout).error?.code || 'granted'})`);
say('');
say(fridge(['board'], { FRIDGE_ACTOR: names[1] }).stdout.trimEnd());

say('');
rule();
say(`RESULT   old way: ${wroteA - survivedA} lines lost.   fridge: ${wroteA - pins} notes lost.`);
rule();
say('');

if (keep) say(`kept: ${path.relative(repo, root)}`);
else fs.rmSync(root, { recursive: true, force: true });

process.exit((wroteA - pins) === 0 ? 0 : 1);
