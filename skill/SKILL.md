---
name: agent-fridge
version: 0.2.0
protocol: wcp/0.1
license: Apache-2.0
homepage: https://github.com/RagnarPitla/agent-fridge
description: >-
  Coordinate with other AI agents and humans working in the same Git checkout.
  Claim the files you are about to edit, pin durable notes, hand work over, and
  read a shared board, without ever overwriting anybody else's state. Use this
  whenever more than one agent or person may be active in the same repository.
keywords:
  - multi-agent
  - coordination
  - file-locking
  - concurrency
  - git
  - workspace
---

# Agent Fridge

One shared fridge door for every coding agent in your checkout.

This skill is vendor-neutral and open. It is bundled with the `fridge` CLI and
carries no vendor's proprietary format. Any agent runtime that can read a
Markdown skill file and run a shell command can use it as-is.

---

## When to use this

Use it whenever the repository contains a `.fridge/` directory, or whenever you
have any reason to believe another agent or a human may be working in the same
checkout at the same time.

If `.fridge/` does not exist and you are the only one here, you do not need this.

## The one rule

**Never edit a file another session has claimed, and never edit anybody else's
state.** Everything below is a way of finding out which files those are.

You pin your own notes. You do not rewrite somebody else's. The generated board
is read-only.

---

## The loop

### 1. Join once per session

```sh
fridge join --agent "<your-name>" --vendor "<claude|copilot|codex|human|other>"
```

Idempotent. Re-running resumes your existing session rather than creating a
second one. Do this before anything else.

### 2. Claim before you edit

```sh
fridge claim "src/api/**" --task "what you are about to do" --ttl 30m
```

Claim the **narrowest** set of paths that covers your work. Claiming `**` blocks
everybody and will be refused unless a human explicitly allows it.

Read the exit code. It is the whole interface:

| Exit | Meaning | What you must do |
| ---: | --- | --- |
| `0` | Granted, the paths are yours | Proceed |
| `10` | Somebody else holds overlapping paths | **Stop.** Do not edit those files |
| `14` | You are outside your claim | Claim the paths first |
| `2` | Bad arguments | Fix the command |
| `40` | Path is invalid or escapes the repository | Use a path inside the repo |

### 3. When you get exit 10

Do exactly one of these. Never work around a conflict by editing anyway.

```sh
fridge board                                  # see who has what, and why
fridge claim "<different-paths>" --task "..." # work somewhere else instead
fridge wait <claim-id> --timeout 10m          # block until they are done
fridge handoff <claim-id> --to <them> --note "..."   # ask them to pass it over
```

Reporting the block is better than guessing:

```sh
fridge pin "blocked on src/api/** which alice holds, starting on docs instead"
```

### 4. While you work

```sh
fridge check <path>...           # 0 mine, 10 theirs, 14 unclaimed
fridge heartbeat                 # "still on it", renews your lease
fridge pin "what just happened"  # durable note, write-once, overwrites nothing
```

Heartbeat if a task runs long. A lease that expires makes your claim sweepable
by somebody else, which is the correct behaviour when an agent dies, and the
wrong outcome when you are simply slow.

### 5. When you stop

```sh
fridge release <claim-id> --outcome done --note "what actually changed"
```

Outcomes are `done`, `partial`, or `abandoned`. Be honest: `partial` with a note
saying what is left is far more useful to the next agent than `done`.

### One-shot alternative

If you are running a single command, this claims, heartbeats while it runs, and
releases afterwards even if the command fails:

```sh
fridge run --claim "src/api/**" --task "run the codemod" -- npm run codemod
```

---

## Hard prohibitions

1. **Do not hand-edit anything under `.fridge/`.** It is the state. Use commands.
2. **Do not edit `.fridge/DOOR.md`.** It is generated from the state and will be
   overwritten. To say something, use `fridge pin`.
3. **Do not create or update a shared Markdown status file** such as
   `FRIDGE.md`, `shared-development-updates.md`, or a running `To-do.done.md`.
   A Markdown file that several agents both read and write is exactly the
   failure this tool exists to remove: a read-modify-write race that silently
   destroys whoever wrote last-but-one. Use `fridge pin` instead. It is
   write-once and cannot lose anybody's note.
4. **Do not use `--force`** to take another session's claim unless a human has
   explicitly told you to in this conversation.
5. **Do not treat exit 10 as advisory.** It means somebody is editing those
   files right now.

---

## Reading the board

```sh
fridge board          # human view: who holds what, recent notes
fridge status --json  # same data, machine first
fridge log --limit 20 # the notes wall, newest last
fridge inbox          # handoffs and messages addressed to you
fridge whoami         # who am I and what am I holding
```

Every command accepts `--json` and prints a key-sorted envelope, so you can
parse instead of scraping. On failure the envelope carries `.exitCode` and
`.error.code`.

---

## Why it is safe to rely on

Agent Fridge uses **sharded authority with a derived overview**. Every
authoritative write goes to a record exactly one session owns, or is contested
at exactly one lock directory. There is no shared file that two agents append to
or rewrite, so concurrent writes cannot clobber each other. The board you read is
generated from those records and is never a source of truth.

It is **cooperative**, not enforced. Nothing physically stops a process from
editing a claimed file. The protocol works because participants follow it, which
is why this skill exists and why the rules above are absolute rather than
suggestions.

---

## If something looks broken

```sh
fridge doctor          # diagnose and repair: stale leases, orphans, corrupt records
fridge doctor --check  # report only, non-zero if something needs fixing
fridge reap            # sweep claims whose leases have expired
```

`doctor` quarantines corrupt records rather than deleting them. If you cannot
make sense of the state, run `fridge doctor` and read `fridge board` again
before doing anything else. Never repair `.fridge/` by hand.

---

## Full reference

- `fridge --help`, and `fridge help <command>` for per-command flags and exit codes
- Protocol: <https://github.com/RagnarPitla/agent-fridge/blob/main/spec/protocol-v0.1.md>
- Exit codes: <https://github.com/RagnarPitla/agent-fridge/blob/main/spec/exit-codes.md>
