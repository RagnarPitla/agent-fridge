// SPDX-License-Identifier: Apache-2.0
// Handoffs, inbox, doctor, and the multi-process simulation.
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { AppError } from '../core/errors.mjs';
import { parseDuration } from '../core/util.mjs';
import { emit } from '../core/output.mjs';
import { withMutex } from '../core/mutex.mjs';
import { exists, listJson, readJsonSafe, rmrf, unlinkQuiet, walkJson, writeAtomic, writeJsonAtomic } from '../core/fsx.mjs';
import {
  archiveClaim, deleteMessage, findMessage, listActors, listClaims, listMessages, openWorkspace, pin, readActor,
  readClaim, readSession, reapStale, requireActor, saveClaim, writeLease, writeMessage, writeSession, GITIGNORE,
} from '../core/store.mjs';
import { autoRender, doorDrift, renderDoor } from '../core/render.mjs';
import { hostId, humanMs, newId, nowIso, processAlive, randomToken, sha256, slug } from '../core/util.mjs';
import * as adapterTemplates from '../adapters/templates.mjs';
import { BIN, PRODUCT, PROTOCOL, STATE_DIR } from '../brand.mjs';

const open = (ctx) => openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });

export async function handoff(ctx) {
  const ws = open(ctx);
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  const id = ctx.positional[0];
  const to = ctx.flags.to;
  if (!id) throw new AppError('E_USAGE', 'Which card?', { hint: `${BIN} handoff <claim-id> --to <housemate> --note "..."` });
  if (!to) throw new AppError('E_USAGE', '--to <housemate> is required.', { hint: `${BIN} handoff ${id} --to claude-b --note "half done"` });
  const target = readActor(ws, to);
  if (!target) {
    throw new AppError('E_NOT_FOUND', `Nobody named '${to}' is on this door.`, {
      hint: `Known: ${listActors(ws).map((a) => a.name).join(', ') || '(nobody yet)'}`,
    });
  }
  const d = readClaim(ws, id);
  if (!d) throw new AppError('E_NOT_FOUND', `No card ${id}.`, { hint: `${BIN} board` });
  if (d.claim.sessionId !== session.id && !ctx.flags.force) {
    throw new AppError('E_NOT_OWNER', `Card ${id} belongs to ${d.claim.actorName}, not you.`, {
      hint: 'You can only hand off your own cards.',
    });
  }
  const message = {
    schema: 'wcp/0.1/message',
    id: newId('msg'),
    kind: 'handoff',
    claimId: id,
    fromName: actor.name,
    fromSessionId: session.id,
    toName: target.name,
    note: ctx.flags.note || null,
    reason: ctx.flags.reason || null,
    createdAt: nowIso(),
    scope: d.claim.scope.include,
    task: d.claim.task,
    state: 'offered',
    writer: ws.config.writer,
  };
  await withMutex(ws, 'handoff', () => {
    writeMessage(ws, message);
    saveClaim(ws, { ...d.claim, state: 'handoff-offered', offeredTo: target.name, offeredMessageId: message.id, updatedAt: nowIso() });
  });
  pin(ws, {
    type: 'handoff.offered', actor, session,
    subject: { kind: 'claim', id },
    summary: `${actor.name} offered ${d.claim.scope.include.join(', ')} to ${target.name}${ctx.flags.note ? `: ${ctx.flags.note}` : ''}`,
    data: { messageId: message.id, to: target.name, note: message.note, reason: message.reason },
  });
  const text = [
    `Offered card ${id} to ${target.name}.`,
    `  message  ${message.id}`,
    `  scope    ${d.claim.scope.include.join(', ')}`,
    '',
    `They accept with: ${BIN} accept ${message.id} --agent ${target.name}`,
    'The card stays yours until they accept, so nothing is ever unowned.',
  ].join('\n');
  autoRender(ws);
  return emit(ctx, 'handoff', { data: { messageId: message.id, claimId: id, to: target.name }, text });
}

export async function accept(ctx) {
  const ws = open(ctx);
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  const key = ctx.positional[0];
  if (!key) throw new AppError('E_USAGE', 'Accept which offer?', { hint: `${BIN} inbox` });
  const message = findMessage(ws, actor.name, key) || listMessages(ws, actor.name).find((m) => m.claimId === key);
  if (!message) throw new AppError('E_NOT_FOUND', `No offer '${key}' in your inbox.`, { hint: `${BIN} inbox` });
  const result = await withMutex(ws, 'accept', () => {
    const d = readClaim(ws, message.claimId);
    if (!d) {
      deleteMessage(ws, actor.name, message.id);
      throw new AppError('E_NOT_FOUND', `Card ${message.claimId} is already gone.`, { hint: `${BIN} claim ... to take the work fresh` });
    }
    const token = randomToken();
    const updated = {
      ...d.claim,
      actorId: actor.id,
      actorName: actor.name,
      vendor: actor.vendor,
      sessionId: session.id,
      host: hostId(),
      process: { pid: process.pid, ppid: process.ppid, startedAt: nowIso() },
      state: 'active',
      offeredTo: null,
      offeredMessageId: null,
      tokenHash: sha256(token),
      handoffHistory: [...(d.claim.handoffHistory || []), { from: message.fromName, to: actor.name, at: nowIso(), note: message.note }],
      updatedAt: nowIso(),
    };
    saveClaim(ws, updated);
    writeLease(ws, updated.id, { sessionId: session.id, ttlMs: updated.ttlMs, renewals: 0 });
    session.tokens = { ...(session.tokens || {}), [updated.id]: token };
    writeSession(ws, session);
    deleteMessage(ws, actor.name, message.id);
    return updated;
  });
  pin(ws, {
    type: 'handoff.accepted', actor, session,
    subject: { kind: 'claim', id: result.id },
    summary: `${actor.name} took over ${result.scope.include.join(', ')} from ${message.fromName}`,
    data: { messageId: message.id, from: message.fromName, note: message.note },
  });
  const text = [
    `Card ${result.id} is now yours (from ${message.fromName}).`,
    `  scope  ${result.scope.include.join(', ')}`,
    message.note ? `  note   ${message.note}` : '',
    '',
    `When you stop: ${BIN} release ${result.id} --outcome done --note "..."`,
  ].filter(Boolean).join('\n');
  autoRender(ws);
  return emit(ctx, 'accept', { data: { claimId: result.id, from: message.fromName, scope: result.scope.include }, text });
}

export async function decline(ctx) {
  const ws = open(ctx);
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  const key = ctx.positional[0];
  if (!key) throw new AppError('E_USAGE', 'Decline which offer?', { hint: `${BIN} inbox` });
  const message = findMessage(ws, actor.name, key) || listMessages(ws, actor.name).find((m) => m.claimId === key);
  if (!message) throw new AppError('E_NOT_FOUND', `No offer '${key}' in your inbox.`, { hint: `${BIN} inbox` });
  await withMutex(ws, 'decline', () => {
    const d = readClaim(ws, message.claimId);
    if (d) saveClaim(ws, { ...d.claim, state: 'active', offeredTo: null, offeredMessageId: null, updatedAt: nowIso() });
    deleteMessage(ws, actor.name, message.id);
  });
  pin(ws, {
    type: 'handoff.declined', actor, session,
    subject: { kind: 'claim', id: message.claimId },
    summary: `${actor.name} declined ${message.fromName}'s handoff${ctx.flags.reason ? `: ${ctx.flags.reason}` : ''}`,
    data: { messageId: message.id, from: message.fromName, reason: ctx.flags.reason || null },
  });
  autoRender(ws);
  return emit(ctx, 'decline', { data: { messageId: message.id, claimId: message.claimId }, text: `declined ${message.id}; the card stays with ${message.fromName}` });
}

export async function inbox(ctx) {
  const ws = open(ctx);
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  const messages = listMessages(ws, actor.name);
  const text = messages.length
    ? messages.map((m) => [
      `${m.id}  ${m.kind}  from ${m.fromName}`,
      `  card   ${m.claimId}  ${m.scope.join(', ')}`,
      m.task ? `  task   ${m.task}` : '',
      m.note ? `  note   ${m.note}` : '',
      `  accept: ${BIN} accept ${m.id}    decline: ${BIN} decline ${m.id}`,
    ].filter(Boolean).join('\n')).join('\n\n')
    : 'nothing addressed to you';
  return emit(ctx, 'inbox', { data: { actor: actor.name, messages }, text });
}

const SYNC_HINTS = ['Dropbox', 'OneDrive', 'Google Drive', 'iCloud Drive', 'Creative Cloud Files'];
const DOCTOR_PASSES = 4;

function scanWorkspace(ws) {
  const findings = [];
  const add = (id, severity, message, opts = {}) => findings.push({ id, severity, message, fixable: Boolean(opts.fix), fixed: false, hint: opts.hint || null });

  if (!exists(ws.paths.version)) add('version-missing', 'error', `${STATE_DIR}/VERSION is missing.`, { fix: true });
  if (!exists(path.join(ws.paths.dir, '.gitignore'))) add('gitignore-missing', 'warn', `${STATE_DIR}/.gitignore is missing; live state could be committed.`, { fix: true });

  const scannable = (f) => !f.startsWith(ws.paths.quarantine + path.sep) && !f.startsWith(ws.paths.tmp + path.sep);
  for (const file of walkJson(ws.paths.dir).filter(scannable)) {
    const r = readJsonSafe(file);
    if (!r.ok) add(`corrupt:${path.relative(ws.root, file)}`, 'error', `Unreadable JSON: ${path.relative(ws.root, file)}`, { fix: true, hint: 'moved to .fridge/quarantine/ by --fix' });
  }

  const claims = listClaims(ws);
  const stale = claims.filter((d) => d.stale);
  if (stale.length) add('stale-claims', 'warn', `${stale.length} card(s) have fallen off the door.`, { fix: true, hint: `${BIN} reap` });
  for (const d of claims) {
    if (!d.lease) add(`lease-missing:${d.claim.id}`, 'warn', `Card ${d.claim.id} has no lease file.`, { fix: true });
    if (d.claim.host !== hostId()) add(`foreign-host:${d.claim.id}`, 'info', `Card ${d.claim.id} was taken on another machine; liveness cannot be checked here.`);
  }
  for (const file of listJson(ws.paths.leases)) {
    const r = readJsonSafe(file);
    if (r.ok && !exists(path.join(ws.paths.claims, `${r.value.claimId}.json`))) {
      add(`orphan-lease:${r.value.claimId}`, 'warn', `Lease for missing card ${r.value.claimId}.`, { fix: true });
    }
  }

  let tmpJunk = 0;
  try {
    for (const f of fs.readdirSync(ws.paths.tmp)) {
      const st = fs.statSync(path.join(ws.paths.tmp, f));
      if (Date.now() - st.mtimeMs > 3600000) tmpJunk++;
    }
  } catch { /* no tmp dir yet */ }
  if (tmpJunk) add('tmp-junk', 'info', `${tmpJunk} stale temp file(s) from interrupted writes.`, { fix: true });

  if (exists(ws.paths.mutex)) {
    const owner = readJsonSafe(path.join(ws.paths.mutex, 'owner.json'));
    const ageMs = Date.now() - (owner.ok ? Date.parse(owner.value.acquiredAt) : 0);
    const dead = owner.ok && owner.value.host === hostId() && !processAlive(owner.value.pid);
    if (dead || ageMs > ws.config.mutex.staleMs) {
      add('mutex-held', 'warn', `The registry lock is held${dead ? ' by a dead process' : ` for ${humanMs(ageMs)}`}.`, { fix: true });
    }
  }

  const doorOnDisk = exists(ws.paths.door) ? fs.readFileSync(ws.paths.door, 'utf8') : null;
  if (doorDrift(ws, doorOnDisk).drift) add('door-drift', 'info', 'DOOR.md is out of date.', { fix: true, hint: `${BIN} render` });

  for (const key of Object.keys(adapterTemplates.VENDORS)) {
    const st = adapterTemplates.statusFor(ws.root, key);
    if (st.state === 'drifted') add(`adapter-drift:${key}`, 'warn', `${st.file} has an out-of-date instruction block.`, { fix: true, hint: `${BIN} adapters install` });
  }

  for (const hint of SYNC_HINTS) {
    if (ws.root.includes(hint)) add('cloud-sync', 'warn', `This repository lives under ${hint}. File sync can duplicate or delay ${STATE_DIR}/ writes; coordination guarantees are weaker.`);
  }
  try {
    if (fs.lstatSync(ws.paths.dir).isSymbolicLink()) add('state-symlink', 'warn', `${STATE_DIR} is a symlink. Only do this if you understand where it points.`);
  } catch { /* checked above */ }

  return findings;
}

async function applyFix(ws, f) {
  if (f.id === 'version-missing') writeAtomic(ws.paths.version, `${PROTOCOL}\n`, ws.paths.tmp);
  else if (f.id === 'gitignore-missing') writeAtomic(path.join(ws.paths.dir, '.gitignore'), GITIGNORE, ws.paths.tmp);
  else if (f.id === 'stale-claims') await withMutex(ws, 'doctor', () => reapStale(ws, {}));
  else if (f.id.startsWith('lease-missing:')) {
    const d = readClaim(ws, f.id.split(':')[1]);
    if (d) writeLease(ws, d.claim.id, { sessionId: d.claim.sessionId, ttlMs: d.claim.ttlMs, renewals: 0 });
  } else if (f.id.startsWith('orphan-lease:')) unlinkQuiet(path.join(ws.paths.leases, `${f.id.split(':')[1]}.json`));
  else if (f.id === 'tmp-junk') { rmrf(ws.paths.tmp); fs.mkdirSync(ws.paths.tmp, { recursive: true }); }
  else if (f.id === 'mutex-held') rmrf(ws.paths.mutex);
  else if (f.id === 'door-drift') writeAtomic(ws.paths.door, renderDoor(ws), ws.paths.tmp);
  else if (f.id.startsWith('adapter-drift:')) adapterTemplates.install(ws.root, [f.id.split(':')[1]], { tmpDir: ws.paths.tmp });
  else if (f.id.startsWith('corrupt:')) {
    const rel = f.id.slice('corrupt:'.length);
    const from = path.join(ws.root, rel);
    const to = path.join(ws.paths.quarantine, `${Date.now()}--${rel.split(path.sep).join('__')}`);
    fs.mkdirSync(ws.paths.quarantine, { recursive: true });
    try { fs.renameSync(from, to); } catch { /* already gone */ }
  }
  f.fixed = true;
}

export async function doctor(ctx) {
  const ws = open(ctx);
  const fix = Boolean(ctx.flags.fix);
  let findings = scanWorkspace(ws);
  const repaired = [];

  // Repairing one thing can uncover the next (quarantining a card orphans its
  // lease, every fix re-dirties the door), so fix to a fixed point.
  if (fix) {
    for (let pass = 0; pass < DOCTOR_PASSES; pass += 1) {
      const fixable = findings.filter((f) => f.fixable);
      if (!fixable.length) break;
      for (const f of fixable) { await applyFix(ws, f); repaired.push(f); }
      findings = scanWorkspace(ws);
    }
  }

  const byId = new Map();
  for (const f of repaired) byId.set(f.id, f);
  for (const f of findings) byId.set(f.id, f); // a finding that survived its fix reports as unfixed
  const report = [...byId.values()];
  const outstanding = findings.filter((f) => f.severity === 'error' || f.fixable);
  const text = report.length
    ? report.map((f) => `${f.fixed ? 'FIXED' : f.severity.toUpperCase().padEnd(5)}  ${f.message}${f.hint && !f.fixed ? `  (${f.hint})` : ''}`).join('\n')
    : 'The door is tidy. Nothing to fix.';
  if (ctx.flags.check && outstanding.length) {
    throw new AppError('E_DRIFT', `${outstanding.length} finding(s) need attention.`, { hint: `${BIN} doctor --fix`, details: { report: text, findings: report } });
  }
  return emit(ctx, 'doctor', { data: { findings: report, fixed: repaired.length, outstanding: outstanding.length }, text });
}

const WORKER = fileURLToPath(new URL('../../tools/worker.mjs', import.meta.url));

export async function simulate(ctx) {
  const ws = open(ctx);
  const agents = Math.max(2, Number(ctx.flags.agents || 6));
  const durationMs = Math.max(1000, ctx.flags.duration ? parseDuration(ctx.flags.duration, 'duration') : 8000);
  const seed = Number(ctx.flags.seed || 1234);
  const startedAt = Date.now();
  const workers = [];
  for (let i = 0; i < agents; i++) {
    workers.push(new Promise((resolve) => {
      const child = spawn(process.execPath, [WORKER], {
        cwd: ws.root,
        env: { ...process.env, FRIDGE_SIM_INDEX: String(i), FRIDGE_SIM_SEED: String(seed + i), FRIDGE_SIM_DURATION: String(durationMs), FRIDGE_SIM_ROOT: ws.root },
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      let out = ''; let err = '';
      child.stdout.on('data', (d) => { out += d; });
      child.stderr.on('data', (d) => { err += d; });
      child.on('close', (code) => {
        let stats = null;
        try { stats = JSON.parse(out.trim().split('\n').pop()); } catch { /* worker crashed before reporting */ }
        resolve({ index: i, code, stats, stderr: err.slice(-2000) });
      });
    }));
  }
  const results = await Promise.all(workers);
  const elapsedMs = Date.now() - startedAt;

  // Only grade what this run produced. Claims already on the door when the
  // simulation started are somebody else's business, and counting them turns a
  // pre-populated workspace into a false I2 failure.
  const notes = walkJson(ws.paths.notes).map(readJsonSafe).filter((r) => r.ok).map((r) => r.value)
    .filter((n) => Date.parse(n.ts) >= startedAt);
  const acquired = notes.filter((n) => n.type === 'claim.acquired');
  const released = notes.filter((n) => n.type === 'claim.released' || n.type === 'claim.expired');
  const denied = notes.filter((n) => n.type === 'claim.denied');
  const pinned = notes.filter((n) => n.type.startsWith('note.'));

  const expectedPins = results.reduce((a, r) => a + (r.stats?.pins || 0), 0);
  const invariants = [
    { id: 'I1-no-lost-notes', ok: pinned.length >= expectedPins, detail: `${pinned.length} pinned notes on disk, workers reported ${expectedPins}` },
    { id: 'I2-no-double-ownership', ok: true, detail: '' },
    { id: 'I3-no-crash', ok: results.every((r) => r.code === 0), detail: results.filter((r) => r.code !== 0).map((r) => `worker ${r.index} exited ${r.code}: ${r.stderr}`).join(' | ') || 'all workers exited 0' },
    { id: 'I5-state-readable', ok: walkJson(ws.paths.dir).every((f) => readJsonSafe(f).ok), detail: 'every JSON record parses' },
  ];

  const byScope = new Map();
  for (const n of acquired) {
    for (const p of n.data.include) {
      const list = byScope.get(p) || [];
      list.push({ start: Date.parse(n.ts), actor: n.actorName, claimId: n.subject?.id });
      byScope.set(p, list);
    }
  }
  const closeAt = new Map();
  for (const n of released) if (n.subject?.id) closeAt.set(n.subject.id, Date.parse(n.ts));
  const overlaps = [];
  for (const [scope, list] of byScope) {
    const spans = list.map((c) => ({ ...c, end: closeAt.get(c.claimId) ?? Date.now() })).sort((a, b) => a.start - b.start);
    for (let i = 1; i < spans.length; i++) {
      if (spans[i].start < spans[i - 1].end && spans[i].actor !== spans[i - 1].actor) {
        overlaps.push({ scope, a: spans[i - 1], b: spans[i] });
      }
    }
  }
  invariants[1].ok = overlaps.length === 0;
  invariants[1].detail = overlaps.length ? `${overlaps.length} overlapping exclusive holds on the same scope` : 'no scope was exclusively held by two actors at once';

  const ok = invariants.every((i) => i.ok);
  const report = [
    `# ${PRODUCT} concurrency simulation`,
    '',
    `- agents: ${agents}`,
    `- duration: ${humanMs(elapsedMs)}`,
    `- seed: ${seed}`,
    `- node: ${process.version} on ${process.platform}/${process.arch}`,
    '',
    '## Traffic',
    '',
    `| metric | count |`,
    `|---|---|`,
    `| claims acquired | ${acquired.length} |`,
    `| claims denied (conflict, exit 10) | ${denied.length} |`,
    `| claims released or expired | ${released.length} |`,
    `| notes pinned | ${pinned.length} |`,
    `| total note files | ${notes.length} |`,
    '',
    '## Invariants',
    '',
    '| invariant | result | detail |',
    '|---|---|---|',
    ...invariants.map((i) => `| ${i.id} | ${i.ok ? 'PASS' : 'FAIL'} | ${i.detail} |`),
    '',
    `Result: ${ok ? 'PASS' : 'FAIL'}`,
    '',
  ].join('\n');
  if (ctx.flags.report) writeAtomic(path.resolve(ws.root, ctx.flags.report), report, ws.paths.tmp);
  if (!ok) {
    throw new AppError('E_INTERNAL', 'Simulation violated an invariant.', { details: { report, invariants } });
  }
  return emit(ctx, 'simulate', {
    data: { agents, elapsedMs, seed, counts: { acquired: acquired.length, denied: denied.length, released: released.length, pinned: pinned.length }, invariants, ok },
    text: report,
  });
}
