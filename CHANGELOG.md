# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Two version numbers matter and they move independently:

- the **CLI version** (`0.1.0`), which is what npm and the tags track, and
- the **protocol version** (`wcp/0.1`), which is what `.fridge/` records declare.

A CLI release that does not change the protocol leaves `wcp/0.1` alone.

---

## [Unreleased]

Nothing yet.

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
- `test/vectors/*.json`: language-neutral conformance vectors for path
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
  eight against `fridge pin`. The old way loses notes; FridgeBoard loses none. The
  demo exits non-zero if a single note goes missing.
- `npm run lint`: ASCII-only, parses, SPDX header on every shipped file.
- `npm run gen:check`: exit-code documentation drift check.
- CI on Linux, macOS, and Windows, on Node 20 and 22, plus a PowerShell job.

### Notes

- FridgeBoard is **cooperative and advisory**. It coordinates participants that
  want to cooperate. It is not a security boundary, and `SECURITY.md` says so
  explicitly rather than implying otherwise.
- No network, no telemetry, no daemon, no database, no required MCP server, and no
  mutating Git commands. All of these are verifiable by grep.
- The shared-board idea is old. Blackboard architectures, advisory locking, leases,
  and tuple spaces all predate this by decades, and the README says so. What is on
  offer here is an open, model-neutral, dependency-free, cross-platform
  implementation with a written protocol and proof that it holds under real
  concurrency.

[Unreleased]: https://github.com/RagnarPitla/fridgeboard/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/RagnarPitla/fridgeboard/releases/tag/v0.1.0
