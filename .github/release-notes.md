One shared fridge door for every coding agent in your checkout.

## Install

macOS and Linux:

```sh
curl -fsSL https://github.com/RagnarPitla/agent-fridge/releases/latest/download/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://github.com/RagnarPitla/agent-fridge/releases/latest/download/install.ps1 | iex
```

Or download the binary for your platform below and put it on your `PATH`. Every
asset has a matching `.sha256`, and `checksums.txt` covers the set.

Prefer a package manager?

```sh
go install github.com/RagnarPitla/agent-fridge/cmd/fridge@latest   # Go 1.21+
npm install -g github:RagnarPitla/agent-fridge                     # Node 20.11+
```

## Verify what you downloaded

```sh
fridge version
fridge conform
```

`fridge conform` runs the protocol's conformance vectors against the binary you
are holding, offline, from vectors embedded in the binary itself. If it does not
say CONFORMANT, do not trust the binary.

## What changed

See [CHANGELOG.md](https://github.com/RagnarPitla/agent-fridge/blob/main/CHANGELOG.md).

## Evidence in this release

Nothing ships until it has proved itself. This release was published only after
the `release` workflow ran, in order: lint, generated-doc drift check, `go vet`,
the Go test suite, the Node suite including real multi-process concurrency
tests, `fridge conform` in both implementations, the command-by-command parity
diff between the Go and Node implementations, and the before/after demo that
shows the old shared-Markdown pattern losing notes and this one losing none.

The exact binary published here is the one that was run through `conform` and
had its checksum verified in CI.
