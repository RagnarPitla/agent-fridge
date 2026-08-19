# ADR 0001: What FridgeBoard ships as

- **Status:** Accepted
- **Date:** 2026-02-14
- **Deciders:** Ragnar Pitla
- **Supersedes:** nothing
- **Related:** [spec/protocol-v0.1.md](../../spec/protocol-v0.1.md), [docs/comparison.md](../comparison.md)

---

## Context

The problem is concrete and it already happened. Two agent terminals were working
in one Git checkout. They coordinated through two shared Markdown files:
`To-do.done.md` for durable history and `shared-development-updates.md` for live
ownership. Both agents had to read and rewrite the same files. One agent's
read-modify-write overwrote roughly **128 lines** of the other's work, which then
had to be reconstructed by hand.

The failure mode is not "agents are careless". It is architectural: **a single file
with multiple writers, edited by read-modify-write, with no locking and no
atomicity.** Two humans in one Google Doc are fine because the Doc is a CRDT with a
server. Two processes in one Markdown file are not fine, because the last writer
wins and the loser never finds out.

So: build something to fix it. But *what shape of thing*? In the current ecosystem
the same capability could plausibly ship as any of five things, and they are not
interchangeable. Choosing wrong here is expensive later, because the shape
determines who can use it, what it depends on, and whether it survives the vendor
that inspired it.

The constraints, restated from the mission:

- Local-first and offline. No daemon, no cloud service, no database, no mandatory
  MCP server.
- Model-neutral and vendor-neutral. Claude Code, Copilot CLI, Codex, Herdr, plain
  terminals, and humans, all first class.
- Multiplexer-neutral. tmux, screen, Windows Terminal, iTerm, or nothing.
- Human-readable, Git-friendly state.
- Safe under genuinely concurrent OS processes.
- Explicit errors, never silent fallback.
- **Must not require every agent to support hooks.**
- Must work when an agent can only read repository instructions and run a command.

That second-to-last constraint does a lot of work, and it is the one that kills two
of the five candidates outright.

---

## Options considered

### Option A: A protocol specification only

Write the spec. Let everyone implement it.

**For:** maximum neutrality, zero maintenance, no language argument, forkable
forever.

**Against:** nothing exists on day one. A spec with no implementation gets zero
adoption, and worse, gets *misimplemented* in five subtly incompatible ways, which
is worse than no standard at all. You cannot test a document. And the mission's
"DONE WHEN" explicitly requires a working CLI and a real concurrency simulation, so
this alone does not satisfy the brief.

**Verdict:** necessary but not sufficient.

### Option B: A CLI only

Ship `fridge` as a binary or an npm package. Behaviour is whatever the code does.

**For:** immediately useful, easy to test, easy to install, works from any shell, so
it is automatically neutral across vendors and multiplexers. Every agent on the
list can run a shell command; that is the one capability they all share.

**Against:** without a written contract, the state format is an accident of the
implementation. A second implementation in Go or Rust would have to reverse-engineer
JavaScript, and the moment two implementations disagree there is no arbiter. The
project would then be as vendor-locked to Node as the thing it replaces is locked to
one editor. It also invites the CLI to grow whatever incidental behaviour is
convenient, which is how coordination tools get subtly unsafe.

**Verdict:** necessary but not sufficient.

### Option C: A skill or plugin (Claude skill, Copilot plugin, MCP server)

Package the capability the way each vendor packages capabilities.

**For:** best possible discovery inside the one vendor. The agent gets a described
tool with a schema and may use it without being told to.

**Against, and this is fatal as a primary form:**

1. **It is vendor-specific by construction.** A Claude skill does nothing for Codex.
   An MCP server does nothing for an agent without MCP. You would ship N of them and
   the semantics would drift, which is exactly the failure the project exists to fix,
   moved up a layer.
2. **A human cannot use it.** The incident involved a human reconstructing 128 lines
   by hand. Humans in a plain terminal have to be first-class participants, and they
   are not going to install an MCP server to leave a note.
3. **MCP is a mandatory-server dependency**, explicitly excluded by the constraints.
4. **The state would live behind an API** rather than in files you can `cat`.

It is, however, an excellent *optional wrapper* over something else.

**Verdict:** rejected as the primary form; accepted as an optional layer.

### Option D: An agent

A coordinator agent that other agents talk to. It arbitrates.

**For:** conceptually tidy. Natural-language negotiation. Could resolve conflicts
cleverly, understand intent, suggest who should yield.

**Against, and this is disqualifying:**

1. **An agent needs a model, an API key, a budget, and a network.** Every one of
   those is excluded by "local-first, offline, model-neutral, no cloud service".
2. **An agent can hallucinate.** The thing being decided is "may I write to this
   file". A locking primitive that is right 99% of the time is not a locking
   primitive. It is a coin flip with good manners. `mkdir` is atomic; a language
   model is not.
3. **It is a daemon in a costume.** Something has to be running for others to talk
   to. That is the excluded daemon, plus latency, plus a per-decision token cost.
4. **It is the slowest possible answer** to a question that should take a
   millisecond and happens hundreds of times an hour.

**Verdict:** rejected outright. This is the most important rejection in this
document. **Not everything in an agentic system should be an agent.** The
coordination substrate specifically must not be, for the same reason a filesystem
is not written in a language model.

### Option E: A harness or runner

A supervisor process that launches the agents, assigns work, and owns the checkout.
Roughly what Herdr and similar orchestrators do.

**For:** genuine enforcement. If the harness owns process spawn, it can enforce
scope rather than advise it.

**Against:** it forces you to adopt the harness, which excludes anyone who already
uses tmux, or Herdr, or nothing, or is just a human with two terminals. It is
multiplexer-hostile, and it is the opposite of neutral: you would be competing with
every orchestrator instead of working inside all of them. It also cannot help the
human who opens a third terminal by hand, which is precisely how the original
incident happened.

**Verdict:** rejected as the primary form; supported as an integration target.

---

## Decision

**Ship a layered stack. The protocol is the product, the CLI is the primary
distributable, and every vendor-specific form is an optional adapter on top.**

| Layer | Artefact | Status | Why it is at this layer |
| --- | --- | --- | --- |
| **0. Protocol** | `spec/protocol-v0.1.md` + `test/vectors/*.json` | Required, normative | The real deliverable. Language-neutral, forkable, outlives this repository and any vendor |
| **1. CLI** | `fridge` (Node, zero dependencies) | Required, primary distributable | The lowest common denominator every participant already has: the ability to run a command and read an exit code |
| **2. Adapters** | `fridge adapters install` writing into `AGENTS.md`, `CLAUDE.md`, `.github/copilot-instructions.md`, Codex, generic | Recommended | Generated from one canonical text so vendor instructions cannot drift apart |
| **3. Skill / MCP wrapper** | Optional packaging over the CLI | Optional, later | Better discovery for vendors that support it, with zero new semantics |
| **4. Hooks / harness glue** | Optional pre-commit and vendor hooks calling `fridge guard` | Optional | Turns advice into enforcement *where available*, without ever requiring it |

Layers 0 and 1 are the project. Layers 2 to 4 must never contain behaviour that is
not already in layer 0.

### Why the CLI is the right primary form

Because **the exit code is the API**, and an exit code is the most portable
interface that exists in this problem space.

- `bash`, `zsh`, `fish`, `cmd.exe`, and PowerShell all understand it.
- Every agent on the target list can run a command and read a status.
- A human can run it.
- A Makefile, a pre-commit hook, and a CI job can all branch on it.
- It needs no schema negotiation, no transport, and no version handshake.

Exit `10` means "somebody else has these paths" in every one of those contexts,
including in a shell script written by someone who has never heard of this project.
That is why `spec/exit-codes.md` is generated from the source and checked in CI:
breaking an exit code is a breaking API change, and it should feel like one.

### Why the protocol has to exist separately

Three reasons, in order of importance:

1. **It is what makes the CLI honest.** Writing the spec first forced questions the
   implementation would have quietly gotten wrong: what happens when scopes partially
   overlap, what happens when a lease expires while the owner is alive, whether
   `advisory` blocks `advisory`. Several real bugs were found by writing the sentence
   and then checking the code against it.
2. **It makes a second implementation possible.** Someone wanting a single static Go
   binary, or a Rust crate, should be able to build it from the spec and prove
   conformance against `test/vectors/`, without reading any JavaScript.
3. **It survives.** If this repository is abandoned, `.fridge/` is still just JSON
   and Markdown, and the document describing it is already on your disk.

### What is deliberately *not* being built

- No agent, for the reasons in Option D.
- No daemon, no server, no database.
- No required MCP server.
- No orchestrator competing with Herdr or tmux.
- No runtime dependency, so that the CLI stays auditable and installable offline.

---

## Consequences

**Good:**

- Works today, in every listed environment, including a human in a bare terminal.
- Testable in a way none of the other options are: real processes, real filesystem,
  deterministic exit codes, conformance vectors.
- Neutral by construction. Nothing to port when a new agent CLI appears next quarter.
- The state is `cat`-readable and Git-friendly, so recovery never needs the tool.
- Optional layers can be added without renegotiating the core.

**Costs, accepted:**

- **Cooperative, not enforced.** An agent that ignores exit 10 and edits anyway is
  not stopped. Mitigated by adapters (layer 2) and by hooks where available (layer 4),
  and honestly documented in `SECURITY.md` and the FAQ. Real enforcement needs
  filesystem permissions or separate checkouts, and that is stated rather than faked.
- **Node is a runtime dependency of the CLI**, though not of the protocol. A future
  static binary is possible precisely because the protocol is written down.
- **Two artefacts to keep in sync.** Mitigated by generating `spec/exit-codes.md`
  from `src/core/errors.mjs`, by CI failing on drift, and by the contribution rule
  that a behaviour change lands in spec, source, and tests together.
- **More upfront work** than shipping a script. Judged worth it: the whole point is
  that the naive version is what broke.

---

## Notes

The layering is not novel and is not claimed to be. It is the ordinary
specification-plus-reference-implementation pattern used by everything from HTTP to
Language Server Protocol, applied to a problem where people have recently reached
for an agent because agents are what is fashionable. The contribution here is
choosing the boring shape on purpose, and being able to say exactly why.
