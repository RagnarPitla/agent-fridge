// SPDX-License-Identifier: Apache-2.0
// Wait on a filesystem barrier so every child starts within the same millisecond.
import fs from 'node:fs';

export function waitForBarrier(file) {
  const spin = new Int32Array(new SharedArrayBuffer(4));
  while (!fs.existsSync(file)) Atomics.wait(spin, 0, 0, 2);
  const go = Number(fs.readFileSync(file, 'utf8').trim());
  while (Date.now() < go) { /* busy-wait to the agreed instant */ }
}
