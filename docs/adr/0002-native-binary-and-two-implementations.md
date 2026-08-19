# ADR 0002: A native Go binary as the primary implementation, with Node kept as a second implementation

- **Status:** Accepted
- **Date:** 2026-02-21
- **Deciders:** Ragnar Pitla
- **Supersedes:** nothing
- **Amends:** [ADR 0001](0001-distributable-form.md), which chose the layered package but left the implementation language open
- **Related:** [spec/protocol-v0.1.md](../../spec/protocol-v0.1.md), [vectors/](../../vectors/)

---

## Context

ADR 0001 settled the *shape* of the distributable: a versioned protocol, a CLI
that is the reference implementation, a bundled Agent Skill, a conformance and
race harness, and optional adapters. It deliberately did not settle the
*language*, because at the time the only evidence available was "JavaScript is
what it was prototyped in".

Since then, three things became clear.

**1. The install step is the whole funnel.** The tool's value shows up in the
first ninety seconds or not at all. `npm install -g` requires Node.js 20.11+,
which is a real ask for the population this is for: people running Claude Code,
Copilot CLI, Codex, Herdr and plain shells on macOS, Linux and Windows. A Rust
shop, a Go shop, a Python shop, and a .NET shop all have terminals and all have
this problem. None of them should have to install a JavaScript runtime to stop
two agents from overwriting each other.

**2. The tool sits in the hot path of other tools.** `fridge check` and
`fridge guard` are meant to be called from pre-commit hooks, from agent loops,
from `run` wrappers, potentially on every file write. Node's process start is
roughly 30-60ms before a single line of our code executes. For a command whose
entire job is to answer "may I write this file", the runtime start dominates the
work. A static binary starts in single-digit milliseconds.

**3. Contributor accessibility is not the same as familiarity.** The instinct is
"JavaScript has the most developers, therefore the most contributors". That is
true for web libraries and false here. This is a filesystem-semantics program:
`renameat`, `mkdir` as a mutex, `O_EXCL`, `fsync` on directories, `flock`,
Windows path rules, symlink resolution. Go's standard library exposes those
primitives directly, its concurrency story matches the problem, and the whole
program compiles to one file with no dependency graph to audit. A reviewer who
has never seen this repository can read `internal/mutex/` and tell whether it is
correct. That is the accessibility that matters for a coordination protocol.

The counter-argument, which is real: rewriting is how projects die, and a second
implementation is a second thing to keep green.

## Decision

**Go is the primary implementation.** `cmd/fridge` plus `internal/` build to a
single static binary for six platform/architecture pairs. The module has no
`require` block: the Go standard library is the entire dependency tree.

**The Node implementation stays, and stays green.** It is not deprecated, not a
legacy shim, and not a thin wrapper. It is a full second implementation of the
same protocol, published as `npm install -g github:RagnarPitla/agent-fridge`.

**Both must pass the same conformance vectors, and both are diffed against each
other, in CI, on every change.**

Concretely:

| Rule | Enforced by |
| --- | --- |
| Both implementations pass every vector in `vectors/*.json` | `fridge conform` in both binaries; `go test ./...`; `npm test` |
| Both produce the same exit code and the same JSON envelope for the same command | `npm run parity`, which replays the command corpus through both binaries in two fresh workspaces and diffs with only genuinely volatile facts masked |
| The exit-code table is identical in both | `internal/errs/errs.go` and `src/core/errors.mjs` are both checked against the generated `spec/exit-codes.md` |
| Neither implementation may grow a behaviour the protocol does not describe | A protocol change is a spec PR first; see [GOVERNANCE.md](../../GOVERNANCE.md) |

## Consequences

### Good

- **Install is a download.** `curl | sh`, `irm | iex`, or a file from the
  releases page. No runtime, no package manager, no post-install script, no
  lockfile churn, nothing added to the user's project.
- **Six platforms from one build command**, including Windows ARM64, which is
  otherwise an awkward target.
- **The vectors ship inside the binary.** `fridge conform` works offline with no
  checkout. A stranger can verify the artifact they downloaded actually
  implements the protocol, which is the difference between a specification and a
  brochure.
- **The second implementation earned its keep immediately.** Writing the Go
  implementation from the spec, rather than from the JavaScript, found:
  - **eight places where the spec prose had drifted from the tested behaviour**
    (claim state named `held` vs `active`; the registry mutex directory name;
    token length and alphabet; the actor-slug algorithm; the three overlap
    reason codes; the mutex backoff curve; note filename zero-padding; the shape
    of the `--json` error envelope), and
  - **one real bug in the Node implementation**: `simulate --duration 60s` did
    `Number('60s')`, produced `NaN`, exited instantly having done nothing, and
    still reported PASS. A race harness that silently tests nothing is worse
    than no race harness.

  None of those were found by the test suite, because the test suite and the
  implementation shared assumptions. The second implementation did not.

### Costs, accepted

- **Two implementations to maintain.** Mitigated by the rule above: the vectors
  and the parity run make drift a CI failure rather than a discovery. If keeping
  both green ever becomes the bottleneck, the honest move is to demote Node to
  "conformance reference, feature-frozen", not to quietly let it rot.
- **Go contributors are a smaller pool than JavaScript contributors** in
  absolute terms. Accepted: the code is stdlib-only, `gofmt`-enforced, and the
  hard parts are filesystem calls rather than framework knowledge.
- **Two known, documented parity gaps**, neither of which affects correctness
  for the protocol as specified:
  - Go has no Unicode NFC normalisation in the standard library, and
    `golang.org/x/text` would break the no-dependencies rule. ASCII inputs,
    which is every input the protocol defines, are unaffected.
  - Node's `localeCompare` and Go's byte-order comparison sort differently in
    general. Note filenames are generated fixed-width ASCII, where the two
    orders are identical.

  Both are recorded in the parity tool rather than hidden.

### Neutral

- `version --json` in the Go implementation reports platform and architecture
  using Node's vocabulary (`win32`, `x64`) so that consumers of the JSON do not
  have to branch on which implementation produced it. The protocol defines the
  field values; neither runtime's native naming is privileged.

## Alternatives considered

**Rust.** Equally good binaries, arguably better correctness guarantees around
the filesystem edge cases. Rejected on compile time, on the size of the
dependency graph needed for anything ergonomic, and because `gofmt` plus a
stdlib-only module makes "read this and tell me if the mutex is correct" a
five-minute job for a reviewer who does not know the project.

**Stay Node-only and ship a bundled runtime** (`pkg`, SEA, Bun compile).
Rejected: 40-90 MB artifacts, per-platform packaging quirks, and the honest
version of that idea is just "ship a binary", which is this ADR.

**Rewrite in Go and delete the Node implementation.** Rejected, and this is the
non-obvious part. Deleting it would have saved maintenance and destroyed the
only mechanism that proves the specification is sufficient. Two independent
implementations passing one vector suite is the evidence; one implementation is
just source code with a README next to it.

**Keep Node primary and add Go later.** Rejected: the install friction is
present from the first user, and the second implementation's value is highest
*before* the protocol ossifies, not after.
