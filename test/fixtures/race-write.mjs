// SPDX-License-Identifier: Apache-2.0
// Two ways to report progress, running at the same instant from many processes:
//   MODE=shared-file  the old habit: read the whole Markdown file, append, write it back
//   MODE=fridge       pin a write-once note
// The first one loses lines. That is the bug this project exists to kill.
import fs from 'node:fs';
import { waitForBarrier } from './barrier.mjs';
import { main } from '../../src/cli.mjs';

const { FRIDGE_BARRIER, FRIDGE_ACTOR, WRITE_MODE, SHARED_FILE, WRITE_COUNT = '25' } = process.env;
const count = Number(WRITE_COUNT);
waitForBarrier(FRIDGE_BARRIER);

const realWrite = process.stdout.write.bind(process.stdout);
const quietly = async (args) => {
  process.stdout.write = () => true;
  try { return await main(args); } finally { process.stdout.write = realWrite; }
};

let written = 0;
for (let i = 0; i < count; i++) {
  if (WRITE_MODE === 'shared-file') {
    // Deliberately the naive read-modify-write every agent reaches for first.
    const current = fs.existsSync(SHARED_FILE) ? fs.readFileSync(SHARED_FILE, 'utf8') : '';
    const next = `${current}- ${FRIDGE_ACTOR} line ${i}\n`;
    fs.writeFileSync(SHARED_FILE, next);
    written++;
  } else {
    const code = await quietly(['pin', `${FRIDGE_ACTOR} line ${i}`, '--quiet']);
    if (code === 0) written++;
  }
}
realWrite(`${JSON.stringify({ actor: FRIDGE_ACTOR, written })}\n`);
process.exitCode = 0;
