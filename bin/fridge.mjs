#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
import { main } from '../src/cli.mjs';

main().then((code) => {
  process.exitCode = code;
}).catch((err) => {
  process.stderr.write(`E_INTERNAL: ${err?.stack || err?.message || String(err)}\n`);
  process.exitCode = 1;
});
