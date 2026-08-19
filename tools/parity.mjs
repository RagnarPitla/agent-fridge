// SPDX-License-Identifier: Apache-2.0
// Parity harness: drive the same command sequence through the Node CLI and the
// Go binary in two fresh workspaces, then diff exit codes and --json envelopes
// with the unavoidably volatile fields (ids, timestamps, hosts, pids) masked.
//
//   npm run go:parity              summary table
//   PARITY_VERBOSE=1 npm run go:parity   full payload diffs on mismatch
//
// Volatile facts (ids, timestamps, hosts, pids, absolute paths, remaining TTL)
// are masked, because two independent runs cannot agree on those. Everything
// else has to match exactly. Exits non-zero on the first sign of drift.
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const repo = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const goBin = path.join(repo, '.scratch', 'parity', process.platform === 'win32' ? 'fridge.exe' : 'fridge');
const nodeCli = path.join(repo, 'bin', 'fridge.mjs');

// Use the vendored toolchain when this checkout has one, otherwise whatever Go
// is on PATH. CI has the latter; a contributor who ran the bootstrap has the
// former. Neither should have to know about the other.
const vendoredGo = path.join(repo, '.toolchain', 'go', 'bin', process.platform === 'win32' ? 'go.exe' : 'go');
const usingVendored = fs.existsSync(vendoredGo);
const goExe = usingVendored ? vendoredGo : 'go';

const goEnv = { ...process.env };
if (usingVendored) {
  goEnv.GOTOOLCHAIN = 'local';
  goEnv.GOMODCACHE = path.join(repo, '.scratch', 'gomodcache');
  goEnv.GOCACHE = path.join(repo, '.scratch', 'gocache');
}

const build = spawnSync(goExe, ['build', '-o', goBin, './cmd/fridge'], { cwd: repo, stdio: 'inherit', env: goEnv });
if (build.error) {
  process.stderr.write(`parity: cannot run '${goExe}'. Install Go 1.21+ or run the toolchain bootstrap.\n`);
  process.exit(1);
}
if (build.status !== 0) process.exit(1);

const fresh = (name) => {
  const dir = path.join(repo, '.scratch', 'parity', name);
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(path.join(dir, 'src', 'api'), { recursive: true });
  fs.mkdirSync(path.join(dir, '.git'), { recursive: true });
  return dir;
};

const run = (impl, dir, args, actor) => {
  const cmd = impl === 'go' ? goBin : process.execPath;
  const argv = impl === 'go' ? args : [nodeCli, ...args];
  const r = spawnSync(cmd, argv, {
    cwd: dir,
    encoding: 'utf8',
    env: { ...process.env, FRIDGE_ACTOR: actor || '', NO_COLOR: '1' },
  });
  return { code: r.status ?? -1, out: r.stdout || '', err: r.stderr || '' };
};

// Everything that is allowed to differ between two independent runs.
const mask = (value) => {
  if (Array.isArray(value)) return value.map(mask);
  if (value && typeof value === 'object') {
    const out = {};
    for (const [k, v] of Object.entries(value)) {
      if (['ts', 'createdAt', 'updatedAt', 'startedAt', 'lastSeenAt', 'expiresAt',
        'expiresAtInitial', 'effectiveExpiresAt', 'generatedAt', 'acquiredAt',
        'renewedAt', 'stateKey', 'workspaceId', 'sessionId', 'host', 'user',
        'pid', 'expiresInMs', 'ttlRemaining', 'token', 'writer', 'root',
        'node', 'runtime', 'implementation', 'platform', 'arch',
        // The Go binary embeds the conformance vectors and the Node
        // implementation reads them from disk. Where the vectors came from is
        // not behaviour; the verdict on them is, and that is compared.
        'vectorDir', 'bundled', 'implementation'].includes(k)) {
        out[k] = `<${k}>`;
        continue;
      }
      out[k] = mask(v);
    }
    return out;
  }
  if (typeof value === 'string') return maskText(value);
  return value;
};

// The same volatile facts also show up inside rendered human text.
const maskText = (s) => s
  .replace(/\b(clm|act|ses|evt|msg|wai|wsp|hnd)_[0-9A-Z]{26}\b/g, '$1_<id>')
  .replace(/\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z/g, '<ts>')
  .replace(/\d{8}T\d{9}Z/g, '<compactTs>')
  .replace(/\bpid \d+/g, 'pid <pid>')
  .replace(/\(in [^)]*\)/g, '(in <ttl>)')
  .replace(/^vectors: .*$/gm, 'vectors: <vectors>')
  .replace(/^Conformance: .* against /gm, 'Conformance: <impl> against ')
  .replace(/\b\d+[hmsd](?: \d+[hms])?\b/g, '<dur>')
  .split(goDirRe).join('<root>')
  .split(nodeDirRe).join('<root>');

const stable = (v) => JSON.stringify(mask(v), null, 2);

const nodeDir = fresh('node-side');
const goDir = fresh('go-side');
const goDirRe = goDir;
const nodeDirRe = nodeDir;

// One shared script, replayed identically on both sides.
const SCRIPT = [
  ['init --no-adapters', ''],
  ['whoami --json', 'alice'],
  ['join --agent alice --vendor claude --json', ''],
  ['join --agent bob --vendor codex --json', ''],
  ['whoami --json', 'alice'],
  ["claim src/api/** --task 'build the api' --ttl 5m --json", 'alice'],
  ["claim src/api/routes.ts --task 'sneak in' --json", 'bob'],
  ["claim src/api/** --task 'same shelf again' --json", 'alice'],
  ["claim src/api/** --mode shared --task 'mode change' --json", 'alice'],
  ["claim 'src/{ui,web}/**' --mode shared --task 'read the ui' --json", 'bob'],
  ["claim src/ui/** --mode shared --task 'also read the ui' --json", 'alice'],
  ["claim src/ui/** --mode advisory --task 'advisory is compatible' --json", 'bob'],
  ["claim docs/** --task 'strict has no self-merge' --strict --json", 'alice'],
  ["claim docs/** --task 'strict again' --strict --json", 'alice'],
  ['check src/api/routes.ts --json', 'bob'],
  ['check src/docs/readme.md --json', 'bob'],
  ['guard src/api/routes.ts --json', 'bob'],
  ['guard src/docs/readme.md --json', 'bob'],
  ['status --json', 'alice'],
  ['board --json', 'alice'],
  ['board --check', 'alice'],
  ['heartbeat --json', 'alice'],
  ['extend $CLAIM --ttl 10m --json', 'alice'],
  ["pin 'a note for the wall' --json", 'alice'],
  ["pin --kind decision 'we are going with sqlite' --json", 'bob'],
  ["note 'the alias writes the same note' --json", 'bob'],
  ['log --limit 5 --json', 'alice'],
  ['log --limit 3 --actor bob --json', 'alice'],
  ['log --type decision --json', 'alice'],
  ['inbox --json', 'bob'],
  ['render --json', 'alice'],
  ['doctor --check --json', 'alice'],
  ['tidy --check --json', 'alice'],
  ['doctor --fix --json', 'alice'],
  ['reap --json', 'alice'],
  ['sweep --force --json', 'alice'],
  ['door --json', 'alice'],
  ['config lease.ttlMs --json', 'alice'],
  ['config mutex.acquireTimeoutMs --json', 'alice'],
  ['adapters --json', 'alice'],
  ['version --json', 'alice'],
  ['help --json', 'alice'],
  ['help claim --json', 'alice'],
  ['migrate --dry-run --json', 'alice'],
  ['wait clm_00000000000000000000000000 --timeout 1s --json', 'bob'],
  ["run --claim 'tools/**' --task 'quick job' --json -- node -e 'process.exit(0)'", 'alice'],
  ["run --claim 'tools/**' --task 'failing job' --json -- node -e 'process.exit(7)'", 'alice'],
  ["run --claim 'tools/**' --task 'missing binary' --json -- definitely-not-a-real-binary", 'alice'],
  // Error paths. These are the ones that actually pin the exit codes down.
  ['claim ../escape --task nope --json', 'alice'],
  ['claim --task no target --json', 'alice'],
  ['claim src/api/** --ttl 90 --json', 'alice'],
  ['release clm_00000000000000000000000000 --json', 'alice'],
  ['accept msg_00000000000000000000000000 --json', 'bob'],
  ['decline msg_00000000000000000000000000 --json', 'bob'],
  ['extend clm_00000000000000000000000000 --json', 'alice'],
  ['config nope.nope --json', 'alice'],
  ['whoami --json', 'nobody-at-all'],
  ['nonsense --json', 'alice'],
  ['claim src/api/** --mode bogus --task x --json', 'alice'],
  // Wind the door down.
  ["handoff $CLAIM --to bob --note 'over to you' --json", 'alice'],
  ['inbox --json', 'bob'],
  ['decline $MSG --reason busy --json', 'bob'],
  ["pass $CLAIM --to bob --note 'try again' --json", 'alice'],
  ['inbox --json', 'bob'],
  ['accept $MSG --json', 'bob'],
  ['status --json', 'bob'],
  ['release $CLAIM --outcome done --json', 'bob'],
  ['release --all --json', 'alice'],
  ['status --json', 'alice'],
  ['doctor --check --json', 'alice'],
  ['board --check', 'alice'],
  ['conform --json', 'alice'],
  ['conform --suite scope-overlap --json', 'alice'],
  ['conform --suite nope-not-a-suite --json', 'alice'],
];

// A tiny argv splitter so tasks and notes can contain spaces.
const tokenize = (line) => {
  const out = [];
  const re = /'([^']*)'|"([^"]*)"|(\S+)/g;
  let m;
  while ((m = re.exec(line))) out.push(m[1] ?? m[2] ?? m[3]);
  return out;
};

let mismatches = 0;
let compared = 0;
const rows = [];

// Ids differ between the two workspaces, so remember each side's own.
const memo = { go: {}, node: {} };
const substitute = (impl, args) => args.map((a) => (a.startsWith('$') ? (memo[impl][a] || a) : a));
const remember = (impl, envelope) => {
  const d = envelope && envelope.data;
  if (!d) return;
  if (d.claimId) memo[impl].$CLAIM = d.claimId;
  if (d.claim && d.claim.id) memo[impl].$CLAIM = d.claim.id;
  if (d.messageId) memo[impl].$MSG = d.messageId;
  if (Array.isArray(d.messages) && d.messages.length && d.messages[0].id) memo[impl].$MSG = d.messages[0].id;
};

for (const [line, actor] of SCRIPT) {
  const args = tokenize(line);
  const g = run('go', goDir, substitute('go', args), actor);
  const n = run('node', nodeDir, substitute('node', args), actor);
  compared += 1;

  const problems = [];
  if (g.code !== n.code) problems.push(`exit ${g.code} vs ${n.code}`);

  let gj = null;
  let nj = null;
  if (args.includes('--json')) {
    try { gj = JSON.parse(g.out); } catch { gj = null; }
    try { nj = JSON.parse(n.out); } catch { nj = null; }
    remember('go', gj);
    remember('node', nj);
    // Usage errors are raised before --json is known, so both sides fall back
    // to plain text. That is parity too, as long as the text matches.
    if (!gj !== !nj) problems.push(`only one side produced JSON (go=${Boolean(gj)}, node=${Boolean(nj)})`);
    if (!gj && !nj) {
      const gt = maskText(g.out + g.err);
      const nt = maskText(n.out + n.err);
      if (gt !== nt) {
        problems.push('plain-text output differs');
        if (process.env.PARITY_VERBOSE) problems.push(`\n--- go ---\n${gt}\n--- node ---\n${nt}`);
      }
    }
  }
  if (gj && nj) {
    const gk = Object.keys(gj).join(',');
    const nk = Object.keys(nj).join(',');
    if (gk !== nk) problems.push(`envelope keys ${gk} vs ${nk}`);
    const gs = stable(gj);
    const ns = stable(nj);
    if (gs !== ns) {
      problems.push('payload differs');
      if (process.env.PARITY_VERBOSE) {
        problems.push(`\n--- go ---\n${gs}\n--- node ---\n${ns}`);
      }
    }
  }
  if (problems.length) mismatches += 1;
  rows.push({
    line,
    actor: actor || '-',
    code: g.code === n.code ? String(g.code) : `${g.code}/${n.code}`,
    verdict: problems.length ? `MISMATCH: ${problems.join('; ')}` : 'same',
  });
}

const width = Math.max(...rows.map((r) => r.line.length));
for (const r of rows) {
  const flag = r.verdict === 'same' ? '  ' : '!!';
  process.stdout.write(`${flag} ${r.line.padEnd(width)}  ${r.actor.padEnd(16)} exit ${r.code.padEnd(7)} ${r.verdict}\n`);
}
process.stdout.write(`\n${compared} commands compared, ${mismatches} mismatch(es)\n`);
process.exit(mismatches ? 1 : 0);
