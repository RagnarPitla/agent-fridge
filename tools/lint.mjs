// SPDX-License-Identifier: Apache-2.0
// The only linter this project needs, and it has no dependencies:
//   1. every shipped .mjs file parses under this Node version
//   2. every shipped file is pure ASCII (Windows consoles, CI logs, and
//      screen readers all stay readable; no smart quotes, no box drawing)
//   3. every source file carries the SPDX header, .mjs and .go alike
//   4. no forbidden output paths or stray debugging statements
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const repo = fileURLToPath(new URL('..', import.meta.url));
const SKIP = new Set(['node_modules', '.git', '.scratch', '.tmp', '.toolchain', '.fridge', 'quarantine', 'dist']);
const problems = [];

const walk = (dir) => {
  for (const e of fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
    if (SKIP.has(e.name)) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p);
    else yieldFile(p);
  }
};

const TEXT = /\.(mjs|js|json|md|yml|yaml|sh|ps1|txt|go|mod)$/;
const files = [];
const yieldFile = (p) => { if (TEXT.test(p)) files.push(p); };
walk(repo);

const rel = (p) => path.relative(repo, p);
const add = (file, line, msg) => problems.push(`${rel(file)}:${line}: ${msg}`);

for (const file of files) {
  const text = fs.readFileSync(file, 'utf8');
  const lines = text.split('\n');

  lines.forEach((line, i) => {
    const bad = [...line].find((ch) => ch.codePointAt(0) > 126 || (ch.codePointAt(0) < 32 && ch !== '\t' && ch !== '\r'));
    if (bad) {
      const cp = bad.codePointAt(0).toString(16).padStart(4, '0');
      add(file, i + 1, `non-ASCII character U+${cp.toUpperCase()} (${JSON.stringify(bad)}); use plain ASCII`);
    }
    if (/\r$/.test(line) && !file.endsWith('.ps1')) add(file, i + 1, 'CRLF line ending in a non-PowerShell file');
  });

  if (/\.(mjs|js)$/.test(file)) {
    const head = text.startsWith('#!') ? text.slice(text.indexOf('\n') + 1) : text;
    if (!head.startsWith('// SPDX-License-Identifier: Apache-2.0')) {
      add(file, 1, 'missing SPDX-License-Identifier header');
    }
    const r = spawnSync(process.execPath, ['--check', file], { encoding: 'utf8' });
    if (r.status !== 0) add(file, 1, `does not parse: ${(r.stderr || '').split('\n')[0]}`);

    const isTool = rel(file).startsWith('tools') || rel(file).startsWith('test');
    if (!isTool) {
      lines.forEach((line, i) => {
        if (/console\.(log|error|warn)\(/.test(line) && !line.includes('lint-allow')) {
          add(file, i + 1, 'use src/core/output.mjs instead of console.* in shipped code');
        }
        if (/\/tmp\/|os\.tmpdir\(\)/.test(line) && !line.includes('lint-allow')) {
          add(file, i + 1, 'shipped code must not write outside the workspace');
        }
      });
    }
  }

  if (/\.go$/.test(file)) {
    // Go files get the same ASCII and SPDX treatment. Parsing them is `go vet`'s
    // job; the Go toolchain is not a prerequisite for running this linter.
    if (!text.startsWith('// SPDX-License-Identifier: Apache-2.0')) {
      add(file, 1, 'missing SPDX-License-Identifier header');
    }
    if (!/_test\.go$/.test(file)) {
      lines.forEach((line, i) => {
        if (/os\.TempDir\(\)|"\/tmp\//.test(line) && !line.includes('lint-allow')) {
          add(file, i + 1, 'shipped code must not write outside the workspace');
        }
      });
    }
  }
}

if (problems.length) {
  process.stderr.write(`${problems.length} lint problem(s):\n${problems.join('\n')}\n`);
  process.exit(1);
}
process.stdout.write(`lint: ${files.length} files clean (ASCII, parse, SPDX)\n`);
