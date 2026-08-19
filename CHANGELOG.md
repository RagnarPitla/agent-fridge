# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Two version numbers matter and they move independently:

- the **CLI version** (`0.2.1`), which is what the tags and the release binaries track, and
- the **protocol version** (`wcp/0.1`), which is what `.fridge/` records declare.

A CLI release that does not change the protocol leaves `wcp/0.1` alone.

---

## [Unreleased]

### Fixed

- Canonical URLs. `LICENSE` and `NOTICE` still carried the former name and
  pointed at `github.com/RagnarPitla/fridgeboard`, which is not this
  repository. `NOTICE` now records the rename as history instead.
- Install instructions that could not work. `docs/quickstart.md` opened with
  `npx agent-fridge` and `npm install -g agent-fridge`, and `docs/interop.md`
  and `docs/migration.md` repeated the second. Agent Fridge is not on the npm
  registry, so all of those fail on a clean machine. Every documented install
  command is now one of the four that were run end to end: the shell
  installer, the PowerShell installer, `go install`, and
  `npm install -g github:RagnarPitla/agent-fridge`.
- `docs/quickstart.md` listed Node.js as a prerequisite. The tool is a single
  static binary and needs no runtime.
- Two documents said exit `41` (`E_FOREIGN_HOST`) was "specified but not
  enforced by the CLI" and told readers not to rely on it. It is enforced;
  verified by forcing a release of another actor's claim held on another host.
- `docs/migration.md` said `--author-map` was "parsed and then ignored". It
  re-attributes entry by entry; verified by a migration that credited a mapped
  author.
- The README described `--dir` and `--version` for both installers. The
  PowerShell one takes `-Dir` and `-Version`, and defaults to a different
  directory. The sample `fridge version` output did not match the real output.
- The global skill instructions tried to read a `skillPath` field from
  `fridge version --json`, but that field does not exist. They now install the
  checksummed skill asset published with each release.
- The `v0.1.0` GitHub release advertised a preview under the former name with
  no downloadable assets. Retitled and re-noted so it says so. `v0.2.0` now
  carries a note about the liveness bug fixed in `v0.2.1`.

### Added

- A CI job that runs the *published* installers on Linux, macOS and Windows and
  then runs `fridge conform`. `install.ps1` had never been executed by any
  automated check.
- Two guard tests: no document may promise an npm registry install while the
  package is unpublished, and no tracked file may link to the old repository.
- `SKILL.md` and `SKILL.md.sha256` as first-class release assets. The skill uses
  strict, portable front matter with exactly `name` and `description`.

---

## [0.2.1] - 2026-08-19

### Fixed

- The 60-second quick start in the README told people to run
  `fridge join --vendor claude-code`, which the CLI rejects with `E_USAGE`. The
  documented first experience was an error message. The vendor is `claude`.
  Binaries are unaffected, so this does not need a new release.

- A blocked release could strand every later acquirer. Releasing the registry
  mutex tried once and gave up. On Windows every operation it can use - rename,
  unlink, rmdir - fails with a sharing violation while any other process has
  `owner.json` open, and waiters open it on every poll. The handle is open for
  microseconds, but one failure left a lock directory that nobody held and
  nobody could take until the stale window expired. In CI, one blocked release
  stranded fifteen of sixteen workers. A release now retries for up to two
  seconds, and only then falls back to dismantling the lock in place. New
  protocol invariant I7c.

### Added

- `test/unit/readme-quickstart.test.mjs` executes the quick start, checks that
  every `fridge` command the README shows exists in `fridge help`, and checks
  that every `--vendor` the docs pass to `join` is a vendor the CLI accepts.
  Documentation that is never executed is a rumour.

### Changed

- The public display name is **Agent Fridge**. The public narrative now leads
  with the shared-checkout collision problem and the local coordination
  solution, then introduces the fridge door as the mental model. The
  compatibility surface is unchanged: the package and repository remain
  `agent-fridge`, the CLI remains `fridge`, state remains under `.fridge/`, and
  the protocol remains `wcp/0.1`.

---

## [0.2.0] - 2026-02-21

Renamed, rewritten in Go, and given a way for strangers to verify it. The
protocol is unchanged: `wcp/0.1` records written by 0.1.0 are read by 0.2.0
without migration.

### Added

**A native binary as the primary implementation**

- Complete second implementation in Go: `cmd/fridge` and `internal/`, standard
  library only, no `require` block, one static binary per platform.
- Six published builds: darwin/linux/windows on amd64/arm64, each with a
  `.sha256`, plus a `checksums.txt` for the set.
- `install.sh` and `install.ps1`: one-line install for macOS, Linux and Windows
  PowerShell, with checksum verification and an explicit warning when a checksum
  is not published rather than a silent skip.
- The rationale, the costs, and the rejected alternatives are in
  [ADR 0002](docs/adr/0002-native-binary-and-two-implementations.md).

**`fridge conform`**

- New command. Runs the protocol's conformance vectors against the binary you
  are holding and prints a per-suite table. Exits `0` when conformant and `31`
  (`E_NONCONFORMANT`) when not.
- The Go binary embeds the vectors, so `fridge conform` works offline with no
  checkout. `--vectors <dir>` runs an external suite; `--suite <name>` narrows;
  `--verbose` lists every case rather than only the failures.
- Vector files may now declare the directory tree they need, via a `fixture`
  key, so a conformance run is reproducible on a stranger's machine instead of
  depending on the caller's working directory.

**Verification**

- `npm run parity` replays the command corpus through both implementations in
  two fresh workspaces and diffs exit codes and JSON envelopes, masking only
  genuinely volatile facts. Current result: 71 commands compared, 0 mismatches.
- `test/unit/markdown-is-not-state.test.mjs` enforces the load-bearing
  invariant three ways: no source file in either language reads a `.md` path;
  deleting or garbling every generated Markdown file changes no answer the CLI
  gives; and a hostile `FRIDGE.md` in the repository root cannot assert or block
  a claim.
- CI now builds, vets, `gofmt`-checks and tests the Go implementation on macOS,
  Linux and Windows, runs the parity diff, and verifies all six cross-compile
  targets.

**Packaging**

- `skill/SKILL.md`: the bundled, vendor-neutral, Apache-2.0 Agent Skill, with
  `skill/README.md` explaining when to install it versus running
  `fridge adapters install`.

### Changed

- **Renamed** from the early working name to **Agent Fridge**; the package is
  `agent-fridge`. The binary is still `fridge`, the state directory is still
  `.fridge/`, and the protocol is still `wcp/0.1`, so nothing on disk moves.
  GitHub redirects the old repository URL.
- The conformance vectors moved from `test/vectors/` to **`vectors/`** at the
  repository root, which is now a Go package that embeds them. One canonical
  copy, read by both implementations and embedded in the binary. A project whose
  premise is one writer per record should not keep two copies of its own
  contract.
- `README.md` install section leads with the native binary; the Node package is
  documented as the second implementation and why it exists.
- `README.md` positioning rewritten around **sharded authority, derived
  overview**, with the prior-art section stating plainly that a shared
  coordination board is not a new idea and that no first-of-its-kind claim is
  made. The differentiator is the design shape and the portability, and it is
  stated as a design difference rather than a quality judgement.

### Fixed

- **Two processes could hold the registry mutex at once on Windows.** Caught by
  the Windows CI job, not by a person: `TestOnlyOneHolderAtATime` reported two
  concurrent holders. When the owner file could not be read, both
  implementations inferred the lock's age from `stat`, and answered a failed
  `stat` with "modified at the epoch". Windows fails `stat` on a directory that
  is pending deletion, so a lock somebody was actively holding looked
  infinitely stale, a waiter deleted it, and a third process then acquired it
  cleanly. Three rules now apply in both implementations, and are written into
  the protocol as invariant I7b: an age that cannot be read is not an age, so
  the waiter keeps waiting; breaking is serialised behind a second lock and the
  evidence is re-read under that exclusion, so two waiters cannot break each
  other's freshly taken locks; and both breaking and releasing rename the
  directory aside before removing it, so no waiter ever sees a half dismantled
  lock. A process whose lock is broken while it is still setting up now fails
  with `E_MUTEX_TIMEOUT` instead of running an unprotected critical section.
  Four tests in each implementation pin this, using a seam that reproduces the
  Windows `stat` failure on any platform.
- **The derived door lagged behind five mutating commands.** `join`,
  `heartbeat`, `extend`, a denied `claim`, and `migrate` all wrote state
  without re-rendering the generated view, so `fridge doctor --check` reported
  drift that no human had caused. `fridge init && fridge join` was enough to
  make a pristine workspace fail its own health check. The overview is only
  trustworthy if it is derived eagerly, so every mutating command now renders
  before it returns, and both implementations have a test that walks a
  workspace through the whole command surface asserting `doctor --check` stays
  clean after each step.
- **`simulate --duration 60s` did nothing and reported PASS.** The duration flag
  was parsed with `Number('60s')`, which is `NaN`, so the harness exited
  immediately having performed zero operations and still printed a pass. Now
  parsed with the same duration parser as every other TTL flag. A four-agent,
  six-second run now performs real work: 31 claims, 15 denials, 61 notes.
- `simulate` invariant I2 could report a false positive by counting notes
  written before the run began. Notes are now filtered to the run window.
- **Eight places where `spec/protocol-v0.1.md` had drifted from the tested
  behaviour**, all found by implementing the protocol a second time from the
  prose alone: the claim state name (`held` vs `active`), the registry mutex
  directory name, claim-token length and alphabet, the actor-slug algorithm, the
  three overlap reason codes, the mutex backoff curve, note filename
  zero-padding, and the shape of the `--json` error envelope. In every case the
  code was correct and the prose moved.
- `tools/gen-exit-codes.mjs` documented the wrong JSON error shape; the
  generated `spec/exit-codes.md` is corrected.

### Added, exit codes

- `E_NONCONFORMANT` = **31**. No existing code changed meaning or value, so the
  0.x exit-code contract holds.

---

## [0.1.0] - 2026-02-14

First public release. Protocol `wcp/0.1`.

### Added

**Protocol**

- `spec/protocol-v0.1.md`: the normative specification. On-disk layout, record
  schemas, atomic write primitives, the `mkdir` registry mutex, claim and scope
  semantics, lease and staleness rules, crash recovery, generated views, actor
  resolution, the interop profile, and 12 testable invariants (I1 to I12).
- `spec/exit-codes.md`: 20 exit codes, generated from `src/core/errors.mjs` and
  checked in CI so the document cannot drift from the implementation.
- `vectors/*.json`: language-neutral conformance vectors for path
  normalization, scope overlap, glob matching, and brace expansion, so a second
  implementation can prove conformance without reading any JavaScript.

**CLI** (`fridge`, zero runtime dependencies, Node >= 20.11)

- Workspace: `init`, `join`, `whoami`, `config`, `adapters`, `migrate`, `version`.
- Claims: `claim`, `check`, `guard`, `heartbeat`, `extend`, `release`, `reap`,
  `wait`, `run`.
- Notes: `pin`, `log`, `inbox`.
- Views: `board`, `status`, `render`.
- Handoffs: `handoff`, `accept`, `decline`.
- Operations: `doctor`, `simulate`.
- Aliases: `note`, `tidy`, `sweep`, `pass`, `door`.
- `--json` on every command, with a stable, key-sorted envelope.
- Global flags: `--json`, `--quiet`, `--verbose`, `--no-color`, `--repo`,
  `--agent`, `--help`.

**Concurrency**

- One writer per record. A claim, a note, and a session are each owned by exactly
  one actor, so the multi-writer single-file failure mode is structurally absent.
- Atomic record writes: temp file in the same directory, `fsync`, `rename`.
- Write-once notes with a monotonic `(ts, seq, slug)` filename, so two processes
  pinning at the same millisecond cannot collide.
- A `mkdir`-based registry mutex with a bounded wait, a recorded owner, and stale
  lock recovery. `mkdir` is used because it is atomic on POSIX, on Windows, and on
  NFS, and needs no native module.
- Leases with TTL, heartbeats, a configurable grace window, and `reap` for expiry,
  with `--force` to sweep past grace when the owner process is still alive.
- Three claim modes: `exclusive`, `shared`, `advisory`.
- Deterministic scope-overlap resolution over normalized paths, a glob subset, and
  brace expansion.
- `--queue` and `wait` for orderly handover instead of spinning.
- Crash recovery through `doctor`, with a fixed-point repair loop, quarantine of
  corrupt records, and `--check` for CI.

**Vendor surfaces**

- `fridge adapters install` writes one canonical, marker-delimited, content-hashed
  block into `AGENTS.md`, `CLAUDE.md`, `.github/copilot-instructions.md`, the Codex
  instruction file, and a generic snippet. Idempotent, and your own text above and
  below the block survives.
- `fridge adapters check` exits `30` when a block has drifted.

**Migration**

- `fridge migrate` imports `To-do.done.md` and `shared-development-updates.md` into
  immutable notes, crediting the original author when the name is a known actor or
  is mapped with `--author-map`. `--dry-run` previews, `--freeze` marks the legacy
  files as history.

**Documentation**

- `README.md` with the shared-fridge story, the incident, demo evidence, install,
  a 60-second quick start with real transcripts, and agent and terminal
  compatibility matrices.
- `docs/`: quickstart, concepts, adapters, interop, comparison, migration, and FAQ.
- `docs/adr/0001-distributable-form.md`: why this ships as a protocol plus a CLI
  and explicitly not as an agent.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, `GOVERNANCE.md`.

**Verification**

- Unit, integration, and real multi-process concurrency test suites.
- `npm run demo`: eight processes against one shared Markdown file, then the same
  eight against `fridge pin`. The old way loses notes; Agent Fridge loses
  none. The demo exits non-zero if a single note goes missing.
- `npm run lint`: ASCII-only, parses, SPDX header on every shipped file.
- `npm run gen:check`: exit-code documentation drift check.
- CI on Linux, macOS, and Windows, on Node 20 and 22, plus a PowerShell job.

### Notes

- Agent Fridge is **cooperative and advisory**. It coordinates participants that
  want to cooperate. It is not a security boundary, and `SECURITY.md` says so
  explicitly rather than implying otherwise.
- No network, no telemetry, no daemon, no database, no required MCP server, and no
  mutating Git commands. All of these are verifiable by grep.
- The shared-board idea is old. Blackboard architectures, advisory locking, leases,
  and tuple spaces all predate this by decades, and the README says so. What is on
  offer here is an open, model-neutral, dependency-free, cross-platform
  implementation with a written protocol and proof that it holds under real
  concurrency.

[Unreleased]: https://github.com/RagnarPitla/agent-fridge/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/RagnarPitla/agent-fridge/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/RagnarPitla/agent-fridge/releases/tag/v0.1.0
