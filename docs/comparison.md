# Comparison

An honest look at the alternatives, including the several cases where one of them is the better answer.

Agent Fridge solves one narrow problem: **several agents and humans editing the
same Git checkout at the same time, without overwriting each other.** Narrow
problems have many adjacent solutions, and some of them are better than this one
depending on what you are actually doing.

This page tries to be fair. Where an alternative wins, it says so.

---

## Doing nothing

**How it works:** you run two agents in one checkout and hope.

**When to prefer it:** more often than people admit.

- One agent at a time. Nothing to coordinate.
- Two agents working in obviously disjoint areas that both of you can hold in
  your head: one on `docs/`, one on `src/parser/`.
- A session that will last twenty minutes.
- You are watching both terminals anyway.

Coordination has a cost: setup, one more command per task, one more thing an
agent can get wrong. Below some threshold of concurrency the cost exceeds the
benefit, and doing nothing is the correct engineering decision.

**What it does not solve:** the failure is silent and delayed. Two agents that
touch the same file do not fail loudly at the moment of collision; one of them
just writes last, and you discover the loss when a test fails an hour later, or
when a reviewer notices a function has quietly reverted. The cost of "doing
nothing" is not zero, it is deferred and hard to attribute.

The honest threshold: **once you have three or more agents, or sessions longer
than an hour, or agents whose scopes you cannot predict, doing nothing stops
being a decision and becomes a bet.**

---

## A shared Markdown file

**How it works:** everybody reads and rewrites `shared-development-updates.md`,
or `To-do.done.md`, or `STATUS.md`. This is the pattern most teams reach for
first, and it is the one that produced this project.

**When to prefer it:** when there is exactly one writer. A human-maintained
`STATUS.md` that agents only read is genuinely useful, costs nothing, and is not
what this section is warning about.

**What it does not solve:** concurrent writes. This is the origin bug, described
in [../README.md](../README.md):

```
agent A reads updates.md      (200 lines)
agent B reads updates.md      (200 lines)
agent A appends and writes    (215 lines)
agent B appends and writes    (212 lines)   <- A's 15 lines are gone
```

Roughly 128 lines of one agent's work disappeared this way in the incident that
started this project. Nobody was careless. Read-modify-write on one shared file
is not safe with concurrent writers, and no amount of "please be careful" in an
instruction file changes that, because neither agent did anything wrong at any
individual step.

`npm run demo` reproduces it on demand: 8 processes, 200 lines written, some
number under 30 surviving, a different number every run.

If you are on this pattern today, [./migration.md](./migration.md) imports the
history instead of throwing it away.

---

## flock, lockf, and O_EXCL lockfiles

**How it works:** wrap the work in an advisory lock.

```bash
flock /tmp/repo.lock -c 'npm run codemod'
```

**When to prefer it:** honestly, quite often.

- One critical section, not many.
- The section is short and you can wrap a single command.
- Everybody is on the same machine and the same POSIX filesystem.
- You want zero new tools and zero new files.

`flock` is battle-tested, in every distribution, and correct. If a shell script
and `flock` cover your case, use them. That is a legitimate choice and this
project would rather you did that than adopt something you do not need.

**What it does not solve:**

- **Granularity.** `flock` locks a file, not a path set. To express "I have
  `src/api/**` but not `src/ui/**`" you have to invent a lock-file naming scheme
  and an overlap rule, at which point you are writing this project.
- **Expiry.** A `flock` held by a process that is killed with `SIGKILL` is
  released by the kernel, which is good, but a lock represented by a lockfile
  that a script forgot to delete is held forever. Agent Fridge claims carry a
  lease with a TTL, so a crashed agent's claim expires on schedule with no
  cleanup process.
- **Attribution.** `flock` cannot tell you who holds the lock, what they are
  doing, or when they expect to be done. `fridge board` answers all three, and
  that turns out to be most of the value in a multi-agent setup: an agent that
  knows *why* it is blocked can pick different work.
- **Handoff.** There is no way to pass a `flock` to another participant.
- **Portability.** `flock` is not on macOS by default and is not a native
  concept on Windows. Agent Fridge's mutex is built on `mkdir`, which is atomic
  everywhere.
- **History.** No record survives the lock being released.

The relationship is not competitive. Agent Fridge uses the same idea internally:
its registry mutex is a `mkdir`-based lock held for milliseconds. Claims are the
layer above.

---

## Git branches

**How it works:** each agent works on its own branch, and you merge.

**When to prefer it:** whenever the work is genuinely independent and you are
willing to pay for merges. Branches are the correct tool for *parallel lines of
development*, and they are much better than Agent Fridge at:

- Reviewing an agent's work as a unit before it touches the mainline.
- Throwing an agent's work away cleanly.
- Bisecting, reverting, cherry-picking.
- Sharing work across machines and with people who are not in the room.

**What it does not solve:** branches do not solve the same problem. Agent Fridge
is for agents that are in **one working tree at the same time**. If you can put
each agent on its own branch, you almost certainly should, and then you may not
need this tool at all.

The friction is real, though, and worth naming:

- Switching branches in one checkout swaps the files under every agent
  simultaneously. Two agents on two branches in one checkout is not a thing.
- Long-lived agent branches produce merge conflicts, and the current generation
  of agents resolves merge conflicts badly.
- Some work does not decompose onto branches. A codemod, a dependency upgrade,
  or a rename that touches every file is one change; four agents on four
  branches all touching every file is worse than one agent doing it.

Branches and Agent Fridge also compose: the branch name is recorded as a label on
every claim, so a board can show that three agents on the same checkout are all
working against `feature/api-v2`.

---

## Git worktrees

**How it works:** `git worktree add` gives each agent its own directory with its
own checked-out branch, backed by one shared object store.

```bash
git worktree add ../repo-claude   feature/api
git worktree add ../repo-copilot  feature/ui
```

**When to prefer it: this is often the right answer, and you should consider it
before Agent Fridge.**

If your agents can work in separate checkouts, worktrees give you something
Agent Fridge cannot: **actual isolation**. Not advisory coordination, not a
cooperative protocol, not a hook that an agent could bypass. Two agents in two
worktrees physically cannot overwrite each other's files, because they are not
the same files. There is no trust boundary to reason about and no rule for an
agent to ignore.

They are also cheap. One object store, one fetch, fast creation, and `git
worktree list` gives you a board for free.

Prefer worktrees when:

- Each agent's work maps onto its own branch.
- The agents do not need to see each other's uncommitted changes.
- Your build tolerates N copies of the working tree (disk, `node_modules`,
  incremental build caches, IDE indexes).
- You are willing to merge at the end.

**What it does not solve:**

- **Shared uncommitted state.** If agent A is refactoring an interface and agent
  B needs to consume it *right now*, worktrees make that awkward: B cannot see
  A's work until A commits and B pulls.
- **Cost per agent.** A large monorepo with a 4GB `node_modules` and a 20-minute
  cold build is painful to instantiate six times. This is the single most common
  practical reason teams end up in one checkout.
- **Tooling that assumes one checkout.** Docker bind mounts, IDE workspaces,
  local dev servers on fixed ports, `.env` files, database fixtures, and license
  daemons frequently assume one working directory.
- **Human plus agent.** You are in the repository too, in your editor, with
  uncommitted changes. Putting yourself in a worktree to avoid your own agent is
  usually not the trade you want.
- **Work that does not decompose.** Same as branches.
- **It does not tell you anything.** Worktrees isolate; they do not coordinate.
  Two agents in two worktrees can still both decide to rewrite the same module,
  and you find out at merge time.

The honest summary: **worktrees are strictly better isolation when they fit.**
Agent Fridge is for the case where they do not fit, or where they fit badly
enough that everybody ends up back in one checkout anyway. The two are also
compatible: run Agent Fridge inside a worktree that several agents share.

---

## File-level OS locks

**How it works:** mandatory locking at the operating system level, or an
exclusive open that other writers cannot bypass.

**When to prefer it:** when you need real enforcement and control the whole
stack, for example a single application managing its own data files.

**What it does not solve:** for a source checkout, this is the wrong shape.

- Mandatory locking is effectively unavailable in practice. Linux mandatory
  locks require a specific mount option, are deprecated, and are documented as
  unreliable. Windows has real mandatory sharing modes, but Node, Git, and most
  editors do not use them.
- Editors, formatters, build tools, and Git itself write files constantly. A
  mandatory lock on a source file makes ordinary tooling fail in ways nobody can
  debug.
- A lock covers a file that exists. Half the interesting conflicts are about
  files that are about to be created, moved, or deleted.
- Locks disappear when the process does. That sounds good, but an agent that
  spawns a fresh process per command holds nothing between commands, so the
  protection is gone exactly when you need it.

This is why Agent Fridge is deliberately advisory. See
[./concepts.md](./concepts.md) and
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#12-security-and-trust-boundaries).

---

## A task queue or an issue tracker

**How it works:** GitHub Issues, Linear, Jira, a `tasks.json`, or a work queue
assigns work to workers.

**When to prefer it:** for deciding **what** should be done and **who** should
do it. A tracker is better than Agent Fridge at everything to do with planning,
prioritising, assigning, discussing, and reporting. If your problem is "the
agents keep doing the wrong work", a queue is the fix and this tool is not.

**What it does not solve:** a tracker allocates *tasks*, not *paths*. Two tasks
that look independent on a board ("add rate limiting", "add request logging")
can both need `src/api/middleware.ts`. The tracker will happily assign them in
parallel, and neither assignee learns about the other until one of them
overwrites the other's edits.

Agent Fridge deliberately does no assignment or scheduling; that is an explicit
non-goal
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#13-non-goals-for-v01)).
The two layers compose well: the tracker says what to work on, the claim says
which files that turned out to mean. Put the issue number in a label:

```bash
fridge claim "src/api/middleware.ts" --task "rate limiting" --label issue=412
```

---

## An MCP coordination server

**How it works:** a Model Context Protocol server exposes coordination tools,
and agents call them.

**When to prefer it:** when your agents are all MCP clients and you want
coordination to appear in the model's tool list rather than in an instruction
file. Tool calls are more reliably invoked than instructions are followed, which
is a genuine advantage. If every agent in your setup speaks MCP, an MCP surface
is the more ergonomic integration.

**What it does not solve:**

- **A server is a dependency.** It must be started, configured per client,
  supervised, and kept alive. When it is down, coordination is down.
- **MCP is not universal.** A shell script, a CI job, a `Makefile`, a
  pre-commit hook, and a human at a prompt are not MCP clients. A process that
  exits with a number works for all of them.
- **Per-client configuration.** Every agent needs its own MCP config entry.
  An instruction file plus a binary on `PATH` needs one install.
- **State still has to live somewhere.** An MCP server that coordinates access
  to a checkout has the same on-disk problems this protocol solves. It does not
  remove them; it wraps them.

Agent Fridge has no MCP server and does not require one. Because the protocol is
a documented on-disk format
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#10-interoperability-profile)),
an MCP server over the same `.fridge/` directory is a perfectly reasonable thing
to build, and it would interoperate with the CLI. That is the layering working
as intended.

---

## A daemon

**How it works:** a long-running background process owns the coordination state
and serves clients over a socket.

**When to prefer it:** when you need things a daemon is genuinely required for:
push notifications to waiters, sub-millisecond lock acquisition, cross-machine
coordination, fair queueing, or a consistent view across many repositories.
A daemon does all of that better than files do.

**What it does not solve, and what it costs:**

- **It has to be running.** Start it, supervise it, restart it after a crash,
  after a reboot, after an upgrade. Every one of those is a new failure mode
  that produces a confused agent.
- **Lifecycle in agent contexts.** Agents run in containers, over SSH, in CI,
  and in sandboxes that are torn down constantly. "Is the daemon up?" is a
  question you now have to answer everywhere.
- **State outlives it badly.** A daemon that dies holding in-memory state loses
  it. Agent Fridge's state is files; if the CLI is killed mid-command the
  workspace is still readable, and `fridge doctor` will tell you what it found.
- **It cannot be inspected with `cat`.** Debugging a daemon needs a client.
  Debugging Agent Fridge needs `ls .fridge/claims/`.

No daemon is an explicit non-goal
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#13-non-goals-for-v01)).
The cost of that choice is real: expiry is swept opportunistically by the next
command rather than at the instant a lease ends, and `fridge wait` polls instead
of being woken. Those are acceptable prices for having nothing to keep alive.

---

## Summary

| Approach | Isolation | Expiry | Attribution | Handoff | Needs a process running | Cost per agent |
| --- | --- | --- | --- | --- | --- | --- |
| Doing nothing | none | n/a | none | n/a | no | zero |
| Shared Markdown file | none | n/a | manual, lossy | manual | no | zero, plus data loss |
| `flock` / lockfiles | advisory | kernel only | none | no | no | low |
| Git branches | full, after merge | n/a | full | via PR | no | merge cost |
| Git worktrees | **full, immediate** | n/a | full | via branch | no | one checkout each |
| File-level OS locks | mandatory | process exit | none | no | no | breaks tooling |
| Task queue / tracker | none | n/a | task-level | assignment | usually a service | low |
| MCP coordination server | advisory | server's choice | full | yes | **yes** | per-client config |
| Daemon | advisory | precise | full | yes | **yes** | supervision |
| Agent Fridge | advisory | lease + TTL | full | yes | no | one command per task |

---

## You probably do not need Agent Fridge if...

- You run **one agent at a time**. This is the most common case and it needs
  nothing.
- Your agents can each have **their own Git worktree**, and your build is cheap
  enough to instantiate N times. Take the real isolation.
- Your agents work on **separate branches** and you are happy merging.
- Your concurrency is **two agents in obviously disjoint directories** for short
  sessions.
- Your actual problem is **which work to do**, not which files are busy. Use an
  issue tracker.
- You have **one critical section** and `flock` covers it. Use `flock`.
- You need **enforcement, not cooperation**. Nothing advisory will satisfy that;
  use separate checkouts.
- You need **coordination across machines**. That is an explicit non-goal for
  v0.1 and the degraded behaviour is documented in
  [./interop.md](./interop.md).

You probably do want it if several agents plus at least one human share one
checkout, the sessions are long, the scopes are unpredictable, and you have
already lost work once.

---

## This is not a new idea

It should not be sold as one.

Shared coordination boards are old and well understood:

- **Blackboard architectures** (HEARSAY-II, 1980): independent knowledge sources
  posting to one shared structure.
- **Advisory locking**: as old as Unix. `flock`, `lockf`, `O_EXCL` lockfiles,
  `git index.lock`.
- **Leases with expiry**: standard distributed-systems practice (Gray and
  Cheriton, 1989); every lock service since Chubby uses them.
- **Tuple spaces** (Linda, 1985): coordination through a shared space.
- **Chore charts on fridge doors**: older than all of it.

Agent Fridge invents none of that. Nothing on this page should be read as a claim
of novelty, and there is no reason it needs to be novel: the pattern is correct,
and the value is in doing it well for one specific context.

What the project does claim is narrower, and every part of it is checkable:

| Claim | How to check it |
| --- | --- |
| Zero runtime dependencies | `npm ls --all` |
| Real concurrency, not simulated | `npm run test:concurrency` spawns real OS processes that race at an agreed instant |
| The failure mode is actually fixed | `npm run demo` reproduces the data loss, then removes it |
| Exit codes are a stable API | [../spec/exit-codes.md](../spec/exit-codes.md), generated from source, verified by `npm run gen:check` |
| The protocol is forkable | [../spec/protocol-v0.1.md](../spec/protocol-v0.1.md) is complete enough to reimplement in Go, Rust, or Python without reading this source |
| No vendor lock-in | Nothing in `.fridge/` names a model vendor except a free-text label |
| Model-neutral | There is no model, no API key, and no network call anywhere in the codebase |

Execution quality and neutrality, not novelty. If one of those checks fails,
that is a bug worth filing.

---

## Next

- [./quickstart.md](./quickstart.md) - try it in ten minutes and decide.
- [./concepts.md](./concepts.md) - the trust boundary, stated plainly.
- [./migration.md](./migration.md) - if you are on the shared-Markdown pattern.
- [./faq.md](./faq.md) - "why not just use branches or worktrees?" answered
  shorter.
