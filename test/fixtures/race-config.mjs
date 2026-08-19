// SPDX-License-Identifier: Apache-2.0
import { waitForBarrier } from './barrier.mjs';
import { main } from '../../src/cli.mjs';

const { FRIDGE_BARRIER, RACE_KEY, RACE_VALUE } = process.env;
waitForBarrier(FRIDGE_BARRIER);

const chunks = [];
const realWrite = process.stdout.write.bind(process.stdout);
process.stdout.write = (s) => { chunks.push(String(s)); return true; };
let code;
try {
  code = await main(['config', RACE_KEY, RACE_VALUE, '--json']);
} finally {
  process.stdout.write = realWrite;
}
let payload = null;
try { payload = JSON.parse(chunks.join('')); } catch { /* reported below */ }
realWrite(`${JSON.stringify({ code, key: RACE_KEY, value: payload?.data?.value, error: payload?.error?.code ?? null })}\n`);
process.exitCode = 0;
