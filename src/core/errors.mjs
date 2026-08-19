// SPDX-License-Identifier: Apache-2.0
// The exit-code contract. Stable across all 0.x releases.
// spec/exit-codes.md is generated from this file by tools/gen-exit-codes.mjs.

export const EXIT = {
  OK: 0,
  E_INTERNAL: 1,
  E_USAGE: 2,
  E_NOT_INITIALIZED: 3,
  E_PROTOCOL_VERSION: 4,
  E_STATE_CORRUPT: 5,
  E_PERMISSION: 6,
  E_NO_SESSION: 7,
  E_CONFLICT: 10,
  E_NOT_FOUND: 11,
  E_NOT_OWNER: 12,
  E_LEASE_EXPIRED: 13,
  E_OUT_OF_SCOPE: 14,
  E_ALREADY_EXISTS: 15,
  E_MUTEX_TIMEOUT: 20,
  E_WAIT_TIMEOUT: 21,
  E_QUEUE_ABANDONED: 22,
  E_DRIFT: 30,
  E_NONCONFORMANT: 31,
  E_PATH_INVALID: 40,
  E_FOREIGN_HOST: 41,
};

export const EXIT_DOC = {
  OK: 'Success.',
  E_INTERNAL: 'Unexpected internal error (a bug). Re-run with --verbose for a stack trace.',
  E_USAGE: 'Bad arguments: unknown flag, missing required flag, invalid duration.',
  E_NOT_INITIALIZED: 'No .fridge/ found from the current directory upward. Run: fridge init',
  E_PROTOCOL_VERSION: '.fridge/VERSION is a protocol version this binary does not support.',
  E_STATE_CORRUPT: 'A record is unparseable or invalid, or a write could not be completed.',
  E_PERMISSION: 'Permission denied or read-only filesystem under .fridge/.',
  E_NO_SESSION: 'No actor/session could be resolved. Run: fridge join --agent <name>',
  E_CONFLICT: 'The requested scope overlaps a claim held by someone else.',
  E_NOT_FOUND: 'No such claim, message, actor, or queue entry.',
  E_NOT_OWNER: 'You do not hold the token for that claim.',
  E_LEASE_EXPIRED: 'Your claim already expired and was reaped.',
  E_OUT_OF_SCOPE: 'That path is not covered by any claim you hold.',
  E_ALREADY_EXISTS: 'Already exists (workspace, actor, or record).',
  E_MUTEX_TIMEOUT: 'Could not acquire the registry mutex before the deadline.',
  E_WAIT_TIMEOUT: 'Wait deadline reached.',
  E_QUEUE_ABANDONED: 'The queue entry expired or was cancelled.',
  E_DRIFT: 'A --check found a problem: doctor findings, unrendered door, or stale adapter block.',
  E_NONCONFORMANT: 'This build disagrees with the protocol vectors. Run: fridge conform --verbose',
  E_PATH_INVALID: 'Path rejected: traversal, escape, reserved location, or unsupported glob.',
  E_FOREIGN_HOST: 'That claim belongs to another host. Pass --allow-multihost to override.',
};

export class AppError extends Error {
  constructor(code, message, extra = {}) {
    super(message);
    this.name = 'AppError';
    if (EXIT[code] === undefined) {
      throw new TypeError(`AppError: unknown exit code '${code}'. Add it to EXIT and EXIT_DOC first.`);
    }
    this.code = code;
    this.exitCode = EXIT[code];
    this.hint = extra.hint || null;
    this.details = extra.details || null;
  }
}

export const fail = (code, message, extra) => {
  throw new AppError(code, message, extra);
};
