// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

export const REPO = fileURLToPath(new URL('..', import.meta.url));
export const CLI = path.join(REPO, 'bin', 'fridge.mjs');
export const TMP = path.join(REPO, 'test', '.tmp');

let counter = 0;

/** A throwaway git checkout with a few files in it, under the repo (never /tmp). */
export function makeRepo(label = 'ws') {
  fs.mkdirSync(TMP, { recursive: true });
  const root = path.join(TMP, `${label}-${process.pid}-${Date.now().toString(36)}-${counter++}`);
  fs.mkdirSync(path.join(root, 'src', 'api'), { recursive: true });
  fs.mkdirSync(path.join(root, 'src', 'ui'), { recursive: true });
  fs.mkdirSync(path.join(root, 'docs'), { recursive: true });
  fs.writeFileSync(path.join(root, 'src', 'api', 'routes.ts'), 'export const routes = [];\n');
  fs.writeFileSync(path.join(root, 'src', 'api', 'db.ts'), 'export const db = 1;\n');
  fs.writeFileSync(path.join(root, 'src', 'ui', 'app.tsx'), 'export const App = () => null;\n');
  fs.writeFileSync(path.join(root, 'docs', 'guide.md'), '# guide\n');
  fs.writeFileSync(path.join(root, 'README.md'), '# demo\n');
  spawnSync('git', ['init', '-q'], { cwd: root });
  return root;
}

export function cleanup(root) {
  try { fs.rmSync(root, { recursive: true, force: true }); } catch { /* best effort */ }
}

/** Run the real binary in a real process, so exit codes are the real contract. */
export function fridge(root, args, { actor = null, env = {}, input = undefined } = {}) {
  const r = spawnSync(process.execPath, [CLI, ...args], {
    cwd: root,
    encoding: 'utf8',
    input,
    env: {
      ...process.env,
      FRIDGE_ACTOR: actor || process.env.FRIDGE_ACTOR || '',
      NO_COLOR: '1',
      ...env,
    },
  });
  let json = null;
  if (args.includes('--json')) { try { json = JSON.parse(r.stdout); } catch { json = null; } }
  return { code: r.status, stdout: r.stdout || '', stderr: r.stderr || '', json };
}

/** init + join, the two lines every test starts with. */
export function bootstrap(label = 'ws', actors = ['alice']) {
  const root = makeRepo(label);
  fridge(root, ['init', '--no-adapters', '--quiet']);
  for (const a of actors) fridge(root, ['join', '--agent', a, '--vendor', 'other', '--quiet']);
  return root;
}

export const readDoor = (root) => fs.readFileSync(path.join(root, '.fridge', 'DOOR.md'), 'utf8');

export function noteFiles(root) {
  const base = path.join(root, '.fridge', 'notes');
  const out = [];
  const walk = (dir) => {
    let entries = [];
    try { entries = fs.readdirSync(dir, { withFileTypes: true }); } catch { return; }
    for (const e of entries) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else if (e.name.endsWith('.json')) out.push(p);
    }
  };
  walk(base);
  return out;
}

export const notes = (root) => noteFiles(root).map((f) => JSON.parse(fs.readFileSync(f, 'utf8')));
