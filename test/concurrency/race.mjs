// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { CLI, REPO, TMP } from '../helpers.mjs';

/** Start n children, release them all at once, and collect what each one reported. */
export async function race(root, fixture, perChild, { leadMs = 250 } = {}) {
  fs.mkdirSync(TMP, { recursive: true });
  const barrier = path.join(TMP, `barrier-${process.pid}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`);
  const script = path.join(REPO, 'test', 'fixtures', fixture);
  const children = perChild.map((env) => new Promise((resolve) => {
    const child = spawn(process.execPath, [script], {
      cwd: root,
      env: { ...process.env, FRIDGE_BARRIER: barrier, NO_COLOR: '1', ...env },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let out = ''; let err = '';
    child.stdout.on('data', (d) => { out += d; });
    child.stderr.on('data', (d) => { err += d; });
    child.on('close', (code, signal) => {
      let report = null;
      try { report = JSON.parse(out.trim().split('\n').filter(Boolean).pop()); } catch { /* left null */ }
      resolve({ env, code, signal, report, stdout: out, stderr: err });
    });
  }));
  // Give every child time to reach the barrier before the agreed start instant.
  await new Promise((r) => setTimeout(r, leadMs));
  fs.writeFileSync(barrier, String(Date.now() + 60));
  const results = await Promise.all(children);
  fs.rmSync(barrier, { force: true });
  return results;
}

export { CLI };
