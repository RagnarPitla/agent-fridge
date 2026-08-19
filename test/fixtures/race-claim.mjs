// SPDX-License-Identifier: Apache-2.0
// One racing housemate: reach for the same chore at exactly the same moment.
import { waitForBarrier } from './barrier.mjs';
import { main } from '../../src/cli.mjs';

const { FRIDGE_BARRIER, FRIDGE_ACTOR, RACE_TARGET, RACE_MODE = 'exclusive', RACE_TTL = '30s' } = process.env;
waitForBarrier(FRIDGE_BARRIER);

const swallow = [];
const realWrite = process.stdout.write.bind(process.stdout);
process.stdout.write = (s) => { swallow.push(String(s)); return true; };
let code;
try {
  code = await main(['claim', RACE_TARGET, '--task', `race ${FRIDGE_ACTOR}`, '--mode', RACE_MODE, '--ttl', RACE_TTL, '--json']);
} finally {
  process.stdout.write = realWrite;
}
let payload = null;
try { payload = JSON.parse(swallow.join('')); } catch { /* reported as null below */ }
realWrite(`${JSON.stringify({ actor: FRIDGE_ACTOR, code, claimId: payload?.data?.claimId ?? null, error: payload?.error?.code ?? null })}\n`);
process.exitCode = 0;
