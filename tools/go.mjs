// SPDX-License-Identifier: Apache-2.0
// One entry point for the Go half of the project, so package.json scripts stay
// short and work the same on macOS, Linux, and Windows.
//
//   node tools/go.mjs build ./...      any go subcommand, forwarded verbatim
//   node tools/go.mjs fmt              gofmt -l over cmd/ and internal/
//   node tools/go.mjs dist             cross-compile every supported target
//
// It prefers the pinned toolchain in .toolchain/go/bin over whatever is on
// PATH, and it keeps the module and build caches inside .scratch/ so a checkout
// never writes to the user's home directory.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const repo = fileURLToPath(new URL('..', import.meta.url));
const exe = process.platform === 'win32' ? '.exe' : '';
const local = path.join(repo, '.toolchain', 'go', 'bin', `go${exe}`);
const go = fs.existsSync(local) ? local : `go${exe}`;
const gofmt = fs.existsSync(local) ? path.join(path.dirname(local), `gofmt${exe}`) : `gofmt${exe}`;

// Only redirect the toolchain and caches when this checkout vendored its own
// Go. On a machine with a real Go installation, including CI, inherit it: a
// build tool that quietly overrides GOTOOLCHAIN produces failures that look
// like source errors and are not.
const vendored = fs.existsSync(local);
const env = {
  ...process.env,
  GOFLAGS: process.env.GOFLAGS || '',
  ...(vendored ? {
    GOTOOLCHAIN: 'local',
    GOMODCACHE: path.join(repo, '.scratch', 'gomodcache'),
    GOCACHE: path.join(repo, '.scratch', 'gocache'),
  } : {}),
};

const run = (cmd, args, extra = {}) => {
  const r = spawnSync(cmd, args, { cwd: repo, env: { ...env, ...extra }, stdio: 'inherit' });
  if (r.error && r.error.code === 'ENOENT') {
    process.stderr.write(`go toolchain not found (looked for ${local} and ${cmd} on PATH)\n`);
    process.exit(127);
  }
  return r.status ?? 1;
};

const capture = (cmd, args) => {
  const r = spawnSync(cmd, args, { cwd: repo, env, encoding: 'utf8' });
  if (r.error) {
    process.stderr.write(`${cmd}: ${r.error.message}\n`);
    process.exit(127);
  }
  return r;
};

const TARGETS = [
  ['darwin', 'amd64'],
  ['darwin', 'arm64'],
  ['linux', 'amd64'],
  ['linux', 'arm64'],
  ['windows', 'amd64'],
  ['windows', 'arm64'],
];

const [sub, ...rest] = process.argv.slice(2);

if (sub === 'fmt') {
  // gofmt -l . would walk .toolchain/go/src and report the whole standard
  // library, so keep it to the code this repo actually owns.
  const r = capture(gofmt, ['-l', 'cmd', 'internal', 'vectors']);
  const listed = (r.stdout || '').split('\n').filter(Boolean);
  if (r.stderr) process.stderr.write(r.stderr);
  if (listed.length) {
    process.stderr.write(`gofmt would reformat ${listed.length} file(s):\n${listed.join('\n')}\n`);
    process.exit(1);
  }
  process.stdout.write('gofmt: cmd/, internal/ and vectors/ are formatted\n');
  process.exit(0);
}

if (sub === 'dist') {
  // Asset names are the ones install.sh and install.ps1 ask for, and the ones
  // the README documents. Changing them breaks every published install line.
  const out = path.join(repo, 'dist');
  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(out, { recursive: true });
  let failed = 0;
  const sums = [];
  for (const [goos, goarch] of TARGETS) {
    const suffix = goos === 'windows' ? '.exe' : '';
    const name = `fridge_${goos}_${goarch}${suffix}`;
    const bin = path.join(out, name);
    // -trimpath so the binary does not embed this machine's paths;
    // -s -w to drop the symbol table, which the CLI never needs.
    const status = run(go, ['build', '-trimpath', '-ldflags', '-s -w', '-o', bin, './cmd/fridge'], { GOOS: goos, GOARCH: goarch });
    const ok = status === 0;
    if (!ok) { failed += 1; process.stdout.write(`FAIL ${goos}/${goarch}\n`); continue; }
    const digest = crypto.createHash('sha256').update(fs.readFileSync(bin)).digest('hex');
    fs.writeFileSync(`${bin}.sha256`, `${digest}  ${name}\n`);
    sums.push(`${digest}  ${name}`);
    process.stdout.write(`ok   ${goos}/${goarch}  ${(fs.statSync(bin).size / 1048576).toFixed(1)} MiB  ${digest.slice(0, 12)}\n`);
  }
  if (!failed) {
    const skillName = 'SKILL.md';
    const skill = path.join(out, skillName);
    fs.copyFileSync(path.join(repo, 'skill', skillName), skill);
    const skillDigest = crypto.createHash('sha256').update(fs.readFileSync(skill)).digest('hex');
    fs.writeFileSync(`${skill}.sha256`, `${skillDigest}  ${skillName}\n`);
    sums.push(`${skillDigest}  ${skillName}`);
    fs.writeFileSync(path.join(out, 'checksums.txt'), `${sums.join('\n')}\n`);
    for (const script of ['install.sh', 'install.ps1']) {
      fs.copyFileSync(path.join(repo, script), path.join(out, script));
    }
    process.stdout.write(`\n${TARGETS.length} binaries and 1 Agent Skill built into dist/, with checksums and installers\n`);
  }
  process.exit(failed ? 1 : 0);
}

if (!sub) {
  process.stderr.write('usage: node tools/go.mjs <build|vet|test|fmt|dist|...> [args]\n');
  process.exit(2);
}

process.exit(run(go, [sub, ...rest]));
