// SPDX-License-Identifier: Apache-2.0
import { waitForBarrier } from './barrier.mjs';
import { main } from '../../src/cli.mjs';

const { FRIDGE_BARRIER } = process.env;
waitForBarrier(FRIDGE_BARRIER);

const chunks = [];
const realWrite = process.stdout.write.bind(process.stdout);
process.stdout.write = (s) => { chunks.push(String(s)); return true; };
let code;
try {
  code = await main(['heartbeat', '--json']);
} finally {
  process.stdout.write = realWrite;
}
let payload = null;
try { payload = JSON.parse(chunks.join('')); } catch { /* reported below */ }
realWrite(`${JSON.stringify({ code, renewed: payload?.data?.renewed?.length ?? null, error: payload?.error?.code ?? null })}\n`);
process.exitCode = 0;
