// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import { AppError, EXIT } from '../core/errors.mjs';
import { emit } from '../core/output.mjs';
import { exists, writeJsonAtomic, writeAtomic } from '../core/fsx.mjs';
import { nowIso, slug } from '../core/util.mjs';
import {
  ensureGitAttributes, initWorkspace, joinActor, listActors, listClaims, openWorkspace, pin, readActor,
  requireActor, resolveActorName, statePaths,
} from '../core/store.mjs';
import { maybeRenew } from '../core/renew.mjs';
import { autoRender, renderDoor } from '../core/render.mjs';
import * as adapterTemplates from '../adapters/templates.mjs';
import { BIN, PACKAGE, PROTOCOL, STATE_DIR, VERSION } from '../brand.mjs';

const VENDOR_VALUES = ['claude', 'copilot', 'codex', 'cursor', 'human', 'other'];

export async function init(ctx) {
  const pre = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd, requireInit: false });
  const { root } = initWorkspace(pre.root, { force: Boolean(ctx.flags.force) });
  const ws = openWorkspace({ repo: root, cwd: ctx.cwd });
  if (ctx.flags['commit-notes'] !== undefined) {
    ws.config.notes.commit = ctx.flags['commit-notes'] !== 'false';
    writeJsonAtomic(ws.paths.config, ws.config, ws.paths.tmp);
  }
  const gitattributes = ensureGitAttributes(root);
  let installed = [];
  if (!ctx.flags['no-adapters']) installed = adapterTemplates.install(root, ['agents'], { tmpDir: ws.paths.tmp });
  pin(ws, { type: 'workspace.initialized', actor: null, session: null, summary: `fridge hung on the door at ${root}`, data: { root, protocol: PROTOCOL, version: VERSION } });
  writeAtomic(ws.paths.door, renderDoor(ws), ws.paths.tmp);
  const text = [
    `The fridge is on the wall.  ${path.join(root, STATE_DIR)}`,
    `  protocol      ${PROTOCOL}`,
    `  git           ${STATE_DIR}/.gitignore keeps live state local; notes/ and actors/ are shared history`,
    gitattributes ? '  gitattributes .gitattributes updated (notes are never auto-merged)' : '',
    installed.length ? `  instructions  ${installed.map((r) => `${r.file} (${r.action})` ).join(', ')}` : '',
    '\nNext:',
    `  ${BIN} join --agent "your-name" --vendor human`,
    `  ${BIN} claim "src/**" --task "what you are doing"`,
    `  ${BIN} board`,
  ].filter(Boolean).join('\n');
  return emit(ctx, 'init', { data: { root, stateDir: path.join(root, STATE_DIR), protocol: PROTOCOL, adapters: installed, gitattributes }, text });
}

export async function join(ctx) {
  const ws = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
  const name = ctx.flags.agent || process.env.FRIDGE_ACTOR || ctx.positional[0];
  if (!name) {
    throw new AppError('E_USAGE', 'Who are you? Pass --agent <name>.', { hint: `${BIN} join --agent "claude-a" --vendor claude` });
  }
  const vendor = ctx.flags.vendor || 'other';
  if (!VENDOR_VALUES.includes(vendor)) {
    throw new AppError('E_USAGE', `Unknown --vendor '${vendor}'.`, { hint: `One of: ${VENDOR_VALUES.join(', ')}` });
  }
  const { actor, session, resumed } = joinActor(ws, { name, vendor });
  pin(ws, {
    type: resumed ? 'session.resumed' : 'session.started', actor, session,
    subject: { kind: 'session', id: session.id },
    summary: `${name} (${vendor}) ${resumed ? 'came back to' : 'walked up to'} the fridge`,
    data: { pid: process.pid, vendor },
  });
  autoRender(ws);
  const text = [
    `Your name is on the door: ${actor.name} (${actor.vendor})`,
    `  actor    ${actor.id}`,
    `  session  ${session.id}${resumed ? ' (resumed)' : ''}`,
    '',
    `Tip: export FRIDGE_ACTOR="${actor.name}" so you can drop --agent from every command.`,
  ].join('\n');
  return emit(ctx, 'join', { data: { actor, sessionId: session.id, resumed }, text });
}

export async function whoami(ctx) {
  const ws = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  maybeRenew(ws, session);
  const mine = listClaims(ws).filter((d) => d.claim.sessionId === session.id && !d.stale);
  const text = [
    `${actor.name} (${actor.vendor})  session ${session.id}  holding ${mine.length} card(s)`,
    ...mine.map((d) => `  ${d.claim.id}  ${d.claim.scope.include.join(', ')}  -> ${d.claim.task || '-'}`),
  ].join('\n');
  return emit(ctx, 'whoami', {
    data: {
      actor, sessionId: session.id, host: actor.host,
      claims: mine.map((d) => ({ id: d.claim.id, include: d.claim.scope.include, task: d.claim.task, expiresAt: d.effectiveExpiresAt })),
    },
    text,
  });
}

export async function version(ctx) {
  const data = {
    product: 'Agent Fridge', package: PACKAGE, version: VERSION, protocol: PROTOCOL,
    implementation: 'node', runtime: process.version, platform: process.platform, arch: process.arch,
  };
  return emit(ctx, 'version', { data, text: `${PACKAGE} ${VERSION}  protocol ${PROTOCOL}  node ${process.version}  ${process.platform}/${process.arch}` });
}

const getPath = (obj, dotted) => dotted.split('.').reduce((o, k) => (o == null ? o : o[k]), obj);
function setPath(obj, dotted, value) {
  const keys = dotted.split('.');
  let cur = obj;
  for (const k of keys.slice(0, -1)) {
    if (cur[k] === undefined || cur[k] === null || typeof cur[k] !== 'object') {
      throw new AppError('E_NOT_FOUND', `No config section '${k}'.`);
    }
    cur = cur[k];
  }
  cur[keys[keys.length - 1]] = value;
}

export async function config(ctx) {
  const ws = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
  const [key, value] = ctx.positional;
  if (!key) return emit(ctx, 'config', { data: ws.config, text: JSON.stringify(ws.config, null, 2) });
  const current = getPath(ws.config, key);
  if (value === undefined) {
    if (current === undefined) throw new AppError('E_NOT_FOUND', `No config key '${key}'.`, { hint: `${BIN} config` });
    return emit(ctx, 'config', { data: { key, value: current }, text: String(typeof current === 'object' ? JSON.stringify(current) : current) });
  }
  if (current === undefined) throw new AppError('E_NOT_FOUND', `No config key '${key}'.`, { hint: `${BIN} config` });
  let parsed = value;
  if (typeof current === 'number') {
    parsed = Number(value);
    if (!Number.isFinite(parsed)) throw new AppError('E_USAGE', `Config key '${key}' needs a number.`);
  } else if (typeof current === 'boolean') {
    if (!['true', 'false'].includes(value)) throw new AppError('E_USAGE', `Config key '${key}' needs true or false.`);
    parsed = value === 'true';
  }
  setPath(ws.config, key, parsed);
  writeJsonAtomic(ws.paths.config, ws.config, ws.paths.tmp);
  return emit(ctx, 'config', { data: { key, value: parsed, previous: current }, text: `${key} = ${parsed} (was ${current})` });
}

export async function adapters(ctx) {
  const ws = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
  const sub = ctx.positional[0] || 'install';
  if (!['install', 'check', 'print', 'list'].includes(sub)) {
    throw new AppError('E_USAGE', `Unknown 'adapters ${sub}'.`, { hint: `${BIN} adapters install|check|print|list` });
  }
  if (sub === 'print') {
    return emit(ctx, 'adapters', { data: { hash: adapterTemplates.bodyHash(), block: adapterTemplates.block() }, text: adapterTemplates.block() });
  }
  if (sub === 'list') {
    const rows = Object.keys(adapterTemplates.VENDORS).map((k) => adapterTemplates.statusFor(ws.root, k));
    return emit(ctx, 'adapters', { data: { vendors: rows }, text: rows.map((r) => `${r.vendor.padEnd(9)}${r.state.padEnd(10)}${r.file}`).join('\n') });
  }
  const requested = ctx.flags.vendor ? String(ctx.flags.vendor).split(',').map((s) => s.trim()) : null;
  const keys = !requested || requested.includes('all') ? Object.keys(adapterTemplates.VENDORS) : requested;
  const check = sub === 'check' || Boolean(ctx.flags.check);
  const results = adapterTemplates.install(ws.root, keys, { check, tmpDir: ws.paths.tmp });
  const bad = results.filter((r) => r.state !== 'current');
  const text = results.map((r) => `${r.state === 'current' ? 'ok      ' : `${r.state.padEnd(8)}`}${r.file}${r.action && !check ? `  (${r.action})` : ''}`).join('\n');
  if (check && bad.length) {
    throw new AppError('E_DRIFT', `${bad.length} instruction file(s) are missing or out of date.`, {
      hint: `${BIN} adapters install`,
      details: { report: text, vendors: results },
    });
  }
  return emit(ctx, 'adapters', { data: { vendors: results, hash: adapterTemplates.bodyHash() }, text: text || 'nothing to do' });
}

const LEGACY = { todo: 'To-do.done.md', updates: 'shared-development-updates.md' };

function parseLegacy(text, file) {
  // Deliberately dumb: one note per bullet or heading, in file order. No cleverness,
  // because guessing wrong about someone's history is worse than importing it verbatim.
  const out = [];
  const lines = text.split(/\r?\n/);
  let heading = null;
  let buf = [];
  const flush = () => {
    if (!buf.length) return;
    const body = buf.join('\n').trim();
    if (body) out.push({ heading, body, file });
    buf = [];
  };
  for (const line of lines) {
    if (/^#{1,6}\s+/.test(line)) { flush(); heading = line.replace(/^#+\s+/, '').trim(); continue; }
    if (/^\s*[-*]\s+/.test(line)) { flush(); buf.push(line.replace(/^\s*[-*]\s+/, '').trim()); continue; }
    buf.push(line);
  }
  flush();
  return out;
}

function parseAuthorMap(raw) {
  const map = new Map();
  if (!raw) return map;
  for (const pair of [].concat(raw).join(',').split(',')) {
    if (!pair.trim()) continue;
    const i = pair.indexOf('=');
    if (i === -1) {
      throw new AppError('E_USAGE', `--author-map wants "old=new" pairs, got '${pair.trim()}'.`, { hint: '--author-map "agent1=claude,agent2=copilot"' });
    }
    map.set(pair.slice(0, i).trim().toLowerCase(), pair.slice(i + 1).trim());
  }
  return map;
}

// Attribution is deliberately conservative. A leading "name:" is only believed
// when the name is already known: either mapped with --author-map or an actor
// that has joined. Guessing an author from prose would put words in somebody's
// mouth, which is worse than attributing the import to the importer.
function attributeEntry(entry, { authorMap, knownActors, fallback }) {
  const m = entry.body.match(/^\s*(?:\*\*|__|\[|@)?\s*([A-Za-z][A-Za-z0-9 ._-]{0,40}?)\s*(?:\*\*|__|\])?\s*[:>-]\s+/);
  const candidate = m ? m[1].trim() : (entry.heading || '').trim();
  if (!candidate) return { actor: fallback, detected: null };
  const key = candidate.toLowerCase();
  if (authorMap.has(key)) return { actor: { id: null, name: authorMap.get(key) }, detected: candidate };
  const known = knownActors.get(key);
  if (known) return { actor: { id: known.id, name: known.name }, detected: candidate };
  return { actor: fallback, detected: null };
}

export async function migrate(ctx) {
  const ws = openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
  const { actor, session } = requireActor(ws, { agent: ctx.flags.agent, vendor: ctx.flags.vendor });
  const authorMap = parseAuthorMap(ctx.flags['author-map']);
  const knownActors = new Map();
  for (const a of listActors(ws)) { knownActors.set(a.name.toLowerCase(), a); knownActors.set(a.slug.toLowerCase(), a); }
  const targets = [];
  const todo = ctx.flags['todo-done'] || (exists(path.join(ws.root, LEGACY.todo)) ? LEGACY.todo : null);
  const updates = ctx.flags.updates || (exists(path.join(ws.root, LEGACY.updates)) ? LEGACY.updates : null);
  if (todo) targets.push({ rel: todo, kind: 'legacy.todo' });
  if (updates) targets.push({ rel: updates, kind: 'legacy.update' });
  if (!targets.length) {
    throw new AppError('E_NOT_FOUND', `No legacy files found (${LEGACY.todo}, ${LEGACY.updates}).`, {
      hint: `${BIN} migrate --todo-done <file> --updates <file>`,
    });
  }
  const dryRun = Boolean(ctx.flags['dry-run']);
  const imported = [];
  for (const t of targets) {
    const abs = path.join(ws.root, t.rel);
    let raw;
    try { raw = fs.readFileSync(abs, 'utf8'); } catch (e) {
      throw new AppError('E_NOT_FOUND', `Cannot read ${t.rel}: ${e.code}`);
    }
    const entries = parseLegacy(raw, t.rel);
    for (const e of entries) {
      const { actor: credited, detected } = attributeEntry(e, { authorMap, knownActors, fallback: actor });
      if (!dryRun) {
        pin(ws, {
          type: t.kind, actor: credited, session,
          subject: { kind: 'file', id: t.rel },
          summary: e.body.split('\n')[0].slice(0, 200),
          data: {
            sourceFile: t.rel, heading: e.heading, body: e.body,
            importedAt: nowIso(), importedBy: actor.name,
            attributedTo: credited.name, detectedAuthor: detected,
          },
        });
      }
      imported.push({ file: t.rel, heading: e.heading, attributedTo: credited.name, summary: e.body.split('\n')[0].slice(0, 120) });
    }
    if (!dryRun && ctx.flags.freeze) {
      const banner = [
        '<!-- FROZEN by fridge migrate.',
        `     Imported into ${STATE_DIR}/notes/ on ${nowIso()} by ${actor.name}.`,
        `     Do not edit this file. Pin notes with: ${BIN} pin "..."`,
        `     Read history with: ${BIN} log -->`,
        '',
      ].join('\n');
      if (!raw.startsWith('<!-- FROZEN')) writeAtomic(abs, banner + raw, ws.paths.tmp);
    }
  }
  if (!dryRun) autoRender(ws);
  const text = [
    `${dryRun ? 'Would import' : 'Imported'} ${imported.length} entr(ies) from ${targets.map((t) => t.rel).join(', ')}.`,
    ctx.flags.freeze && !dryRun ? 'Legacy files marked FROZEN. They are now read-only history.' : '',
    dryRun ? '' : `Read them back with: ${BIN} log --limit ${Math.min(imported.length, 50)}`,
  ].filter(Boolean).join('\n');
  return emit(ctx, 'migrate', { data: { dryRun, count: imported.length, entries: imported.slice(0, 200), files: targets.map((t) => t.rel) }, text });
}
