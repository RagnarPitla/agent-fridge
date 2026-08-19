// SPDX-License-Identifier: Apache-2.0
// Notes are write-once files. Two writers can never collide, because they never
// touch the same file. This is the whole answer to the 128-lines-overwritten bug.
import { AppError } from '../core/errors.mjs';
import { emit } from '../core/output.mjs';
import { openWorkspace, pin as pinNote, readNotes, requireActor } from '../core/store.mjs';
import { maybeRenew } from '../core/renew.mjs';
import { autoRender } from '../core/render.mjs';
import { parseDuration, sleep } from '../core/util.mjs';
import { BIN } from '../brand.mjs';

const SECRETY = [
  [/-----BEGIN [A-Z ]*PRIVATE KEY-----/, 'a private key'],
  [/\bghp_[A-Za-z0-9]{20,}\b/, 'a GitHub token'],
  [/\bgithub_pat_[A-Za-z0-9_]{20,}\b/, 'a GitHub fine-grained token'],
  [/\bAKIA[0-9A-Z]{16}\b/, 'an AWS access key id'],
  [/\bsk-[A-Za-z0-9]{20,}\b/, 'an OpenAI-style key'],
  [/\bxox[baprs]-[A-Za-z0-9-]{10,}\b/, 'a Slack token'],
  [/\b(password|passwd|secret|api[_-]?key|client[_-]?secret)\s*[=:]\s*\S{8,}/i, 'a credential assignment'],
];

export function looksSecret(text) {
  for (const [re, what] of SECRETY) if (re.test(text)) return what;
  return null;
}

const readStdin = async () => {
  if (process.stdin.isTTY) return '';
  const chunks = [];
  for await (const c of process.stdin) chunks.push(c);
  return Buffer.concat(chunks).toString('utf8').trim();
};

export async function pin(ctx) {
  const ws = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  maybeRenew(ws, session);
  let text = ctx.positional.join(' ').trim();
  if (!text) text = await readStdin();
  if (!text) {
    throw new AppError('E_USAGE', 'A note needs some words.', { hint: `${BIN} pin "rewrote the retry loop in src/api"` });
  }
  const found = looksSecret(text);
  if (found && !ctx.flags['allow-secret-like']) {
    throw new AppError('E_USAGE', `That note looks like it contains ${found}.`, {
      hint: 'Notes are committed history. Remove it, or pass --allow-secret-like if it is a false positive.',
    });
  }
  const kind = ctx.flags.kind || 'note';
  const note = pinNote(ws, {
    type: `note.${kind}`, actor, session,
    subject: ctx.flags.claim ? { kind: 'claim', id: [].concat(ctx.flags.claim)[0] } : null,
    summary: text.split('\n')[0].slice(0, 300),
    data: { body: text, kind, task: ctx.flags.task || null },
  });
  autoRender(ws);
  return emit(ctx, 'pin', { data: { noteId: note.id, ts: note.ts, type: note.type, summary: note.summary }, text: `pinned ${note.id}` });
}

const fmt = (n) => `${n.ts}  ${String(n.actorName).padEnd(14).slice(0, 14)}  ${String(n.type).padEnd(18).slice(0, 18)}  ${n.summary}`;

export async function log(ctx) {
  const ws = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
  const limit = ctx.flags.limit ? Number(ctx.flags.limit) : 50;
  if (!Number.isFinite(limit) || limit < 1) throw new AppError('E_USAGE', '--limit must be a positive number.');
  const opts = {
    limit,
    actor: ctx.flags.actor || null,
    type: ctx.flags.type || null,
    since: ctx.flags.since ? Date.now() - parseDuration(ctx.flags.since, 'since') : null,
    until: ctx.flags.until ? Date.now() - parseDuration(ctx.flags.until, 'until') : null,
  };
  if (!ctx.flags.follow) {
    const notes = readNotes(ws, opts);
    return emit(ctx, 'log', { data: { notes }, text: notes.map(fmt).join('\n') || 'no notes yet' });
  }
  const seen = new Set(readNotes(ws, { ...opts, limit: 1000 }).map((n) => n.id));
  for (const n of readNotes(ws, opts)) process.stdout.write(`${fmt(n)}\n`);
  for (;;) {
    await sleep(750);
    for (const n of readNotes(ws, { ...opts, limit: 200 })) {
      if (seen.has(n.id)) continue;
      seen.add(n.id);
      process.stdout.write(`${fmt(n)}\n`);
    }
  }
}
