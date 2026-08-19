// SPDX-License-Identifier: Apache-2.0
// Notes are durable, and on most workspaces they are committed to Git. Every
// piece of free text that reaches a note is checked here, not just the body of
// `fridge pin`: a task description and a release note land in exactly the same
// permanent file.
import { AppError } from './errors.mjs';

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
  if (typeof text !== 'string' || !text) return null;
  for (const [re, what] of SECRETY) if (re.test(text)) return what;
  return null;
}

/**
 * Refuse to record any of these fields if one of them looks like a credential.
 *
 * `fields` is a map of flag name to value, so the error can say which one.
 */
export function guardSecrets(fields, { allow = false } = {}) {
  if (allow) return null;
  for (const [where, value] of Object.entries(fields)) {
    const found = looksSecret(value);
    if (found) {
      throw new AppError('E_USAGE', `${where} looks like it contains ${found}.`, {
        hint: 'That text becomes a durable note. Remove it, or pass --allow-secret-like if it is a false positive.',
        details: { field: where, kind: found },
      });
    }
  }
  return null;
}
