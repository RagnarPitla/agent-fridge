# Concepts

The six things Agent Fridge stores, what each one means, and why the design is shaped the way it is.

Read [./quickstart.md](./quickstart.md) first if you want to see the commands in
use. This page explains what they are. The normative definitions live in
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md); this page does not repeat
them, it explains them.

Each concept below is introduced three times: once with the fridge metaphor,
once precisely, once with the command.

---

## The metaphor, and the rule that keeps it honest

The metaphor is a household fridge door. Housemates pin their own notes. A chore
gets one magnet. Magnets fall off. Chores can be handed over. The door is
readable at a glance.

| Kitchen | Protocol term | On-disk location | The command |
| --- | --- | --- | --- |
| A housemate | actor | `.fridge/actors/<slug>.json` | `fridge join --agent <name>` |
| Being in the kitchen right now | session | `.fridge/sessions/<id>.json` | implicit, created by `join` |
| A chore card with a magnet on it | claim | `.fridge/claims/<id>.json` | `fridge claim "<paths>" --task "..."` |
| How long before the magnet falls off | lease | `.fridge/leases/<claimId>.json` | `fridge heartbeat`, `fridge extend` |
| A note pinned to the door | note | `.fridge/notes/YYYY/MM/DD/<name>.json` | `fridge pin "..."` |
| "Can you finish this?" | message | `.fridge/inbox/<toSlug>/<id>.json` | `fridge handoff` / `accept` / `decline` |
| The door itself | view | `.fridge/DOOR.md`, `.fridge/views/` | `fridge board`, `fridge render` |

### Metaphor discipline

This is a normative rule, not a style preference
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#12-the-metaphor-and-its-limits)):

> Metaphor vocabulary MUST NOT appear in any on-disk field name, schema
> identifier, error code, or JSON key.

"Door", "card", "chore", and "housemate" appear only in generated human text and
in documentation. The wire format says `claim`, `lease`, `note`, `actor`,
`session`, `message`, and the errors say `E_CONFLICT`, `E_LEASE_EXPIRED`,
`E_NOT_OWNER`.

The reason is practical. A second implementation in Go or Rust has to read the
same files. A metaphor is a teaching aid for one audience in one language; a
field name is a contract for everybody. If you fork this protocol and present it
as a Kanban board, a parking lot, or nothing at all, the files still work. If
`claims/` had been called `chores/`, they would not.

---

## Actor

**Fridge:** the person whose name is on the door.

**Precisely:** a named participant in one workspace: an agent instance or a
human. Identity is a name, not a credential. An actor record holds the name, a
slug, a free-text `vendor` label, the host and OS user it was created on, and a
pointer to its current session. Actor files live in `.fridge/actors/` and are
committed to Git, because "who works in this repo" is shared history.

**Command:**

```bash
fridge join --agent claude --vendor claude
fridge whoami
```

`--vendor` is one of `claude`, `copilot`, `codex`, `cursor`, `human`, `other`.
It is a label for the board and nothing else. No behaviour depends on it, and no
model provider is involved anywhere in the codebase.

### Resolution: no guessing, ever

Agent Fridge decides who you are in exactly this order and stops at the first
hit:

1. `--agent <name>` on the command line
2. the `FRIDGE_ACTOR` environment variable
3. the sole actor, if the workspace has exactly one
4. otherwise `E_NO_SESSION`, exit `7`

A conforming implementation must not infer identity from a pid, a tty, a parent
process, or the Git author
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#9-actor-and-session-resolution)).
Guessing is how one agent silently ends up holding another agent's claim, and
that failure is invisible until somebody's work is gone.

```
E_NO_SESSION: More than one housemate is on this door (claude, copilot).
hint: Pass --agent <name>, or export FRIDGE_ACTOR=<name>.
```

That refusal is the design working.

---

## Session

**Fridge:** being in the kitchen at the moment, as opposed to living in the
house.

**Precisely:** one actor's participation period in one workspace. A session is
resumable across processes: `fridge join` with the same name resumes the
existing session rather than starting a new one, so an agent CLI that spawns a
fresh process for every command keeps one identity. Sessions hold the
per-claim ownership tokens that prove you may release a card, so
`.fridge/sessions/` is chmod 0700 with 0600 files and is git-ignored.

**Command:** sessions are implicit. `fridge join` creates or resumes one, and
`fridge whoami` prints it.

```
claude (claude)  session ses_01M0D3YXQ8M7JBM0KEHNJRYDAF  holding 1 card(s)
  clm_01M0D41839D701M6J51EBT7GFZ  src/**  -> api only
```

Ownership is tracked per session, not per actor and not per process. Two shells
that both export `FRIDGE_ACTOR=claude` share one session and therefore one set
of cards, which is usually what you want. Two agents that must not share
ownership must use two different names.

---

## Claim

**Fridge:** the magnet you put on a chore card so nobody else starts it.

**Precisely:** an assertion of intent to modify a set of paths, held by one
session, with a mode and a time-bounded lease. A claim record carries the
actor, the session, the host, the pid, the mode, the task text, labels
(including the current Git branch), the scope, the TTL, and a hash of the
ownership token. Claims live in `.fridge/claims/` and are git-ignored, because
they are live machine-local state: committing a claim would let a merge
resurrect ownership that no live process holds.

**Command:**

```bash
fridge claim "src/api/**" --task "refactor the router"
fridge check src/api/routes.ts
fridge release clm_... --outcome done --note "what changed"
```

### Scope

A scope is a set of include patterns, a set of exclude patterns, and the
concrete file list they expand to. Expansion uses `git ls-files -co
--exclude-standard` when Git is available and a directory walk when it is not.

The supported glob subset is small and deliberate: `*`, `**`, `?`, `[abc]`,
`{a,b}`. Extended globs and leading `!` negation are rejected with
`E_PATH_INVALID` (exit 40); use `--exclude` instead. Paths that traverse out of
the repository, use reserved Windows names, or resolve through a symlink that
escapes the checkout are also rejected. The full list is in
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#62-supported-glob-subset).

Overlap detection is intentionally conservative. It compares literal prefixes,
then materialized file sets, then cross-pattern matches, and falls back to
prefix nesting when a file list was truncated. It may refuse a pair of scopes
that would not actually have collided. It must never allow a pair that would.
That asymmetry is invariant I4 in
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#63-overlap-decision).

The practical consequence: **claim narrowly.** `src/api/**` blocks anybody who
wants `src/api/routes.ts`. `src/**` blocks everybody. If you find yourself
hitting exit `10` constantly, the scopes in your team are too wide.

### Shared vs exclusive

| Mode | Coexists with | Blocks | Use it for |
| --- | --- | --- | --- |
| `exclusive` | nothing overlapping except `advisory` | everything overlapping | Editing files. The default |
| `shared` | other `shared` claims | overlapping `exclusive` claims | Reading, reviewing, running the test suite |
| `advisory` | everything | nothing | Recording intent without reserving anything |

Two agents can hold `--mode shared` on `src/**` at the same time, and both
succeed:

```bash
# terminal A
fridge claim "src/**" --mode shared --task "reading for a review"
# terminal B
fridge claim "src/**" --mode shared --task "running the test suite"
```

```
Card clm_01M0D417GSTEZVV17RKB9PX3Q1 is yours.
  scope    src/**
  files    3
  mode     shared
  back by  15m from now
```

But a `shared` claim still blocks an overlapping `exclusive` one, which is the
point: "I am reading this" is a real statement about safety, not just a comment.
An agent that wants to edit while somebody is mid-review gets exit `10` and has
to negotiate.

`advisory` is the weakest form. It records that you are interested in some paths
and never blocks anyone. Use it for long-running background work where a refusal
would be worse than a collision.

### Why a claim is not a lock

A lock is held until you release it. A claim is held until you release it **or
its lease runs out**, whichever comes first. That difference is the whole
crash-recovery story, and it is the next concept.

---

## Lease

**Fridge:** the magnet is not glued on. If Sam's note is three days old and Sam
left for a work trip, the chore is up for grabs again. That is normal, not a
betrayal.

**Precisely:** the time-bounded validity of a claim, stored as its own record in
`.fridge/leases/<claimId>.json` so that renewing a lease never rewrites the
claim. Default TTL is 15 minutes, capped at 4 hours. A claim is **expired** when
`now > lease.expiresAt`. A claim is **stale**, and may be swept, when it is
expired and either the one-minute grace period has also passed, or the owning
process is provably dead on this same host.

**Command:**

```bash
fridge heartbeat                       # renew everything you hold
fridge heartbeat --claim clm_... --ttl 30m
fridge extend clm_... --ttl 1h         # raise the TTL on one card
fridge reap --dry-run                  # what would be swept
fridge reap                            # sweep it
```

```
still on it: renewed 1 card(s)
  clm_01M0D3Z1WDG81DRPWQDTVCQMTG  until 2026-08-19T13:54:37.890Z
```

### Piggyback renewal

You will rarely type `fridge heartbeat`. Any command from the owning session
renews a lease that is past half its TTL. `fridge check`, `fridge pin`, `fridge
board` from your own session all count as "still here". An agent that is working
normally keeps its cards alive by working normally.

Set `FRIDGE_NO_RENEW=1` to turn that off, which is mostly useful when testing
expiry.

### What "stale" means, and why it is not a pid check

A claim's record stores the pid of the process that created it. It is tempting
to treat "that pid is gone" as "that claim is dead". **That is the wrong
primitive here**, for a specific reason:

> Agent CLIs spawn a fresh process for every command. By the time anybody looks
> at a claim, the pid that created it is normally already dead, even though the
> agent is alive and about to run its next command.

So liveness is driven by lease expiry, not by process liveness. The pid check is
kept, but only as an accelerator: on the same host, a provably dead owner lets
the sweep skip the one-minute grace period. It can only ever make sweeping
faster. It can never keep a claim alive, and it can never be the reason a claim
is considered live. On a different host, liveness cannot be checked at all, and
the lease is the only signal there is. See
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#66-leases-heartbeats-and-staleness).

### Sweeping without a daemon

Nothing runs in the background. Expired claims are swept opportunistically: the
next mutating command from any participant reaps stale claims under the registry
mutex before it does its own work. In practice the agent that needs the paths is
the one that frees them.

```
would sweep clm_01M0D4257XVMWRVWE4C78CJYFC (claude, expired 2026-08-19T13:41:09.982Z)
```

Every sweep writes a `claim.expired` note, so an expiry is never silent. It is
attributable history you can read months later.

A crashed agent therefore cannot block the repository for longer than its TTL
plus a minute, and nobody has to run a cleanup service to make that true.

---

## Note

**Fridge:** you pin your own note next to somebody else's. You never rewrite
theirs to add yours.

**Precisely:** an immutable, attributed, timestamped record on the shared
history. Every note is its own file, created write-once, under
`.fridge/notes/YYYY/MM/DD/`. The filename is
`<compact-ts>--<seq>--<actor-slug>--<id>.json`, which is globally unique and
sorts chronologically. Notes are committed to Git.

**Command:**

```bash
fridge pin "router split into 3 files; tests still red on auth"
fridge pin "auth tests are flaky on CI today" --kind warning
fridge log --limit 20
fridge log --actor copilot --since 1h
fridge log --follow
```

The message is a positional argument. `fridge pin --why "..."` is not a thing;
neither is `fridge claim --why "..."`.

### Why notes are separate write-once files

This is the single most important design decision in the project, and it comes
directly from the incident described in [../README.md](../README.md): two agents
coordinated through two shared Markdown files, one agent's read-modify-write
cycle wrote a stale copy back, and about 128 lines of the other agent's work
disappeared.

Nobody was careless. Read-modify-write on one shared file is simply not safe
with concurrent writers:

```
agent A reads updates.md      (200 lines)
agent B reads updates.md      (200 lines)
agent A appends and writes    (215 lines)
agent B appends and writes    (212 lines)   <- A's 15 lines are gone
```

There is no amount of "please be careful" in an instruction file that fixes
this, because neither agent did anything wrong at any individual step.

The fix is structural, not procedural. Give every note its own file, create it
with `O_EXCL` so the create either succeeds or fails and never silently
clobbers, and give it a name that no other process will generate. Then:

- Two writers never touch the same file, so there is nothing to lose.
- There is no read step, so there is no stale read.
- A partial write is impossible: the file is staged in `.fridge/tmp/` and moved
  into place with an atomic rename.
- Git sees added files, never edited ones, so concurrent branches merge cleanly.
  `.gitattributes` additionally marks notes as never auto-merged.

This is invariant I1, "one writer per record", in
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#21-directory-semantics), and
`test/concurrency/nobody-erases-the-door.test.mjs` asserts it with real OS
processes: 200 notes written by 8 racing processes, 200 survive, every run, on
every platform.

The cost is that reading the history means reading many small files. That is
what `fridge log` is for, and it is a cost worth paying: the alternative loses
data.

Notes are committed history, so `fridge pin` refuses text that looks like a
credential (private keys, GitHub tokens, AWS key ids, OpenAI-style keys, Slack
tokens, `password=` assignments). Pass `--allow-secret-like` for a genuine false
positive.

---

## Message

**Fridge:** "I got the first half, can you finish?"

**Precisely:** a directed record from one actor to another, written into the
recipient's inbox at `.fridge/inbox/<toSlug>/<id>.json`. In v0.1 the only kind
is a handoff offer.

**Command:**

```bash
fridge handoff clm_... --to copilot --note "routes.ts done, server.ts left"
fridge inbox
fridge accept msg_...
fridge decline msg_... --reason "busy with the UI"
```

The important property: **a handoff never leaves work unowned.** Offering a card
sets its state to `handoff-offered`, which still counts as held. Ownership stays
with the offerer until the other side accepts. If the recipient declines, or
never looks, the card returns to the offerer and eventually expires like any
other card. There is no window in which the paths are free but the work is
half-done.

Accepting mints a **new** ownership token under the registry mutex and rewrites
the claim's actor and session. The old token stops working immediately, so a
handoff is a real transfer, not a shared key.

---

## View

**Fridge:** the door itself. You walk into the kitchen and know who is doing
what without asking anybody.

**Precisely:** a generated, disposable, human-readable rendering of the records.
`.fridge/DOOR.md` is the main one; `.fridge/views/status.json` is the machine
one. Both are git-ignored.

**Command:**

```bash
fridge board            # render and print the door
fridge board --write    # also write .fridge/DOOR.md
fridge board --check    # exit 30 if the file on disk is out of date
fridge status           # terser, machine-first layout
fridge status --watch --interval 2000
fridge render           # rewrite every generated view
fridge render --check   # exit 30 if any view is stale
```

Every door starts with the same banner:

```
<!-- GENERATED by agent-fridge 0.1.0 at 2026-08-19T13:39:31.529Z state:7183cc2dcdf4
     Source of truth: .fridge/. DO NOT EDIT. Regenerate with: fridge render -->
```

### Why the door is generated and never authored

A generated view is never an input. Deleting every view must not lose
information. That is invariant I5
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#7-generated-views)), and it
follows straight from the origin incident.

If `DOOR.md` were hand-editable, it would be a shared Markdown file that
everybody reads and rewrites, which is the exact bug this project exists to
remove. Making it generated means:

- **Nobody has to coordinate to write it.** It is derived from records that were
  already written safely.
- **Losing it costs nothing.** `fridge render` rebuilds it from `.fridge/`.
- **Concurrent renders cannot interleave.** A view is rewritten by a whole-file
  atomic replace, so a reader sees one complete version or the previous one,
  never a half-written mixture.
- **It stays out of Git.** Committing a file that every participant regenerates
  would reintroduce exactly the merge churn the protocol removes. If a project
  wants a committed copy, add a path to `door.extraTargets` in
  `.fridge/config.json` and `fridge render` will write there too.

The `state:<hash>` in the banner hashes the *records the view was built from*,
excluding wall-clock text like "in 14m 59s". Drift detection compares that hash,
not the rendered bytes, because comparing bytes would report drift on every
clock tick.

`fridge board --check` and `fridge render --check` exit `30` (`E_DRIFT`) when
the file on disk no longer matches the records. That is useful in a pre-commit
hook and useless as a source of truth, which is the correct balance.

---

## What lives where, and what gets committed

```
.fridge/
  VERSION            protocol version, checked on every command   COMMITTED
  config.json        tunables (TTLs, timeouts, path rules)        COMMITTED
  workspace.json     workspace identity                           COMMITTED
  actors/            one file per participant                     COMMITTED
  notes/             write-once history, one file per note        COMMITTED
  DOOR.md            GENERATED human view                         ignored
  views/             GENERATED machine views                      ignored
  claims/            live ownership records                       ignored
  leases/            live lease records                           ignored
  sessions/          ownership tokens, chmod 0700                 ignored
  inbox/             handoff messages                             ignored
  queue/             waiters                                      ignored
  locks/             the registry mutex, mkdir-based              ignored
  tmp/               staging for atomic renames                   ignored
  quarantine/        damaged records, never silently deleted      ignored
```

The split is not arbitrary. Ask "would a Git merge of this file be meaningful?"

- **Notes and actors:** yes. They are append-only history with unique names.
  Merging two branches produces the union, which is correct.
- **Claims, leases, sessions, locks:** no. They describe what a live process on
  a specific machine is doing right now. Merging a lease would resurrect
  ownership that nobody holds, and that is worse than having no record at all.
- **Views:** no. They are derived. Regenerate, do not merge.

See [../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#22-git-behaviour).

---

## The trust boundary

Agent Fridge is **cooperative and advisory**. It coordinates participants that
want to be coordinated, inside a boundary where everyone already has write
access to the checkout. It is not a security mechanism and does not try to be:
any process that can run `fridge` can also just write the file.

That is not a weakness to apologise for; it is the correct scope. Enforcement
inside a shared checkout would mean OS-level mandatory locks, which do not
compose with Git, editors, or build tools. If you need real isolation, give each
agent its own checkout with a Git worktree, and read
[./comparison.md](./comparison.md), which says so honestly.

What Agent Fridge does guarantee is that **cooperating participants cannot
destroy each other's work by accident**, which is the failure that actually
happens. Optional hooks ([./adapters.md](./adapters.md)) narrow the gap between
advisory and enforced, at the cost of setup. The full threat model is
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#12-security-and-trust-boundaries).

---

## Next

- [./quickstart.md](./quickstart.md) - the commands in sequence.
- [./adapters.md](./adapters.md) - getting an agent to follow the rules.
- [./interop.md](./interop.md) - where it runs.
- [./comparison.md](./comparison.md) - when to use something else.
- [./faq.md](./faq.md) - short answers.
- [../spec/protocol-v0.1.md](../spec/protocol-v0.1.md) - the normative text.
- [../spec/exit-codes.md](../spec/exit-codes.md) - the exit-code contract.
