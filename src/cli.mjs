#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
import { AppError, EXIT, EXIT_DOC } from './core/errors.mjs';
import { emitError } from './core/output.mjs';
import { withMutex } from './core/mutex.mjs';
import { openWorkspace, renewOwnLeases, requireActor } from './core/store.mjs';
import { autoRender } from './core/render.mjs';
import { BIN, PACKAGE, PRODUCT, PROTOCOL, TAGLINE, VERSION } from './brand.mjs';
import * as workspace from './commands/workspace.mjs';
import * as claims from './commands/claims.mjs';
import * as notes from './commands/notes.mjs';
import * as view from './commands/view.mjs';
import * as coord from './commands/coord.mjs';
import * as conformance from './commands/conform.mjs';

const GLOBAL_BOOL = ['json', 'quiet', 'verbose', 'no-color', 'yes', 'help', 'allow-multihost', 'allow-secret-like'];
const GLOBAL_VALUE = ['repo', 'agent', 'vendor'];

export const COMMANDS = {
  init: { fn: workspace.init, summary: 'Hang the door: create .fridge/ in this repository.', bool: ['force', 'no-adapters'], value: ['commit-notes'], exits: ['E_ALREADY_EXISTS', 'E_PERMISSION'] },
  join: { fn: workspace.join, summary: 'Put your name on the door and start a session.', bool: [], value: [], exits: ['E_NOT_INITIALIZED', 'E_USAGE'] },
  whoami: { fn: workspace.whoami, summary: 'Who am I, and what am I holding?', bool: [], value: [], exits: ['E_NO_SESSION'] },
  claim: { fn: claims.claim, summary: 'Take a chore card over one or more paths.', bool: ['queue', 'strict', 'confirm-global'], value: ['task', 'mode', 'ttl', 'exclude', 'wait', 'label'], exits: ['E_CONFLICT', 'E_PATH_INVALID', 'E_MUTEX_TIMEOUT', 'E_WAIT_TIMEOUT'] },
  check: { fn: claims.check, summary: 'May I write these paths right now?', bool: ['for-write'], value: [], exits: ['E_CONFLICT', 'E_OUT_OF_SCOPE', 'E_PATH_INVALID'] },
  guard: { fn: claims.guard, summary: 'Assert paths are inside your claims (for hooks and pre-commit).', bool: ['staged'], value: [], exits: ['E_CONFLICT', 'E_OUT_OF_SCOPE'] },
  heartbeat: { fn: claims.heartbeat, summary: 'Shout "still on it" and renew your leases.', bool: ['all'], value: ['claim', 'ttl'], exits: ['E_LEASE_EXPIRED', 'E_NOT_FOUND'] },
  extend: { fn: claims.extend, summary: 'Raise the TTL on one claim.', bool: [], value: ['ttl'], exits: ['E_NOT_FOUND', 'E_NOT_OWNER'] },
  release: { fn: claims.release, summary: 'Take the card down.', bool: ['all', 'force'], value: ['outcome', 'note'], exits: ['E_NOT_FOUND', 'E_NOT_OWNER', 'E_LEASE_EXPIRED'] },
  reap: { fn: claims.reap, summary: 'Sweep cards that fell off the door.', bool: ['dry-run', 'force'], value: [], exits: ['E_MUTEX_TIMEOUT'] },
  wait: { fn: claims.wait, summary: 'Wait for a card to come down.', bool: [], value: ['timeout'], exits: ['E_WAIT_TIMEOUT', 'E_NOT_FOUND'] },
  run: { fn: claims.run, summary: 'Claim, run a command with automatic check-ins, then release.', bool: ['keep-on-failure'], value: ['claim', 'task', 'ttl', 'mode'], exits: ['E_CONFLICT', 'E_USAGE'] },
  pin: { fn: notes.pin, summary: 'Pin a durable note to the door.', bool: [], value: ['kind', 'claim', 'task'], exits: ['E_USAGE'] },
  log: { fn: notes.log, summary: 'Read the notes wall.', bool: ['follow'], value: ['limit', 'since', 'until', 'actor', 'type'], exits: [] },
  board: { fn: view.board, summary: 'Read the door.', bool: ['write', 'stdout', 'check', 'wide'], value: [], exits: ['E_DRIFT'] },
  status: { fn: view.status, summary: 'Same data as the door, machine first.', bool: ['mine', 'wide', 'watch'], value: ['interval'], exits: [] },
  render: { fn: view.render, summary: 'Regenerate the door and views.', bool: ['check'], value: ['output'], exits: ['E_DRIFT'] },
  handoff: { fn: coord.handoff, summary: 'Offer a chore to another housemate.', bool: ['force'], value: ['to', 'note', 'reason'], exits: ['E_NOT_FOUND', 'E_NOT_OWNER', 'E_USAGE'] },
  accept: { fn: coord.accept, summary: 'Take an offered chore.', bool: [], value: [], exits: ['E_NOT_FOUND'] },
  decline: { fn: coord.decline, summary: 'Refuse an offered chore.', bool: [], value: ['reason'], exits: ['E_NOT_FOUND'] },
  inbox: { fn: coord.inbox, summary: 'Notes addressed to me.', bool: [], value: [], exits: [] },
  doctor: { fn: coord.doctor, summary: 'Tidy the door: diagnose and repair.', bool: ['fix', 'check'], value: [], exits: ['E_DRIFT'] },
  simulate: { fn: coord.simulate, summary: 'Run a real multi-process household simulation.', bool: [], value: ['agents', 'duration', 'seed', 'report'], exits: [] },
  conform: { fn: conformance.conform, summary: 'Check this build against the protocol vectors.', bool: ['verbose'], value: ['vectors', 'suite'], exits: ['E_NONCONFORMANT', 'E_NOT_FOUND'] },
  adapters: { fn: workspace.adapters, summary: 'Install or check vendor instruction blocks.', bool: ['check', 'print'], value: ['vendor'], exits: ['E_DRIFT', 'E_USAGE'] },
  migrate: { fn: workspace.migrate, summary: 'Import legacy shared Markdown files into the notes wall.', bool: ['dry-run', 'freeze'], value: ['todo-done', 'updates', 'author-map'], exits: ['E_USAGE', 'E_NOT_FOUND'] },
  config: { fn: workspace.config, summary: 'Read or write .fridge/config.json.', bool: [], value: [], exits: ['E_USAGE', 'E_NOT_FOUND'] },
  version: { fn: workspace.version, summary: 'Version and protocol information.', bool: [], value: [], exits: [] },
};

const ALIASES = { note: 'pin', tidy: 'doctor', sweep: 'reap', pass: 'handoff', door: 'board' };

export function parseArgs(argv) {
  const out = { command: null, positional: [], flags: {}, rest: [] };
  const args = [...argv];
  const dd = args.indexOf('--');
  if (dd !== -1) {
    out.rest = args.slice(dd + 1);
    args.length = dd;
  }
  if (args.length && !args[0].startsWith('-')) out.command = args.shift();
  const spec = out.command ? COMMANDS[ALIASES[out.command] || out.command] : null;
  const boolFlags = new Set([...GLOBAL_BOOL, ...(spec?.bool || [])]);
  const valueFlags = new Set([...GLOBAL_VALUE, ...(spec?.value || [])]);
  for (let i = 0; i < args.length; i++) {
    const token = args[i];
    if (token === '-h' || token === '--help') { out.flags.help = true; continue; }
    if (token === '-v' || token === '--version') { out.flags.version = true; continue; }
    if (!token.startsWith('--')) { out.positional.push(token); continue; }
    const eq = token.indexOf('=');
    const name = (eq === -1 ? token.slice(2) : token.slice(2, eq)).trim();
    const inlineValue = eq === -1 ? null : token.slice(eq + 1);
    if (boolFlags.has(name)) {
      if (inlineValue !== null) out.flags[name] = inlineValue !== 'false' && inlineValue !== '0';
      else out.flags[name] = true;
      continue;
    }
    if (valueFlags.has(name)) {
      const value = inlineValue !== null ? inlineValue : args[++i];
      if (value === undefined) throw new AppError('E_USAGE', `Flag --${name} needs a value.`);
      if (name === 'exclude' || name === 'label' || name === 'claim') {
        out.flags[name] = [].concat(out.flags[name] || [], value);
      } else out.flags[name] = value;
      continue;
    }
    throw new AppError('E_USAGE', `Unknown flag --${name} for '${out.command || 'fridge'}'.`, {
      hint: `${BIN} ${out.command || ''} --help`.trim(),
    });
  }
  if (out.command) out.command = ALIASES[out.command] || out.command;
  return out;
}

function usage() {
  const rows = Object.entries(COMMANDS).map(([name, c]) => `  ${name.padEnd(11)}${c.summary}`);
  return [
    `${PRODUCT} ${VERSION} (protocol ${PROTOCOL})`,
    '',
    `${TAGLINE} Everyone pins their own note. Nobody erases the board.`,
    '',
    `usage: ${BIN} <command> [args] [flags]`,
    '',
    'commands:',
    ...rows,
    '',
    'aliases: ' + Object.entries(ALIASES).map(([a, b]) => `${a}=${b}`).join(', '),
    '',
    'global flags: --json --quiet --verbose --no-color --repo <path> --agent <name> --allow-secret-like --help',
    '',
    `60-second start:  ${BIN} init && ${BIN} join --agent me && ${BIN} claim "src/**" --task "work" && ${BIN} board`,
    `docs: https://github.com/RagnarPitla/${PACKAGE}`,
  ].join('\n');
}

function commandHelp(name) {
  const c = COMMANDS[name];
  const flags = [...c.bool.map((f) => `--${f}`), ...c.value.map((f) => `--${f} <value>`)];
  const exits = ['OK', ...c.exits].map((code) => `  ${String(EXIT[code]).padStart(3)}  ${code.padEnd(20)} ${EXIT_DOC[code]}`);
  return [
    `${BIN} ${name} - ${c.summary}`,
    '',
    `flags: ${flags.length ? flags.join(' ') : '(none beyond global flags)'}`,
    '',
    'exit codes:',
    ...exits,
  ].join('\n');
}

async function renewForInvocation(ctx) {
  const explicit = ctx.flags.agent || process.env.FRIDGE_ACTOR;
  if (!explicit || ['init', 'join', 'version', 'conform'].includes(ctx.command)) return;
  let ws;
  try {
    ws = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
  } catch {
    return; // the command reports workspace errors through its normal path
  }
  let session;
  try {
    ({ session } = requireActor(ws, { agent: explicit }));
  } catch (error) {
    if (ctx.flags.agent) throw error;
    if (error?.code === 'E_NO_SESSION') return; // stale environment identity
    throw error;
  }
  if (process.env.FRIDGE_NO_RENEW === '1') return;
  try {
    await withMutex(ws, 'renew', () => {
      const renewed = renewOwnLeases(ws, session);
      if (renewed.length) autoRender(ws);
    });
  } catch {
    // Renewal is opportunistic. Identity was validated above; the command
    // itself still reports corruption or mutex errors through its normal path.
  }
}

export async function main(argv = process.argv.slice(2)) {
  let ctx = { json: false, quiet: false, verbose: false, cwd: process.cwd(), command: 'fridge' };
  try {
    const parsed = parseArgs(argv);
    ctx = {
      ...ctx,
      ...parsed,
      json: Boolean(parsed.flags.json) || process.env.FRIDGE_JSON === '1',
      quiet: Boolean(parsed.flags.quiet),
      verbose: Boolean(parsed.flags.verbose),
    };
    if (parsed.flags.version && !parsed.command) { process.stdout.write(`${PACKAGE} ${VERSION} (${PROTOCOL})\n`); return 0; }
    if (!parsed.command) { process.stdout.write(`${usage()}\n`); return parsed.flags.help ? 0 : 0; }
    if (parsed.command === 'help') {
      const target = parsed.positional[0];
      process.stdout.write(`${target && COMMANDS[target] ? commandHelp(target) : usage()}\n`);
      return 0;
    }
    const spec = COMMANDS[parsed.command];
    if (!spec) {
      throw new AppError('E_USAGE', `Unknown command '${parsed.command}'.`, { hint: `${BIN} help` });
    }
    if (parsed.flags.help) { process.stdout.write(`${commandHelp(parsed.command)}\n`); return 0; }
    for (const key of Object.keys(process.env)) {
      if (key.startsWith('FRIDGE_') && !['FRIDGE_REPO', 'FRIDGE_ACTOR', 'FRIDGE_SESSION', 'FRIDGE_CLAIM_TOKEN', 'FRIDGE_TTL', 'FRIDGE_JSON', 'FRIDGE_NO_RENEW', 'FRIDGE_TEST', 'FRIDGE_FAULT'].includes(key)) {
        process.stderr.write(`warning: unknown environment variable ${key} (typo?)\n`);
      }
    }
    if (process.env.FRIDGE_FAULT && process.env.FRIDGE_TEST !== '1') {
      throw new AppError('E_USAGE', 'FRIDGE_FAULT is only honoured when FRIDGE_TEST=1.');
    }
    await renewForInvocation(ctx);
    const code = await spec.fn(ctx);
    return code ?? 0;
  } catch (err) {
    return emitError(ctx, ctx.command || 'fridge', err instanceof AppError ? err : Object.assign(err, { code: 'E_INTERNAL', exitCode: EXIT.E_INTERNAL }));
  }
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().then((code) => { process.exitCode = code; }).catch((e) => {
    process.stderr.write(`E_INTERNAL: ${e.stack || e.message}\n`);
    process.exitCode = 1;
  });
}
