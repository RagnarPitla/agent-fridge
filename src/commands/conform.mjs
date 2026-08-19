// SPDX-License-Identifier: Apache-2.0
// The conformance harness. Layer 3 of the package: the thing that makes the
// protocol document real rather than decorative. Any implementation of wcp/0.1
// in any language runs the same vector files and must produce the same answers.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { AppError } from '../core/errors.mjs';
import { emit } from '../core/output.mjs';
import { PROTOCOL, PRODUCT, VERSION } from '../brand.mjs';
import { expandBraces, normalizePattern, patternToRegExp, scopesOverlap } from '../core/paths.mjs';

const BUNDLED = fileURLToPath(new URL('../../vectors/', import.meta.url));

function vectorDir(flag) {
  const dir = flag ? path.resolve(flag) : BUNDLED;
  if (!fs.existsSync(dir)) {
    throw new AppError('E_NOT_FOUND', `No vector directory at ${dir}.`, {
      hint: 'Pass --vectors <dir>, or reinstall so the bundled vectors are present.',
    });
  }
  return dir;
}

// Each runner takes one case and returns null when it conforms, or a string
// explaining the disagreement. Deliberately dumb: a conformance harness that
// shares clever helpers with the implementation is testing nothing.
const RUNNERS = {
  'path-normalization': (c, ctx) => {
    const input = String(c.input).replace('<ROOT>', ctx.root);
    const opts = { root: ctx.root, cwd: path.join(ctx.root, c.cwd || '.') };
    if (c.expect === 'E_PATH_INVALID') {
      try {
        const got = normalizePattern(input, opts).pattern;
        return `expected rejection E_PATH_INVALID, got ${JSON.stringify(got)}`;
      } catch (e) {
        return e instanceof AppError && e.code === 'E_PATH_INVALID' ? null : `expected E_PATH_INVALID, got ${e.code || e.message}`;
      }
    }
    try {
      const got = normalizePattern(input, opts).pattern;
      return got === c.expect ? null : `expected ${JSON.stringify(c.expect)}, got ${JSON.stringify(got)}`;
    } catch (e) {
      return `expected ${JSON.stringify(c.expect)}, threw ${e.code || e.message}`;
    }
  },

  'scope-overlap': (c) => {
    const got = scopesOverlap({ include: c.a, exclude: [] }, { include: c.b, exclude: [] });
    if (got.overlap !== c.overlap) return `expected overlap=${c.overlap}, got ${got.overlap}`;
    if (c.reason && got.reason !== c.reason) return `expected reason ${c.reason}, got ${got.reason}`;
    return null;
  },

  'glob-matching': (c) => {
    const hits = (file) => expandBraces(c.pattern).some((p) => patternToRegExp(p).test(file));
    for (const m of c.matches || []) if (!hits(m)) return `${c.pattern} should match ${m}`;
    for (const m of c.rejects || []) if (hits(m)) return `${c.pattern} should not match ${m}`;
    return null;
  },

  'brace-expansion': (c) => {
    if (c.expect_error) {
      try {
        expandBraces(c.input);
        return `expected ${c.expect_error}, got no error`;
      } catch (e) {
        return e.code === c.expect_error ? null : `expected ${c.expect_error}, got ${e.code || e.message}`;
      }
    }
    let got;
    try { got = expandBraces(c.input); } catch (e) { return `threw ${e.code || e.message}`; }
    const a = [...got].sort();
    const b = [...c.expect].sort();
    return JSON.stringify(a) === JSON.stringify(b) ? null : `expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}`;
  },
};

// Vector files may declare the directory tree they need. Materializing it here
// rather than assuming the caller's cwd is what makes a conformance run
// reproducible on a stranger's machine.
function materializeFixture(fixture, dest) {
  fs.mkdirSync(dest, { recursive: true });
  if (fixture) {
    for (const d of fixture.dirs || []) fs.mkdirSync(path.join(dest, d), { recursive: true });
    for (const [rel, body] of Object.entries(fixture.files || {})) {
      fs.mkdirSync(path.dirname(path.join(dest, rel)), { recursive: true });
      fs.writeFileSync(path.join(dest, rel), body);
    }
  }
  return fs.realpathSync(dest);
}

export async function conform(ctx) {
  const dir = vectorDir(ctx.flags.vectors);
  const files = fs.readdirSync(dir).filter((f) => f.endsWith('.json')).sort();
  if (files.length === 0) {
    throw new AppError('E_NOT_FOUND', `No vector files in ${dir}.`, { hint: 'Vector files are named <suite>.json.' });
  }

  const only = ctx.flags.suite ? String(ctx.flags.suite).split(',').map((s) => s.trim()) : null;
  const scratchParent = fs.realpathSync(ctx.cwd || process.cwd());
  const scratch = fs.mkdtempSync(path.join(scratchParent, '.fridge-conform-'));

  const suites = [];
  let total = 0; let failed = 0; let skipped = 0;

  try {
    for (const file of files) {
      const suite = file.replace(/\.json$/, '');
      if (only && !only.includes(suite)) continue;

      let doc;
      try {
        doc = JSON.parse(fs.readFileSync(path.join(dir, file), 'utf8'));
      } catch (e) {
        throw new AppError('E_STATE_CORRUPT', `Vector file ${file} is not valid JSON: ${e.message}`);
      }

      if (doc.protocol && doc.protocol !== PROTOCOL) {
        suites.push({ suite, title: doc.title || suite, protocol: doc.protocol, status: 'skipped', reason: `vectors target ${doc.protocol}, this build speaks ${PROTOCOL}`, cases: [], passed: 0, failed: 0 });
        skipped += (doc.cases || []).length;
        continue;
      }

      const run = RUNNERS[suite];
      if (!run) {
        suites.push({ suite, title: doc.title || suite, protocol: doc.protocol || null, status: 'skipped', reason: 'no runner for this suite in this implementation', cases: [], passed: 0, failed: 0 });
        skipped += (doc.cases || []).length;
        continue;
      }

      const root = materializeFixture(doc.fixture, path.join(scratch, suite));
      const cases = [];
      let pass = 0; let fail = 0;
      for (const c of doc.cases || []) {
        total += 1;
        let problem;
        try {
          problem = run(c, { root });
        } catch (e) {
          problem = `runner threw: ${e.message}`;
        }
        if (problem) { fail += 1; failed += 1; cases.push({ name: c.name, ok: false, detail: problem }); } else { pass += 1; cases.push({ name: c.name, ok: true, detail: null }); }
      }
      suites.push({ suite, title: doc.title || suite, protocol: doc.protocol || null, status: fail === 0 ? 'pass' : 'fail', reason: null, cases, passed: pass, failed: fail });
    }
  } finally {
    try { fs.rmSync(scratch, { recursive: true, force: true }); } catch { /* best effort */ }
  }

  const ok = failed === 0;
  const data = {
    implementation: `${PRODUCT} (node) ${VERSION}`,
    protocol: PROTOCOL,
    vectorDir: dir,
    bundled: dir === BUNDLED,
    totals: { cases: total, passed: total - failed, failed, skipped },
    conformant: ok,
    suites: ctx.flags.verbose ? suites : suites.map((s) => ({ ...s, cases: s.cases.filter((c) => !c.ok) })),
  };

  const lines = [];
  lines.push(`Conformance: ${PRODUCT} (node) ${VERSION} against ${PROTOCOL}`);
  lines.push(`vectors: ${dir}${data.bundled ? ' (bundled)' : ''}`);
  lines.push('');
  lines.push('| suite | cases | result |');
  lines.push('|---|---:|---|');
  for (const s of suites) {
    const label = s.status === 'skipped' ? `SKIPPED (${s.reason})` : s.status === 'pass' ? 'PASS' : `FAIL (${s.failed})`;
    lines.push(`| ${s.suite} | ${s.passed + s.failed} | ${label} |`);
  }
  for (const s of suites) {
    for (const c of s.cases) {
      if (!c.ok) lines.push(`\n  ${s.suite} / ${c.name}\n    ${c.detail}`);
    }
  }
  lines.push('');
  lines.push(ok
    ? `Result: CONFORMANT. ${total} case(s) passed${skipped ? `, ${skipped} skipped` : ''}.`
    : `Result: NOT CONFORMANT. ${failed} of ${total} case(s) disagree with ${PROTOCOL}.`);

  emit(ctx, 'conform', { data, text: lines.join('\n') });
  if (!ok) throw new AppError('E_NONCONFORMANT', `${failed} conformance case(s) failed.`, { hint: 'Run with --verbose to see every case, or --suite <name> to narrow.' });
}
