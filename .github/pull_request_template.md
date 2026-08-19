# What and why

<!-- What breaks without this? A sentence is fine if the change is small. -->

Fixes #

## Layer touched

<!-- Tick every one that applies. See docs/adr/0001-distributable-form.md. -->

- [ ] Protocol (`spec/`, `vectors/`) - **this is a contract change**
- [ ] Reference implementation (`src/`, `bin/`)
- [ ] Adapters (vendor instruction blocks)
- [ ] Tests
- [ ] Docs
- [ ] Tooling / CI

## If this changes behaviour another implementation could observe

<!-- Delete this whole section if it does not. Otherwise all three must be ticked:
     spec, source, and tests move together. See CONTRIBUTING.md. -->

- [ ] `spec/protocol-v0.1.md` updated
- [ ] `vectors/*.json` updated, or not applicable
- [ ] A test that fails without this change and passes with it
- [ ] Exit codes unchanged, or `npm run gen` re-run and `spec/exit-codes.md` committed

## Proof

<!-- Paste real output. "Tests pass" without the output is not proof. -->

```text
$ npm run lint

$ npm run gen:check

$ npm test

$ npm run test:concurrency

```

## Checklist

- [ ] `npm run lint` clean (ASCII only, parses, SPDX headers)
- [ ] `npm run gen:check` clean
- [ ] `npm run test:all` green
- [ ] No new runtime dependency (`dependencies` in `package.json` is still empty)
- [ ] Works in PowerShell as well as a POSIX shell, or is platform-independent
- [ ] No `/tmp`-style absolute temp paths, no shell invocation, no network call
- [ ] Docs updated if user-visible
- [ ] `CHANGELOG.md` entry under `[Unreleased]` if user-visible

## Risk

<!-- What could this break? Concurrency changes: say what you raced and how. -->
