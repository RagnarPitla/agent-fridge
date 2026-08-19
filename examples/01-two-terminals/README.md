# Example 01: two terminals, one checkout

The smallest complete demonstration of what Agent Fridge is for. Two scripts, same
machine, same load, opposite outcomes.

Nothing here is simulated. Both scripts spawn real background processes that write
to a real filesystem.

---

## Run it

```bash
cd examples/01-two-terminals
./without-fridge.sh
./with-fridge.sh
```

On Windows, in PowerShell:

```powershell
cd examples\01-two-terminals
.\without-fridge.ps1
.\with-fridge.ps1
```

Both scripts are self-cleaning on the next run, and they tell you what to delete
when you are done. Nothing is written outside this directory.

---

## What `without-fridge.sh` does

Six processes, twelve notes each, coordinating through one shared Markdown file
called `shared-development-updates.md`. Each process does exactly what an agent
does when you tell it to "update the shared file":

1. read the whole file,
2. add its line,
3. write the whole file back.

That is a read-modify-write with no lock. Between step 1 and step 3, somebody else
finished their whole cycle, and your step 3 wrote a copy of the file that never
contained their line.

A real run on a laptop:

```text
Two-plus agents, one shared Markdown file, read-modify-write.
6 processes x 12 notes each = 72 notes expected.

expected notes : 72
notes on disk  : 32
LOST           : 40
elapsed        : 1s

Work was destroyed. Nobody was told. No error was raised anywhere.
Every one of those writes exited 0.
```

**40 of 72 notes gone in one second.** Read that last line again: every single one
of those writes succeeded. There is no error to catch, no exit code to branch on,
and no way for the losing process to find out. This is how a real incident
destroyed about 128 lines of an agent's work that then had to be rebuilt by hand.

The number changes on every run, and that is the worst part. On an idle machine you
might lose nothing and conclude the pattern is fine. It fails hardest when the
machine is busy, which is exactly when several agents are working.

---

## What `with-fridge.sh` does

The same six processes, the same twelve notes each, the same sleeps. The only
change is `fridge pin "update N"` instead of rewriting a shared file:

```text
Same 6 processes, same 12 notes each, via 'fridge pin'.

expected notes : 72
notes on disk  : 72
LOST           : 0
elapsed        : 3s
```

**Zero lost.** Not because of a smarter merge, but because there is nothing to
merge. Each note is its own write-once file named with a timestamp, a sequence
number, and the author's slug. No process ever opens a file another process is
writing. The multi-writer problem is not solved, it is structurally absent.

It costs two seconds across 72 writes. That is the whole price.

Then the script does the thing the shared file could never do at all:

```text
--- now the part a shared file could never do: two agents want the same paths
agent-1 claim exit: 0

E_CONFLICT: 1 card(s) already cover those paths.
hint: fridge board  |  fridge wait clm_01M0D5WF...  |  fridge handoff ...
agent-2 claim exit: 10  (10 = E_CONFLICT, and it is told exactly who has it)
```

`agent-1` holds `src/api/**`. `agent-2` asks for `src/api/routes.ts`, which is
inside it, and is refused with **exit code 10** before it opens a single file. It
is told who has the card, what they are doing, and the three legitimate ways
forward: claim something else, `wait`, or ask for a `handoff`.

The shared-Markdown version had no concept of "these paths are taken". It could not
have refused, because it never knew.

And the door shows the state to a human with no tooling at all:

```text
# The door

Fridge `wsp_01M0D5WCS0...` | 1 chore(s) claimed | 0 waiting

## Claimed right now

| Card | Who | Mode | Scope | Doing | Back by |
|---|---|---|---|---|---|
| `clm_01M0D5WF64...` | agent-1 (other) | exclusive | `src/api/**` | refactor the client | in 29m 59s |

## Last 5 notes

- 2026-08-19T14:12:58.400Z agent-2 `note.note` update 12
- 2026-08-19T14:12:58.726Z agent-1 `claim.acquired` agent-1 took src/api/** for "refactor the client"
- 2026-08-19T14:12:58.817Z agent-2 `claim.denied` agent-2 was blocked on src/api/routes.ts
```

Note that the refusal is itself a note. Being blocked is history, not a silent
event, so tomorrow you can see who was waiting on whom.

---

## Try it by hand, in two real terminals

The scripts prove it under load. This shows it at human speed. Open two terminals
in the same throwaway repository.

**Terminal A**

```bash
mkdir -p ~/fridge-demo && cd ~/fridge-demo && git init -q .
fridge init
fridge join --agent alice --vendor human
fridge claim "src/**" --task "big refactor" --ttl 30m
echo $?     # 0
```

**Terminal B**

```bash
cd <same directory>
fridge join --agent bob --vendor human
fridge check src/api/routes.ts ; echo $?    # 10, alice has it
fridge claim "docs/**" --task "update docs" ; echo $?   # 0, no overlap
fridge pin "starting on docs, alice has src"
```

**Terminal A**, hand it over:

```bash
fridge handoff <claim-id> --to bob --note "api client is done, tests are not"
```

**Terminal B**, take it:

```bash
fridge inbox
fridge accept <claim-id>
fridge board
```

Neither terminal ever edited a file the other was editing, and neither ever opened
a file the other had open. Everything either of them did is still on the door.

---

## The one-liner version

If you only want to see the mechanism, this is it:

```bash
fridge run --claim "src/api/**" --task "codemod" -- npm run codemod
```

Claim, heartbeat while the command runs, release when it exits, even if it fails.
If somebody else holds those paths, the command never starts, and you get exit 10.

---

## Where to go next

- [../../README.md](../../README.md) - the 60-second quick start
- [../../docs/quickstart.md](../../docs/quickstart.md) - the guided version
- [../../docs/concepts.md](../../docs/concepts.md) - claims, leases, modes, overlap
- [../../spec/protocol-v0.1.md](../../spec/protocol-v0.1.md) - what an implementation must do
- `npm run demo` from the repository root - the same comparison, instrumented, and
  it exits non-zero if a single note goes missing
