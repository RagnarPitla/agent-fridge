# FAQ

Direct answers to the questions people actually ask, including the sceptical ones.

---

### Why not just use branches or worktrees?

Often you should, and [./comparison.md](./comparison.md) says so at length.

**Git worktrees give real isolation.** Two agents in two worktrees physically
cannot overwrite each other, because they are not editing the same files. That
is strictly stronger than anything an advisory protocol can offer. If your work
decomposes onto branches and your build is cheap enough to instantiate N times,
use worktrees and skip this project.

Agent Fridge is for the case where that does not fit:

- A 4GB `node_modules` and a 20-minute cold build, six times over.
- Agent B needs agent A's *uncommitted* refactor right now.
- A human is in the checkout too, with an editor open and uncommitted changes.
- Work that does not decompose: a codemod, a rename, a dependency upgrade.
- Tooling that assumes one working directory: Docker bind mounts, dev servers on
  fixed ports, `.env` files, database fixtures.

Branches solve a different problem again: parallel *lines of development*, not
parallel *editing of one tree*. They are better than Agent Fridge at review,
revert, and bisect, and they compose with it. The branch name is recorded as a
label on every claim.

---

### What if an agent ignores the rules?

Then it ignores them, and you can lose work. Agent Fridge is **advisory and
cooperative, not enforcement**. Say that out loud before adopting it.

The trust boundary is explicit
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#12-security-and-trust-boundaries)):
every participant already has write access to the checkout. Any process that can
run `fridge` can also just write the file. There is no privileged component, no
credential, and nothing that could stop a determined or badly-behaved process.

What the design actually buys you:

1. **Accidents become visible.** The common failure is not malice, it is two
   well-behaved agents that did not know about each other. Exit `10` fixes that
   case completely.
2. **Structural safety for the shared record.** Even an agent that ignores every
   claim cannot destroy the notes wall, because notes are separate write-once
   files. The 128-line incident is impossible regardless of behaviour.
3. **Attribution.** If an agent does edit outside its claims, `fridge log` shows
   who held what and when, so you can diagnose it instead of guessing.

If you want a harder stop, [./adapters.md](./adapters.md) has an optional Claude
Code `PreToolUse` hook and a Git pre-commit hook, both calling `fridge guard`.
They narrow the gap. They do not close it: a hook is defence in depth, not a
security boundary. If you need genuine isolation, give each agent its own
checkout.

---

### What happens if an agent crashes?

Its claims expire and get swept. No daemon is involved and no cleanup command
has to be scheduled.

Concretely:

- Every claim carries a lease. Default TTL is 15 minutes, capped at 4 hours.
- A crashed agent stops renewing. When `now > expiresAt`, the claim is
  **expired**.
- It becomes **stale** and sweepable once the one-minute grace period passes, or
  immediately if the owning process is provably dead on this same host.
- The next mutating command from **any** participant reaps stale claims under
  the registry mutex before doing its own work. In practice the agent that wants
  the paths is the one that frees them.

```
would sweep clm_01M0D4257XVMWRVWE4C78CJYFC (claude, expired 2026-08-19T13:41:09.982Z)
```

Every sweep writes a `claim.expired` note, so an expiry is never silent.

The other crash cases are covered too. A process killed between staging a write
and renaming it leaves a file in `.fridge/tmp/` and never a half-written record.
A process killed while holding the registry mutex has its lock broken by the
next acquirer, which checks for a dead pid on the same host and for a stale age.
A corrupt record is quarantined by `fridge doctor --fix`, never deleted. See
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#68-crash-recovery).

---

### Do I need hooks?

No. Hooks are an optional upgrade and nothing is gated behind them.

"Cooperation MUST be possible without hooks" is a design constraint of the
protocol, not a nice-to-have
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#13-design-constraints)).
An agent that can only read repository instructions and run a shell command can
take part fully.

Add hooks if your team wants a hard stop before a write or before a commit. The
Git pre-commit hook is the better value of the two: it is one file, it works for
every agent and every human at once, and it catches the case that actually
matters. Both are in [./adapters.md](./adapters.md).

---

### Does it call an API or send telemetry?

No. There is no network code in the codebase at all.

Not "telemetry is off by default", not "telemetry is anonymised": there is no
HTTP client, no socket, no analytics, no model, no API key, and no provider.
"Local-first and offline. No network I/O of any kind is permitted in a
conforming implementation's core operations" is constraint 1 of the protocol.

You can check this yourself rather than trusting the claim:

```bash
grep -rn "http\|fetch(\|net\.\|https\|socket" src/
npm ls --all
```

The only child processes it ever spawns are `git` (read-only: `ls-files`,
`rev-parse`, `diff --cached`) and whatever you explicitly pass to `fridge run`.
`npm ls --all` shows one line, because there are zero runtime dependencies.

---

### Can two people on two machines share one checkout?

Over NFS or SMB, it works in a degraded way, and the degradation is documented
rather than hidden.

What still works: leases. Lease expiry is wall-clock arithmetic on a timestamp
in a file, and that survives a network filesystem. This is one reason liveness
is lease-driven rather than pid-driven.

What does not work:

- **Cross-host liveness.** `processAlive(pid)` is meaningless for a pid on
  another machine, so the grace period can never be skipped for a dead owner.
- **Mutex strength.** The registry mutex relies on atomic `mkdir`. Not every NFS
  or SMB mount delivers that.
- **Timestamp resolution.** Coarser than the protocol assumes.

`fridge doctor` reports every foreign-host claim:

```
INFO   Card clm_01M0D3Z1WDG81DRPWQDTVCQMTG was taken on another machine; liveness cannot be checked here.
```

The protocol reserves exit `41` (`E_FOREIGN_HOST`) and an `--allow-multihost`
override for this
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#125-shared-and-networked-filesystems)).
As of 0.1.0 that refusal is specified but not enforced by the CLI, so do not
build a workflow that depends on exit `41` stopping you.

**The recommendation: do not do this.** Multi-machine coordination is an
explicit non-goal for v0.1. Give each machine its own clone and coordinate
through Git, or run all the agents on the machine that owns the filesystem, over
SSH. More detail in [./interop.md](./interop.md).

---

### Why is there no colour?

Because colour makes the output worse in the places it is most often read.

Agent Fridge emits pure ASCII with no ANSI escape sequences, ever. That is a
deliberate v0.1 rule, not an unfinished feature. The beneficiaries:

- **Windows PowerShell 5.1**, where ANSI handling is inconsistent and a
  transcript full of escape sequences is unreadable.
- **CI logs**, where escape codes end up as literal `ESC[31m` noise in a web UI.
- **Screen readers and terminal recorders**, which handle plain text correctly
  and escape sequences badly.
- **`grep`, `awk`, and diffs**, which do not want invisible bytes in the middle
  of a field.

`--no-color` is accepted and documented as a no-op. There was never any colour
to turn off; the flag exists so that a script written for a colourful tool does
not fail with `E_USAGE` here.

The same rule applies to the source tree: `node tools/lint.mjs` fails the build
on any non-ASCII character in a shipped `.md`, `.mjs`, `.json`, `.yml`, `.sh`,
or `.ps1` file. No smart quotes, no em dashes, no box drawing.

If you want colour, pipe `--json` through your own formatter. The data is
structured precisely so the presentation can be somebody else's problem.

---

### Why is DOOR.md git-ignored?

Because it is generated, and committing a generated file that every participant
regenerates would recreate the exact merge churn this project removes.

Think it through. If `DOOR.md` were committed, then every claim, release, and
note would dirty it. Every agent would regenerate it. Every branch would have a
different version. Every merge would conflict on it. You would end up
hand-resolving a file that is, by construction, derived from data that already
merged cleanly. That is a shared Markdown file with extra steps, which is the
origin bug.

So `DOOR.md` is a **view**, and views obey invariant I5: a generated view is
never an input, and deleting every view must not lose information
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#7-generated-views)). Delete
it whenever you like; `fridge render` rebuilds it from `.fridge/`.

If your project genuinely wants a committed board, add a path to
`door.extraTargets` in `.fridge/config.json` and `fridge render` will write
there too. Then own the merge conflicts knowingly.

---

### Should I commit .fridge/notes/?

**Yes.** Notes and actors are shared history and are meant to be committed.
`fridge init` writes `.fridge/.gitignore` as an allowlist that does exactly
that:

```
/*
!/.gitignore
!/VERSION
!/config.json
!/workspace.json
!/notes/
!/actors/
```

Everything else is machine-local live state and must not be committed.
Committing a lease would let a merge resurrect ownership that no live process
holds, which is worse than having no record at all.

Notes merge cleanly by construction: each is a write-once file with a globally
unique name, so two branches produce added files, never edited ones.
`fridge init` also adds a `.gitattributes` rule so Git never tries to auto-merge
note content.

If you have a policy reason not to commit history, set `notes.commit` to `false`
in `.fridge/config.json`. You lose the durable record, which is most of the
point.

---

### How is this different from flock?

`flock` is a good tool and Agent Fridge uses the same idea internally: its
registry mutex is a `mkdir`-based lock held for milliseconds. Claims are the
layer above that. The differences that matter:

| | `flock` | Agent Fridge claim |
| --- | --- | --- |
| Granularity | one file | a path set with globs and excludes, with conservative overlap |
| Expiry | on process exit (or never, for a lockfile) | a lease with a TTL, renewed by any command from the owner |
| Who holds it | not recorded | actor, session, host, pid, task text, branch |
| Why they hold it | not recorded | `--task`, shown on the board and in the conflict report |
| Handoff | not possible | `handoff` / `accept` / `decline`, with a new token minted on accept |
| History | none | a permanent note for every acquire, deny, release, and expiry |
| Portability | not on macOS by default, not native on Windows | `mkdir` is atomic everywhere |
| Machine output | exit code only | `--json` envelope on every command |

If you have one critical section and everybody is on one POSIX machine, `flock`
and a shell script are a legitimate choice and you should take it.
[./comparison.md](./comparison.md) covers this in full.

---

### Is this a new idea?

No, and that is fine.

Shared coordination boards are old and well understood: blackboard architectures
(HEARSAY-II, 1980), advisory locking as old as Unix, leases with expiry (Gray
and Cheriton, 1989), tuple spaces (Linda, 1985), and chore charts on fridge
doors, which predate all of it. Agent Fridge invents none of that and does not
claim to.

Novelty is a poor thing to optimise for in a coordination primitive. You want
the boring, well-understood pattern, implemented carefully, with the edges
tested. The claims this project does make are narrower and checkable:

| Claim | How to check it |
| --- | --- |
| Zero runtime dependencies | `npm ls --all` |
| Real concurrency, not simulated | `npm run test:concurrency` spawns real OS processes that race at one agreed instant |
| The failure mode is actually fixed | `npm run demo` reproduces the data loss, then removes it |
| Exit codes are a stable API | [../spec/exit-codes.md](../spec/exit-codes.md), generated from source |
| The protocol is forkable | [../spec/protocol-v0.1.md](../spec/protocol-v0.1.md) is complete enough to reimplement in another language |
| Model-neutral | no model, no API key, no network call anywhere |

Execution quality and neutrality, not novelty.

---

### What is the performance cost?

One short-lived Node process per command, on the order of 30 milliseconds on a
normal machine, and no background cost at all because there is no daemon.

Where the time goes on a `claim`: process start, reading `.fridge/config.json`
and `VERSION`, acquiring the `mkdir` mutex, listing the claims directory,
materializing the scope with `git ls-files`, writing two small JSON files, and
rendering the door. The scope materialization is the only part that scales with
repository size, and it is capped by `paths.materializeLimit` (default 5000
files) after which overlap falls back to a conservative prefix comparison.

Read-only commands (`check`, `board`, `status`, `log`, `whoami`) do not take the
mutex at all.

In practice the cost is not the milliseconds, it is the one extra command per
task. `fridge run` collapses claim, heartbeat, and release into a single call
when that suits you:

```bash
fridge run --claim "src/api/**" --task "tests" -- npm test
```

If 30ms per call matters for your loop, batch: claim a scope once at the start
of a work unit rather than per file.

---

### Can I use it without any AI agent?

Yes. Humans work fine, and it is a reasonable tool for a small team sharing one
machine, one server checkout, or one pairing box.

```bash
fridge join --agent ragnar --vendor human
export FRIDGE_ACTOR=ragnar
fridge claim "src/billing/**" --task "invoice rounding bug" --ttl 2h
fridge pin "found it: the tax line rounds before summing"
fridge release clm_... --outcome done --note "fixed in invoice.ts"
```

Nothing about the design assumes a model. `--vendor human` is a first-class
value, `.fridge/DOOR.md` is a Markdown file you can just open, and the whole
thing works over SSH on a shared box.

It is also useful in a mixed team, which is the common case: one human in an
editor and two agents in terminals. The human gets a live answer to "what are
they touching right now" without interrupting anybody.

---

### What happens on Windows?

It works, and it is tested there in CI on Node 20 and 22.

Three things were designed for it specifically:

- **The mutex uses `mkdir`**, which is atomic on Windows as well as POSIX.
- **Output is pure ASCII with no ANSI colour**, so PowerShell 5.1 transcripts
  and CI logs stay readable.
- **Path handling normalises backslashes** to forward slashes, rejects reserved
  device names (`con`, `nul`, `com1`, ...), rejects segments containing `:` or
  ending in a dot or space, and rejects UNC paths.

The one thing to remember is that PowerShell does not put a numeric exit code in
`$?`. Branch on `$LASTEXITCODE`, and capture it immediately, because any command
in between overwrites it:

```powershell
$env:FRIDGE_ACTOR = "claude"
fridge claim "src/api/**" --task "refactor the router"
switch ($LASTEXITCODE) {
  0  { "the chore is yours" }
  10 { "somebody else has it" }
  default { throw "fridge failed with $LASTEXITCODE" }
}
```

`cmd.exe` and Git Bash are supported too. On WSL, keep the checkout on the Linux
filesystem rather than under `/mnt/c`. Full details in
[./interop.md](./interop.md).

---

### How do I uninstall it?

Delete `.fridge/` and the adapter blocks. Nothing else is touched.

```bash
# 1. remove the state
rm -rf .fridge

# 2. remove the generated blocks from the vendor instruction files.
#    Delete everything between and including these markers:
#      <!-- BEGIN WCP-ADAPTER v0.1 hash:... -->
#      <!-- END WCP-ADAPTER v0.1 -->
#    in whichever of these exist:
#      AGENTS.md
#      CLAUDE.md
#      .github/copilot-instructions.md
#      .codex/instructions.md
#      .cursor/rules/agent-fridge.mdc
#      docs/AGENT-COORDINATION.md

# 3. remove the three .gitattributes lines under the "# Agent Fridge" comment

# 4. remove the package, if you installed it
npm uninstall agent-fridge          # or: npm uninstall -g agent-fridge
```

If the vendor file contained *only* the block, delete the file. If it also
contains your own prose, keep the file and delete the block: your text above and
below the markers was never modified.

Two things to know before you do it:

- **Your notes go too.** `.fridge/notes/` is committed history. If you want to
  keep it, export first: `fridge log --limit 100000 --json > coordination-history.json`.
- **Nothing else in the repository was ever changed.** Agent Fridge never runs a
  mutating Git command, never writes outside the workspace, and never touches
  your source files. `git status` after uninstalling shows exactly the four
  categories above and nothing else.

---

### Why does `fridge claim` reject `--why`?

Because the flag is `--task`. Every command has an explicit allowlist of flags
and anything else is refused with exit `2` rather than silently ignored:

```
E_USAGE: Unknown flag --why for 'claim'.
hint: fridge claim --help
```

Silently ignoring an unknown flag is how an agent ends up believing it recorded
a reason that nobody can read. `fridge <command> --help` prints the exact
allowlist and the exit codes that command can produce.

---

### Why do I get exit 7 when I have already joined?

Because identity is resolved per command, and with more than one actor on the
door there is nothing to resolve to:

```
E_NO_SESSION: More than one housemate is on this door (claude, copilot).
hint: Pass --agent <name>, or export FRIDGE_ACTOR=<name>.
```

The resolution order is `--agent` first, then `FRIDGE_ACTOR`, then the sole
actor if there is exactly one, then exit `7`. Agent Fridge will not guess from a
pid, a tty, a parent process, or the Git author, because a wrong guess means one
agent silently holding another agent's claim
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#9-actor-and-session-resolution)).

Fix it by exporting the variable once per terminal:

```bash
export FRIDGE_ACTOR=claude          # bash, zsh
```

```powershell
$env:FRIDGE_ACTOR = "claude"        # PowerShell
```

If you set `FRIDGE_AGENT` by mistake, you get a warning on stderr rather than
silence:

```
warning: unknown environment variable FRIDGE_AGENT (typo?)
```

---

### Why is my claim blocking somebody when the files do not actually overlap?

Because overlap detection is deliberately conservative. It may refuse a pair of
scopes that would not actually have collided; it must never allow a pair that
would. That asymmetry is invariant I4
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#63-overlap-decision)).

The most common cause is a claim wider than the work. `src/**` blocks everybody.
`src/api/**` blocks anybody who wants `src/api/routes.ts`, which is correct, and
the conflict report tells you why:

```
  clash   literal-prefix-nesting: src/api/routes.ts, src/api/**
```

The fix is almost always to claim narrower paths. If your team hits exit `10`
all day, the scopes are too wide, not the tool. For genuinely read-only work,
`--mode shared` coexists with other `shared` claims.

---

### Can I run several agents under one name?

You can, and sometimes you should not.

Ownership is tracked per **session**, and two shells that both export
`FRIDGE_ACTOR=claude` share one session and therefore one set of claims. Either
of them can release the other's cards, and neither will ever block the other.

That is right for a single agent that spawns a fresh process per command, which
is exactly how agent CLIs behave. It is wrong for two independent agents. Give
them distinct names:

```bash
fridge join --agent claude-a --vendor claude
fridge join --agent claude-b --vendor claude
```

---

### What does `fridge doctor` actually check?

It scans for the states that a filesystem-backed protocol can get into, and
`--fix` repairs the fixable ones to a fixed point (repairing one thing can
uncover the next).

It looks for: a missing `VERSION` or `.fridge/.gitignore`; unparseable JSON
records; stale claims; claims with no lease file; leases with no claim;
hour-old junk in `tmp/` from interrupted writes; a registry lock held by a dead
process or held too long; a stale `DOOR.md`; drifted adapter blocks; claims
taken on another host; a checkout inside Dropbox, OneDrive, Google Drive,
iCloud Drive, or Creative Cloud Files; and a symlinked `.fridge`.

```bash
fridge doctor            # report
fridge doctor --fix      # repair
fridge doctor --check    # exit 30 if anything needs attention (good in CI)
```

Corrupt records are moved to `.fridge/quarantine/`, never deleted.

---

### Where do I report a bug or a security issue?

Issues and pull requests on the repository. New behaviour needs a test, the
exit-code table only ever grows (numbers are never reused or renumbered), and
everything shipped stays ASCII and dependency-free.

For security reports, read
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#12-security-and-trust-boundaries)
first. Agent Fridge is a cooperative tool with an explicit trust boundary: "an
agent could ignore a claim" and "a process with write access can write files"
are not vulnerabilities, they are the documented model. Path traversal, symlink
escape, a record write that lands outside the workspace, or a way to make one
session release another session's claim without `--force` all are.

---

## Next

- [./quickstart.md](./quickstart.md) - the two-terminal walkthrough.
- [./concepts.md](./concepts.md) - actors, claims, leases, notes, views.
- [./adapters.md](./adapters.md) - instruction blocks and optional hooks.
- [./interop.md](./interop.md) - tmux, PowerShell, WSL, CI, degraded cases.
- [./comparison.md](./comparison.md) - the honest comparison.
- [./migration.md](./migration.md) - coming from shared Markdown files.
- [../README.md](../README.md) - the incident this all comes from.
- [../spec/protocol-v0.1.md](../spec/protocol-v0.1.md) - the normative protocol.
- [../spec/exit-codes.md](../spec/exit-codes.md) - the exit-code contract.
