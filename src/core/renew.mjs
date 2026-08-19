// SPDX-License-Identifier: Apache-2.0
// Piggyback renewal: any command you run is proof you are still alive, so it
// refreshes your own leases when they are more than half used up. No daemon
// needed.
//
// The implementation lives in the store, next to identity resolution, so that
// every command that says who it is renews without having to remember to.
// This module stays as the documented entry point.
export { renewOwnLeases as maybeRenew, renewOwnLeases } from './store.mjs';
