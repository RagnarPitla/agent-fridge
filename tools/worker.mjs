// SPDX-License-Identifier: Apache-2.0
// One simulation housemate. Runs as its own OS process so the contention is real,
// not cooperative scheduling inside a single event loop.
import { main } from '../src/cli.mjs';
import { mulberry32 } from '../src/core/util.mjs';

const index = Number(process.env.FRIDGE_SIM_INDEX || 0);
const seed = Number(process.env.FRIDGE_SIM_SEED || 1);
const durationMs = Number(process.env.FRIDGE_SIM_DURATION || 5000);
const name = `sim-${String(index).padStart(2, '0')}`;
process.env.FRIDGE_ACTOR = name;

const POOL = ['sim/alpha/**', 'sim/beta/**', 'sim/gamma/**', 'sim/alpha/deep/**', 'sim/delta/notes.md'];
const rand = mulberry32(seed);
const pick = (arr) => arr[Math.floor(rand() * arr.length)];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const stats = { name, claims: 0, conflicts: 0, pins: 0, releases: 0, abandoned: 0, mutexTimeouts: 0, errors: [] };

const swallowed = [];
const realWrite = process.stdout.write.bind(process.stdout);
const quietly = async (args) => {
  process.stdout.write = (s) => { swallowed.push(String(s)); return true; };
  try { return await main(args); } finally { process.stdout.write = realWrite; }
};

const vendors = ['claude', 'copilot', 'codex', 'human', 'other'];
await quietly(['join', '--agent', name, '--vendor', vendors[index % vendors.length], '--quiet']);

const end = Date.now() + durationMs;
while (Date.now() < end) {
  const scope = pick(POOL);
  const code = await quietly(['claim', scope, '--task', `sim work ${stats.claims}`, '--ttl', '4s', '--quiet']);
  if (code === 10) { stats.conflicts++; await sleep(20 + Math.floor(rand() * 60)); continue; }
  if (code === 20) { stats.mutexTimeouts++; await sleep(50); continue; }
  if (code !== 0) { stats.errors.push(`claim ${scope} -> ${code}`); await sleep(30); continue; }
  stats.claims++;

  const noteCount = 1 + Math.floor(rand() * 3);
  for (let i = 0; i < noteCount; i++) {
    const c = await quietly(['pin', `${name} touched ${scope} step ${i}`, '--quiet']);
    if (c === 0) stats.pins++; else stats.errors.push(`pin -> ${c}`);
  }
  if (rand() < 0.4) await quietly(['heartbeat', '--quiet']);
  await sleep(10 + Math.floor(rand() * 80));

  // One in eight rounds we walk away without tidying up, exactly like a crashed
  // terminal. The lease must expire and somebody else must be able to take over.
  if (rand() < 0.125) { stats.abandoned++; await sleep(30); continue; }
  const rc = await quietly(['release', '--all', '--outcome', 'done', '--note', `sim round ${stats.claims}`, '--quiet']);
  if (rc === 0) stats.releases++; else stats.errors.push(`release -> ${rc}`);
}

await quietly(['release', '--all', '--outcome', 'abandoned', '--note', 'simulation over', '--quiet']);
realWrite(`${JSON.stringify(stats)}\n`);
process.exitCode = stats.errors.length ? 1 : 0;
