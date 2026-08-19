// SPDX-License-Identifier: Apache-2.0
// spec/exit-codes.md is generated. src/core/errors.mjs is the source of truth.
// Usage: node tools/gen-exit-codes.mjs [--check]
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { EXIT, EXIT_DOC } from '../src/core/errors.mjs';
import { PRODUCT, BIN, PROTOCOL } from '../src/brand.mjs';

const repo = fileURLToPath(new URL('..', import.meta.url));
const out = path.join(repo, 'spec', 'exit-codes.md');
const check = process.argv.includes('--check');

const rows = Object.entries(EXIT)
  .sort((a, b) => a[1] - b[1] || a[0].localeCompare(b[0]))
  .map(([name, code]) => `| \`${code}\` | \`${name}\` | ${EXIT_DOC[name] || ''} |`);

const missing = Object.keys(EXIT).filter((k) => !EXIT_DOC[k]);
if (missing.length) {
  process.stderr.write(`every exit code needs a description; missing: ${missing.join(', ')}\n`);
  process.exit(1);
}

const body = `<!-- GENERATED FILE. Edit src/core/errors.mjs, then run: npm run gen -->
# ${PRODUCT} exit codes (protocol ${PROTOCOL})

Exit codes are the public API of \`${BIN}\`. A script may branch on them.
They are stable for the whole 0.x line: numbers are never reused or renumbered,
only added.

Rules:

1. \`0\` means the operation happened. Nothing else means it happened.
2. Every non-zero code is a deliberate, documented refusal. There is no silent
   fallback and no partial success.
3. All codes are below \`126\`, so they never collide with the shell's
   "command not found" (127) or "killed by signal" (128+N) range.
4. \`--json\` prints the same information on stdout as
   \`{"ok":false,"error":{"code":"E_...","exit":N,...}}\`, so callers that cannot
   read \`$?\` portably can parse instead.

| Exit | Code | Meaning |
| ---: | ---- | ------- |
${rows.join('\n')}

## Reading them from a shell

\`\`\`bash
if ${BIN} claim "src/api/**" --task "refactor"; then
  : # the chore is yours
elif [ $? -eq 10 ]; then
  echo "someone else has it; pick another chore"
else
  exit 1 # a real error, do not guess
fi
\`\`\`

## Reading them from PowerShell

\`\`\`powershell
${BIN} claim "src/api/**" --task "refactor"
switch ($LASTEXITCODE) {
  0  { "the chore is yours" }
  10 { "someone else has it" }
  default { throw "fridge failed with $LASTEXITCODE" }
}
\`\`\`
`;

if (check) {
  const current = fs.existsSync(out) ? fs.readFileSync(out, 'utf8') : '';
  if (current !== body) {
    process.stderr.write(`spec/exit-codes.md is out of date. Run: npm run gen\n`);
    process.exit(30);
  }
  process.stdout.write('spec/exit-codes.md is up to date.\n');
  process.exit(0);
}

fs.mkdirSync(path.dirname(out), { recursive: true });
fs.writeFileSync(out, body);
process.stdout.write(`wrote ${path.relative(repo, out)} (${Object.keys(EXIT).length} codes)\n`);
