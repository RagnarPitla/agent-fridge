# Contributing to Agent Fridge

Thanks for helping. This project has an unusual shape: it is a **protocol** with a
reference implementation, not an application. That changes what a good contribution
looks like, so please read the first section before you open a pull request.

---

## The one rule that matters

**The protocol is the product. The CLI is evidence that the protocol works.**

If you change behaviour that another implementation could observe, you are changing
the protocol, and the change has to land in three places at once:

1. `spec/protocol-v0.1.md` - the normative text.
2. `src/` - the reference implementation.
3. `test/` - a test that fails without the change and passes with it.

A pull request that changes `src/` but not `spec/` will be asked "is this a bug fix or
a protocol change?" A pull request that changes `spec/` but not `test/` will be asked
"how would a second implementation know it got this right?"

Things that are *not* protocol changes and need none of this ceremony: wording,
docs, error message text, performance, refactors that keep every observable byte the
same, and new tests for existing behaviour.

---

## Getting set up

There are two implementations of the same protocol and you can contribute to
either. Neither has dependencies to install.

### Go, the primary implementation

You need Go 1.21 or newer. That is the whole list: the module has no `require`
block, so there is nothing to download.

```bash
git clone https://github.com/RagnarPitla/agent-fridge.git
cd agent-fridge
go run ./cmd/fridge --help     # it already works
go test ./...                  # 109 tests, seconds
go build -o fridge ./cmd/fridge
```

`internal/` is laid out to match the protocol document: `mutex/` is the one
contested resource, `store/` is the record layer, `paths/` is normalisation and
overlap, `render/` builds the derived views, `commands/` is the CLI surface.

### Node, the second implementation

You need Node 20.11 or newer. No dependencies, no build step, no code generation
you have to run by hand.

```bash
node bin/fridge.mjs --help     # it already works
npm run test:all               # takes well under a minute
```

To use your working copy as the real `fridge` command:

```bash
npm link                       # `fridge` on your PATH is now this checkout
npm unlink -g agent-fridge     # undo
```

### The rule that governs both

**A behaviour change must land in both implementations, or in neither.**

This is not busywork. Two independent implementations passing one vector suite
is the only real evidence that `spec/protocol-v0.1.md` is sufficient rather than
decorative. Writing the second implementation from the prose alone is what found
eight places where the spec had drifted and one bug that every existing test had
missed. If you change behaviour in one language only, `npm run parity` fails and
you have created exactly the drift this project exists to prevent.

If you genuinely cannot do both, say so in the pull request. A maintainer will
either pair with you or hold the change. What we will not do is merge a
divergence quietly.

---

## The checks

One command runs what CI runs, in the order CI runs it:

```bash
npm run check          # everything, a few minutes
npm run check:fast     # unit tests only, for a tight edit loop
```

Use it. Every check below has failed CI at least once after a change its author
was sure was safe, usually because the local loop was a remembered subset.

| Command | What it enforces |
| --- | --- |
| `npm run lint` | Every shipped file, in both languages, is ASCII-only, parses, and carries an SPDX header |
| `npm run gen:check` | `spec/exit-codes.md` still matches the exit-code table |
| `npm run test:all` | Node unit, integration, and real multi-process concurrency tests |
| `go test ./...` | The Go equivalents, including the same conformance vectors |
| `gofmt -l internal cmd vectors` | Must print nothing |
| `go vet ./...` | Must be silent |
| `npm run conform` | This build agrees with `vectors/*.json` |
| `npm run parity` | Go and Node give the same answer to the same command |
| `npm run demo` | The before/after demo still proves zero notes lost |

Shortcuts: `npm run go build`, `npm run go test`, `npm run go fmt`,
`npm run go vet`, `npm run go dist` (cross-compiles all six targets).

If `gen:check` fails, you edited the exit codes. Run `npm run gen` and commit the
regenerated file; do not hand-edit `spec/exit-codes.md`. Remember that the table
exists twice, in `src/core/errors.mjs` and `internal/errs/errs.go`, and both must
agree.

If `parity` fails, read the diff it prints before assuming the tool is wrong. It
masks timestamps, ids, pids, hostnames, absolute paths, and remaining lease time.
Anything else that differs is a real divergence.

If `lint` fails on ASCII, you typed a smart quote, an em dash, an arrow, or an
ellipsis. Use `'`, `-`, `->`, and `...`. This is not a style preference: non-ASCII
characters render as mojibake in some terminals and in some agents' instruction
parsers, and this project's whole job is to be readable in a plain terminal.

---

## Writing tests

Tests live in three tiers and the tier decides how you write it.

**`test/unit/`** - pure functions, no filesystem where avoidable, fast. Path
normalization, glob matching, scope overlap, duration parsing, exit-code mapping.
If you can express your case as data, add it to `vectors/*.json` instead of
writing a bespoke test: those vectors are the language-neutral conformance suite
that a Rust or Go implementation can run without reading any JavaScript.

**`test/integration/`** - one process, real `.fridge/` directory, real CLI invoked
through `bin/fridge.mjs`. Assert on exit codes and on `--json` output, never on
human-readable text. Human output is allowed to change; the exit code is a contract.

**`test/concurrency/`** - real child processes, real filesystem, no mocked clock and
no mocked `fs`. These are the tests that justify the project's existence, and they
have rules:

- Spawn real processes with `child_process`. Do not simulate concurrency in one process.
- Use a **barrier**: every child waits for a shared start timestamp, then goes. Racing
  without a barrier tests process startup time, not your lock.
- Assert **invariants**, not counts. "No two granted claims overlap" is an invariant.
  "Exactly 1 winner" is only true when the target sets are genuinely nested; three
  agents claiming three disjoint globs *should* all win. A wrong count assertion here
  already cost this project an afternoon.
- Never assert on wall-clock durations. CI machines are slow and shared.

Concurrency tests run with `--test-concurrency=1` because they measure contention on
the filesystem and will fight each other for cores otherwise. `tools/run-tests.mjs`
handles this automatically.

---

## Style

There is no formatter and no linter config, on purpose: the code has zero
dependencies and adding a toolchain to enforce indentation would be the largest
dependency in the repo. Match the surrounding code.

- Two-space indent, semicolons, single quotes, `const` by default.
- ESM only (`.mjs`, `import`), Node built-ins only. **A pull request that adds a
  runtime dependency will be declined** unless it removes more than it adds. This is
  a hard constraint, not a preference: the CLI must be installable and auditable in
  an air-gapped environment.
- Comment only what needs clarifying. Prefer a clear name to a comment.
- Every source file starts with `// SPDX-License-Identifier: Apache-2.0`.
- Errors are thrown as `WcpError` with a code from `src/core/errors.mjs`. Never
  `process.exit()` outside `bin/fridge.mjs`. Never `console.log` outside the output
  layer.
- **Explicit errors, never silent fallback.** If something is ambiguous, fail with a
  code and a message that names the fix. Guessing is how you lose 128 lines of work.

---

## Cross-platform rules

This project claims to work in PowerShell, so it has to actually work in PowerShell.

- Never build a path with string concatenation and `/`. Use `path.join`.
- Never assume a glob is expanded by the shell. `cmd.exe` does not expand globs, which
  is why `tools/run-tests.mjs` discovers test files itself instead of passing
  `test/**/*.test.mjs` to `node --test`.
- Never assume `fs.rename` semantics beyond POSIX-atomic-replace, and remember that
  on Windows a rename over an open file can fail with `EPERM`. The retry helper in
  `src/core/atomic.mjs` exists for exactly this.
- Do not use `flock`, `fcntl`, symlink tricks, or anything that needs a native module.
  Mutual exclusion is `fs.mkdirSync` on a lock directory, because `mkdir` is atomic
  everywhere including on NFS and on Windows.
- Quote your examples so they work in `bash`, `zsh`, `fish`, `cmd.exe`, and
  PowerShell. When they cannot be identical, show both.

---

## Commits and pull requests

Conventional-ish commit subjects, lower case, imperative, under 72 characters:

```
fix: reap --force no longer skips claims inside their grace window
spec: document advisory mode compatibility in section 6.4
test: add a nested-glob race proving exactly one winner
```

In the pull request body, include:

- **What breaks without this.** A failing test, a transcript, a reproduction.
- **Proof.** Paste the relevant test output. If you touched concurrency, paste the
  concurrency run.
- **Protocol impact.** "None", or a pointer to the spec section you changed.

Keep GitHub text ASCII-clean, same rule as the code.

---

## Reporting bugs

The best bug report for this project is a **transcript**. Two terminals, the commands
you ran in each, and the output. Include `fridge doctor --json` and your OS, shell,
Node version, and filesystem (especially if it is a network mount, a container bind
mount, or a case-insensitive volume, all of which have bitten locking schemes before).

If the bug is "an agent ignored the claim and edited anyway", that is a real and
interesting issue, but it is an *adapter* or *instruction* issue, not a locking bug.
Label it as such. Agent Fridge is cooperative by design; see
[docs/faq.md](docs/faq.md) and Section 12 of the spec.

Security issues: do not open an issue. See [SECURITY.md](SECURITY.md).

---

## Adding a vendor adapter

Adapters are the most-wanted contribution. To add one:

1. Add an entry to the vendor table in `src/commands/workspace.mjs` with the vendor's
   id, its instruction file path, and its comment syntax if it is not Markdown.
2. Do not write a new instruction text. There is exactly one canonical block and every
   vendor gets the same words; that is the entire point of the design. If a vendor
   genuinely cannot use the canonical text, say why in the pull request.
3. Add an integration test that installs into a temp workspace and asserts the markers,
   the hash, and idempotency (installing twice changes nothing).
4. Add a row to the compatibility matrix in `README.md` and a section in
   [docs/adapters.md](docs/adapters.md).

---

## What is out of scope for v0.1

Please read the scope section of the README before proposing these. All of them are
deliberate omissions, not oversights: a daemon, a server, a database, a required MCP
server, cloud sync, cross-machine coordination, enforcement via filesystem
permissions, a TUI, editor plugins, and Git-history rewriting.

Proposals to move any of them from "no" to "yes" are welcome as issues with the
`protocol` label. Pull requests implementing them without that discussion will not be
merged, however good the code is.

---

## Licence

By contributing you agree that your contributions are licensed under the Apache
License 2.0, the same licence as the project. There is no CLA.
