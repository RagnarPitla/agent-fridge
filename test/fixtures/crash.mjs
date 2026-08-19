// SPDX-License-Identifier: Apache-2.0
// Take a card, then die without warning. No release, no goodbye, no cleanup.
import { main } from '../../src/cli.mjs';

const { FRIDGE_ACTOR, CRASH_TARGET, CRASH_TTL = '2s', CRASH_MODE = 'hard' } = process.env;
const realWrite = process.stdout.write.bind(process.stdout);
const swallow = [];
process.stdout.write = (s) => { swallow.push(String(s)); return true; };
await main(['claim', CRASH_TARGET, '--task', 'about to crash', '--ttl', CRASH_TTL, '--json']);
process.stdout.write = realWrite;
realWrite(`${swallow.join('')}\n`);

if (CRASH_MODE === 'mutex') {
  // Simulate a process killed while holding the registry lock.
  const fs = await import('node:fs');
  const path = await import('node:path');
  const dir = path.join(process.cwd(), '.fridge', 'locks', 'registry.lock.d');
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, 'owner.json'), JSON.stringify({
    pid: 999999, host: 'someone-else', op: 'claim', acquiredAt: new Date(Date.now() - 3600000).toISOString(),
  }));
}
process.kill(process.pid, 'SIGKILL');
