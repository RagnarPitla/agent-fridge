// SPDX-License-Identifier: Apache-2.0
// Library surface. The CLI is the product; this exists so tests, hooks and
// editor extensions can reuse the same primitives without shelling out.
export { main, parseArgs, COMMANDS } from './cli.mjs';
export { EXIT, EXIT_DOC, AppError } from './core/errors.mjs';
export * as store from './core/store.mjs';
export * as paths from './core/paths.mjs';
export * as fsx from './core/fsx.mjs';
export { withMutex } from './core/mutex.mjs';
export { renderDoor, renderStatusText, snapshot } from './core/render.mjs';
export { BIN, PACKAGE, PRODUCT, PROTOCOL, STATE_DIR, VERSION } from './brand.mjs';
