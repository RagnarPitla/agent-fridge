// SPDX-License-Identifier: Apache-2.0
// Chore cards: claim, check, heartbeat, extend, release, reap, wait, guard, run.
import fs from 'node:fs';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { AppError, EXIT } from '../core/errors.mjs';
import { emit, warn } from '../core/output.mjs';
import { withMutex } from '../core/mutex.mjs';
import {
  archiveClaim, clearQueueFor, isHeld, listClaims, listQueue, mutateSession, openWorkspace, pin, readClaim, reapStale,
  removeQueueEntry, requireActor, saveClaim, writeLease, writeQueueEntry,
} from '../core/store.mjs';
import { guardSecrets } from '../core/secrets.mjs';
import { autoRender } from '../core/render.mjs';
import {
  defaultCaseInsensitive, isGlobal, materialize, normalizePattern, scopesOverlap,
} from '../core/paths.mjs';
import { hostId, humanMs, newId, nowIso, parseDuration, randomToken, sha256, sleep, jitter } from '../core/util.mjs';
import { BIN } from '../brand.mjs';

const open = (ctx) => openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });

function currentBranch(root) {
  const r = spawnSync('git', ['-C', root, 'rev-parse', '--abbrev-ref', 'HEAD'], { encoding: 'utf8' });
  return r.status === 0 ? r.stdout.trim() : null;
}

function normalizeAll(ws, inputs, { confirmGlobal = false } = {}) {
  const out = [];
  for (const raw of inputs) {
    const { pattern, dirIntent, isGlob } = normalizePattern(raw, { root: ws.root, cwd: ws.cwd });
    if (isGlobal(pattern) && !confirmGlobal && !ws.config.paths.allowGlobalClaims) {
      throw new AppError('E_USAGE', `'${raw}' would claim the whole repository.`, {
        hint: 'Claim the narrowest paths you need, or pass --confirm-global if you really mean it.',
      });
    }
    out.push(dirIntent && !isGlob ? `${pattern}/**` : pattern);
  }
  return [...new Set(out)];
}

function buildScope(ws, include, exclude) {
  const insensitive = defaultCaseInsensitive(ws.config.paths.caseSensitivity);
  const m = materialize(ws.root, include, { limit: ws.config.paths.materializeLimit, insensitive });
  const excluded = exclude.length
    ? materialize(ws.root, exclude, { limit: ws.config.paths.materializeLimit, insensitive, files: m.materialized })
    : { materialized: [] };
  const drop = new Set(excluded.materialized);
  return {
    include,
    exclude,
    materialized: m.materialized.filter((f) => !drop.has(f)),
    materializedTruncated: m.materializedTruncated,
    matchers: m.matchers,
    materializer: m.materializer,
  };
}

/** Exclusive blocks everything except advisory. Shared coexists with shared. */
export function modesCollide(requested, existing) {
  if (requested === 'advisory' || existing === 'advisory') return false;
  if (requested === 'shared' && existing === 'shared') return false;
  return true;
}

function conflictReport(ws, mine, conflicts) {
  const lines = ['Somebody already has that chore.', ''];
  for (const c of conflicts) {
    const d = c.holder;
    lines.push(`  card    ${d.claim.id}`);
    lines.push(`  who     ${d.claim.actorName} (${d.claim.vendor})  pid ${d.claim.process?.pid ?? '?'}`);
    lines.push(`  mode    ${d.claim.mode}   doing: ${d.claim.task || '-'}`);
    lines.push(`  scope   ${d.claim.scope.include.join(', ')}`);
    lines.push(`  back by ${d.effectiveExpiresAt} (in ${humanMs(d.expiresInMs)})`);
    lines.push(`  clash   ${c.overlap.reason}: ${(c.overlap.paths || []).slice(0, 5).join(', ')}`);
    lines.push('');
  }
  lines.push('You can:');
  lines.push(`  ${BIN} board                          # see the whole door`);
  lines.push(`  ${BIN} claim <narrower-path> ...      # take a different chore`);
  lines.push(`  ${BIN} wait ${conflicts[0].holder.claim.id} --timeout 10m`);
  lines.push(`  ${BIN} handoff ${conflicts[0].holder.claim.id} --to ${conflicts[0].holder.claim.actorName} --note "..."`);
  return lines.join('\n');
}

export async function claim(ctx) {
  const ws = open(ctx);
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor, mutating: true });
  if (!ctx.positional.length) {
    throw new AppError('E_USAGE', 'What do you want to claim?', { hint: `${BIN} claim "src/**" --task "refactor api"` });
  }
  const task = ctx.flags.task || null;
  if (!task && ws.config.policy.requireTaskOnClaim) {
    throw new AppError('E_USAGE', 'A claim needs --task so the others know what you are doing.', {
      hint: `${BIN} claim "${ctx.positional[0]}" --task "what you are doing"`,
    });
  }
  guardSecrets({ '--task': task, '--label': [].concat(ctx.flags.label || []).join(' ') }, { allow: Boolean(ctx.flags['allow-secret-like']) });
  const mode = ctx.flags.mode || 'exclusive';
  if (!['exclusive', 'shared', 'advisory'].includes(mode)) {
    throw new AppError('E_USAGE', `Unknown --mode '${mode}'.`, { hint: 'One of: exclusive, shared, advisory' });
  }
  const maxTtl = ws.config.lease.maxTtlMs;
  let ttlMs = parseDuration(ctx.flags.ttl || process.env.FRIDGE_TTL || ws.config.lease.defaultTtlMs, 'ttl');
  if (ttlMs > maxTtl) {
    warn(ctx, `--ttl capped at ${humanMs(maxTtl)} (lease.maxTtlMs).`);
    ttlMs = maxTtl;
  }
  if (ttlMs < 1000) throw new AppError('E_USAGE', '--ttl must be at least 1s.');

  const include = normalizeAll(ws, ctx.positional, { confirmGlobal: Boolean(ctx.flags['confirm-global']) });
  const exclude = normalizeAll(ws, [].concat(ctx.flags.exclude || []));
  const insensitive = defaultCaseInsensitive(ws.config.paths.caseSensitivity);
  const waitMs = ctx.flags.wait ? parseDuration(ctx.flags.wait, 'wait') : 0;
  const deadline = Date.now() + waitMs;

  for (;;) {
    const result = await withMutex(ws, 'claim', () => {
      reapStale(ws, { actor, session });
      const scope = buildScope(ws, include, exclude);
      const existing = listClaims(ws).filter((d) => !d.stale && isHeld(d.claim));

      const conflicts = [];
      for (const d of existing) {
        if (d.claim.sessionId === session.id) continue;
        if (!modesCollide(mode, d.claim.mode)) continue;
        const overlap = scopesOverlap(scope, d.claim.scope, { insensitive });
        if (overlap.overlap) conflicts.push({ holder: d, overlap });
      }
      if (conflicts.length) return { conflict: true, conflicts, scope };

      // Only after nobody else is in the way may I widen a card I already hold.
      // Widening never changes its mode: that would be a new promise to everyone else.
      const mineOverlapping = existing.filter((d) => d.claim.sessionId === session.id
        && d.claim.mode === mode
        && scopesOverlap(scope, d.claim.scope, { insensitive }).overlap);
      if (mineOverlapping.length && !ctx.flags.strict) {
        const target = mineOverlapping[0];
        const merged = [...new Set([...target.claim.scope.include, ...include])];
        const nextScope = buildScope(ws, merged, target.claim.scope.exclude);
        const updated = { ...target.claim, scope: nextScope, task: task || target.claim.task, updatedAt: nowIso() };
        saveClaim(ws, updated);
        writeLease(ws, updated.id, { sessionId: session.id, ttlMs, renewals: (target.lease?.renewals || 0) + 1 });
        return { merged: true, claim: updated };
      }

      const token = randomToken();
      const record = {
        schema: 'wcp/0.1/claim',
        id: newId('clm'),
        workspaceId: ws.config.workspaceId,
        actorId: actor.id,
        actorName: actor.name,
        vendor: actor.vendor,
        sessionId: session.id,
        host: hostId(),
        process: { pid: process.pid, ppid: process.ppid, startedAt: nowIso() },
        mode,
        task,
        labels: { branch: currentBranch(ws.root), ...(Object.fromEntries(([].concat(ctx.flags.label || [])).map((l) => {
          const i = l.indexOf('=');
          return i === -1 ? [l, 'true'] : [l.slice(0, i), l.slice(i + 1)];
        }))) },
        scope,
        createdAt: nowIso(),
        updatedAt: nowIso(),
        ttlMs,
        expiresAtInitial: nowIso(new Date(Date.now() + ttlMs)),
        state: 'active',
        tokenHash: sha256(token),
        writer: ws.config.writer,
      };
      saveClaim(ws, record);
      writeLease(ws, record.id, { sessionId: session.id, ttlMs, renewals: 0 });
      mutateSession(ws, session, (s2) => { s2.tokens = { ...(s2.tokens || {}), [record.id]: token }; });
      pin(ws, {
        type: 'claim.acquired', actor, session,
        subject: { kind: 'claim', id: record.id },
        summary: `${actor.name} took ${include.join(', ')}${task ? ` for "${task}"` : ''}`,
        data: { mode, ttlMs, include, exclude, files: scope.materialized.length },
      });
      return { claim: record, token };
    });

    if (!result.conflict) {
      const c = result.claim;
      autoRender(ws);
      const text = result.merged
        ? `Extended your card ${c.id} -> ${c.scope.include.join(', ')}`
        : [
          `Card ${c.id} is yours.`,
          `  scope    ${c.scope.include.join(', ')}${c.scope.exclude.length ? `  (minus ${c.scope.exclude.join(', ')})` : ''}`,
          `  files    ${c.scope.materialized.length}${c.scope.materializedTruncated ? '+ (truncated, treated conservatively)' : ''}`,
          `  mode     ${c.mode}`,
          `  back by  ${humanMs(c.ttlMs)} from now`,
          '',
          `When you stop: ${BIN} release ${c.id} --outcome done --note "what changed"`,
        ].join('\n');
      return emit(ctx, 'claim', {
        data: {
          claimId: c.id, merged: Boolean(result.merged), mode: c.mode, task: c.task, ttlMs: c.ttlMs,
          scope: c.scope, expiresAt: c.expiresAtInitial, token: result.token || null,
        },
        text,
      });
    }

    if (Date.now() < deadline) { await sleep(jitter(Math.min(1000, Math.max(200, waitMs / 20)))); continue; }

    pin(ws, {
      type: 'claim.denied', actor, session,
      summary: `${actor.name} was blocked on ${include.join(', ')}`,
      data: { include, blockedBy: result.conflicts.map((c) => ({ claimId: c.holder.claim.id, actor: c.holder.claim.actorName, reason: c.overlap.reason })) },
    });
    let queued = [];
    if (ctx.flags.queue) {
      queued = result.conflicts.map((c) => writeQueueEntry(ws, {
        id: newId('que'),
        claimId: c.holder.claim.id,
        actorName: actor.name,
        sessionId: session.id,
        include,
        task: ctx.flags.task || null,
      }));
      pin(ws, {
        type: 'queue.joined', actor, session,
        summary: `${actor.name} is waiting for ${result.conflicts.map((c) => c.holder.claim.id).join(', ')}`,
        data: { include, waitingFor: queued.map((q) => q.claimId) },
      });
    }
    autoRender(ws);
    if (waitMs > 0) {
      throw new AppError('E_WAIT_TIMEOUT', `Still claimed after ${humanMs(waitMs)}.`, {
        hint: `${BIN} board`,
        details: { report: conflictReport(ws, include, result.conflicts), queued: queued.map((q) => q.id) },
      });
    }
    throw new AppError('E_CONFLICT', `${result.conflicts.length} card(s) already cover those paths.`, {
      hint: `${BIN} board  |  ${BIN} wait ${result.conflicts[0].holder.claim.id}  |  ${BIN} handoff ...`,
      details: {
        report: conflictReport(ws, include, result.conflicts) + (queued.length ? `\n\nYou are on the waiting list (${queued.length} card(s)). ${BIN} wait ${result.conflicts[0].holder.claim.id}` : ''),
        queued: queued.map((q) => q.id),
        conflicts: result.conflicts.map((c) => ({
          claimId: c.holder.claim.id, actorName: c.holder.claim.actorName, vendor: c.holder.claim.vendor,
          mode: c.holder.claim.mode, task: c.holder.claim.task, include: c.holder.claim.scope.include,
          expiresAt: c.holder.effectiveExpiresAt, reason: c.overlap.reason, paths: c.overlap.paths,
        })),
      },
    });
  }
}

function classify(ws, session, queries) {
  const insensitive = defaultCaseInsensitive(ws.config.paths.caseSensitivity);
  const active = listClaims(ws).filter((d) => !d.stale && isHeld(d.claim));
  return queries.map((pattern) => {
    const scope = buildScope(ws, [pattern], []);
    const hits = active
      .map((d) => ({ d, overlap: scopesOverlap(scope, d.claim.scope, { insensitive }) }))
      .filter((h) => h.overlap.overlap);
    const mine = hits.filter((h) => h.d.claim.sessionId === session?.id);
    const theirs = hits.filter((h) => h.d.claim.sessionId !== session?.id && h.d.claim.mode !== 'advisory');
    const status = theirs.length ? 'theirs' : mine.length ? 'yours' : 'unclaimed';
    return {
      path: pattern,
      status,
      claimId: (mine[0] || theirs[0])?.d.claim.id || null,
      actorName: (theirs[0] || mine[0])?.d.claim.actorName || null,
      mode: (theirs[0] || mine[0])?.d.claim.mode || null,
      expiresAt: (theirs[0] || mine[0])?.d.effectiveExpiresAt || null,
      reason: (theirs[0] || mine[0])?.overlap.reason || null,
    };
  });
}

function verdict(ctx, command, rows, { requireClaim }) {
  const theirs = rows.filter((r) => r.status === 'theirs');
  const unclaimed = rows.filter((r) => r.status === 'unclaimed');
  const text = rows.map((r) => {
    const tag = r.status === 'yours' ? 'yours    ' : r.status === 'theirs' ? 'THEIRS   ' : 'unclaimed';
    return `${tag} ${r.path}${r.actorName ? `  (${r.actorName}, ${r.claimId})` : ''}`;
  }).join('\n');
  if (theirs.length) {
    throw new AppError('E_CONFLICT', `${theirs.length} path(s) belong to somebody else.`, {
      hint: `${BIN} board`,
      details: { report: text, paths: rows },
    });
  }
  if (unclaimed.length && requireClaim) {
    throw new AppError('E_OUT_OF_SCOPE', `${unclaimed.length} path(s) are outside any card you hold.`, {
      hint: `${BIN} claim "${unclaimed[0].path}" --task "..."`,
      details: { report: text, paths: rows },
    });
  }
  return emit(ctx, command, { data: { paths: rows, ok: true }, text: text || 'nothing to check' });
}

export async function check(ctx) {
  const ws = open(ctx);
  const { session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  if (!ctx.positional.length) throw new AppError('E_USAGE', 'Which paths?', { hint: `${BIN} check src/api/routes.ts` });
  const queries = normalizeAll(ws, ctx.positional, { confirmGlobal: true });
  const rows = classify(ws, session, queries);
  return verdict(ctx, 'check', rows, { requireClaim: Boolean(ctx.flags['for-write']) });
}

export async function guard(ctx) {
  const ws = open(ctx);
  const { session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  let inputs = ctx.positional;
  if (ctx.flags.staged) {
    const r = spawnSync('git', ['-C', ws.root, 'diff', '--cached', '--name-only', '-z'], { encoding: 'utf8' });
    if (r.status !== 0) throw new AppError('E_USAGE', 'git diff --cached failed; is this a git repository?');
    inputs = r.stdout.split('\0').filter(Boolean);
  }
  if (!inputs.length) return emit(ctx, 'guard', { data: { paths: [], ok: true }, text: 'nothing staged, nothing to guard' });
  const queries = normalizeAll(ws, inputs, { confirmGlobal: true });
  const rows = classify(ws, session, queries);
  const requireClaim = ws.config.policy.requireClaimForWrite === 'strict';
  return verdict(ctx, 'guard', rows, { requireClaim });
}

export async function heartbeat(ctx) {
  const ws = open(ctx);
  const { session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor, mutating: true });
  const ids = [].concat(ctx.flags.claim || []);
  const mine = listClaims(ws).filter((d) => d.claim.sessionId === session.id);
  const targets = ids.length ? mine.filter((d) => ids.includes(d.claim.id)) : mine;
  if (ids.length && targets.length !== ids.length) {
    const missing = ids.filter((id) => !targets.some((d) => d.claim.id === id));
    throw new AppError('E_NOT_FOUND', `No live card of yours named ${missing.join(', ')}.`, { hint: `${BIN} whoami` });
  }
  if (!targets.length) return emit(ctx, 'heartbeat', { data: { renewed: [] }, text: 'you are not holding any cards' });
  const expired = targets.filter((d) => d.stale);
  if (expired.length) {
    throw new AppError('E_LEASE_EXPIRED', `${expired.length} of your card(s) already fell off the door.`, {
      hint: `${BIN} reap && ${BIN} claim ... again`,
      details: { claims: expired.map((d) => d.claim.id) },
    });
  }
  const ttlOverride = ctx.flags.ttl ? parseDuration(ctx.flags.ttl, 'ttl') : null;
  const renewed = targets.map((d) => {
    const ttl = Math.min(ttlOverride || d.claim.ttlMs, ws.config.lease.maxTtlMs);
    const lease = writeLease(ws, d.claim.id, { sessionId: session.id, ttlMs: ttl, renewals: (d.lease?.renewals || 0) + 1 });
    return { claimId: d.claim.id, expiresAt: lease.expiresAt };
  });
  autoRender(ws);
  return emit(ctx, 'heartbeat', {
    data: { renewed },
    text: `still on it: renewed ${renewed.length} card(s)\n${renewed.map((r) => `  ${r.claimId}  until ${r.expiresAt}`).join('\n')}`,
  });
}

export async function extend(ctx) {
  const ws = open(ctx);
  const { session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor, mutating: true });
  const id = ctx.positional[0];
  if (!id) throw new AppError('E_USAGE', 'Which card?', { hint: `${BIN} extend <claim-id> --ttl 1h` });
  if (!ctx.flags.ttl) throw new AppError('E_USAGE', '--ttl is required.', { hint: `${BIN} extend ${id} --ttl 1h` });
  const d = readClaim(ws, id);
  if (!d) throw new AppError('E_NOT_FOUND', `No card ${id}.`, { hint: `${BIN} board` });
  if (d.claim.sessionId !== session.id) {
    throw new AppError('E_NOT_OWNER', `Card ${id} belongs to ${d.claim.actorName}.`, { hint: `${BIN} handoff ${id} --to <you>` });
  }
  const ttlMs = Math.min(parseDuration(ctx.flags.ttl, 'ttl'), ws.config.lease.maxTtlMs);
  saveClaim(ws, { ...d.claim, ttlMs, updatedAt: nowIso() });
  const lease = writeLease(ws, id, { sessionId: session.id, ttlMs, renewals: (d.lease?.renewals || 0) + 1 });
  autoRender(ws);
  return emit(ctx, 'extend', { data: { claimId: id, ttlMs, expiresAt: lease.expiresAt }, text: `${id} now runs until ${lease.expiresAt}` });
}

export async function release(ctx) {
  const ws = open(ctx);
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor, mutating: true });
  const all = Boolean(ctx.flags.all);
  const ids = ctx.positional;
  if (!all && !ids.length) throw new AppError('E_USAGE', 'Which card?', { hint: `${BIN} release <claim-id> | ${BIN} release --all` });
  guardSecrets({ '--note': ctx.flags.note }, { allow: Boolean(ctx.flags['allow-secret-like']) });
  const outcome = ctx.flags.outcome || 'done';
  if (!['done', 'partial', 'abandoned', 'failed'].includes(outcome)) {
    throw new AppError('E_USAGE', `Unknown --outcome '${outcome}'.`, { hint: 'One of: done, partial, abandoned, failed' });
  }
  const released = await withMutex(ws, 'release', () => {
    const mine = listClaims(ws).filter((d) => d.claim.sessionId === session.id);
    let targets;
    if (all) targets = mine;
    else {
      targets = [];
      for (const id of ids) {
        const d = readClaim(ws, id);
        if (!d) throw new AppError('E_NOT_FOUND', `No card ${id}.`, { hint: `${BIN} board` });
        const token = (session.tokens || {})[id];
        const owns = d.claim.sessionId === session.id || (token && sha256(token) === d.claim.tokenHash);
        if (!owns && !ctx.flags.force) {
          throw new AppError('E_NOT_OWNER', `Card ${id} belongs to ${d.claim.actorName} (${d.claim.vendor}).`, {
            hint: `${BIN} handoff ${id} --to ${actor.name}   (or --force if a human told you to)`,
          });
        }
        if (d.stale && !ctx.flags.force) {
          throw new AppError('E_LEASE_EXPIRED', `Card ${id} already fell off the door.`, { hint: `${BIN} reap` });
        }
        if (!owns && d.claim.host !== hostId() && !ctx.flags['allow-multihost']) {
          throw new AppError('E_FOREIGN_HOST', `Card ${id} was taken on another machine, so its owner's liveness cannot be checked from here.`, {
            hint: `${BIN} release ${id} --force --allow-multihost   (only if you know that machine is done)`,
            details: { claimId: id, host: d.claim.host, thisHost: hostId() },
          });
        }
        targets.push(d);
      }
    }
    const out = [];
    const dropped = [];
    for (const d of targets) {
      // The wire format distinguishes a card its owner took down from one an
      // operator took away. Forcing somebody else's card is `revoked`.
      const forcedOther = Boolean(ctx.flags.force) && d.claim.sessionId !== session.id;
      archiveClaim(ws, d.claim, forcedOther ? 'revoked' : 'released');
      dropped.push(d.claim.id);
      pin(ws, {
        type: 'claim.released', actor, session,
        subject: { kind: 'claim', id: d.claim.id },
        summary: `${actor.name} finished ${d.claim.scope.include.join(', ')} (${outcome})${ctx.flags.note ? `: ${ctx.flags.note}` : ''}`,
        data: { outcome, note: ctx.flags.note || null, forced: Boolean(ctx.flags.force), heldMs: Date.now() - Date.parse(d.claim.createdAt), include: d.claim.scope.include },
      });
      out.push({ claimId: d.claim.id, include: d.claim.scope.include, outcome });
    }
    mutateSession(ws, session, (s2) => {
      for (const id of dropped) if (s2.tokens) delete s2.tokens[id];
    });
    return out;
  });
  autoRender(ws);
  return emit(ctx, 'release', {
    data: { released, outcome },
    text: released.length ? `took down ${released.length} card(s):\n${released.map((r) => `  ${r.claimId}  ${r.include.join(', ')}`).join('\n')}` : 'you were not holding any cards',
  });
}

export async function reap(ctx) {
  const ws = open(ctx);
  let actor = null; let session = null;
  try { ({ actor, session } = requireActor(ws, { agent: ctx.flags.agent, mutating: false })); } catch { /* reap works without a session */ }
  const force = Boolean(ctx.flags.force);
  const targets = () => listClaims(ws).filter((d) => d.stale || (force && d.expired));
  if (ctx.flags['dry-run']) {
    const stale = targets();
    return emit(ctx, 'reap', {
      data: { dryRun: true, force, wouldReap: stale.map((d) => ({ claimId: d.claim.id, actorName: d.claim.actorName, expiredAt: d.effectiveExpiresAt })) },
      text: stale.length ? stale.map((d) => `would sweep ${d.claim.id} (${d.claim.actorName}, expired ${d.effectiveExpiresAt})`).join('\n') : 'nothing has fallen off the door',
    });
  }
  if (force && !ctx.flags['allow-multihost']) {
    const foreign = targets().filter((d) => d.claim.host !== hostId());
    if (foreign.length) {
      throw new AppError('E_FOREIGN_HOST', `${foreign.length} card(s) were taken on another machine.`, {
        hint: `${BIN} reap --force --allow-multihost   (only if you know that machine is done)`,
        details: { claimIds: foreign.map((d) => d.claim.id), thisHost: hostId() },
      });
    }
  }
  const reaped = await withMutex(ws, 'reap', () => reapStale(ws, { actor, session, force }));
  autoRender(ws);
  return emit(ctx, 'reap', {
    data: { forced: force, reaped: reaped.map((c) => ({ claimId: c.id, actorName: c.actorName, include: c.scope.include })) },
    text: reaped.length ? `swept ${reaped.length} fallen card(s):\n${reaped.map((c) => `  ${c.id}  ${c.actorName}  ${c.scope.include.join(', ')}`).join('\n')}` : 'nothing has fallen off the door',
  });
}

export async function wait(ctx) {
  const ws = open(ctx);
  const id = ctx.positional[0];
  if (!id) throw new AppError('E_USAGE', 'Wait for which card?', { hint: `${BIN} wait <claim-id> --timeout 10m` });
  const timeoutMs = parseDuration(ctx.flags.timeout || '10m', 'timeout');
  const started = Date.now();
  const initial = readClaim(ws, id);
  if (!initial) throw new AppError('E_NOT_FOUND', `No card ${id}. It may already be gone.`, { hint: `${BIN} board` });

  // Put a marker on the waiting list so the board can show who is blocked.
  let entry = null;
  try {
    const { actor, session } = requireActor(ws, { agent: ctx.flags.agent });
    entry = writeQueueEntry(ws, { id: newId('que'), claimId: id, actorName: actor.name, sessionId: session.id, include: initial.claim.scope.include, task: null });
  } catch { /* waiting without a session is allowed; it just is not attributed */ }
  const done = () => { if (entry) removeQueueEntry(ws, entry.id); };

  try {
    for (;;) {
      const d = readClaim(ws, id);
      if (!d || d.stale) {
        const waitedMs = Date.now() - started;
        return emit(ctx, 'wait', { data: { claimId: id, waitedMs, gone: !d, stale: Boolean(d?.stale) }, text: `card ${id} is gone after ${humanMs(waitedMs)}` });
      }
      if (Date.now() - started > timeoutMs) {
        throw new AppError('E_WAIT_TIMEOUT', `Card ${id} is still held after ${humanMs(timeoutMs)}.`, {
          hint: `${BIN} handoff ${id} --to <you> --note "can I take this?"`,
          details: { claimId: id, actorName: d.claim.actorName, expiresAt: d.effectiveExpiresAt },
        });
      }
      await sleep(jitter(500));
    }
  } finally {
    done();
  }
}

const IS_WIN = process.platform === 'win32';

/**
 * Find what `npm` actually means on this machine.
 *
 * On Windows `npm` is `npm.cmd`, and `spawn` with `shell: false` cannot see
 * that, so `fridge run -- npm test` fails with ENOENT while the same command
 * works in the terminal. PATHEXT is consulted the way the shell would, and a
 * batch wrapper is then run through the shell because Node refuses to spawn
 * `.cmd` and `.bat` directly.
 */
function resolveExecutable(cmd, cwd) {
  if (!IS_WIN) return { file: cmd, useShell: false };
  const isBatch = (f) => /\.(cmd|bat)$/i.test(f);
  if (cmd.includes('/') || cmd.includes('\\')) {
    const abs = path.resolve(cwd, cmd);
    return { file: abs, useShell: isBatch(abs) };
  }
  const exts = (process.env.PATHEXT || '.COM;.EXE;.BAT;.CMD').split(';').filter(Boolean);
  if (path.extname(cmd)) {
    for (const dir of (process.env.PATH || '').split(path.delimiter).filter(Boolean)) {
      const full = path.join(dir, cmd);
      if (fs.existsSync(full)) return { file: full, useShell: isBatch(full) };
    }
    return { file: cmd, useShell: isBatch(cmd) };
  }
  for (const dir of (process.env.PATH || '').split(path.delimiter).filter(Boolean)) {
    for (const ext of exts) {
      const full = path.join(dir, cmd + ext);
      if (fs.existsSync(full)) return { file: full, useShell: isBatch(full) };
    }
  }
  // Not found on PATH. Hand it to the shell so the error message comes from
  // the shell rather than from a bare ENOENT with no context.
  return { file: cmd, useShell: true };
}

/** Quote one argument for cmd.exe, which is the only thing that reads the string we build. */
const winQuote = (a) => (/[\s"^&|<>()]/.test(a) ? `"${String(a).replace(/(\\*)"/g, '$1$1\\"').replace(/(\\+)$/, '$1$1')}"` : a);

export async function run(ctx) {
  const ws = open(ctx);
  if (!ctx.rest.length) {
    throw new AppError('E_USAGE', 'Nothing to run.', { hint: `${BIN} run --claim "src/**" --task "tests" -- npm test` });
  }
  const claimPaths = [].concat(ctx.flags.claim || []);
  if (!claimPaths.length) throw new AppError('E_USAGE', '--claim <path> is required.', { hint: `${BIN} run --claim "src/**" -- npm test` });
  const claimCtx = {
    ...ctx, command: 'claim', positional: claimPaths, json: true, quiet: true,
    flags: { ...ctx.flags, task: ctx.flags.task || ctx.rest.join(' ').slice(0, 120) },
  };
  let claimId = null;
  const chunks = [];
  const realWrite = process.stdout.write.bind(process.stdout);
  process.stdout.write = (s) => { chunks.push(String(s)); return true; };
  try {
    await claim(claimCtx);
  } finally {
    process.stdout.write = realWrite;
  }
  try { claimId = JSON.parse(chunks.join('')).data.claimId; } catch { /* claim() throws on conflict, so this is unreachable in practice */ }

  const [cmd, ...args] = ctx.rest;
  const ttlMs = parseDuration(ctx.flags.ttl || ws.config.lease.defaultTtlMs, 'ttl');
  const timer = setInterval(() => {
    try {
      const ws2 = open(ctx);
      const { session } = requireActor(ws2, { agent: ctx.flags.agent, mutating: true });
      writeLease(ws2, claimId, { sessionId: session.id, ttlMs, renewals: 0 });
    } catch { /* the child matters more than the heartbeat */ }
  }, Math.max(1000, Math.floor(ttlMs / 3)));
  timer.unref?.();

  const target = resolveExecutable(cmd, ctx.cwd);
  const outcome = await new Promise((resolve) => {
    const child = target.useShell
      ? spawn(`${winQuote(target.file)} ${args.map(winQuote).join(' ')}`.trim(), { stdio: 'inherit', cwd: ctx.cwd, shell: true })
      : spawn(target.file, args, { stdio: 'inherit', cwd: ctx.cwd, shell: false });
    // A command that never started is not a command that failed. Say which,
    // and say why, instead of returning a bare 127.
    child.on('error', (e) => resolve({ code: e.code === 'ENOENT' ? 127 : 1, spawnError: e }));
    child.on('close', (c, signal) => resolve({ code: signal ? 128 + (({ SIGINT: 2, SIGTERM: 15, SIGKILL: 9 })[signal] || 0) : c ?? 0 }));
  });
  clearInterval(timer);
  const code = outcome.code;
  if (outcome.spawnError) {
    process.stderr.write(`${BIN} run: could not start '${cmd}': ${outcome.spawnError.code || outcome.spawnError.message}\n`);
    if (IS_WIN) process.stderr.write(`  looked for '${target.file}' using PATHEXT (${process.env.PATHEXT || '.COM;.EXE;.BAT;.CMD'})\n`);
  }

  if (code !== 0 && ctx.flags['keep-on-failure']) {
    process.stderr.write(`command exited ${code}; keeping card ${claimId} (--keep-on-failure)\n`);
    return code;
  }
  await release({
    ...ctx, command: 'release', positional: [claimId], quiet: true,
    flags: { ...ctx.flags, outcome: code === 0 ? 'done' : 'failed', note: `${cmd} exited ${code}` },
  });
  return code;
}
