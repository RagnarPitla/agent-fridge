# FridgeBoard

**A fridge door for your repo.** Several AI coding agents and humans work in the
same Git checkout without overwriting or interrupting each other.

Local-first. Zero runtime dependencies. Works with any agent that can run a
command. No daemon, no cloud service, no database, no mandatory MCP server.

[![CI](https://github.com/RagnarPitla/fridgeboard/actions/workflows/ci.yml/badge.svg)](https://github.com/RagnarPitla/fridgeboard/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Node](https://img.shields.io/badge/node-%3E%3D20.11-green.svg)](package.json)
[![Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen.svg)](package.json)

---

## The story: a shared fridge door

Think about a kitchen shared by a couple, a family, or four roommates.

The fridge door is where the household coordinates. It works because of a few
unwritten rules that everybody already understands:

- **Everyone pins their own note.** You do not rewrite somebody else's note to
  add yours. You add a note next to it.
- **A chore gets one magnet.** If a magnet on the "groceries" card says
  *Sam, back by 6pm*, nobody else silently starts doing groceries.
- **Magnets fall off.** If Sam's note is three days old and Sam has left for a
  work trip, the chore is up for grabs again. That is normal, not a betrayal.
- **You can hand a chore over.** "I got the first half, can you finish?" is a
  handoff, not an abandonment. The chore always has an owner.
- **The door is readable at a glance.** You walk into the kitchen and know who
  is doing what without asking anybody.

Now replace the roommates with Claude Code in one terminal, GitHub Copilot CLI
in a second, Codex in a third, and you in a fourth. Same kitchen (one Git
checkout), same problem, and none of the unwritten rules are enforced.

FridgeBoard is that fridge door, made explicit and machine-checkable:

| Kitchen | FridgeBoard | The command |
| --- | --- | --- |
| The door | `.fridge/DOOR.md` (generated, human-readable) | `fridge board` |
| Putting your name on the door | a session | `fridge join --agent claude` |
| A chore with a magnet on it | a **claim** over some paths | `fridge claim "src/api/**" --task "refactor"` |
| "Is anybody doing this?" | a scope check | `fridge check src/api/routes.ts` |
| "Still on it" | a lease heartbeat | `fridge heartbeat` |
| The magnet falling off | lease expiry, then a sweep | `fridge reap` |
| Pinning a note nobody can erase | a write-once note file | `fridge pin "deploy is flaky today"` |
| "Can you finish this?" | a handoff | `fridge handoff <card> --to codex` |
| Tidying the door | diagnose and repair | `fridge doctor --fix` |

---

## The actual failure this prevents

This project exists because of a real incident, not a hypothetical one.

Two terminals ran two agents on one repository. They coordinated through two
shared Markdown files: `To-do.done.md` for durable history and
`shared-development-updates.md` for live ownership. Both agents had to read and
rewrite the same files.

One agent read the file, worked for a while, then wrote its version back.
**About 128 lines of the other agent's work disappeared** and had to be
reconstructed by hand.

Nobody did anything wrong. Read-modify-write on one shared file is simply not
safe with concurrent writers, and no amount of "please be careful" in an
instruction file fixes it.

Here is the same failure, reproduced on demand by this repository, and the same
workload run through FridgeBoard:

```
$ npm run demo

A. THE OLD WAY: 8 processes, one shared Markdown file
   wrote:    200 lines
   survived: 15 lines
   LOST:     185 lines  <- this is the bug, reproduced

B. THE FRIDGE WAY: the same 8 processes, same instant
   wrote:    200 notes
   survived: 200 notes
   LOST:     0 notes
```

The exact "survived" number in part A changes on every run, which is precisely
the point: it is a race. Part B is 0 lost, every run, on every platform, and
that is enforced by
[`test/concurrency/nobody-erases-the-door.test.mjs`](test/concurrency/nobody-erases-the-door.test.mjs).

---

## Install

Pick one. Node.js 20.11 or newer is the only requirement.

```bash
# straight from GitHub, no clone, nothing to build
npm install -g github:RagnarPitla/fridgeboard

# or pin the release
npm install -g github:RagnarPitla/fridgeboard#v0.1.0

# or clone and link, if you want to read the source first (it is 3k lines)
git clone https://github.com/RagnarPitla/fridgeboard.git
cd fridgeboard && npm link
```

No npm account, no registry, no build step. Once the package is on npm, the
usual `npx fridgeboard init` and `npm install -g fridgeboard` will work too.

Not a Node person? You do not have to be. Node is a runtime for the CLI, the
same way Git is a runtime for `git`. There is nothing to configure and nothing
is added to your project.

No compiler, no post-install script, no native module, no lockfile churn:
**FridgeBoard has zero runtime dependencies.** `npm ls -g fridgeboard --all`
shows one line, and that line is FridgeBoard.

Verify:

```bash
fridge version
# fridgeboard 0.1.0 (protocol wcp/0.1)
```

If `fridge` is not found, your npm global bin is not on `PATH`. `npm bin -g`
prints the directory to add.

---

## 60-second quick start

```bash
# 1. hang the door in your repo (once per repository, commit the result)
fridge init

# 2. put your name on it (once per agent, per checkout)
fridge join --agent claude --vendor claude-code
export FRIDGE_ACTOR=claude          # PowerShell: $env:FRIDGE_ACTOR = "claude"

# 3. take a chore before you touch files
fridge claim "src/api/**" --task "refactor the router"
```

```
Card clm_01M0D3D2G09E0WZ5QD6085Q9HJ is yours.
  scope    src/api/**
  files    2
  mode     exclusive
  back by  15m from now
```

Meanwhile, in the second terminal, a different agent tries to touch the same
files:

```bash
fridge claim "src/api/routes.ts" --task "fix a typo"
```

```
Somebody already has that chore.

  card    clm_01M0D3D2G09E0WZ5QD6085Q9HJ
  who     claude (other)  pid 54209
  mode    exclusive   doing: refactor the router
  scope   src/api/**
  back by 2026-08-19T13:44:37.043Z (in 14m 59s)
  clash   literal-prefix-nesting: src/api/routes.ts, src/api/**

You can:
  fridge board                          # see the whole door
  fridge claim <narrower-path> ...      # take a different chore
  fridge wait clm_01M0D3D2G09E0WZ5QD6085Q9HJ --timeout 10m
  fridge handoff clm_01M0D3D2G09E0WZ5QD6085Q9HJ --to claude --note "..."
```

Exit code `10`. Not a crash, not a warning, not a suggestion: a refusal a script
can branch on.

```bash
# 4. read the door at any time
fridge board

# 5. hand it over, or put it back
fridge handoff clm_01M0D... --to copilot --note "tests pass, docs left"
fridge release clm_01M0D... --outcome done --note "router split into 3 files"
```

Prefer one command that does the whole cycle? `fridge run` claims, heartbeats
while your command runs, and releases even if the command fails:

```bash
fridge run --claim "src/api/**" --task "run the codemod" -- npm run codemod
```

---

## Where it works

FridgeBoard talks to your agent the way a fridge door talks to a roommate: it
does not. **The agent runs a command and reads the exit code.** That is the
entire integration surface, which is why the compatibility table is boring.

### Agents

| Agent | How it participates | Needs hooks? |
| --- | --- | --- |
| Claude Code | `CLAUDE.md` block + CLI; optional `PreToolUse` hook | No |
| GitHub Copilot CLI | `.github/copilot-instructions.md` block + CLI | No |
| OpenAI Codex / Codex CLI | `AGENTS.md` block + CLI | No |
| Cursor | `.cursor/rules/fridgeboard.mdc` block + CLI | No |
| Windsurf, Cline, Aider, Continue | generic `AGENTS.md` block + CLI | No |
| Any agent with shell access | `fridge claim` / `fridge check` | No |
| A human being | `fridge board`, or just read `.fridge/DOOR.md` | No |

Hooks are an optional upgrade, never a requirement. If your agent can only read
repository instructions and run commands, you have everything you need. See
[docs/adapters.md](docs/adapters.md).

### Terminals and multiplexers

| Environment | Status | Notes |
| --- | --- | --- |
| Plain terminal (bash, zsh, fish) | Supported | Nothing special required |
| tmux, screen, Zellij | Supported | One pane per agent is the classic setup |
| Herdr and similar orchestrators | Supported | Set `FRIDGE_ACTOR` per pane or pass `--agent` |
| VS Code integrated terminal | Supported | |
| Windows PowerShell 5.1 and PowerShell 7 | Supported | ASCII-only output, `$LASTEXITCODE` contract, CI-tested |
| Windows `cmd.exe` | Supported | |
| Git Bash / MSYS2 on Windows | Supported | |
| WSL 1 and WSL 2 | Supported | Keep the checkout on the Linux filesystem |
| SSH / remote / devcontainer | Supported | State is per checkout, so it travels with the repo |
| macOS, Linux, Windows | Supported | CI runs all three, on Node 20 and 22 |
| Two machines sharing one checkout over NFS/SMB | Degraded, and it tells you | Cross-host liveness cannot be verified; `E_FOREIGN_HOST` unless you pass `--allow-multihost` |
| A repo inside Dropbox, OneDrive, or iCloud Drive | Degraded, and it tells you | `fridge doctor` warns; file sync can delay or duplicate writes |

FridgeBoard never emits ANSI colour or non-ASCII characters in v0.1. That is not
an oversight, it is the reason the PowerShell and CI logs stay readable.
`--no-color` is accepted and documented as a no-op.

---

## This is not a new idea

It should not be sold as one.

Shared coordination boards are old and well understood:

- **Blackboard architectures** (HEARSAY-II, 1980) had independent knowledge
  sources posting to a shared structure.
- **Advisory locking** is as old as Unix: `flock`, `lockf`, `O_EXCL` lockfiles,
  `git index.lock`.
- **Leases with expiry** are standard distributed-systems practice (Gray and
  Cheriton, 1989), and every lock service since Chubby has used them.
- **Tuple spaces** (Linda, 1985) solved coordination through a shared space.
- **Chore charts on fridge doors** predate all of it.

FridgeBoard invents none of that. What it claims is narrower and checkable:

> The best-engineered open, model-neutral, dependency-free implementation of
> this pattern for multi-agent coding workspaces, one that works the same on
> Claude Code, Copilot CLI, Codex, tmux, Herdr, a plain terminal, and
> PowerShell.

Concretely, that means:

| Claim | How you can check it |
| --- | --- |
| Zero runtime dependencies | `npm ls --all` |
| Real concurrency, not simulated | `npm run test:concurrency` spawns genuine OS processes that race at an agreed instant |
| The failure mode is actually fixed | `npm run demo` reproduces the data loss, then removes it |
| Exit codes are a stable API | [spec/exit-codes.md](spec/exit-codes.md), generated from the source, verified by `npm run gen:check` |
| The protocol is forkable | [spec/protocol-v0.1.md](spec/protocol-v0.1.md) is a complete specification: you can reimplement it in Go, Rust, or Python without reading our source |
| No vendor lock-in | Nothing in `.fridge/` names a model vendor except a free-text `vendor` label |
| Model-neutral | There is no model, no API key, no network call, anywhere in the codebase |

If you would rather use `flock` and a shell script, that is a legitimate choice
and [docs/comparison.md](docs/comparison.md) says so in more detail, including
where the alternatives are genuinely better.

---

## What ships, and why it is shaped this way

A coordination tool can be delivered as a protocol, a CLI, a skill, an agent, a
harness, or a plugin. We evaluated all of them
([ADR-0001](docs/adr/0001-distributable-form.md)) and ship a **layered stack**,
because each layer fails differently:

| Layer | What it is | Why | Required? |
| --- | --- | --- | --- |
| 0 | **Protocol** (`spec/protocol-v0.1.md`) | On-disk format and semantics. Makes the project forkable and outlives this implementation. | Yes, it is the contract |
| 1 | **CLI** (`fridge`) | The reference implementation and the primary distributable. Exit codes are the API. Anything that can spawn a process can participate. | Yes, this is what you install |
| 2 | **Instruction adapters** (`fridge adapters install`) | Generated blocks in `AGENTS.md`, `CLAUDE.md`, `.github/copilot-instructions.md`, and friends. One canonical text, spliced into vendor files, drift-detected. | Recommended |
| 3 | **Skill / plugin packaging** | The same rules as a Claude skill or a Copilot CLI skill, for people who prefer that distribution channel. | Optional |
| 4 | **Hooks and harness glue** | `PreToolUse` hooks, pre-commit hooks, tmux and Herdr helpers, for enforcement rather than cooperation. | Optional |

Deliberately **not** an agent. An agent that coordinates agents needs a model,
an API key, a network call, and a budget, and it can hallucinate. A fridge door
cannot hallucinate. Layer 1 is a 30-millisecond process that either exits `0` or
does not.

---

## How it works, briefly

The full details are in [spec/protocol-v0.1.md](spec/protocol-v0.1.md). The
short version is four rules:

1. **One writer per record.** Every note, claim, lease, and message is its own
   file, created write-once with `O_EXCL` by exactly one process. No file is
   ever read-modify-written by two parties, so the 128-line incident is
   structurally impossible rather than discouraged.
2. **Human views are generated, never authored.** `.fridge/DOOR.md` is built
   from the record files and carries a `DO NOT EDIT` banner and a state hash.
   Losing it costs you nothing: `fridge render` rebuilds it.
3. **Mutation is serialised by an atomic primitive.** Registry changes happen
   inside a mutex built on `mkdir`, which is atomic on POSIX and on Windows.
   Locks record their owner PID and host, so a lock left by a dead process gets
   broken instead of waited on forever.
4. **Ownership expires.** A claim carries a lease with a TTL. Heartbeats extend
   it, any command from the owner counts as a heartbeat, and an expired claim is
   swept by the next process that looks. A crashed agent cannot block the repo.

```
.fridge/
  VERSION            protocol version, checked on every command
  config.json        tunables (TTLs, timeouts, path rules)
  workspace.json     workspace identity
  DOOR.md            GENERATED human view
  actors/            one file per participant
  sessions/          one file per session
  claims/            one file per chore card    <- the ownership records
  leases/            one file per live lease
  notes/             append-only, one file per note, write-once
  inbox/             one file per handoff message
  queue/             one file per waiter
  locks/             the mutex, mkdir-based
  tmp/               staging for atomic renames
  quarantine/        damaged records, never silently deleted
```

`claims/`, `leases/`, `sessions/`, `locks/`, `tmp/` are machine-local and
git-ignored. `notes/` and `actors/` are shared history and are meant to be
committed. `.gitattributes` marks notes as never auto-merged, so Git cannot
recreate the very problem this tool removes.

---

## Command reference

```
init       Hang the door: create .fridge/ in this repository.
join       Put your name on the door and start a session.
whoami     Who am I, and what am I holding?
claim      Take a chore card over one or more paths.
check      May I write these paths right now?
guard      Assert paths are inside your claims (for hooks and pre-commit).
heartbeat  Shout "still on it" and renew your leases.
extend     Raise the TTL on one claim.
release    Take the card down.
reap       Sweep cards that fell off the door.
wait       Wait for a card to come down.
run        Claim, run a command with automatic check-ins, then release.
pin        Pin a durable note to the door.
log        Read the notes wall.
board      Read the door.
status     Same data as the door, machine first.
render     Regenerate the door and views.
handoff    Offer a chore to another housemate.
accept     Take an offered chore.
decline    Refuse an offered chore.
inbox      Notes addressed to me.
doctor     Tidy the door: diagnose and repair.
simulate   Run a real multi-process household simulation.
adapters   Install or check vendor instruction blocks.
migrate    Import legacy shared Markdown files into the notes wall.
config     Read or write .fridge/config.json.
version    Version and protocol information.
```

Every command accepts `--json` and prints a stable envelope on stdout:

```json
{ "command": "claim", "data": { "claimId": "clm_..." }, "ok": true, "protocol": "wcp/0.1" }
```

Exit codes are the contract. The full table is
[spec/exit-codes.md](spec/exit-codes.md); the ones you will actually branch on:

| Exit | Code | Meaning |
| ---: | --- | --- |
| `0` | `OK` | It happened |
| `2` | `E_USAGE` | Bad arguments |
| `3` | `E_NOT_INITIALIZED` | No `.fridge/` here. Run `fridge init` |
| `7` | `E_NO_SESSION` | Nobody said who you are. Run `fridge join` |
| `10` | `E_CONFLICT` | Somebody else holds those paths |
| `12` | `E_NOT_OWNER` | That card is not yours |
| `13` | `E_LEASE_EXPIRED` | Your card fell off the door |
| `14` | `E_OUT_OF_SCOPE` | You are writing outside your claim |
| `21` | `E_WAIT_TIMEOUT` | You waited long enough |
| `30` | `E_DRIFT` | A `--check` found something out of date |

---

## Wiring it into an agent

```bash
fridge adapters install                # detects and updates every vendor file present
fridge adapters install --vendor claude,copilot,codex
fridge adapters check                  # exits 30 if a block has drifted
```

This splices one canonical, marker-delimited block into each vendor's
instruction file. The block does not duplicate the documentation. It states the
rules, shows the commands, and points at the protocol for the rest:

````markdown
<!-- BEGIN WCP-ADAPTER v0.1 hash:b33f7caedcfc -->
<!-- Generated by `fridge adapters install`. Edit the protocol, not this block. -->
## Shared fridge: how we avoid erasing each other

This repository may have more than one agent or human working in it at the same time.
The fridge door is the shared board. Everybody pins their own note. Nobody edits anybody
else's note. State lives in `.fridge/`; `.fridge/DOOR.md` is a generated view, never edit it.

Before you edit files:

```sh
fridge join --agent "<your-name>" --vendor "<claude|copilot|codex|human|other>"   # once per session
fridge claim "<path-or-glob>" --task "<what you are doing>" --ttl 30m            # take the chore
```

If `fridge claim` exits **10**, somebody else already has that chore. Do not edit those paths.
Read `fridge board`, then either claim different paths, `fridge wait <claim-id>`, or ask via
`fridge handoff <claim-id> --to <them> --note "..."`. Never work around a conflict by editing anyway.

While you work:

```sh
fridge check <path>...            # exit 0 mine, 10 theirs, 14 unclaimed
fridge heartbeat                  # "still on it" - renews your lease
fridge pin "what just happened"   # durable note, write-once, never overwrites anyone
fridge release <claim-id> --outcome done --note "what changed"
```

Rules:

1. Do not hand-edit anything under `.fridge/` and do not edit `.fridge/DOOR.md`.
2. Do not use `--force` to take somebody else's card unless a human told you to.
3. Claim the narrowest paths that cover your work, and release when you stop.
4. Report progress with `fridge pin`, not by editing a shared Markdown file.

Full protocol: `.fridge/` and https://github.com/RagnarPitla/fridgeboard (protocol wcp/0.1).
<!-- END WCP-ADAPTER v0.1 -->
````

The markers carry a content hash, so re-running `install` is idempotent, your own
edits above and below the block survive, and `adapters check` can tell "someone
edited the generated block" apart from "the block is simply older".

---

## Migrating from shared Markdown files

If you already have the `To-do.done.md` and `shared-development-updates.md`
pattern, this imports the history instead of throwing it away:

```bash
fridge migrate --updates shared-development-updates.md --todo-done To-do.done.md
```

Each parsed entry becomes its own immutable note, credited to the actor the entry
names when that name is a joined actor or is supplied via `--author-map "Old Name=slug"`.
The originals are left on disk untouched unless you pass `--freeze`, which prepends a
"this file is history now" header pointing future readers at the door. Add `--dry-run`
to see the parse before anything is written. See [docs/migration.md](docs/migration.md).

---

## Proving it works

```bash
npm test                 # unit + integration
npm run test:concurrency # real processes racing on the real filesystem
npm run test:all         # everything
npm run demo             # the 60-second before/after
npm run lint             # ASCII-only, parses, SPDX headers
fridge simulate --agents 6 --duration 60s # a full simulated household
```

The concurrency suite is the interesting one. It does not mock the filesystem
and it does not fake time. It spawns real child processes, holds them at a
barrier, releases them at one agreed instant, and then asserts invariants:

- eight processes reaching for one file produce exactly one winner and seven
  honest `E_CONFLICT` refusals
- overlapping globs never produce two winners whose scopes intersect
- eight disjoint chores all succeed, because coordination must not become a
  global lock
- when a lease expires, exactly one of six racing processes takes over
- a `SIGKILL`ed agent leaves readable state, and its card expires on schedule
- a lock left by a dead process is broken, not waited on forever
- 200 notes written by 8 processes: 200 survive

---

## Documentation

| Document | What is in it |
| --- | --- |
| [spec/protocol-v0.1.md](spec/protocol-v0.1.md) | The complete protocol: schemas, algorithms, invariants. Enough to reimplement in another language |
| [spec/exit-codes.md](spec/exit-codes.md) | The exit-code contract (generated) |
| [docs/quickstart.md](docs/quickstart.md) | A longer walkthrough with two real terminals |
| [docs/concepts.md](docs/concepts.md) | Actors, claims, leases, notes, handoffs |
| [docs/adapters.md](docs/adapters.md) | Per-vendor wiring, including optional hooks |
| [docs/interop.md](docs/interop.md) | tmux, Herdr, PowerShell, CI, pre-commit, devcontainers |
| [docs/comparison.md](docs/comparison.md) | Honest comparison with `flock`, Git worktrees, branches, and doing nothing |
| [docs/migration.md](docs/migration.md) | Coming from shared Markdown files |
| [docs/faq.md](docs/faq.md) | Including "why not just use branches?" |
| [docs/adr/0001-distributable-form.md](docs/adr/0001-distributable-form.md) | Why this is a protocol plus a CLI, and explicitly not an agent |
| [examples/01-two-terminals/](examples/01-two-terminals/) | Runnable before/after scripts, bash and PowerShell |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to change the protocol without breaking it |
| [GOVERNANCE.md](GOVERNANCE.md) | Who decides what, and how the protocol is versioned |
| [SECURITY.md](SECURITY.md) | The trust boundary, stated plainly, and what counts as a vulnerability |
| [CHANGELOG.md](CHANGELOG.md) | What changed, and which version of what |

---

## Scope of v0.1

**In:** one checkout on one machine, claims with leases, notes, handoffs,
generated views, adapters, migration, doctor, a real simulation.

**Out, on purpose:** networked or multi-machine coordination, a daemon, task
assignment or scheduling, merge-conflict resolution, a web UI, mandatory hooks,
telemetry of any kind, and any dependency on a model provider.

FridgeBoard coordinates who is working where. It does not do the work, and it
does not pretend to be Git.

---

## Contributing

Issues and pull requests are welcome. Start with
[CONTRIBUTING.md](CONTRIBUTING.md); the short version is that new behaviour
needs a test, the exit-code table only ever grows, and everything shipped stays
ASCII and dependency-free.

Security reports: [SECURITY.md](SECURITY.md). FridgeBoard is a cooperative tool
with an explicit trust boundary; read
[the threat model](spec/protocol-v0.1.md#12-security-and-trust-boundaries)
before filing.

## License

[Apache-2.0](LICENSE). See [NOTICE](NOTICE).
