# Quickstart

A hands-on walkthrough with two real terminals, two real agents, and the actual output `fridge` prints.

This is the long version of the 60-second start in [../README.md](../README.md).
Everything below was run end to end. The transcripts are copied from a real
session, so the identifiers are real identifiers and the timestamps are real
timestamps. Yours will differ; the shape will not.

## What you need

- Node.js 20.11 or newer.
- One Git checkout that two or more people or agents will work in.
- Two terminals. Terminal A runs Claude Code, Terminal B runs GitHub Copilot
  CLI. Any two agents work; the tool does not care which.

Install one of these ways:

```bash
npx agent-fridge init          # try it with no install
npm install -g agent-fridge    # machine-wide
npm install --save-dev agent-fridge
```

Check it:

```bash
fridge version
```

```
agent-fridge 0.1.0  protocol wcp/0.1  node v22.22.3  darwin/arm64
```

---

## 1. Hang the door (once per repository)

Run this once, in the repository root, and commit the result.

```bash
fridge init
```

```
The fridge is on the wall.  /work/demo/.fridge
  protocol      wcp/0.1
  git           .fridge/.gitignore keeps live state local; notes/ and actors/ are shared history
  gitattributes .gitattributes updated (notes are never auto-merged)
  instructions  AGENTS.md (created)

Next:
  fridge join --agent "your-name" --vendor human
  fridge claim "src/**" --task "what you are doing"
  fridge board
```

Three things happened:

1. `.fridge/` was created, with a `.gitignore` that commits `notes/` and
   `actors/` and ignores every piece of machine-local live state.
2. `.gitattributes` was updated so Git never auto-merges note files.
3. `AGENTS.md` got the coordination block appended. `fridge init --no-adapters`
   skips that. To wire up the other vendors, see
   [./adapters.md](./adapters.md).

Commit it:

```bash
git add .fridge .gitattributes AGENTS.md
git commit -m "Add Agent Fridge coordination"
```

---

## 2. Put your name on the door (once per agent, per checkout)

### Terminal A: Claude Code

```bash
fridge join --agent claude --vendor claude
```

```
Your name is on the door: claude (claude)
  actor    act_01M0D3YXQ8M7JBM0KEHNJRYDAE
  session  ses_01M0D3YXQ8M7JBM0KEHNJRYDAF

Tip: export FRIDGE_ACTOR="claude" so you can drop --agent from every command.
```

### Terminal B: GitHub Copilot CLI

```bash
fridge join --agent copilot --vendor copilot
```

```
Your name is on the door: copilot (copilot)
  actor    act_01M0D3YXSZT1X4BXJ4Z0FJZFM5
  session  ses_01M0D3YXT0BTFKFJJHPQS0VKEV
```

`--vendor` is a free-text label from a fixed list: `claude`, `copilot`,
`codex`, `cursor`, `human`, `other`. Nothing in `.fridge/` depends on it. It is
there so the board reads well.

### Export your identity

Agent Fridge never guesses who you are. It resolves identity in exactly this
order, and stops at the first hit:

1. `--agent <name>` on the command line
2. the `FRIDGE_ACTOR` environment variable
3. the sole actor, if the workspace has exactly one
4. otherwise exit `7` (`E_NO_SESSION`)

It will not infer you from a pid, a tty, a parent process, or the Git author.
See [../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#9-actor-and-session-resolution).

With two actors on the door, an unqualified command refuses:

```bash
fridge whoami
```

```
E_NO_SESSION: More than one housemate is on this door (claude, copilot).
hint: Pass --agent <name>, or export FRIDGE_ACTOR=<name>.
```

So export it once per terminal.

Terminal A, bash / zsh / fish:

```bash
export FRIDGE_ACTOR=claude
```

Terminal B, bash / zsh / fish:

```bash
export FRIDGE_ACTOR=copilot
```

PowerShell 5.1 and PowerShell 7:

```powershell
$env:FRIDGE_ACTOR = "claude"
```

Windows `cmd.exe`:

```
set FRIDGE_ACTOR=claude
```

More shells and multiplexers are in [./interop.md](./interop.md).

---

## 3. Take a chore before you touch files

### Terminal A

```bash
fridge claim "src/api/**" --task "refactor the router"
```

```
Card clm_01M0D3Z1WDG81DRPWQDTVCQMTG is yours.
  scope    src/api/**
  files    2
  mode     exclusive
  back by  15m from now

When you stop: fridge release clm_01M0D3Z1WDG81DRPWQDTVCQMTG --outcome done --note "what changed"
```

`--task` is required by default, because a card with no reason on it is a card
nobody else can act on. The flag is `--task`, not `--why`.

Useful flags on `claim`:

| Flag | Effect |
| --- | --- |
| `--task "..."` | What you are doing. Required by `policy.requireTaskOnClaim` |
| `--mode shared` | Coexists with other `shared` claims; still blocks `exclusive` |
| `--mode exclusive` | The default. Blocks any overlapping claim except `advisory` |
| `--mode advisory` | Records intent and blocks nobody |
| `--ttl 45m` | Lease length. Default 15m, capped at 4h |
| `--exclude "src/api/legacy/**"` | Carve paths out of the claim. Repeatable |
| `--wait 10m` | Retry until the conflict clears, then take it |
| `--label sprint=12` | Attach a key=value label. Repeatable |
| `--strict` | Do not merge into a card you already hold; make a new one |
| `--confirm-global` | Required to claim the whole repository |

Durations are `500ms`, `30s`, `15m`, `2h`, `1d`. A bare number is rejected.

### Terminal B: the exit-10 refusal

```bash
fridge claim "src/api/routes.ts" --task "fix a typo"
```

```
Somebody already has that chore.

  card    clm_01M0D3Z1WDG81DRPWQDTVCQMTG
  who     claude (claude)  pid 8819
  mode    exclusive   doing: refactor the router
  scope   src/api/**
  back by 2026-08-19T13:54:26.239Z (in 14m 59s)
  clash   literal-prefix-nesting: src/api/routes.ts, src/api/**

You can:
  fridge board                          # see the whole door
  fridge claim <narrower-path> ...      # take a different chore
  fridge wait clm_01M0D3Z1WDG81DRPWQDTVCQMTG --timeout 10m
  fridge handoff clm_01M0D3Z1WDG81DRPWQDTVCQMTG --to claude --note "..."
E_CONFLICT: 1 card(s) already cover those paths.
hint: fridge board  |  fridge wait clm_01M0D3Z1WDG81DRPWQDTVCQMTG  |  fridge handoff ...
```

Exit code `10`. The report goes to stderr, so a script that only reads stdout
is not confused by it. Overlap detection is deliberately conservative: it may
refuse a pair of scopes that would not actually have collided, but it will never
allow a pair that would. The rules are in
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#63-overlap-decision).

Terminal B takes something else instead:

```bash
fridge claim "src/ui/**" --task "restyle the header"
```

```
Card clm_01M0D3Z6X1RRCD0JKYDHF17CGE is yours.
  scope    src/ui/**
  files    1
  mode     exclusive
  back by  15m from now

When you stop: fridge release clm_01M0D3Z6X1RRCD0JKYDHF17CGE --outcome done --note "what changed"
```

---

## 4. Read the door

From either terminal, at any time:

```bash
fridge board
```

```
<!-- GENERATED by agent-fridge 0.1.0 at 2026-08-19T13:39:31.529Z state:7183cc2dcdf4
     Source of truth: .fridge/. DO NOT EDIT. Regenerate with: fridge render -->
# The door

Fridge `wsp_01M0D3YS8WQ4WVQMB8CXD9QPYN` | 2 chore(s) claimed | 0 waiting

## Claimed right now

| Card | Who | Mode | Scope | Doing | Back by |
|---|---|---|---|---|---|
| `clm_01M0D3Z1WDG81DRPWQDTVCQMTG` | claude (claude) | exclusive | `src/api/**` | refactor the router | in 14m 54s |
| `clm_01M0D3Z6X1RRCD0JKYDHF17CGE` | copilot (copilot) | exclusive | `src/ui/**` | restyle the header | in 14m 59s |

## Last 5 notes

- 2026-08-19T13:39:21.981Z claude `session.started` claude (claude) walked up to the fridge
- 2026-08-19T13:39:22.070Z copilot `session.started` copilot (copilot) walked up to the fridge
- 2026-08-19T13:39:26.258Z claude `claim.acquired` claude took src/api/** for "refactor the router"
- 2026-08-19T13:39:26.363Z copilot `claim.denied` copilot was blocked on src/api/routes.ts
- 2026-08-19T13:39:31.423Z copilot `claim.acquired` copilot took src/ui/** for "restyle the header"
```

`fridge status` shows the same data in a terser layout, and `fridge status
--watch` refreshes it. `.fridge/DOOR.md` on disk holds the last rendered copy.
It is generated, it is git-ignored, and losing it costs nothing: `fridge render`
rebuilds it. See [./concepts.md](./concepts.md).

### Ask about one path instead of reading the whole door

```bash
fridge check src/api/routes.ts     # a path you hold
```

```
yours     src/api/routes.ts  (claude, clm_01M0D3Z1WDG81DRPWQDTVCQMTG)
```

```bash
fridge check src/ui/App.tsx        # a path somebody else holds
```

```
THEIRS    src/ui/App.tsx  (copilot, clm_01M0D3Z6X1RRCD0JKYDHF17CGE)
E_CONFLICT: 1 path(s) belong to somebody else.
hint: fridge board
```

Exit `0` means yours or free, exit `10` means theirs. Add `--for-write` to also
fail on unclaimed paths:

```bash
fridge check README.md --for-write
```

```
unclaimed README.md
E_OUT_OF_SCOPE: 1 path(s) are outside any card you hold.
hint: fridge claim "README.md" --task "..."
```

Exit `14`.

---

## 5. Pin a note

Notes are the durable, attributed history. They are write-once files with
globally unique names, which is why two agents pinning at the same instant can
never overwrite each other.

```bash
fridge pin "router split into 3 files; tests still red on auth"
```

```
pinned evt_01M0D3ZD5XREYVG2720W95BSWD
```

The message is a positional argument, not a flag. Optional flags:

| Flag | Effect |
| --- | --- |
| `--kind warning` | Note type becomes `note.warning`. Default kind is `note` |
| `--claim clm_...` | Attach the note to a card |
| `--task "..."` | Record what the note is about |
| `--allow-secret-like` | Override the credential heuristic |

Notes are committed history, so `pin` refuses anything that looks like a
credential:

```bash
fridge pin "deploy key is ghp_abcdefghijklmnopqrstuvwxyz012345"
```

```
E_USAGE: That note looks like it contains a GitHub token.
hint: Notes are committed history. Remove it, or pass --allow-secret-like if it is a false positive.
```

Read the wall back:

```bash
fridge log --limit 5
fridge log --actor copilot
fridge log --type note.warning
fridge log --since 2m
fridge log --follow
```

```
2026-08-19T13:42:13.178Z  copilot         claim.expired       expired claude's card on src/api/**
2026-08-19T13:42:13.266Z  copilot         claim.acquired      copilot took src/api/** for "taking over the stale card"
2026-08-19T13:42:36.494Z  copilot         note.warning        auth tests are flaky on CI today
```

---

## 6. Say "still on it"

A claim carries a lease. The default TTL is 15 minutes. Two things renew it:

- `fridge heartbeat`, explicitly.
- Any command at all from the owning session, once the lease is past half its
  TTL. That is why a working agent rarely needs an explicit heartbeat.

```bash
fridge heartbeat
```

```
still on it: renewed 1 card(s)
  clm_01M0D3Z1WDG81DRPWQDTVCQMTG  until 2026-08-19T13:54:37.890Z
```

`fridge extend <card> --ttl 1h` raises the TTL on one card. `fridge heartbeat
--claim clm_... --ttl 30m` renews one card at a longer TTL.

If a lease runs out, the card becomes stale after a one-minute grace period and
the next mutating command sweeps it:

```bash
fridge reap --dry-run
```

```
would sweep clm_01M0D4257XVMWRVWE4C78CJYFC (claude, expired 2026-08-19T13:41:09.982Z)
```

There is no daemon. Nothing runs in the background. The sweep happens because
somebody else needed the paths. See
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#66-leases-heartbeats-and-staleness).

---

## 7. Hand a chore over

Half-finished work should change hands, not fall on the floor. The card stays
with the offerer until the other side accepts, so it is never unowned.

### Terminal A offers

```bash
fridge handoff clm_01M0D3Z1WDG81DRPWQDTVCQMTG --to copilot \
  --note "routes.ts done, server.ts still needs the error path"
```

```
Offered card clm_01M0D3Z1WDG81DRPWQDTVCQMTG to copilot.
  message  msg_01M0D3ZJ87SMP11NGDPGG8HXG1
  scope    src/api/**

They accept with: fridge accept msg_01M0D3ZJ87SMP11NGDPGG8HXG1 --agent copilot
The card stays yours until they accept, so nothing is ever unowned.
```

### Terminal B reads its inbox

```bash
fridge inbox
```

```
msg_01M0D3ZJ87SMP11NGDPGG8HXG1  handoff  from claude
  card   clm_01M0D3Z1WDG81DRPWQDTVCQMTG  src/api/**
  task   refactor the router
  note   routes.ts done, server.ts still needs the error path
  accept: fridge accept msg_01M0D3ZJ87SMP11NGDPGG8HXG1    decline: fridge decline msg_01M0D3ZJ87SMP11NGDPGG8HXG1
```

### Terminal B accepts

```bash
fridge accept msg_01M0D3ZJ87SMP11NGDPGG8HXG1
```

```
Card clm_01M0D3Z1WDG81DRPWQDTVCQMTG is now yours (from claude).
  scope  src/api/**
  note   routes.ts done, server.ts still needs the error path
When you stop: fridge release clm_01M0D3Z1WDG81DRPWQDTVCQMTG --outcome done --note "..."
```

`fridge decline <msg> --reason "busy"` sends it back. The card returns to the
original owner.

---

## 8. Put the card back

```bash
fridge release clm_01M0D3Z1WDG81DRPWQDTVCQMTG --outcome done --note "error path added"
```

```
took down 1 card(s):
  clm_01M0D3Z1WDG81DRPWQDTVCQMTG  src/api/**
```

`--outcome` is one of `done`, `partial`, `abandoned`, `failed`. It defaults to
`done`. The outcome and the note become a permanent `claim.released` note, so
the next agent can read what actually happened.

`fridge release --all` takes down every card you hold. Releasing somebody
else's card fails:

```
E_NOT_OWNER: Card clm_01M0D41839D701M6J51EBT7GFZ belongs to claude (claude).
hint: fridge handoff clm_01M0D41839D701M6J51EBT7GFZ --to copilot   (or --force if a human told you to)
```

Exit `12`. `--force` exists for a human operator, and it is recorded.

---

## 9. Do the whole cycle in one command

`fridge run` claims, heartbeats while the command runs, then releases with an
outcome that matches the command's exit code. The child's exit code becomes
`fridge run`'s exit code.

The paths go in `--claim`, and the command goes after `--`:

```bash
fridge run --claim "src/api/**" --task "typecheck the api" -- npm run typecheck
```

```
api typechecks
```

If the command fails, the card is released with `--outcome failed` and the note
`node exited 3`, and `fridge run` exits `3`. Add `--keep-on-failure` to hold the
card so you can investigate:

```bash
fridge run --claim "src/api/**" --task "tests" --keep-on-failure -- npm test
```

```
command exited 1; keeping card clm_01M0D498G69YH5BYRKB6E0X5A0 (--keep-on-failure)
```

`run` also accepts `--ttl` and `--mode`. Repeat `--claim` for several scopes.

---

## 10. Machine-readable output

Every command accepts `--json` and prints exactly one JSON envelope on stdout,
and nothing else:

```bash
fridge claim "src/ui/**" --task "json demo" --json
```

```json
{
  "command": "claim",
  "data": {
    "claimId": "clm_01M0D406P0TFVXCTHEP8JJYKS6",
    "expiresAt": "2026-08-19T13:55:03.921Z",
    "merged": false,
    "mode": "exclusive",
    "scope": {
      "exclude": [],
      "include": [
        "src/ui/**"
      ],
      "matchers": [
        "src/ui/**"
      ],
      "materialized": [
        "src/ui/App.tsx"
      ],
      "materializedTruncated": false,
      "materializer": "git"
    },
    "task": "json demo",
    "token": "SXxpwYzzilfmrTIQToqvggIrknfWmXSS",
    "ttlMs": 900000
  },
  "error": null,
  "exitCode": 0,
  "ok": true,
  "protocol": "wcp/0.1",
  "ts": "2026-08-19T13:40:03.996Z"
}
```

Keys are sorted, so the output diffs cleanly. On failure the envelope has
`"ok": false` and an `error` object carrying the same code, message, and hint
that the human output prints. `FRIDGE_JSON=1` turns JSON on for every command in
a shell.

Output is pure ASCII with no ANSI colour, always. That is what keeps PowerShell
transcripts and CI logs readable. `--no-color` is accepted and does nothing.

---

## What to do when you see exit 10

Exit `10` is `E_CONFLICT`: somebody else holds paths that overlap what you
asked for. It is not an error in your setup. Work through this list in order.

1. **Read the door.** `fridge board` tells you who has it, what they are doing,
   and when their lease runs out.
2. **Claim something narrower.** The most common cause is a scope wider than the
   work. If they hold `src/api/**` and you only need `src/db/schema.sql`, claim
   that. Narrow claims are the single biggest thing you can do to keep a
   multi-agent checkout moving.
3. **Do the read-only part now.** `fridge claim "src/api/**" --mode shared
   --task "reading for a review"` coexists with other `shared` claims. It still
   blocks an `exclusive` claim, so only use it when you genuinely are not going
   to write.
4. **Wait, if the lease is short.** `fridge wait <card> --timeout 10m` blocks
   until the card comes down and exits `0`; it exits `21` on timeout.
   `fridge claim <paths> --wait 10m` does the same thing and then takes the
   card.

   ```
   card clm_01M0D49JJDBT7MTD92HPVP4ZXX is gone after 3s
   ```
5. **Ask for a handoff.** `fridge handoff <card> --to <them> --note "can I take
   this?"` if the card is yours, or pin a note addressed at them:
   `fridge pin "blocked on src/api; can you release when the tests pass?"`.
6. **Check whether it is actually stale.** If the "back by" time in the report
   is already in the past, run `fridge reap`, then claim again. A crashed agent
   cannot hold a card past its lease.
7. **Escalate to a human.** Somebody with judgement can run `fridge release
   <card> --force`. That is an operator action and it is recorded as a note with
   your name on it. An agent should not do this on its own.

What you must never do is edit the paths anyway. That is precisely the failure
this tool exists to prevent, and it will not be visible to anyone until the
other agent's work disappears.

For scripting the branch, both shells:

```bash
if fridge claim "src/api/**" --task "refactor"; then
  echo "the chore is yours"
elif [ $? -eq 10 ]; then
  echo "somebody else has it; pick another chore"
else
  exit 1   # a real error; do not guess
fi
```

```powershell
fridge claim "src/api/**" --task "refactor"
switch ($LASTEXITCODE) {
  0  { "the chore is yours" }
  10 { "somebody else has it" }
  default { throw "fridge failed with $LASTEXITCODE" }
}
```

The full table is [../spec/exit-codes.md](../spec/exit-codes.md).

---

## Next

- [./concepts.md](./concepts.md) - what actors, claims, leases, notes and views
  actually are.
- [./adapters.md](./adapters.md) - teaching your agent the rules automatically.
- [./interop.md](./interop.md) - tmux, PowerShell, WSL, devcontainers, CI.
- [./comparison.md](./comparison.md) - when a Git worktree is the better answer.
- [./migration.md](./migration.md) - coming from shared Markdown files.
- [./faq.md](./faq.md) - the questions people actually ask.
- [../spec/protocol-v0.1.md](../spec/protocol-v0.1.md) - the normative protocol.
