// SPDX-License-Identifier: Apache-2.0
// Stream discipline: results on stdout, diagnostics on stderr.
// With --json, stdout is exactly one JSON object and nothing else.
// Output is plain ASCII with no ANSI escapes, so PowerShell and CI logs stay clean.
import { stableStringify } from './util.mjs';
import { PROTOCOL } from '../brand.mjs';

export function emit(ctx, command, { data = null, text = '' } = {}) {
  if (ctx.json) {
    process.stdout.write(stableStringify({
      ok: true, protocol: PROTOCOL, command, exitCode: 0, ts: new Date().toISOString(), data, error: null,
    }));
  } else if (!ctx.quiet && text) {
    process.stdout.write(text.endsWith('\n') ? text : `${text}\n`);
  }
  return 0;
}

export function emitError(ctx, command, err) {
  const code = err.code && typeof err.code === 'string' && err.exitCode !== undefined ? err.code : 'E_INTERNAL';
  const exitCode = err.exitCode ?? 1;
  if (ctx.json) {
    process.stdout.write(stableStringify({
      ok: false, protocol: PROTOCOL, command, exitCode, ts: new Date().toISOString(), data: null,
      error: { code, message: err.message, hint: err.hint || null, details: err.details || null },
    }));
  } else {
    if (err.details && err.details.report) process.stderr.write(`${err.details.report}\n`);
    process.stderr.write(`${code}: ${err.message}\n`);
    if (err.hint) process.stderr.write(`hint: ${err.hint}\n`);
    if (ctx.verbose && err.stack) process.stderr.write(`${err.stack}\n`);
  }
  return exitCode;
}

export const warn = (ctx, msg) => { if (!ctx.quiet) process.stderr.write(`warning: ${msg}\n`); };
