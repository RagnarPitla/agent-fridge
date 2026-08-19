# Security Policy

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Report it privately through GitHub's advisory form:
<https://github.com/RagnarPitla/agent-fridge/security/advisories/new>

If that is not available to you, open a normal issue titled "security contact
request" with no details, and a maintainer will arrange a private channel.

What to include:

- What an attacker can do that they should not be able to do.
- A reproduction: the exact commands, the workspace state, the OS and filesystem.
- The version (`fridge --version`) and protocol version.
- Whether it needs local filesystem access, and whether it needs write access.

You will get an acknowledgement within 7 days. Fixes for accepted reports are
released as soon as they are ready; the advisory is published at the same time,
crediting you unless you ask otherwise.

Supported versions: the latest `0.x` release only, until `1.0`.

---

## The trust boundary, stated plainly

Read this before deciding whether something is a vulnerability. Agent Fridge's
threat model is narrow, and being clear about it saves everyone time.

**Agent Fridge trusts every process that can write to `.fridge/`.**

Anyone who can write to `.fridge/` can:

- take, edit, or delete any claim, including yours,
- write a note with anybody's name on it,
- change the config,
- take the registry lock and hold it.

That is not a bug. It is the same trust boundary as the working tree itself. If a
hostile process can write to your repository, it can also write to your source
code, your `.git/hooks/`, and your `package.json` install scripts. Agent Fridge is
not, and cannot be, a defence against that. It is a **coordination** tool for
participants who want to cooperate, in the way a lock on a bathroom door
coordinates a household without being a security control.

Enforcement, if you need it, comes from outside: separate OS users with separate
filesystem permissions, separate containers, separate checkouts, or a pre-commit
hook that a privileged process controls.

### So what *is* a vulnerability here?

These are in scope, and we want to hear about them:

| In scope | Why |
| --- | --- |
| **Path traversal** - making `fridge claim` or any command touch a file outside the workspace root | The root is a hard boundary; `..` and absolute escapes must be rejected with exit 40 |
| **Symlink escape** - a symlink under `.fridge/` or in a claimed path causing a write outside the root | Records are resolved and validated; a link that defeats that is a bug |
| **Command injection** - any input that reaches a shell | `fridge run` uses `spawn` without a shell; anything that reintroduces one is a bug |
| **Lock defeat** - two processes both being granted overlapping exclusive claims | This is the core promise. A reproducible instance of this is the most serious bug the project can have |
| **Silent data loss** - any input that causes a note or claim to be overwritten, truncated, or lost rather than added | Write-once is the core promise on the notes side |
| **Denial of service via crafted records** - a record that makes the CLI hang, spin, or exhaust memory rather than fail with an exit code | Corrupt input must be quarantined by `doctor`, never crash the tool |
| **Deadlock** - a lock that is never released and that `doctor` cannot recover | Crash recovery must always converge |
| **Untrusted-input mishandling in `migrate`** - a hostile legacy Markdown file causing anything worse than a bad note | `migrate` reads files you did not write |

These are **out of scope**, and will be closed as "working as designed" with a
pointer back to this file:

- A local process that can write to `.fridge/` tampering with records.
- An agent choosing to ignore a claim and editing anyway. Agent Fridge is advisory
  by design. See the FAQ.
- `--force` doing what `--force` says it does.
- A user hand-editing `.fridge/` and getting a corrupt workspace. `doctor` will
  quarantine it; that is the designed response.
- Anything requiring physical access, root, or an already-compromised account.
- Anything about a *network* attack. There is no network. Agent Fridge opens no
  sockets, makes no requests, and has no telemetry.

---

## Design properties you can verify

These are testable claims, not marketing. If any of them is false, that is a bug
report.

1. **Zero runtime dependencies.** `dependencies` in `package.json` is empty.
   Nothing is fetched at runtime. The entire supply chain is Node itself.
2. **No network.** No `http`, `https`, `net`, `dns`, or `fetch` in `src/`. You can
   grep for it. Agent Fridge works fully offline and on an air-gapped machine.
3. **No telemetry.** Nothing is collected, counted, or sent. Ever.
4. **No shell.** `fridge run` spawns your command directly with `spawn(cmd, args)`
   and `shell: false`. Your arguments are never concatenated into a shell string.
5. **No mutating Git commands.** Agent Fridge may *read* Git state. It never runs
   `git add`, `git commit`, `git checkout`, `git stash`, or anything else that
   changes your repository or your index.
6. **Writes stay inside the workspace root**, with two documented exceptions that
   only happen when you ask for them: the adapter files (`AGENTS.md`, `CLAUDE.md`,
   `.github/copilot-instructions.md`, and friends) and `render --output <path>`.
   Both are still inside the repository.
7. **Every path is normalized and bounded.** Absolute paths outside the root, `..`
   escapes, and symlinks that resolve outside the root are rejected with
   `E_PATH_INVALID` (exit 40), not silently clamped.
8. **Records are written atomically.** Write to a temp file in the same directory,
   `fsync`, then `rename`. A crash mid-write leaves either the old record or the
   new one, never a half-written one.

## Hardening notes

- `.fridge/` inherits your repository's permissions. If your checkout is
  world-writable, so is the board. Do not put a shared checkout in a
  world-writable directory and then rely on Agent Fridge for isolation.
- Notes are **not encrypted and not private**. Treat `fridge pin` like a comment in
  the code: it will be read, and if `.fridge/` is committed, it will be pushed.
  Never pin a credential, a token, or a customer name.
- `migrate` reads legacy Markdown that may have come from anywhere. It writes the
  parsed content into notes; it does not execute anything. Review the result with
  `--dry-run` first if the source is not yours.
- If you commit `.fridge/`, remember that notes are permanent history in Git as
  well as on disk.
