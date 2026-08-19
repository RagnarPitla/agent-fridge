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
const PAGES_URL = 'https://ragnarpitla.github.io/agent-fridge/';

test('the top README CTA links to Pages and a real quickstart anchor', () => {
  const markdown = readme();
  const hero = markdown.indexOf('docs/assets/agent-fridge-hero.svg');
  const heroEnd = markdown.indexOf('</p>', hero);
  // The first prose heading below the fold. The point of the assertion is that
  // the CTA sits between the hero and the long-form explanation, whatever that
  // explanation is currently called.
  const prose = markdown.indexOf('\n## The problem');
  const pagesLink = markdown.indexOf(`href="${PAGES_URL}"`);
  const quickstartLink = markdown.indexOf('href="#60-second-quick-start"');

  assert.ok(hero >= 0 && heroEnd > hero, 'README hero markup is missing');
  assert.ok(prose > 0, 'README has no leading prose section to place the CTA above');
  assert.ok(
    pagesLink > heroEnd && pagesLink < prose,
    'live-site CTA must appear directly below the hero and before the long prose',
  );
  assert.ok(
    quickstartLink > heroEnd && quickstartLink < prose,
    'quickstart CTA must appear beside the live-site CTA',
  );
  assert.match(markdown, /^## 60-second quick start$/m, 'quickstart anchor has no matching heading');

  const pkg = JSON.parse(fs.readFileSync(path.join(REPO, 'package.json'), 'utf8'));
  assert.equal(pkg.homepage, PAGES_URL, 'package homepage metadata must use the Pages URL');
});

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

/**
 * Every shell line in every tracked Markdown file, as {file, line, text}.
 * Only fenced blocks count, so prose like "the fridge door" is not mistaken
 * for a `fridge door` command.
 */
function docCommandLines() {
  const files = spawnSync('git', ['ls-files', '*.md'], { cwd: REPO, encoding: 'utf8' })
    .stdout.trim().split('\n').filter(Boolean);
  const out = [];
  for (const rel of files) {
    const lines = fs.readFileSync(path.join(REPO, rel), 'utf8').split('\n');
    let fenced = false;
    lines.forEach((raw, i) => {
      if (/^\s*```/.test(raw)) { fenced = /^\s*```(bash|sh|console|shell|powershell|ps1)?\s*$/.test(raw) ? !fenced : fenced; return; }
      if (!fenced) return;
      const text = raw.replace(/^\s*[$>]\s+/, '').replace(/\s+#.*$/, '').trim();
      if (text.startsWith('fridge ')) out.push({ where: `${rel}:${i + 1}`, text });
    });
  }
  assert.ok(out.length > 20, `only found ${out.length} documented commands; the extractor is probably broken`);
  return out;
}

test('every fridge command the docs show is a command that exists', () => {
  const help = spawnSync(process.execPath, [CLI, 'help'], { encoding: 'utf8', cwd: REPO }).stdout;
  const known = new Set([
    'help',
    ...[...help.matchAll(/^ {2}([a-z]+)\s{2,}/gm)].map((m) => m[1]),
  ]);
  assert.ok(known.size > 10, `could not parse the command list from help:\n${help}`);

  const missing = docCommandLines()
    .map((c) => ({ ...c, cmd: c.text.split(/\s+/)[1] }))
    .filter((c) => !known.has(c.cmd))
    .map((c) => `${c.where}  fridge ${c.cmd}`);
  assert.deepEqual(missing, [], `docs show commands that do not exist:\n${missing.join('\n')}`);
});

test('every --vendor the docs pass to join is a vendor the CLI accepts', (t) => {
  const root = makeRepo('vendors');
  t.after(() => cleanup(root));
  run(root, ['init', '--no-adapters'], null);

  const rejected = run(root, ['join', '--agent', 'x', '--vendor', 'definitely-not-a-vendor'], null);
  const listed = /One of: (.+)$/m.exec(rejected.stderr || rejected.stdout);
  assert.ok(listed, `could not read the vendor list:\n${rejected.stdout}${rejected.stderr}`);
  const allowed = new Set(listed[1].split(',').map((s) => s.trim()));

  // `adapters --vendor` names instruction files, not agent vendors, so only
  // the values handed to `join` have to be in this list.
  const offenders = docCommandLines()
    .filter((c) => c.text.startsWith('fridge join'))
    .map((c) => ({ ...c, vendor: (/--vendor\s+"?([a-z][a-z0-9-]*)"?/.exec(c.text) || [])[1] }))
    .filter((c) => c.vendor && !c.vendor.startsWith('$') && !allowed.has(c.vendor))
    .map((c) => `${c.where}  --vendor ${c.vendor}`);
  assert.deepEqual(offenders, [], `docs tell people to join with a vendor the CLI rejects.\n${offenders.join('\n')}\nAllowed: ${[...allowed].join(', ')}`);
});

// Agent Fridge is not on the npm registry. Until `npm publish` actually
// happens, any doc that tells a reader to run `npx agent-fridge` or
// `npm install -g agent-fridge` is promising something that fails on a clean
// machine. The `github:` form installs straight from the repository and does
// work, so it is the one to use.
test('no doc claims an npm registry install that does not exist', () => {
  const published = false;
  if (published) return;

  const offenders = [];
  const files = spawnSync('git', ['ls-files', '*.md', '*.json', '*.yml', '*.yaml'], { cwd: REPO, encoding: 'utf8' })
    .stdout.trim().split('\n').filter(Boolean);
  const bad = (line) => /\bnpx\s+(-y\s+)?agent-fridge\b/.test(line)
    || /\bnpm\s+(install|i|add)\b(?![^\n]*github:)[^\n]*\bagent-fridge\b/.test(line);

  for (const rel of files) {
    const lines = fs.readFileSync(path.join(REPO, rel), 'utf8').split('\n');
    // In Markdown, only a fenced block is an instruction. Prose *about* a
    // command - a changelog entry saying it was removed, or a line warning
    // that it does not work - is the fix, not the bug. Everything in a JSON or
    // YAML file is executable config, so all of it counts.
    const markdown = rel.endsWith('.md');
    let fenced = !markdown;
    lines.forEach((raw, i) => {
      if (markdown && /^\s*```/.test(raw)) { fenced = !fenced; return; }
      if (!fenced) return;
      if (bad(raw)) offenders.push(`${rel}:${i + 1}  ${raw.trim()}`);
    });
  }
  assert.deepEqual(offenders, [], `these promise an npm registry install that does not exist yet:\n${offenders.join('\n')}\nUse: npm install -g github:RagnarPitla/agent-fridge`);
});

// The canonical repository moved from RagnarPitla/fridgeboard before the first
// release. A stale URL in a badge, a link or package.json sends people to a
// repository that is not this one.
test('every link points at the canonical repository', () => {
  const offenders = [];
  const files = spawnSync('git', ['ls-files'], { cwd: REPO, encoding: 'utf8' })
    .stdout.trim().split('\n').filter(Boolean);
  for (const rel of files) {
    if (rel === 'NOTICE' || rel === 'CHANGELOG.md') continue; // both record the rename on purpose
    let text;
    try { text = fs.readFileSync(path.join(REPO, rel), 'utf8'); } catch { continue; }
    text.split('\n').forEach((line, i) => {
      if (/github\.com\/RagnarPitla\/fridgeboard/i.test(line)) offenders.push(`${rel}:${i + 1}`);
    });
  }
  assert.deepEqual(offenders, [], `stale repository URL (RagnarPitla/fridgeboard) in:\n${offenders.join('\n')}`);
});
