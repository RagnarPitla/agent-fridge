// SPDX-License-Identifier: Apache-2.0
// Piggyback renewal: any command you run is proof you are still alive, so it
// refreshes your own leases when they are more than half used up. No daemon needed.
import { listClaims, writeLease } from './store.mjs';

export function maybeRenew(ws, session) {
  if (!session || process.env.FRIDGE_NO_RENEW === '1') return [];
  if (!ws.config.lease.renewOnAnyCommand) return [];
  const ratio = ws.config.lease.renewThresholdRatio;
  const renewed = [];
  for (const d of listClaims(ws)) {
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
