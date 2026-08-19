# Workspace Coordination Protocol (WCP) v0.1

Status: **Draft, stable for 0.1.x**
Protocol identifier: `wcp/0.1`
Reference implementation: Agent Fridge (`fridge`), Apache-2.0
Specification license: Apache-2.0

This document is normative and self-contained. It is written so that a second
implementation in Go, Rust, Python, or PowerShell can interoperate with the
reference implementation on the same checkout without reading its source code.

The key words MUST, MUST NOT, REQUIRED, SHOULD, SHOULD NOT, and MAY are to be
interpreted as described in RFC 2119.

---

## 1. Purpose and model

### 1.1 Problem

Several independent processes, some driven by AI agents and some by humans,
operate on one Git working tree at the same time. They need to know who is
editing what, communicate durable notes, and hand work over, without a daemon,
a server, or a shared mutable document.

The failure mode this protocol exists to remove is **read-modify-write on a
shared file**. When two processes both read a Markdown file, edit it in memory,
and write it back, the second write silently destroys the first process's
changes.

### 1.2 The metaphor, and its limits

The user-facing metaphor is a household fridge door: housemates pin their own
notes, a chore gets one magnet, magnets fall off, chores can be handed over, and
the door is readable at a glance.

**Metaphor discipline (normative):** metaphor vocabulary MUST NOT appear in any
on-disk field name, schema identifier, error code, or JSON key. The wire format
uses `claim`, `lease`, `note`, `actor`, `session`, `message`. "Door", "card",
"chore", and "housemate" exist only in generated human text and documentation.
An implementation may present any metaphor it likes, or none.

### 1.3 Design constraints

1. Local-first and offline. No network I/O of any kind is permitted in a
   conforming implementation's core operations.
2. Model-neutral and vendor-neutral. No model, API key, or provider is involved.
3. No daemon, no background process, no database, no required MCP server.
4. Human-readable, Git-friendly state.
5. Safe under genuinely concurrent OS processes.
6. Explicit, documented refusals. Never a silent fallback.
7. Cooperation MUST be possible without hooks. A participant that can only read
   repository instructions and run a command MUST be able to take part fully.

### 1.4 Roles

| Term | Definition |
| --- | --- |
| Workspace | One Git working tree containing one `.fridge/` state directory |
| Actor | A named participant: an agent instance or a human. Identity is a name, not a credential |
| Session | One actor's participation period in one workspace. Resumable across processes |
| Claim | An assertion of intent to modify a set of paths, held by one session |
| Lease | The time-bounded validity of a claim |
| Note | An immutable, attributed, timestamped record on the shared history |
| Message | A directed record from one actor to another (handoff offer, decline) |
| View | A generated, disposable human-readable rendering of records |

### 1.5 Trust model in one sentence

WCP is **cooperative and advisory**: it coordinates participants that want to be
coordinated, inside a trust boundary where every participant already has write
access to the repository. It is not a security mechanism. See Section 12.

---

## 2. On-disk layout

The state directory is `.fridge/` at the workspace root. Discovery walks up from
the current directory to the filesystem root, and the first directory containing
`.fridge/VERSION` wins. Failure to find one is `E_NOT_INITIALIZED` (exit 3).

```
.fridge/
  VERSION                 text, exactly "wcp/0.1\n"
  config.json             tunables
  workspace.json          workspace identity
  .gitignore              managed; splits local state from shared history
  DOOR.md                 GENERATED view, disposable
  actors/<slug>.json      one file per actor
  sessions/<id>.json      one file per session
  claims/<id>.json        one file per claim
  leases/<claimId>.json   one file per live lease
  notes/YYYY/MM/DD/<ts>--<seq4>--<slug>--<id>.json  write-once
  inbox/<toSlug>/<id>.json                          directed messages
  queue/<id>.json                                   waiters (each names its claimId)
  locks/registry.lock.d/                            the mutex, a directory
  tmp/                    staging area for atomic renames
  quarantine/             damaged records, preserved
  archive/                released claims, if retention is enabled
  views/                  additional generated views
```

### 2.1 Directory semantics

| Directory | Writers | Mutation | Committed to Git |
| --- | --- | --- | --- |
| `notes/` | many, one per file | write-once | Yes |
| `actors/` | one per file | rewritten by its own actor | Yes |
| `claims/` | one per file | rewritten by owner under the mutex | No |
| `leases/` | one per file | rewritten by owner | No |
| `sessions/` | one per file | rewritten by its own session | No |
| `inbox/` | one per file | state transitions under the mutex | No |
| `queue/` | one per file | write-once, deleted on wake | No |
| `locks/` | exclusive | created and removed | No |
| `tmp/` | many | staging only | No |
| `quarantine/` | many | write-once | No |

**Invariant I1 (one writer per record).** Every record file has exactly one
process that may create it, and at most one identity that may subsequently
modify it. No record file is ever read-modify-written by two identities.

### 2.2 Git behaviour

`fridge init` writes `.fridge/.gitignore` with an allowlist:

```
/*
!/.gitignore
!/VERSION
!/config.json
!/workspace.json
!/notes/
!/actors/
```

Rationale:

- `notes/` and `actors/` are shared history and are meant to be committed. Note
  files are write-once with globally unique names, so concurrent branches
  produce added files, never edited ones, and merge cleanly.
- `claims/`, `leases/`, `sessions/`, `queue/`, `locks/`, `tmp/` are
  machine-local and MUST NOT be committed. Committing a lease would let a merge
  resurrect ownership that no live process holds.
- `DOOR.md` is generated and is deliberately ignored. Committing a file that
  every participant regenerates would reintroduce exactly the merge-conflict
  churn this protocol removes. `fridge render --output <path>` can write a
  committed copy if a project wants one.

`fridge init` also appends to `.gitattributes`:

```
# Agent Fridge
.fridge/notes/** -text -merge
.fridge/DOOR.md  linguist-generated=true
.fridge/views/** linguist-generated=true
```

`-merge` means Git never attempts to auto-merge note content: a note is either
present or absent, and both sides of a merge keep all of their notes. `-text`
stops end-of-line rewriting from changing bytes an implementation may hash. The
`linguist-generated` lines keep the generated views out of diff review and out of
language statistics. An implementation MUST NOT remove or reorder lines a user
has added to `.gitattributes`, and MUST NOT re-append its block if it is present.

A conforming implementation MUST NOT run mutating Git commands. It MAY read Git
state (`git ls-files`, current branch) and MUST degrade gracefully when Git is
absent.

### 2.3 Identifiers

- `evt_`, `clm_`, `ses_`, `act_`, `msg_`, `wsp_` prefixes, followed by a ULID.
- ULIDs are monotonic within a process: if two are generated in the same
  millisecond, the random component is incremented. This makes filename sort
  order equal to creation order.
- Identifiers are opaque. Implementations MUST NOT parse meaning out of them
  beyond the prefix.

### 2.4 Common record fields

Every JSON record MUST carry:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema` | string | `wcp/0.1/<kind>`, for example `wcp/0.1/claim` |
| `writer` | string | `<implementation>/<version>`, for example `agent-fridge/0.1.0` |

All JSON is written with sorted keys, two-space indentation, and a trailing
newline, so that diffs are stable. All timestamps are ISO 8601 in UTC with
millisecond precision. All durations on disk are integer milliseconds with an
`Ms` suffix in the field name.

---

## 3. Record schemas

### 3.1 `workspace.json`

```json
{
  "createdAt": "2026-08-19T13:29:36.826Z",
  "createdOnHost": "sha256:32c0f5c3a4da7f06",
  "schema": "wcp/0.1/workspace",
  "workspaceId": "wsp_01M0D3D28TSGQXH7475WNQMGCT",
  "writer": "agent-fridge/0.1.0"
}
```

`createdOnHost` is a salted hash of the hostname (Section 12.4). Raw hostnames
and usernames beyond the local account name MUST NOT be recorded.

### 3.2 `config.json`

Defaults, all overridable by `fridge config set`:

```json
{
  "door":   { "autoRender": true, "extraTargets": [], "path": ".fridge/DOOR.md" },
  "git":    { "readOnly": true, "warnOnSyncedFolder": true },
  "lease":  { "defaultTtlMs": 900000, "graceMs": 60000, "maxTtlMs": 14400000,
              "renewOnAnyCommand": true, "renewThresholdRatio": 0.5 },
  "mutex":  { "acquireTimeoutMs": 10000, "maxHoldMs": 2000, "staleMs": 15000 },
  "notes":  { "commit": true, "retainDays": 0 },
  "paths":  { "allowGlobalClaims": false, "caseSensitivity": "auto",
              "materializeLimit": 5000, "strictExcludes": false,
              "unicodeNormalization": "NFC" },
  "policy": { "requireClaimForWrite": "advisory", "requireTaskOnClaim": true },
  "schema": "wcp/0.1/config",
  "workspaceId": "wsp_...",
  "writer": "agent-fridge/0.1.0"
}
```

`config.json` is the one shared file that is read-modify-written, so every write
MUST happen under the registry mutex (Section 5).

### 3.3 `actors/<slug>.json`

```json
{
  "createdAt": "2026-08-19T13:29:36.920Z",
  "currentSessionId": "ses_01M0D3D2CRG8X26A6GWNZKZPAR",
  "host": "sha256:32c0f5c3a4da7f06",
  "id": "act_01M0D3D2CRG8X26A6GWNZKZPAQ",
  "lastSeenAt": "2026-08-19T13:29:36.921Z",
  "name": "claude",
  "schema": "wcp/0.1/actor",
  "slug": "claude",
  "user": "ragnarpitla",
  "vendor": "other",
  "writer": "agent-fridge/0.1.0"
}
```

The **filename is the identity key**, not the `id` field. `slug` is the actor
name lowercased, NFC-normalised, with every run of characters outside
`[a-z0-9-]` replaced by a single `-`, leading and trailing `-` trimmed, and the
result truncated to 24 characters. An empty result becomes `anon`. Two actors whose names slug identically are the same
actor. This makes actor creation a write-once `O_EXCL` operation and removes any
need to scan a registry to answer "does this name exist".

`vendor` is a free-text label (`claude-code`, `copilot-cli`, `codex`, `cursor`,
`human`, `other`). It is descriptive only. **No behaviour anywhere in this
protocol may depend on its value.**

### 3.4 `sessions/<id>.json`

```json
{
  "actorId": "act_...",
  "actorName": "claude",
  "host": "sha256:32c0f5c3a4da7f06",
  "id": "ses_...",
  "pid": 54208,
  "schema": "wcp/0.1/session",
  "seq": 3,
  "startedAt": "2026-08-19T13:29:36.931Z",
  "tokens": { "clm_...": "BVAJmZPaoBdflVQD3U05G9U3A94hYHkR" },
  "updatedAt": "2026-08-19T13:29:44.125Z",
  "writer": "agent-fridge/0.1.0"
}
```

`tokens` maps claim id to the bearer token that proves ownership. Only the
owning session writes this file, so I1 holds.

**Sessions are resumable and MUST NOT be tied to process lifetime.** Agent CLIs
spawn a fresh process for every command, so a session that ended when its
process exited would be useless. A session ends when its last lease has expired
and been reaped, or when the actor starts a new session with `join`.

`seq` is a per-session monotonic counter used to order notes written in the same
millisecond.

### 3.5 `claims/<id>.json`

```json
{
  "actorId": "act_...",
  "actorName": "claude",
  "createdAt": "2026-08-19T13:29:37.032Z",
  "expiresAtInitial": "2026-08-19T13:44:37.032Z",
  "host": "sha256:32c0f5c3a4da7f06",
  "id": "clm_...",
  "labels": { "branch": "main" },
  "mode": "exclusive",
  "offeredMessageId": null,
  "offeredTo": null,
  "process": { "pid": 54209, "ppid": 54176, "startedAt": "..." },
  "schema": "wcp/0.1/claim",
  "scope": {
    "exclude": [],
    "include": ["src/api/**"],
    "matchers": ["src/api/**"],
    "materialized": ["src/api/db.ts", "src/api/routes.ts"],
    "materializedTruncated": false,
    "materializer": "git"
  },
  "sessionId": "ses_...",
  "state": "active",
  "task": "refactor the router",
  "tokenHash": "sha256:0e87b646...",
  "ttlMs": 900000,
  "updatedAt": "2026-08-19T13:29:37.032Z",
  "vendor": "other",
  "workspaceId": "wsp_...",
  "writer": "agent-fridge/0.1.0"
}
```

Claim states:

| State | Meaning | Held? |
| --- | --- | --- |
| `active` | Normal ownership | Yes |
| `handoff-offered` | Offered to another actor, still owned until accepted | **Yes** |
| `released` | Voluntarily given up | No |
| `expired` | Lease ran out and it was reaped | No |
| `revoked` | Force-released by an operator | No |

`handoff-offered` counting as held is load-bearing: a chore is never unowned
mid-handoff, so an offered claim cannot be stolen by a third party.

Only the true token holder may modify a claim, except for:

- expiry sweeps, which any process may perform under the mutex (Section 6.4);
- `--force` operations, which are recorded as `revoked` with an attributed note.

`tokenHash` is `sha256(token)`. The plaintext token lives only in the owner's
session file. This means a reader of `claims/` cannot forge ownership without
also being able to read and write the owner's session file, which inside this
trust boundary is a filesystem-permissions question, not a protocol question.

### 3.6 `leases/<claimId>.json`

```json
{
  "claimId": "clm_...",
  "expiresAt": "2026-08-19T13:44:37.043Z",
  "pid": 54209,
  "renewals": 0,
  "renewedAt": "2026-08-19T13:29:37.043Z",
  "schema": "wcp/0.1/lease",
  "seq": 0,
  "sessionId": "ses_...",
  "writer": "agent-fridge/0.1.0"
}
```

The lease is a separate file from the claim so that a heartbeat, the hottest and
most frequent write, touches a small file and never rewrites the claim.

### 3.7 `notes/YYYY/MM/DD/<ts>--<seq4>--<slug>--<id>.json`

```json
{
  "actorId": "act_...",
  "actorName": "copilot",
  "data": { "include": ["src/api/routes.ts"],
            "blockedBy": [ { "actor": "claude", "claimId": "clm_...",
                             "reason": "literal-prefix-nesting" } ] },
  "id": "evt_...",
  "schema": "wcp/0.1/note",
  "seq": 2,
  "sessionId": "ses_...",
  "subject": null,
  "summary": "copilot was blocked on src/api/routes.ts",
  "ts": "2026-08-19T13:29:37.290Z",
  "type": "claim.denied",
  "writer": "agent-fridge/0.1.0"
}
```

**Invariant I2 (notes are write-once).** A note file MUST be created with
`O_EXCL` and MUST NOT be modified or deleted afterwards, except by an explicit
retention policy that moves whole day directories to `archive/`.

`seq4` is `seq` zero-padded to 4 digits so that byte order and numeric order
agree. The filename encodes sort order (`ts`, then `seq4`) and attribution (`slug`) so
that a human can read the wall with `ls` and no tooling at all.

Reserved note types, and the ones the reference implementation emits today:

| Type | Written when |
| --- | --- |
| `workspace.initialized` | `init` |
| `session.started` / `session.resumed` | `join` |
| `claim.acquired` | a claim is granted, including via handoff |
| `claim.denied` | a claim is refused because somebody else holds the paths |
| `claim.released` | `release`, and after `run` |
| `claim.expired` | `reap` sweeps a lease (`forced: true` when `--force`) |
| `queue.joined` | `claim --queue` or `wait` registers a waiter |
| `handoff.offered` / `handoff.accepted` / `handoff.declined` | `handoff`, `accept`, `decline` |
| `legacy.update` / `legacy.todo` | `migrate` imports a line from a legacy file |
| `note` | `pin` (the default kind) |

Additional reserved names an implementation MAY emit: `session.ended`,
`claim.extended`, `claim.revoked`, `guard.violation`. Implementations MAY add
their own types under a vendor prefix (`x-<vendor>.<name>`) and MUST NOT invent
unprefixed types outside this list.

### 3.8 `inbox/<toSlug>/<id>.json`

```json
{
  "claimId": "clm_...",
  "createdAt": "2026-08-19T13:29:44.084Z",
  "fromName": "claude",
  "fromSessionId": "ses_...",
  "id": "msg_...",
  "kind": "handoff",
  "note": "tests pass, docs left",
  "reason": null,
  "schema": "wcp/0.1/message",
  "scope": ["src/api/**"],
  "state": "offered",
  "task": "refactor the router",
  "toName": "copilot",
  "writer": "agent-fridge/0.1.0"
}
```

`state` moves `offered -> accepted | declined | withdrawn | expired`. Transitions
happen under the mutex. The message file is owned by the recipient's inbox
directory, so it has one writer at a time by construction.

---

## 4. Atomic primitives

A conforming implementation MUST build every mutation from these primitives and
MUST NOT rely on any other atomicity assumption.

| Primitive | Guarantee | Used for |
| --- | --- | --- |
| `open(O_CREAT \| O_EXCL \| O_WRONLY)` | Fails if the path exists. Atomic on POSIX and on Windows | Notes, actors, queue entries, first-time records |
| `mkdir` | Fails if the directory exists. Atomic everywhere | The registry mutex |
| `rename` within one filesystem | Atomic replace on POSIX; on Windows atomic via `MoveFileEx` semantics, with retry on `EPERM`/`EACCES` for antivirus and indexer interference | Every update to an existing record |
| `fsync` on file, then on parent directory | Durability across power loss | Every record write |
| `readdir` | Never blocks a writer | All scans |
| `unlink` | Idempotent when treated as such | Releases, sweeps |

### 4.1 The write-atomic algorithm

```
writeAtomic(target, bytes):
  tmp = .fridge/tmp/<ulid>.<pid>.tmp
  fd = open(tmp, O_CREAT|O_EXCL|O_WRONLY, 0600)
  write(fd, bytes); fsync(fd); close(fd)
  rename(tmp, target)              # retry up to 5 times with backoff on Windows
  fsync(dirname(target))           # best-effort; ignore EINVAL/EPERM
```

A reader therefore sees either the complete previous version or the complete new
version, never a partial write. A crash between `open` and `rename` leaves a
file in `tmp/` that is never a record; `fridge doctor` cleans those.

### 4.2 Reading

Readers do not take the mutex. A read that encounters unparseable JSON MUST NOT
crash and MUST NOT silently skip the record: it reports it as a corruption
finding (`E_STATE_CORRUPT` where relevant, a `doctor` finding otherwise), and
`doctor --fix` moves the file to `quarantine/` rather than deleting it.

---

## 5. The registry mutex

Serialisation of decisions uses one lock: `.fridge/locks/registry.lock.d/`, a
**directory**, because `mkdir` is atomic on every filesystem this protocol
targets, including SMB and NFS where `O_EXCL` on files is historically unsafe.

### 5.1 Acquire

```
deadline = now + config.mutex.acquireTimeoutMs
loop:
  if mkdir(lockdir) succeeds:
      writeAtomic(lockdir/owner.json, {pid, host, sessionId, op, acquiredAt})
      return active
  owner = readJsonSafe(lockdir/owner.json)
  if owner is unreadable and lockdir age > mutex.staleMs:  break it
  if owner.host == thisHost and not processAlive(owner.pid): break it
  if now - owner.acquiredAt > mutex.staleMs:                break it
  if now > deadline: fail E_MUTEX_TIMEOUT (exit 20)
  sleep(backoff)   # delay *= 1.6, capped at 250ms, with jitter
```

"Break it" means: record a `lock.broken` note, remove `owner.json`, remove the
directory, then continue the loop. Breaking is never silent.

### 5.2 Release

Release removes `owner.json` and then the directory. Release MUST be registered
on `SIGINT`, `SIGTERM`, and normal exit, and MUST be idempotent. A release whose
`owner.json` no longer names this process MUST NOT remove the directory, because
another process has legitimately broken and re-acquired the lock.

### 5.3 Hold discipline

- The critical section MUST contain only: read the relevant records, decide,
  write, unlock. `config.mutex.maxHoldMs` (default 2000 ms) is a warning
  threshold; exceeding it emits a `lock.slow` note.
- No user command, network call, or interactive prompt may run inside the mutex.
  `fridge run` acquires for the claim, releases, runs the user's command
  unlocked, then re-acquires to release the claim.
- Reads never take the mutex, so `board`, `status`, and `log` can never be
  blocked by a writer.

---

## 6. Claims, scopes, and conflict

### 6.1 Path normalisation

Every input path or pattern goes through, in order:

1. Unicode NFC normalisation (`config.paths.unicodeNormalization`).
2. Backslash to forward slash conversion, so PowerShell input behaves like POSIX
   input.
3. Resolution relative to the current working directory, then relativisation to
   the workspace root.
4. Rejection with `E_PATH_INVALID` (exit 40) if the result escapes the root
   (`..` at the top), is absolute outside the root, names `.git/**` or
   `.fridge/**`, or contains a NUL byte.
5. Symlink containment: the deepest existing ancestor of the path is resolved
   with `realpath` and MUST still be inside the workspace root. Probing the
   deepest existing ancestor, rather than the path itself, catches a symlinked
   parent directory and a dangling symlink, both of which a naive check misses.
6. Case folding for comparison only when `config.paths.caseSensitivity`
   resolves to insensitive (`auto` probes the filesystem once per workspace).
7. A trailing `/` or an existing directory becomes `<dir>/**`.

### 6.2 Supported glob subset

Deliberately small, because every implementation must agree exactly:

| Token | Meaning |
| --- | --- |
| `*` | Any run of characters except `/` |
| `**` | Any run of characters including `/` |
| `?` | Exactly one character except `/` |
| `[abc]`, `[a-z]`, `[!abc]` | Character class |
| `{a,b}` | Brace alternation, expanded before matching, nesting allowed |

Not supported, and rejected with `E_PATH_INVALID`: extended globs (`+(...)`,
`!(...)`), regular expressions, and `~` expansion. Rejecting is better than
guessing.

### 6.3 Overlap decision

Two scopes A and B overlap if any include pattern of A can match any path that
any include pattern of B can match.

```
overlap(A, B):
  for pa in A.include, pb in B.include:
      if isRootGlobal(pa) or isRootGlobal(pb):     return true, "global-pattern"
      la, lb = literalPrefix(pa), literalPrefix(pb)
      if la != "" and lb != "" and (isPrefixPath(la, lb) or isPrefixPath(lb, la)):
          return true, "literal-prefix-nesting"
  if both A and B materialized without truncation:
      if setIntersect(A.materialized, B.materialized) != {}:
          return true, "materialized-intersection"
      return false
  if either pattern matches any file in the other's materialized set:
      return true, "cross-pattern-match"
  return true, "truncated-scope-fallback"  # cannot prove disjoint
```

`literalPrefix` is the portion of a pattern before its first metacharacter, cut
back to the last `/`.

**Invariant I3 (conservative overlap).** The overlap test MAY report an overlap
that does not exist. It MUST NEVER miss one. Every uncertain case, including
truncated materialisation, resolves to "overlap".

**Invariant I4 (no two held claims overlap).** For any two claims in a held
state (`active` or `handoff-offered`) owned by different sessions, their scopes
MUST NOT overlap, unless both are `mode: "shared"`.

Excludes narrow a scope for reporting, but by default they do **not** make two
otherwise-overlapping scopes disjoint (`config.paths.strictExcludes: false`).
Proving disjointness from excludes is subtle, and quietly getting it wrong
recreates the bug this protocol exists to remove.

### 6.4 Modes

| Mode | Compatible with | Use |
| --- | --- | --- |
| `exclusive` | nothing overlapping, except `advisory` | Editing files. The default |
| `shared` | other `shared` claims, and `advisory` | Reading, analysing, running tests |
| `advisory` | everything, including `exclusive` | Recording intent without reserving anything |

A `shared` claim is documentation of intent plus a way to see who is reading;
it never blocks another `shared` claim, and it does block an overlapping
`exclusive` one.

An `advisory` claim never blocks and is never blocked. It exists so a long
watcher (`tsc --watch`, a test runner, a human reading around) can be visible on
the door without reserving anything. Conformance rule: an implementation MUST
treat `advisory` as compatible with every mode in both directions, so
`compatible(a, b)` remains symmetric.

### 6.5 Acquiring a claim

```
claim(paths, mode, ttl, task):
  normalize every path                              -> E_PATH_INVALID (40)
  if any pattern is root-global and not config.paths.allowGlobalClaims
     and not --confirm-global:                      -> E_USAGE (2)
  materialize (git ls-files, else a bounded walk, limit materializeLimit)
  withMutex:
      reapStale()                                   # expiry is decided here
      conflicts = [c for c in heldClaims
                     if c.sessionId != me and overlap(c.scope, requested)
                        and not (c.mode == shared and requested.mode == shared)]
      if conflicts:
          if --queue: create queue/<id>.json naming the blocking claimId
          write note claim.denied
          -> E_CONFLICT (10), listing every blocker and the overlap reason
      if I already hold an overlapping claim with the same mode:
          merge into it, extend the lease, return that claim id
      token = 24 random bytes, base64url
      writeAtomic(claims/<id>.json, {..., tokenHash: sha256(token)})
      writeAtomic(leases/<id>.json, {expiresAt: now + ttl})
      session.tokens[<id>] = token; writeAtomic(sessions/<me>.json)
      write note claim.acquired
  render the door if config.door.autoRender
  -> exit 0
```

The conflict check MUST run **before** any self-merge. Merging first lets a
`shared` holder silently upgrade itself to `exclusive` over somebody else's
overlapping claim. That ordering bug was found by the concurrency suite in
development and is called out here so reimplementations do not repeat it.

### 6.6 Leases, heartbeats, and staleness

- `ttlMs` defaults to `config.lease.defaultTtlMs` (15 min), capped at `maxTtlMs`
  (4 h).
- `fridge heartbeat` renews every lease held by the session.
- With `config.lease.renewOnAnyCommand`, **any** command from the owning session
  renews a lease that is past `renewThresholdRatio` (default half) of its TTL.
  A working agent therefore rarely needs an explicit heartbeat.
- A claim is **expired** when `now > lease.expiresAt`.
- A claim is **stale**, and may be swept, when it is expired and either
  `now > lease.expiresAt + config.lease.graceMs`, or the owner is provably dead
  (same host, `processAlive(pid)` false).

Note on liveness: because agent CLIs spawn a new process per command, a claim's
recorded pid is normally dead by the time anybody looks. **Liveness is therefore
driven by lease expiry, not by pid checks.** The pid check only ever makes
sweeping faster; it can never keep a claim alive.

Sweeping (`reapStale`) happens under the mutex, sets `state: "expired"`, removes
the lease, writes a `claim.expired` note, and wakes queue waiters. It runs
opportunistically at the start of every mutating command, so a crashed agent's
claim is cleaned up by the next participant who needs it, with no daemon.

### 6.7 Release, handoff, revoke

- `release <claimId> --outcome done|partial|abandoned [--note ...]` requires the
  session token. Wrong session: `E_NOT_OWNER` (12). Already expired:
  `E_LEASE_EXPIRED` (13).
- `handoff <claimId> --to <actor>` writes `inbox/<toSlug>/<msgId>.json`, sets
  the claim to `handoff-offered`, and keeps ownership with the offerer. The
  offer carries its own TTL.
- `accept <msgId>` transfers `actorId`, `sessionId`, and mints a **new** token
  under the mutex. The old token is invalidated by the rewrite. `decline`
  returns the claim to `active` with the original owner.
- `release --force` and `reap --force` are operator actions, recorded as
  `revoked` with an attributed note. There is no silent seizure.

### 6.8 Crash recovery

| What crashed | Recovery |
| --- | --- |
| Between `open(tmp)` and `rename` | Orphan in `tmp/`, never a record. `doctor --fix` removes it |
| Holding the mutex | Next acquirer sees a dead pid on the same host, or a stale age, breaks it with a note |
| Holding claims | Leases expire, next mutating command sweeps them |
| Mid-handoff | Message stays `offered`; the claim stays owned by the offerer, so nothing is unowned |
| Corrupt record | Quarantined by `doctor --fix`, never deleted |
| Whole `.fridge/` lost | `notes/` and `actors/` are in Git; live claims are local state and are gone, which is correct |

---

## 7. Generated views

**Invariant I5 (views are derived).** A generated view is never an input.
Deleting every view MUST NOT lose information.

`.fridge/DOOR.md` begins with:

```
<!-- GENERATED by agent-fridge 0.1.0 at 2026-08-19T13:28:12.027Z state:e04e5bcf0f1a
     Source of truth: .fridge/. DO NOT EDIT. Regenerate with: fridge render -->
```

`state:<hash>` is a hash of the *records the view was built from*, excluding
wall-clock values such as "in 14m 59s". Drift detection compares that hash and
not the rendered text; comparing text would report drift on every clock tick.

A view is rewritten by whole-file `writeAtomic`, so concurrent renders produce
one complete version, never an interleaved one, and a lost render is only ever
one `fridge render` away from correct.

**Invariant I5b (views are derived eagerly).** Every command that mutates
records MUST regenerate the configured views before it returns, including the
commands that fail. A denied `claim` still writes a `claim.denied` note, and
`join` still writes a session and a `session.started` note, so both MUST render
even though one of them exits non-zero.

This is not cosmetic. `fridge doctor --check` reports `E_DRIFT` when the view's
`state:` hash does not match the records, which is the signal that a human hand
edited a generated file. If ordinary commands leave the view behind, that signal
turns into noise and stops meaning anything. The rule is easy to state and easy
to test: after any command, in any order, `fridge doctor --check` MUST exit `0`
unless a human actually edited something.

Implementations SHOULD render after the last write and before emitting output,
so a caller that reads the door immediately after a command sees the effect of
that command.

---

## 8. Exit codes

The exit-code table is normative and lives in
[exit-codes.md](exit-codes.md), generated from the reference implementation.

Rules:

1. `0` means the operation happened. Nothing else means it happened.
2. Codes are stable for the whole 0.x line: never reused, never renumbered, only
   added.
3. Every code is below 126, so it never collides with the shell's 127
   (not found) or 128+N (signal) conventions.
4. With `--json`, the same information appears on stdout as
   `{"ok":false,"error":{"code":"E_...","exit":N,"message":"...","hint":"..."}}`.

Stream discipline: machine-readable output goes to stdout, human diagnostics go
to stderr, and with `--json` stdout carries exactly one JSON document and
nothing else.

---

## 9. Actor and session resolution

Resolution order, with no guessing:

1. `--agent <name>`
2. `FRIDGE_ACTOR`
3. If the workspace has exactly one actor, that actor
4. Otherwise `E_NO_SESSION` (exit 7)

A conforming implementation MUST NOT infer identity from pid, tty, parent
process, or Git author. Silently picking an identity is how one agent ends up
holding another agent's claim.

`FRIDGE_REPO` overrides workspace discovery. Unknown `FRIDGE_*` variables that
are within edit distance 2 of a known one produce a warning on stderr, because
`FRIDGE_AGENT` is an easy and otherwise silent mistake.

---

## 10. Interoperability profile

A second implementation is conforming if it:

1. Reads and writes exactly the schemas in Section 3, preserving unknown fields
   on rewrite.
2. Uses only the primitives in Section 4 and the mutex in Section 5.
3. Implements the overlap rules in Section 6.3 with invariants I3 and I4.
4. Uses the exit codes in Section 8 with the same meanings.
5. Refuses to operate on a `.fridge/VERSION` whose major or minor differs, with
   `E_PROTOCOL_VERSION` (exit 4), rather than guessing.
6. Never writes metaphor vocabulary into a field name.
7. Passes the conformance vectors in `vectors/` (path normalisation and
   overlap decisions, expressed as language-neutral JSON).

Version negotiation: `wcp/0.1` files are read only by `0.1` implementations.
A future `0.2` implementation MUST offer an explicit migration and MUST NOT
silently upgrade a `0.1` directory in place.

---

## 11. Concurrency invariants (testable)

| ID | Invariant |
| --- | --- |
| I1 | Every record file has exactly one writer identity |
| I2 | Notes are write-once and never modified |
| I3 | Overlap detection may over-report, never under-report |
| I4 | No two held claims from different sessions overlap, unless both are shared |
| I5 | Generated views are derived; deleting them loses nothing |
| I5b | Every mutating command regenerates views before returning, including on failure |
| I6 | Exactly one process may hold the registry mutex at a time |
| I7 | A lock held by a dead process on the same host is broken, not waited on |
| I8 | An expired claim is swept by the next mutating command, without a daemon |
| I9 | A claim is never unowned during a handoff |
| I10 | Any partially written file is invisible as a record |
| I11 | Every refusal has a documented exit code and a note on the wall |
| I12 | Reads are never blocked by writes |

---

## 12. Security and trust boundaries

### 12.1 What this is not

WCP is **not** a security boundary, an access-control system, or a defence
against a malicious participant. Every participant already has write access to
the repository; a hostile process can simply edit files and ignore the protocol
entirely. Anyone who needs enforcement needs filesystem permissions, a
pre-receive hook, or a sandbox, not this.

What it does defend against is **accident**: the ordinary, extremely common case
of two cooperating agents stepping on each other.

### 12.2 Ownership tokens

Tokens are 24 random bytes from a CSPRNG, base64url-encoded. Only `sha256(token)`
is stored on the claim. The plaintext lives in the owner's session file. This
prevents a *cooperating* participant from accidentally releasing somebody else's
claim by id. It does not, and is not meant to, stop a determined local attacker.

### 12.3 Path safety

Claim patterns are data, never shell input. A conforming implementation MUST NOT
pass user paths to a shell, MUST reject traversal and root escape
(`E_PATH_INVALID`), MUST refuse to claim `.git/**` or `.fridge/**`, and MUST
perform the symlink containment check of Section 6.1 step 5. `fridge run`
executes with `shell: false` and an argv array.

### 12.4 What is recorded

Recorded: actor name, vendor label, local account name, pid, salted hostname
hash, timestamps, claimed paths, free-text task and note strings.

Not recorded, ever: environment variables, command output, file contents, raw
hostnames, IP addresses, absolute paths outside the workspace, and any telemetry
whatsoever. There is no network code in a conforming implementation.

`pin` and `--task` run a heuristic secret scan (high-entropy strings, common key
prefixes) and refuse with `E_USAGE` unless `--allow-secret-like` is passed. It is
a courtesy, not a guarantee, and it is documented as such.

### 12.5 Shared and networked filesystems

On NFS, SMB, or a cloud-synced folder, `mkdir` atomicity and mtime resolution
are weaker than assumed here. `fridge doctor` warns when the workspace lives
under Dropbox, OneDrive, iCloud Drive, or Google Drive. Claims from another host
cannot have their liveness verified, so operating on them requires
`--allow-multihost` and otherwise fails with `E_FOREIGN_HOST` (exit 41).

---

## 13. Non-goals for v0.1

Networked coordination, a daemon, task assignment or scheduling, merge-conflict
resolution, a web UI, mandatory hooks, telemetry, model integration, and
enforcement. WCP coordinates who is working where. It does not do the work, and
it is not a replacement for Git.
