# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Two version numbers matter and they move independently:

- the **CLI version** (`0.2.2`), which is what the tags and the release binaries track, and
- the **protocol version** (`wcp/0.1`), which is what `.fridge/` records declare.

A CLI release that does not change the protocol leaves `wcp/0.1` alone.

---

## [Unreleased]

## [0.2.2] - 2026-08-19

### Fixed

Hardening pass from two independent reviews, tracked in issue #1. Every item
below is a way exclusive ownership could be lost, and every one now has a
regression test in both implementations.

- **Overlapping globs could both be granted.** Overlap was decided by
  intersecting *materialised* file sets, so two patterns that share no file
  today but could both match a file created tomorrow were judged disjoint.
  `*.md` and `CHANGELOG.md` were both grantable, as were `a*/x.ts` and
  `*b/x.ts`. Overlap is now decided on the patterns themselves: the supported
  glob subset is regular, so "can these both match some string" is decided
  rather than approximated. Brace expansion is bounded at 256 alternatives and
  exceeding it is an explicit error, never a silent truncation or an allocated
  expansion bomb. Inclusive and negated character ranges now participate in the
  same exact intersection decision, so `[a-z].md` conflicts with `b.md`.
  Case-insensitive ranges and Unicode simple-fold equivalents now agree across
  Node and Go, including Kelvin/Angstrom symbols and the two lowercase sigma
  forms.
- **Identity could be inherited.** A mutating command with no `FRIDGE_ACTOR`
  and exactly one actor in the workspace silently ran *as* that actor, so a
  second terminal shared the first one's claims. Reads may still guess the
  sole actor; writes now refuse with `E_NO_SESSION` and name the candidate. A
  stale `FRIDGE_ACTOR` no longer blocks identity-free administration such as
  `doctor`, `reap --dry-run`, or an unattributed `wait`; an explicit misspelled
  `--agent` still fails.
- **A broken lock holder could delete its replacement.** Release removed the
  lock directory without proving it still owned it, so a holder that had been
  judged stale and broken would, on exit, delete the *new* holder's lock. Each
  acquisition now writes a fencing nonce into `owner.json`, and release only
  removes a lock that still carries its own nonce, checked under the break
  lock so the answer cannot go stale between reading and acting. Bounded batch
  operations can also refresh a generation-fenced `heartbeatAt`; migration
  uses it between note writes so a long live import is never broken merely for
  exceeding `staleMs`.
- **Corrupt records failed open.** An unparseable claim was skipped, which made
  a damaged file look like free space. Reads that feed a mutation or an overlap
  decision now fail with `E_STATE_CORRUPT`; generated views still render, but
  name every unreadable record in a warning banner rather than dropping it.
- **A superseded handoff offer could still be redeemed.** Offering the same card
  to a second agent left the first offer live in the first agent's inbox, and
  accepting it stole the card from whoever had legitimately taken it. Offering
  now withdraws the prior offer, and `accept` validates the envelope against the
  live claim before moving anything.
- **Notes were visible at their final path before they were written.** Creation
  staged into the workspace tmp directory, fsynced, then linked into place, so a
  reader can never see a zero-length or half-written note.
- **Path escapes.** `render --output`, `door.extraTargets` and
  `simulate --report` resolved their targets without checking containment, and
  pattern normalisation only followed symlinks on the literal prefix. All of
  them now resolve through a single containment check that judges a path by
  where it really lands.
- **Secret scanning missed most durable text.** Only `fridge pin`'s body was
  scanned. `--task`, `--note`, `--reason` and `--label` land in the same
  write-once notes and are now scanned too, with `--allow-secret-like` as the
  documented escape hatch.
- **Lost updates.** `session` was read before the mutex and written back inside
  it, and `fridge config` did an unlocked read-modify-write of the whole file.
  Both now read and write fresh state inside one critical section. Real
  multi-process races cover concurrent config keys, same-session claim tokens,
  and heartbeat renewal counters.
- **Lease renewal did not match its own documentation.**
  `lease.renewOnAnyCommand` was hand-wired into four commands. Renewal now
  happens once, centrally, only for explicit `--agent` or `FRIDGE_ACTOR`
  identity, under the registry mutex. Sole-actor read inference never extends
  ownership.
- **`notes.commit: false` did not keep notes out of Git.** The `.gitignore` was
  a constant that always un-ignored `/notes/`. It is generated from the setting,
  and `fridge doctor` reports and repairs drift between the two.
- **The door body and its state stamp could disagree.** Rendering took two
  snapshots, so a claim arriving between them produced a stamp certifying a body
  that was never rendered. One snapshot now feeds the body, the stamp and
  `status.json`.
- **`fridge run` could not find `npm` on Windows.** Executables are resolved
  through `PATHEXT` before spawning. `.cmd` and `.bat` targets run through
  `cmd.exe`; native targets do not. A missing command has an explicit diagnostic
  and exit 127.
- **`fridge run` could recreate a lease after release.** Shutdown now stops new
  heartbeats and waits for any heartbeat already in flight before taking down
  the card.
- **Configured view paths were trusted too late.** `door.path` now drives the
  real automatic-render target, every configured target is confined by its
  symlink-resolved destination, and malformed path or target-array shapes are
  rejected before a view is written.
- **Migration could partially publish unsafe input.** Non-dry migration requires
  explicit identity, source paths stay inside the workspace, all source text is
  secret-scanned before any write, and note creation plus optional freezing are
  serialised under the registry mutex. Sources are capped at 10 MiB and re-read
  after preflight and immediately before freezing; a concurrent edit now causes
  `E_CONFLICT` instead of being overwritten.
- **Automatic rendering could claim success without converging.** The body and
  state stamp come from one snapshot, rendering retries when state changes
  during publication, and an exhausted retry budget reports failure rather than
  certifying a stale view.
- **Breaking a lock, and holding one too long, were silent.** Section 5.2 and
  5.3 of the protocol require a `lock.broken` and a `lock.slow` note. Both were
  optional callbacks no caller passed; both are now unconditional.
- **`revoked` was specified but never written.** Force-releasing somebody else's
  card archives it as `revoked`; releasing your own still archives as
  `released`. Superseded offers archive as `withdrawn`.

### Changed

- README now leads with the engineering problem, not the metaphor. Order is
  hero (`Stop AI coding agents from overwriting each other's work`), the
  problem, the solution, then the fridge-door story. The repository slug
  `agent-fridge`, the `fridge` binary, the `.fridge/` state directory and the
  `wcp/0.1` protocol identifier are unchanged.

- `spec/protocol-v0.1.md` Section 6.3 rewritten to describe the decision
  procedure the implementations actually run, including the directory-intent
  expansion and its deliberate over-reporting, the bounded brace expansion, and
  the narrow subsumption rule for excludes. New Invariant I4b: reads that feed a
  decision must fail closed on a corrupt record.
- `vectors/scope-overlap.json` grew from 17 to 51 cases. One existing case
  changed: `src/api/*.ts` versus `src/api/*.js` was recorded as overlapping,
  which was an over-approximation the old implementation could not avoid. These
  two patterns cannot match the same path, and both implementations now agree.

### Documentation

- The README leads with the problem and the solution rather than the metaphor:
  what breaks when two agents share a checkout, and the five primitives that
  replace the shared Markdown file. New "What it solves, and what it does not"
  section states the limits plainly, including that this is not a security
  boundary, not a merge tool and not distributed.
- Category line added: **the shared whiteboard for AI coding agents**, with the
  fridge door kept as the explanatory metaphor rather than the headline.

### Fixed (earlier in this cycle)

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
