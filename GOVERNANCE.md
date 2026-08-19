# Governance

Agent Fridge Board is small, young, and opinionated. This document says who decides what,
so that nobody has to guess.

---

## The shape of the project

There are three artefacts and they are governed differently.

| Artefact | What it is | Change bar |
| --- | --- | --- |
| **The protocol** (`spec/protocol-v0.1.md`, `vectors/`) | The normative contract. On-disk layout, record schemas, algorithms, exit codes, invariants | High. Deliberate, versioned, discussed in the open |
| **The primary implementation** (`cmd/`, `internal/`) | The Go binary that most people install. Conforming, not privileged | Normal. Bug fixes and improvements land quickly |
| **The second implementation** (`bin/`, `src/`, `tools/`) | The Node implementation, kept green as conformance evidence | Normal, with the same behaviour bar |

The protocol is the thing worth being careful about. Anyone may write another
implementation in any language, and they should be able to do it from `spec/` and
`vectors/` alone, without reading a line of our source. That is the test of
whether the spec is doing its job. If you had to read the source to implement
something, **that is a spec bug and a legitimate issue**.

We know this test works, because we ran it on ourselves. The Go implementation
was written from the prose, not from the JavaScript, and doing so found eight
places where the spec had drifted from the tested behaviour plus one real bug
that every existing test had missed. See
[ADR 0002](docs/adr/0002-native-binary-and-two-implementations.md).

**Neither implementation is normative.** Where they disagree, the spec decides;
where the spec is silent, that is a spec bug. `npm run parity` diffs them command
by command in CI so that a disagreement is a build failure rather than a
discovery six months later.

---

## Roles

**Users** file issues, ask questions, and report transcripts. The single most
valuable contribution to this project is a reproducible concurrency transcript.

**Contributors** open pull requests. No CLA, no paperwork; contributions are
Apache-2.0 by the act of contributing.

**Maintainers** review and merge, cut releases, and decide scope. Today that is
Ragnar Pitla ([@RagnarPitla](https://github.com/RagnarPitla)), as the BDFL for as
long as the project is this small, which is not a permanent arrangement (see
Succession).

**Implementers** maintain an implementation of the protocol in some language. An
implementer that passes the conformance vectors gets a listing in the README, a
seat in every protocol discussion, and a veto-by-argument on protocol changes that
would be unimplementable in their language. Sensible protocols are shaped by the
languages that have to implement them.

This role is not hypothetical. It already has two occupants inside this
repository, Go and Node, and the friction between them is deliberate: it is what
keeps the specification honest. A third implementation in Rust, Python, or
anything else is welcome and does not need permission. Run `fridge conform`
against your build, open an issue with the output, and we will link you.

---

## How decisions get made

Lazy consensus, with an explicit bar that scales with blast radius.

| Change | Bar |
| --- | --- |
| Typo, docs, test, refactor with no observable change | One maintainer approval |
| Bug fix in the implementation | One maintainer approval, plus a test that fails without the fix |
| New CLI flag or command, backwards compatible | One maintainer approval, plus docs, plus a spec note if another implementation would need it |
| **Protocol change** | An issue labelled `protocol`, open for at least 7 days, with a written rationale, migration story, and updated conformance vectors |
| **New exit code, or a changed exit-code meaning** | Same as a protocol change. Exit codes are the public API |
| Adding a runtime dependency | Effectively never. Requires a written case for why the zero-dependency constraint should break |
| Anything in the v0.1 non-goals list | An issue, a discussion, and a maintainer decision before any code |

When maintainers disagree and discussion has not converged, the BDFL decides and
writes down why. Decisions with lasting consequences become an ADR in
[`docs/adr/`](docs/adr/), which is also where you will find the reasoning behind
the current architecture, starting with
[ADR 0001: the form of the distributable](docs/adr/0001-distributable-form.md).

---

## Protocol versioning

The protocol version (`wcp/0.1`) is independent of the CLI version (`0.2.1`).

- Every record carries a `schema` field naming its protocol version.
- **Additive** changes (a new optional field, a new note type, a new mode) bump the
  minor version. Older implementations must ignore fields they do not know.
- **Breaking** changes (a removed field, a changed meaning, a new required field, a
  changed exit code) bump the major version and need a migration path.
- An implementation that meets a workspace with a protocol version it does not
  understand MUST fail with `E_PROTOCOL_UNSUPPORTED` and say so. It MUST NOT guess,
  and MUST NOT "upgrade" the workspace without being asked. Silent fallback is how
  you lose data.

Unknown fields are preserved on rewrite wherever practical, so that a mixed-version
household degrades instead of corrupting.

---

## Releases

Semantic versioning on the CLI. `0.x` means the CLI surface may still change
between minors; the protocol is what gets stability guarantees first.

A release requires, on all three operating systems:

- `npm run lint` clean,
- `npm run gen:check` clean,
- `npm run test:all` green, including the concurrency suite,
- `npm run demo` proving zero notes lost,
- a `CHANGELOG.md` entry,
- a tag, `v<version>`.

Releases are tagged in Git and published to npm. There is no release cadence:
things ship when they are ready.

---

## Adding and removing maintainers

A contributor who has landed meaningful work, reviewed other people's work well,
and shown they understand the concurrency constraints may be invited to maintain.
The invitation is proposed in a public issue and needs no objection from existing
maintainers within 7 days.

A maintainer who has been unreachable for 6 months moves to emeritus. This is
administrative, not a judgement, and it is reversible by asking.

---

## Succession

If the BDFL is unavailable for 90 days, the remaining maintainers may act by
majority, including appointing a new lead.

If the project is ever abandoned, the licence (Apache-2.0), the spec, and the
conformance vectors are enough for anyone to fork and continue, which is the
point of writing the spec down separately from the code. **Nothing about
Agent Fridge Board requires this repository to keep existing.** Your `.fridge/` directory
is plain files that you can read with `ls` and `cat`, and the protocol that
describes them is a document you already have a copy of.

---

## What this project will not become

Stated up front so that nobody invests effort in a direction that will be
declined:

- It will not grow a daemon, a server, or a database.
- It will not require a network, an account, a key, or a model.
- It will not become an agent framework or an orchestrator.
- It will not add a dependency to save a hundred lines.
- It will not couple itself to one vendor, one editor, or one multiplexer.

Anything that would make Agent Fridge Board stop working on a plane, in an air-gapped
network, or in a bare `sh` on a machine you just SSH'd into is out of scope.
