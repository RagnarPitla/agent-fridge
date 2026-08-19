// SPDX-License-Identifier: Apache-2.0
//
// The quick start is the first thing anybody runs. It shipped once with
// `--vendor claude-code`, which the CLI rejects, so the documented first
// experience was an E_USAGE error. Documentation that is never executed is
// just a rumour, so this executes it.
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { REPO, CLI, makeRepo, cleanup } from '../helpers.mjs';

const readme = () => fs.readFileSync(path.join(REPO, 'README.md'), 'utf8');

/** Pull the bash blocks out of one "## " section of a Markdown file. */
function bashBlocks(markdown, heading) {
  const start = markdown.indexOf(`## ${heading}`);
  assert.notEqual(start, -1, `README has no "## ${heading}" section any more`);
  const rest = markdown.slice(start + 3);
  const end = rest.indexOf('\n## ');
  const section = end === -1 ? rest : rest.slice(0, end);
  return [...section.matchAll(/```bash\n([\s\S]*?)```/g)].map((m) => m[1]);
}

/** Every runnable fridge command in a block, with trailing comments stripped. */
const fridgeCommands = (block) => block
  .split('\n')
  .map((l) => l.replace(/\s+#.*$/, '').trim())
  .filter((l) => l.startsWith('fridge '));

/** Split a shell line into argv, keeping quoted phrases together. */
const argvOf = (line) => [...line.matchAll(/"([^"]*)"|(\S+)/g)].map((m) => m[1] ?? m[2]).slice(1);

const run = (root, args, actor) => spawnSync(process.execPath, [CLI, ...args], {
  cwd: root,
  encoding: 'utf8',
  env: { ...process.env, NO_COLOR: '1', FRIDGE_ACTOR: actor || '' },
});

test('the 60-second quick start in the README actually runs', (t) => {
  // The first block is the numbered walkthrough. Later blocks in the section
  // are the second terminal and illustrative ids, which are not a script.
  const [walkthrough] = bashBlocks(readme(), '60-second quick start');
  const commands = fridgeCommands(walkthrough);
  assert.ok(commands.length >= 3, `expected a walkthrough of commands, found ${commands.length}`);

  const root = makeRepo('quickstart');
  t.after(() => cleanup(root));

  let actor = null;
  for (const line of commands) {
    const args = argvOf(line);
    const at = args.indexOf('--agent');
    if (args[0] === 'join' && at !== -1) actor = args[at + 1];
    const r = run(root, args, actor);
    assert.equal(
      r.status,
      0,
      `the README tells people to run:\n  ${line}\nand it exits ${r.status}\n${r.stdout}${r.stderr}`,
    );
  }
});

test('every fridge command the README shows is a command that exists', () => {
  const help = spawnSync(process.execPath, [CLI, 'help'], { encoding: 'utf8', cwd: REPO }).stdout;
  const known = new Set([
    ...[...help.matchAll(/^ {2}([a-z]+)\s{2,}/gm)].map((m) => m[1]),
    ...[...help.matchAll(/([a-z]+)=([a-z]+)/g)].flatMap((m) => [m[1], m[2]]),
  ]);
  assert.ok(known.size > 10, `could not parse the command list from help:\n${help}`);

  const shown = new Set([...readme().matchAll(/^\s*(?:\$ )?fridge ([a-z]+)/gm)].map((m) => m[1]));
  const missing = [...shown].filter((c) => !known.has(c));
  assert.deepEqual(missing, [], `README shows commands that do not exist: ${missing.join(', ')}`);
});

test('every --vendor the docs suggest for join is a vendor the CLI accepts', (t) => {
  const root = makeRepo('vendors');
  t.after(() => cleanup(root));
  run(root, ['init', '--no-adapters'], null);

  const rejected = run(root, ['join', '--agent', 'x', '--vendor', 'definitely-not-a-vendor'], null);
  const listed = /One of: (.+)$/m.exec(rejected.stderr || rejected.stdout);
  assert.ok(listed, `could not read the vendor list:\n${rejected.stdout}${rejected.stderr}`);
  const allowed = new Set(listed[1].split(',').map((s) => s.trim()));

  // `adapters --vendor` names instruction files, not agent vendors, so only
  // the values handed to `join` have to be in this list.
  const offenders = [];
  for (const rel of ['README.md', 'docs/adapters.md', 'docs/interop.md', 'skill/SKILL.md', 'CONTRIBUTING.md']) {
    const abs = path.join(REPO, rel);
    if (!fs.existsSync(abs)) continue;
    for (const line of fs.readFileSync(abs, 'utf8').split('\n')) {
      if (!/\bjoin\b/.test(line)) continue;
      const m = /--vendor\s+"?([a-z][a-z0-9-]*)"?/.exec(line);
      if (!m || m[1].includes('<') || m[1].startsWith('$')) continue;
      if (!allowed.has(m[1])) offenders.push(`${rel}: --vendor ${m[1]}`);
    }
  }
  assert.deepEqual(offenders, [], `docs tell people to join with a vendor the CLI rejects. Allowed: ${[...allowed].join(', ')}`);
});
