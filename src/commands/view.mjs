// SPDX-License-Identifier: Apache-2.0
import fs from 'node:fs';
import path from 'node:path';
import { AppError } from '../core/errors.mjs';
import { emit } from '../core/output.mjs';
import { writeAtomic, writeJsonAtomic } from '../core/fsx.mjs';
import { openWorkspace } from '../core/store.mjs';
import { doorDrift, renderDoor, renderDoorFrom, renderStatusText, snapshot } from '../core/render.mjs';
import { resolveInsideWorkspace } from '../core/paths.mjs';
import { sleep } from '../core/util.mjs';
import { BIN } from '../brand.mjs';

const open = (ctx) => openWorkspace({ repo: ctx.flags.repo, cwd: ctx.cwd });
const readOr = (file) => { try { return fs.readFileSync(file, 'utf8'); } catch { return null; } };

export async function board(ctx) {
  const ws = open(ctx);
  const doc = renderDoor(ws);
  if (ctx.flags.check) {
    const onDisk = readOr(ws.paths.door);
    if (doorDrift(ws, onDisk).drift) {
      throw new AppError('E_DRIFT', 'DOOR.md does not match the state in .fridge/.', {
        hint: `${BIN} render`,
        details: { path: ws.paths.door, exists: onDisk !== null },
      });
    }
    return emit(ctx, 'board', { data: { drift: false }, text: 'door is up to date' });
  }
  if (ctx.flags.write) writeAtomic(ws.paths.door, doc, ws.paths.tmp);
  return emit(ctx, 'board', { data: snapshot(ws), text: doc });
}

export async function status(ctx) {
  const ws = open(ctx);
  const mine = ctx.flags.mine ? (ctx.flags.agent || process.env.FRIDGE_ACTOR || null) : null;
  if (!ctx.flags.watch) {
    return emit(ctx, 'status', { data: snapshot(ws), text: renderStatusText(ws, { mine, wide: Boolean(ctx.flags.wide) }) });
  }
  const intervalMs = Math.max(250, Number(ctx.flags.interval || 2000));
  for (;;) {
    process.stdout.write(`${renderStatusText(open(ctx), { mine, wide: Boolean(ctx.flags.wide) })}\n\n`);
    await sleep(intervalMs);
  }
}

export async function render(ctx) {
  const ws = open(ctx);
  // One snapshot for the body, the stamp and status.json, so all three
  // describe the same instant.
  const snap = snapshot(ws);
  const doc = renderDoorFrom(snap);
  const targets = [
    ...(ctx.flags.output ? [resolveInsideWorkspace(ws.root, ctx.flags.output, '--output')] : [ws.paths.door]),
    ...(ws.config.door.extraTargets || []).map((t) => resolveInsideWorkspace(ws.root, t, 'door.extraTargets entry')),
  ];
  if (ctx.flags.check) {
    const drifted = targets.filter((t) => doorDrift(ws, readOr(t)).drift);
    if (drifted.length) {
      throw new AppError('E_DRIFT', `${drifted.length} generated view(s) are out of date.`, {
        hint: `${BIN} render`,
        details: { report: drifted.join('\n'), targets: drifted },
      });
    }
    return emit(ctx, 'render', { data: { drift: false, targets }, text: 'all generated views are up to date' });
  }
  for (const t of targets) writeAtomic(t, doc, ws.paths.tmp);
  writeJsonAtomic(path.join(ws.paths.views, 'status.json'), snap, ws.paths.tmp);
  return emit(ctx, 'render', {
    data: { targets, snapshot: snap },
    text: `wrote ${targets.length + 1} generated view(s):\n${[...targets, path.join(ws.paths.views, 'status.json')].map((t) => `  ${path.relative(ws.root, t)}`).join('\n')}`,
  });
}
