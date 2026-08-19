// SPDX-License-Identifier: Apache-2.0
// Workspace resolution and record IO. One writer per record, always.
import fs from 'node:fs';
import path from 'node:path';
import { AppError } from './errors.mjs';
import {
  createJsonExclusive, ensureDir, exists, listJson, readJsonSafe, walkJson, writeAtomic, writeJsonAtomic,
} from './fsx.mjs';
import { compactTs, hostId, newId, nowIso, processAlive, slug, stableStringify } from './util.mjs';
import { PRODUCT, PROTOCOL, STATE_DIR, WRITER } from '../brand.mjs';

export const DEFAULT_CONFIG = (workspaceId) => ({
  schema: 'wcp/0.1/config',
  workspaceId,
  lease: { defaultTtlMs: 900000, maxTtlMs: 14400000, renewOnAnyCommand: true, renewThresholdRatio: 0.5, graceMs: 60000 },
  mutex: { acquireTimeoutMs: 10000, staleMs: 15000, maxHoldMs: 2000 },
  paths: { caseSensitivity: 'auto', unicodeNormalization: 'NFC', strictExcludes: false, materializeLimit: 5000, allowGlobalClaims: false },
  notes: { commit: true, retainDays: 0 },
  door: { path: `${STATE_DIR}/DOOR.md`, autoRender: true, extraTargets: [] },
  git: { readOnly: true, warnOnSyncedFolder: true },
  policy: { requireTaskOnClaim: true, requireClaimForWrite: 'advisory' },
  writer: WRITER,
});

const deepMerge = (base, over) => {
  const out = { ...base };
  for (const [k, v] of Object.entries(over || {})) {
    out[k] = v && typeof v === 'object' && !Array.isArray(v) && base[k] && typeof base[k] === 'object'
      ? deepMerge(base[k], v)
      : v;
  }
  return out;
};

export function statePaths(root) {
  const dir = path.join(root, STATE_DIR);
  return {
    dir,
    version: path.join(dir, 'VERSION'),
    config: path.join(dir, 'config.json'),
    workspace: path.join(dir, 'workspace.json'),
    door: path.join(dir, 'DOOR.md'),
    actors: path.join(dir, 'actors'),
    sessions: path.join(dir, 'sessions'),
    claims: path.join(dir, 'claims'),
    leases: path.join(dir, 'leases'),
    notes: path.join(dir, 'notes'),
    queue: path.join(dir, 'queue'),
    inbox: path.join(dir, 'inbox'),
    locks: path.join(dir, 'locks'),
    mutex: path.join(dir, 'locks', 'registry.lock.d'),
    tmp: path.join(dir, 'tmp'),
    archive: path.join(dir, 'archive', 'claims'),
    quarantine: path.join(dir, 'quarantine'),
    views: path.join(dir, 'views'),
  };
}

export function findRoot(start = process.cwd()) {
  let dir = path.resolve(start);
  let gitRoot = null;
  for (;;) {
    if (exists(path.join(dir, STATE_DIR))) return { root: dir, initialized: true };
    if (!gitRoot && exists(path.join(dir, '.git'))) gitRoot = dir;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return { root: gitRoot || path.resolve(start), initialized: false };
}

export function openWorkspace({ repo, cwd = process.cwd(), requireInit = true } = {}) {
  const start = repo ? path.resolve(cwd, repo) : cwd;
  const { root, initialized } = findRoot(start);
  const paths = statePaths(root);
  if (!initialized) {
    if (requireInit) {
      throw new AppError('E_NOT_INITIALIZED', `No ${STATE_DIR}/ found from ${start} upward.`, { hint: 'fridge init' });
    }
    return { root, paths, initialized: false, config: DEFAULT_CONFIG(newId('wsp')), cwd };
  }
  const versionRaw = fs.existsSync(paths.version) ? fs.readFileSync(paths.version, 'utf8').trim() : '';
  if (!versionRaw) {
    throw new AppError('E_STATE_CORRUPT', `${STATE_DIR}/VERSION is missing or empty.`, { hint: 'fridge doctor --fix' });
  }
  if (versionRaw !== PROTOCOL) {
    throw new AppError('E_PROTOCOL_VERSION', `Workspace speaks ${versionRaw}; this binary speaks ${PROTOCOL}.`, {
      hint: 'Upgrade agent-fridge, or use a matching version.',
    });
  }
  const loaded = readJsonSafe(paths.config);
  if (!loaded.ok) throw new AppError('E_STATE_CORRUPT', `Unreadable config: ${paths.config}`, { hint: 'fridge doctor --fix' });
  const config = deepMerge(DEFAULT_CONFIG(loaded.value.workspaceId || newId('wsp')), loaded.value);
  return { root, paths, initialized: true, config, cwd, version: versionRaw };
}

/**
 * The split between machine-local state and shared history.
 *
 * `notes.commit: false` has to be honoured here, not just in documentation:
 * the notes wall is only kept out of Git if the ignore file stops un-ignoring
 * it. Callers rewrite this file whenever that setting changes.
 */
export const gitignoreFor = ({ commitNotes = true } = {}) => `# Managed by ${PRODUCT} (${PROTOCOL}).
# Live coordination state is machine-local.${commitNotes ? ' The notes wall is shared history.' : ' notes.commit is false, so the notes wall stays local too.'}
/*
!/.gitignore
!/VERSION
!/config.json
!/workspace.json
${commitNotes ? '!/notes/\n' : ''}!/actors/
`;

export const GITIGNORE = gitignoreFor({ commitNotes: true });

export function writeGitignore(ws) {
  const commitNotes = ws.config?.notes?.commit !== false;
  return writeAtomic(path.join(ws.paths.dir, '.gitignore'), gitignoreFor({ commitNotes }), ws.paths.tmp);
}

export function initWorkspace(root, { force = false } = {}) {
  const paths = statePaths(root);
  if (exists(paths.dir) && !force) {
    throw new AppError('E_ALREADY_EXISTS', `${STATE_DIR}/ already exists in ${root}.`, { hint: 'Use --force to re-write config and ignore rules.' });
  }
  for (const key of ['dir', 'actors', 'sessions', 'claims', 'leases', 'notes', 'queue', 'inbox', 'locks', 'tmp', 'archive', 'quarantine', 'views']) {
    ensureDir(paths[key]);
  }
  fs.chmodSync(paths.sessions, 0o700);
  const workspaceId = newId('wsp');
  writeAtomic(paths.version, `${PROTOCOL}\n`, paths.tmp);
  writeAtomic(path.join(paths.dir, '.gitignore'), GITIGNORE, paths.tmp);
  writeJsonAtomic(paths.workspace, {
    schema: 'wcp/0.1/workspace', workspaceId, createdAt: nowIso(), createdOnHost: hostId(), writer: WRITER,
  }, paths.tmp);
  if (!exists(paths.config) || force) writeJsonAtomic(paths.config, DEFAULT_CONFIG(workspaceId), paths.tmp);
  return { root, paths, workspaceId };
}

export function ensureGitAttributes(root) {
  const file = path.join(root, '.gitattributes');
  const lines = [
    `${STATE_DIR}/notes/** -text -merge`,
    `${STATE_DIR}/DOOR.md  linguist-generated=true`,
    `${STATE_DIR}/views/** linguist-generated=true`,
  ];
  let current = '';
  try { current = fs.readFileSync(file, 'utf8'); } catch { current = ''; }
  const missing = lines.filter((l) => !current.includes(l.split(' ')[0]));
  if (!missing.length) return false;
  const next = (current && !current.endsWith('\n') ? current + '\n' : current)
    + (current ? '\n' : '') + `# ${PRODUCT}\n` + missing.join('\n') + '\n';
  fs.writeFileSync(file, next);
  return true;
}

// ---------------------------------------------------------------- actors

export const actorFile = (ws, name) => path.join(ws.paths.actors, `${slug(name)}.json`);

export function listActors(ws) {
  return listJson(ws.paths.actors).map(readJsonSafe).filter((r) => r.ok).map((r) => r.value);
}

export function readActor(ws, name) {
  const r = readJsonSafe(actorFile(ws, name));
  return r.ok ? r.value : null;
}

export function resolveActorName(ws, explicit, { mutating = false } = {}) {
  if (explicit) return explicit;
  if (process.env.FRIDGE_ACTOR) return process.env.FRIDGE_ACTOR;
  const actors = listActors(ws);
  if (actors.length === 0) {
    throw new AppError('E_NO_SESSION', 'Nobody has put their name on the door yet.', { hint: 'fridge join --agent <your-name>' });
  }
  // Being the only name on the door is not proof of who is typing. Two
  // terminals share one checkout, so inheriting the sole actor silently gives
  // the second terminal the first one's claims, which is exactly the failure
  // this tool exists to prevent. Reads may guess; writes may not.
  if (mutating) {
    throw new AppError('E_NO_SESSION', 'This command changes the door, so it has to know who you are.', {
      hint: `Pass --agent <name>, or export FRIDGE_ACTOR=<name>. On the door: ${actors.map((a) => a.name).join(', ')}`,
    });
  }
  if (actors.length === 1) return actors[0].name;
  throw new AppError('E_NO_SESSION', `More than one housemate is on this door (${actors.map((a) => a.name).join(', ')}).`, {
    hint: 'Pass --agent <name>, or export FRIDGE_ACTOR=<name>.',
  });
}

export function joinActor(ws, { name, vendor = 'other' }) {
  const existing = readActor(ws, name);
  const actorId = existing?.id || newId('act');
  const sessionId = existing?.currentSessionId && exists(sessionFile(ws, existing.currentSessionId))
    ? existing.currentSessionId
    : newId('ses');
  const actor = {
    schema: 'wcp/0.1/actor',
    id: actorId,
    name,
    slug: slug(name),
    vendor: vendor || existing?.vendor || 'other',
    host: hostId(),
    user: process.env.USER || process.env.USERNAME || 'unknown',
    createdAt: existing?.createdAt || nowIso(),
    lastSeenAt: nowIso(),
    currentSessionId: sessionId,
    writer: WRITER,
  };
  writeJsonAtomic(actorFile(ws, name), actor, ws.paths.tmp);
  const prior = readSession(ws, sessionId);
  const session = {
    schema: 'wcp/0.1/session',
    id: sessionId,
    actorId,
    actorName: name,
    startedAt: prior?.startedAt || nowIso(),
    updatedAt: nowIso(),
    pid: process.pid,
    host: hostId(),
    seq: prior?.seq || 0,
    tokens: prior?.tokens || {},
    writer: WRITER,
  };
  writeSession(ws, session);
  return { actor, session, resumed: Boolean(prior) };
}

// ---------------------------------------------------------------- sessions

export const sessionFile = (ws, id) => path.join(ws.paths.sessions, `${id}.json`);

export function readSession(ws, id) {
  if (!id) return null;
  const r = readJsonSafe(sessionFile(ws, id));
  return r.ok ? r.value : null;
}

export function writeSession(ws, session) {
  ensureDir(ws.paths.sessions);
  const file = sessionFile(ws, session.id);
  writeJsonAtomic(file, { ...session, updatedAt: nowIso() }, ws.paths.tmp);
  try { fs.chmodSync(file, 0o600); } catch { /* Windows has no POSIX modes */ }
  return session;
}

/**
 * Piggyback renewal: running any command at all is proof the session is alive.
 *
 * This lives next to identity resolution rather than being sprinkled over
 * individual commands, because `lease.renewOnAnyCommand` is documented as
 * *any* command, and the version that was sprinkled kept missing some.
 */
export function renewOwnLeases(ws, session) {
  if (!session || process.env.FRIDGE_NO_RENEW === '1') return [];
  if (!ws.config?.lease?.renewOnAnyCommand) return [];
  const ratio = ws.config.lease.renewThresholdRatio;
  const renewed = [];
  for (const d of listClaims(ws, { tolerateCorrupt: true })) {
    const c = d.claim;
    if (c.sessionId !== session.id) continue;
    if (d.stale) continue;
    const ttl = c.ttlMs || ws.config.lease.defaultTtlMs;
    if (d.expiresInMs > ttl * ratio) continue;
    writeLease(ws, c.id, { sessionId: session.id, ttlMs: ttl, renewals: (d.lease?.renewals || 0) + 1 });
    renewed.push(c.id);
  }
  return renewed;
}

export function requireActor(ws, { agent, vendor, mutating = false, renew = true } = {}) {
  const name = resolveActorName(ws, agent, { mutating });
  const actor = readActor(ws, name);
  if (!actor) {
    if (agent || process.env.FRIDGE_ACTOR) return joinActor(ws, { name, vendor });
    throw new AppError('E_NO_SESSION', `No housemate named '${name}' on this door.`, { hint: `fridge join --agent ${name}` });
  }
  let session = readSession(ws, actor.currentSessionId);
  if (!session) ({ session } = joinActor(ws, { name, vendor: actor.vendor }));
  ws.actor = actor;
  ws.session = session;
  ws.sessionId = session.id;
  if (renew) {
    try { ws.renewed = renewOwnLeases(ws, session); } catch { /* renewal must never be the thing that fails a command */ }
  }
  return { actor, session };
}

/**
 * Read-modify-write of one session record, always from what is on disk.
 *
 * A session object read before the mutex is a photograph, and writing it back
 * afterwards silently reverts anything another process in the same session
 * recorded meanwhile. Callers mutate the fresh copy this hands them.
 */
export function mutateSession(ws, sessionOrId, fn) {
  const id = typeof sessionOrId === 'string' ? sessionOrId : sessionOrId?.id;
  if (!id) return null;
  const fresh = readSession(ws, id) || (typeof sessionOrId === 'object' ? { ...sessionOrId } : null);
  if (!fresh) return null;
  fn(fresh);
  writeSession(ws, fresh);
  if (typeof sessionOrId === 'object' && sessionOrId) {
    sessionOrId.tokens = fresh.tokens;
    sessionOrId.seq = fresh.seq;
  }
  return fresh;
}

// ---------------------------------------------------------------- notes

export function pin(ws, { type, actor, session, subject = null, summary = '', data = {} }) {
  const ts = nowIso();
  const d = new Date(ts);
  const dir = path.join(ws.paths.notes, String(d.getUTCFullYear()),
    String(d.getUTCMonth() + 1).padStart(2, '0'), String(d.getUTCDate()).padStart(2, '0'));
  let seq = 0;
  if (session) {
    seq = (session.seq || 0) + 1;
    session.seq = seq;
    try { writeSession(ws, session); } catch { /* seq is advisory */ }
  }
  for (let attempt = 0; attempt < 5; attempt++) {
    const id = newId('evt');
    const name = `${compactTs(ts)}--${String(seq).padStart(4, '0')}--${slug(actor?.name || 'system')}--${id}.json`;
    const note = {
      schema: 'wcp/0.1/note', id, type, ts,
      actorId: actor?.id || null, actorName: actor?.name || 'system', sessionId: session?.id || null,
      seq, subject, summary, data, writer: WRITER,
    };
    try {
      createJsonExclusive(path.join(dir, name), note, ws.paths.tmp);
      return note;
    } catch (e) {
      if (e.code !== 'EEXIST') throw e;
    }
  }
  throw new AppError('E_STATE_CORRUPT', 'Could not pin a note: filename collision after 5 attempts.');
}

export function readNotes(ws, { limit = 50, since = null, until = null, actor = null, type = null } = {}) {
  const files = walkJson(ws.paths.notes);
  const out = [];
  for (let i = files.length - 1; i >= 0; i--) {
    const r = readJsonSafe(files[i]);
    if (!r.ok) continue;
    const n = r.value;
    if (actor && n.actorName !== actor) continue;
    if (type && n.type !== type) continue;
    if (since && Date.parse(n.ts) < since) continue;
    if (until && Date.parse(n.ts) > until) continue;
    out.push(n);
    if (out.length >= limit) break;
  }
  return out.reverse();
}

export const countNotes = (ws) => walkJson(ws.paths.notes).length;

// ---------------------------------------------------------------- claims and leases

/** A card is still on the door while it is offered: a handoff never leaves work unowned. */
export const HELD_STATES = new Set(['active', 'handoff-offered']);
export const isHeld = (claim) => HELD_STATES.has(claim.state);

export const claimFile = (ws, id) => path.join(ws.paths.claims, `${id}.json`);
export const leaseFile = (ws, id) => path.join(ws.paths.leases, `${id}.json`);

export function readLease(ws, claimId) {
  const r = readJsonSafe(leaseFile(ws, claimId));
  return r.ok ? r.value : null;
}

export function writeLease(ws, claimId, { sessionId, ttlMs, renewals = 0 }) {
  const lease = {
    schema: 'wcp/0.1/lease', claimId, sessionId, pid: process.pid,
    renewedAt: nowIso(), expiresAt: nowIso(new Date(Date.now() + ttlMs)),
    renewals, seq: renewals, writer: WRITER,
  };
  writeJsonAtomic(leaseFile(ws, claimId), lease, ws.paths.tmp);
  return lease;
}

export function decorate(ws, claim) {
  const lease = readLease(ws, claim.id);
  const effectiveExpiresAt = lease && lease.claimId === claim.id ? lease.expiresAt : claim.expiresAtInitial;
  const expiresMs = Date.parse(effectiveExpiresAt);
  const grace = ws.config.lease.graceMs;
  const ownerHere = claim.host === hostId();
  const ownerAlive = ownerHere ? processAlive(claim.process?.pid) : null;
  const expired = Date.now() > expiresMs;
  const stale = expired && (Date.now() > expiresMs + grace || (ownerHere && ownerAlive === false));
  return { claim, lease, effectiveExpiresAt, expiresInMs: expiresMs - Date.now(), expired, stale, ownerAlive };
}

/**
 * Every claim on the door, refusing to answer at all if one of them is damaged.
 *
 * A record we cannot parse is not an absent claim. Skipping it makes an
 * unreadable exclusive card look like free space, and the next `claim` hands
 * the same paths to somebody else. So a corrupt card blocks every decision
 * until a human or `doctor --fix` quarantines it.
 */
export function listClaims(ws, { includeStale = true, tolerateCorrupt = false } = {}) {
  const out = [];
  const corrupt = [];
  for (const file of listJson(ws.paths.claims)) {
    const r = readJsonSafe(file);
    if (!r.ok) { corrupt.push(path.relative(ws.root, file)); continue; }
    const d = decorate(ws, r.value);
    if (!includeStale && d.stale) continue;
    out.push(d);
  }
  if (corrupt.length && !tolerateCorrupt) {
    throw new AppError('E_STATE_CORRUPT', `${corrupt.length} claim record(s) cannot be read, so ownership is unknown.`, {
      hint: 'fridge doctor --fix   (moves damaged records to .fridge/quarantine/)',
      details: { corrupt },
    });
  }
  out.sort((a, b) => a.claim.createdAt.localeCompare(b.claim.createdAt));
  // Views tolerate damage but must never present it as an empty, safe board.
  Object.defineProperty(out, 'corrupt', { value: corrupt, enumerable: false });
  return out;
}

export function readClaim(ws, id) {
  const file = claimFile(ws, id);
  if (!exists(file)) return null;
  const r = readJsonSafe(file);
  if (!r.ok) {
    throw new AppError('E_STATE_CORRUPT', `Card ${id} cannot be read, so its owner is unknown.`, {
      hint: 'fridge doctor --fix',
      details: { corrupt: [path.relative(ws.root, file)] },
    });
  }
  return decorate(ws, r.value);
}

export const saveClaim = (ws, claim) => writeJsonAtomic(claimFile(ws, claim.id), claim, ws.paths.tmp);

export function archiveClaim(ws, claim, state) {
  const final = { ...claim, state, closedAt: nowIso() };
  ensureDir(ws.paths.archive);
  writeJsonAtomic(path.join(ws.paths.archive, `${claim.id}.json`), final, ws.paths.tmp);
  try { fs.unlinkSync(claimFile(ws, claim.id)); } catch { /* already gone */ }
  try { fs.unlinkSync(leaseFile(ws, claim.id)); } catch { /* already gone */ }
  clearQueueFor(ws, claim.id);
  return final;
}

// ---------------------------------------------------------------- queue
// Advisory "I am waiting for that card" markers. One file per waiter, created
// write-once, so the waiting list has the same one-writer-per-record property
// as everything else.

export function writeQueueEntry(ws, entry) {
  ensureDir(ws.paths.queue);
  const record = { schema: `${PROTOCOL}/queue`, writer: WRITER, createdAt: nowIso(), ...entry };
  writeJsonAtomic(path.join(ws.paths.queue, `${record.id}.json`), record, ws.paths.tmp);
  return record;
}

export function listQueue(ws, { claimId = null } = {}) {
  return listJson(ws.paths.queue)
    .map(readJsonSafe).filter((r) => r.ok).map((r) => r.value)
    .filter((e) => !claimId || e.claimId === claimId)
    .sort((a, b) => String(a.createdAt).localeCompare(String(b.createdAt)));
}

export function removeQueueEntry(ws, id) {
  try { fs.unlinkSync(path.join(ws.paths.queue, `${id}.json`)); } catch { /* already gone */ }
}

export function clearQueueFor(ws, claimId) {
  const woken = listQueue(ws, { claimId });
  for (const e of woken) removeQueueEntry(ws, e.id);
  return woken;
}

/** Sweep fallen cards. Must be called while holding the registry mutex. */
export function reapStale(ws, { actor = null, session = null, force = false, tolerateCorrupt = false } = {}) {
  const reaped = [];
  for (const d of listClaims(ws, { tolerateCorrupt })) {
    if (!d.stale && !(force && d.expired)) continue;
    archiveClaim(ws, d.claim, 'expired');
    pin(ws, {
      type: 'claim.expired', actor, session,
      subject: { kind: 'claim', id: d.claim.id },
      summary: `expired ${d.claim.actorName}'s card on ${d.claim.scope.include.join(', ')}`,
      data: { ownerProcessAlive: d.ownerAlive, expiredAt: d.effectiveExpiresAt, owner: d.claim.actorName, forced: Boolean(force) && !d.stale },
    });
    reaped.push(d.claim);
  }
  return reaped;
}

// ---------------------------------------------------------------- inbox

export const inboxDir = (ws, actorSlug) => path.join(ws.paths.inbox, actorSlug);

export function writeMessage(ws, message) {
  const dir = inboxDir(ws, slug(message.toName));
  ensureDir(dir);
  writeJsonAtomic(path.join(dir, `${message.id}.json`), message, ws.paths.tmp);
  return message;
}

export function listMessages(ws, actorName) {
  return listJson(inboxDir(ws, slug(actorName))).map(readJsonSafe).filter((r) => r.ok).map((r) => r.value);
}

export function deleteMessage(ws, actorName, id) {
  try { fs.unlinkSync(path.join(inboxDir(ws, slug(actorName)), `${id}.json`)); return true; } catch { return false; }
}

/**
 * Move a message out of the inbox with its terminal state recorded.
 *
 * The wire format says a message moves `offered -> accepted | declined |
 * withdrawn | expired`. Deleting it loses that, and an offer whose outcome is
 * unrecorded is exactly the kind of thing a stale acceptance can replay.
 */
export function archiveMessage(ws, message, state) {
  const dir = path.join(ws.paths.dir, 'archive', 'messages');
  ensureDir(dir);
  const final = { ...message, state, closedAt: nowIso() };
  writeJsonAtomic(path.join(dir, `${message.id}.json`), final, ws.paths.tmp);
  deleteMessage(ws, message.toName, message.id);
  return final;
}

export function findMessage(ws, actorName, id) {
  return listMessages(ws, actorName).find((m) => m.id === id) || null;
}

export const stable = stableStringify;
