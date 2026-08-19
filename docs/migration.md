# Migration

Moving off the `To-do.done.md` plus `shared-development-updates.md` pattern without losing the history you already have.

If two or more agents in your repository coordinate by reading and rewriting
shared Markdown files, you have the exact setup that produced this project's
origin incident. This page moves you off it in three days, imports the old
entries as immutable notes, and shows how to verify that nothing was lost.

Read [./concepts.md](./concepts.md) for why the new shape is safe. Read
[../README.md](../README.md) for the incident itself.

---

## The pattern you are on

Two files, both read and rewritten by everybody:

`To-do.done.md` - durable history, appended to as work completes:

```markdown
# Done

## 2026-08-14

- claude: split the router into three files
- copilot: added the missing error path in server.ts

## 2026-08-15

- claude: rewrote the retry loop
```

`shared-development-updates.md` - live ownership, rewritten constantly:

```markdown
# Live updates

- claude is holding src/api until 6pm
- copilot is on src/ui
```

Both files work fine with one writer. With two concurrent writers they are a
read-modify-write race:

```
agent A reads updates.md      (200 lines)
agent B reads updates.md      (200 lines)
agent A appends and writes    (215 lines)
agent B appends and writes    (212 lines)   <- A's 15 lines are gone
```

That is how about 128 lines disappeared. Nobody was careless.

Agent Fridge splits the two files by what they actually are:

| Old file | What it was really for | New home |
| --- | --- | --- |
| `To-do.done.md` | durable, attributed history | `.fridge/notes/`, one write-once file per note, committed to Git |
| `shared-development-updates.md` | live ownership, right now | `.fridge/claims/` with leases, git-ignored, rendered into `.fridge/DOOR.md` |

The live half stops being a document you edit and becomes state you query. The
history half stops being one file and becomes many files that nobody can
overwrite.

---

## What `fridge migrate` does

**Read the flags carefully.** The command takes `--todo-done` and `--updates`,
not `--from`:

```bash
fridge migrate                                 # auto-detects both default filenames
fridge migrate --dry-run                       # parse and count, write nothing
fridge migrate --todo-done HISTORY.md --updates STATUS.md
fridge migrate --freeze                        # also add a header to the originals
```

Behaviour, from `src/commands/workspace.mjs`:

1. **It finds the files.** With no flags it looks for `To-do.done.md` and
   `shared-development-updates.md` in the workspace root. `--todo-done <file>`
   and `--updates <file>` override those paths. If neither file is found it
   exits `11` (`E_NOT_FOUND`) and tells you the flags.

2. **It parses deliberately dumbly.** One entry per Markdown bullet (`-` or `*`)
   or per block of prose under a heading, in file order. The nearest preceding
   heading is remembered and stored alongside the entry. There is no attempt to
   parse dates, infer status, or restructure anything. Guessing wrong about
   somebody's history is worse than importing it verbatim, so it does not guess.

3. **It writes one immutable note per entry.** Each entry becomes its own
   write-once file under `.fridge/notes/YYYY/MM/DD/`, with:
   - `type`: `legacy.todo` for entries from the to-do file, `legacy.update` for
     entries from the updates file.
   - `subject`: `{ kind: "file", id: "<the source filename>" }`.
   - `summary`: the first line of the entry, truncated to 200 characters.
   - `data.body`: the full entry text, verbatim.
   - `data.heading`: the Markdown heading the entry sat under.
   - `data.sourceFile`, `data.importedAt`, `data.importedBy`.

4. **It leaves the originals on disk.** Migration never deletes your files. With
   `--freeze` it prepends a header pointing readers at the new home; without
   `--freeze` it does not touch them at all.

### Attribution: read this before you run it

Every imported note is attributed to **the actor running the migration**, not to
the person or agent named inside the entry text. This is worth understanding
before you migrate, because it is the one thing that surprises people.

`actorName` on every imported note is you. The original name is preserved
verbatim inside `data.body` and in the `summary`, because the entry text itself
usually names its author:

```json
{
  "actorName": "claude",
  "data": {
    "body": "copilot is on src/ui",
    "heading": "Live updates",
    "importedAt": "2026-08-19T13:40:27.385Z",
    "importedBy": "claude",
    "sourceFile": "shared-development-updates.md"
  },
  "summary": "copilot is on src/ui",
  "type": "legacy.update"
}
```

That is the correct record of what happened: claude imported a line that says
copilot was on `src/ui`. It is not a claim that copilot wrote this note through
Agent Fridge.

A practical consequence: pick who runs the migration deliberately. A neutral
name reads better in `fridge log` a year from now than one of the agents' names:

```bash
fridge join --agent migration --vendor human
fridge migrate --freeze --agent migration
```

`migrate` takes `--author-map "old=new,other=also"` and re-attributes entry by
entry. An entry is credited to a mapped name only when a name can be read off
the entry itself - the `name:` prefix on an update line, or the heading it sits
under. Entries with no legible author stay credited to the agent running the
migration, so check the result with `fridge log` rather than assuming every
line moved.

---

## Before and after

### Before

```
repo/
  To-do.done.md                     <- everybody rewrites this
  shared-development-updates.md     <- everybody rewrites this
  src/
```

Coordination is prose. Ownership is a sentence somebody typed. Two agents
writing at the same instant silently lose one of the writes. Nothing is
machine-checkable.

### Migrate

```bash
fridge init
fridge join --agent migration --vendor human
export FRIDGE_ACTOR=migration
fridge migrate --dry-run
```

```
Would import 5 entr(ies) from To-do.done.md, shared-development-updates.md.
```

```bash
fridge migrate --freeze
```

```
Imported 5 entr(ies) from To-do.done.md, shared-development-updates.md.
Legacy files marked FROZEN. They are now read-only history.
Read them back with: fridge log --limit 5
```

### After

```
repo/
  To-do.done.md                     <- FROZEN header, read-only, still on disk
  shared-development-updates.md     <- FROZEN header, read-only, still on disk
  .fridge/
    notes/2026/08/19/...json        <- one file per imported entry, committed
    claims/                         <- live ownership, git-ignored
    DOOR.md                         <- generated, git-ignored
  src/
```

The history is queryable:

```bash
fridge log --limit 5 --type legacy.todo
```

```
2026-08-19T13:40:27.293Z  claude          legacy.todo         claude: split the router into three files
2026-08-19T13:40:27.314Z  claude          legacy.todo         copilot: added the missing error path in server.ts
2026-08-19T13:40:27.340Z  claude          legacy.todo         claude: rewrote the retry loop
```

And ownership is state, not prose:

```bash
fridge board
```

```
# The door

Fridge `wsp_01M0D3YS8WQ4WVQMB8CXD9QPYN` | 2 chore(s) claimed | 0 waiting

## Claimed right now

| Card | Who | Mode | Scope | Doing | Back by |
|---|---|---|---|---|---|
| `clm_01M0D3Z1WDG81DRPWQDTVCQMTG` | claude (claude) | exclusive | `src/api/**` | refactor the router | in 14m 54s |
| `clm_01M0D3Z6X1RRCD0JKYDHF17CGE` | copilot (copilot) | exclusive | `src/ui/**` | restyle the header | in 14m 59s |
```

---

## A three-day rollout

Do not do all of this at once. Each day is independently useful and
independently revertible.

### Day 1: install and migrate

Nothing changes for the agents yet. You are only making the history safe.

```bash
cd /path/to/repo
curl -fsSL https://github.com/RagnarPitla/agent-fridge/releases/latest/download/install.sh | sh

fridge init --no-adapters
fridge join --agent migration --vendor human
export FRIDGE_ACTOR=migration

fridge migrate --dry-run                # check the count looks right
fridge migrate                          # import, do not freeze yet

git add .fridge .gitattributes
git commit -m "Import shared Markdown history into Agent Fridge notes"
```

`--no-adapters` keeps the agent instruction files untouched for now, so nothing
about agent behaviour changes today. The old files are still writable and the
agents keep using them.

Verify before you go home; the verification section is below.

### Day 2: adapters, and the humans start claiming

Now teach the agents. Install the instruction blocks and have each agent join:

```bash
fridge adapters install
git add AGENTS.md CLAUDE.md .github/copilot-instructions.md \
        .codex/instructions.md .cursor/rules/agent-fridge.mdc \
        docs/AGENT-COORDINATION.md
git commit -m "Add Agent Fridge coordination rules for agents"
```

Restart every agent session, because instruction files are read at session
start. Then, in each terminal:

```bash
fridge join --agent claude --vendor claude
export FRIDGE_ACTOR=claude
```

Run both systems in parallel for a day. The agents claim before editing and pin
notes, and they may still write to the old files. Watch `fridge board` and
`fridge log --follow`. What you are looking for is whether the claims that
appear match the work you expect, and whether the scopes are narrow enough to
avoid constant exit `10`.

If claims are colliding all day, the scopes are too wide, not the tool. See the
"what to do when you see exit 10" list in [./quickstart.md](./quickstart.md).

### Day 3: freeze the old files, delete the ritual

When the board has been accurate for a day, stop the old files from being
written:

```bash
export FRIDGE_ACTOR=migration
fridge migrate --freeze                 # re-imports and adds the header
```

Re-running `migrate` re-imports the entries, which produces duplicate notes for
anything already imported on day 1. Two ways to avoid that:

```bash
# Option A: only add the header, skip the second import.
fridge migrate --freeze --todo-done To-do.done.md --updates shared-development-updates.md --dry-run
# then add the header by hand (see the template below)

# Option B: accept the duplicates. Notes are cheap, write-once, and
# `fridge log --type legacy.todo` still reads correctly.
```

Option A is tidier for a repository you care about. The header `--freeze` writes
looks like this, and you can paste it yourself:

```markdown
<!-- FROZEN by fridge migrate.
     Imported into .fridge/notes/ on 2026-08-19T13:40:27.358Z by migration.
     Do not edit this file. Pin notes with: fridge pin "..."
     Read history with: fridge log -->
```

Then remove the coordination ritual from every instruction file. Search your
repository for the old filenames and delete the rules that tell agents to write
them:

```bash
grep -rn "shared-development-updates\|To-do.done" \
  --include=*.md --include=*.mdc --include=*.json . | grep -v '^./.fridge/'
```

Anything that says "append your status to `shared-development-updates.md`" must
go, or agents will keep doing it. The Agent Fridge block already says to use
`fridge pin` instead:

> 4. Report progress with `fridge pin`, not by editing a shared Markdown file.

Finally, commit:

```bash
git add -A
git commit -m "Freeze legacy coordination files; Agent Fridge is the board"
```

---

## What to do with the old files

**Keep them.** Do not delete them, at least not in the same change.

The recommended end state is read-only with a pointer header:

1. **Add the FROZEN header** (above), so any human or agent opening the file
   sees immediately that it is history.
2. **Leave them in Git.** They are small, and `git log --follow` on them still
   answers questions the notes cannot, such as who edited what and when, at the
   commit level.
3. **Optionally move them out of the way.** Once the header has been in place
   for a few weeks and nothing writes to them:

   ```bash
   mkdir -p docs/history
   git mv To-do.done.md docs/history/To-do.done.md
   git mv shared-development-updates.md docs/history/shared-development-updates.md
   git commit -m "Move frozen coordination files to docs/history/"
   ```

   Leave the headers on. A file in `docs/history/` with a FROZEN banner is
   unambiguous.

4. **Do not make them read-only with `chmod`.** It does not stop an agent, it
   does confuse Git and editors, and the header communicates the intent better.

Deleting them entirely is fine eventually, because the entries are in
`.fridge/notes/` and both are in Git history. There is just no hurry, and the
cost of keeping them is a few kilobytes.

---

## Verifying that nothing was lost

Do all four of these. They take about two minutes.

### 1. Count the entries against the source

`--dry-run` reports what it would import without writing anything:

```bash
fridge migrate --dry-run
```

```
Would import 5 entr(ies) from To-do.done.md, shared-development-updates.md.
```

Compare against a rough count of bullets and prose blocks in the sources:

```bash
grep -c '^\s*[-*]\s' To-do.done.md shared-development-updates.md
```

The numbers will not always match exactly, because a block of prose under a
heading also becomes one entry. If the migration count is **lower** than the
bullet count, something was dropped and you should investigate before
proceeding. If it is equal or slightly higher, that is expected.

### 2. Count the notes on disk

Every imported entry is one file:

```bash
find .fridge/notes -name '*.json' | wc -l
```

```bash
fridge log --limit 500 --type legacy.todo   | wc -l
fridge log --limit 500 --type legacy.update | wc -l
```

The two `fridge log` numbers should add up to the number `migrate` reported.
Note that `.fridge/notes/` also contains notes that Agent Fridge itself wrote
(`workspace.initialized`, `session.started`), so the `find` count is higher.

### 3. Spot-check the text is verbatim

Pick a distinctive line from the original and find it in the notes:

```bash
grep -rl "rewrote the retry loop" .fridge/notes/
```

Then read the whole record and confirm `data.body` matches the source line
exactly, including any trailing detail that the 200-character `summary` cut off:

```bash
cat "$(grep -rl 'rewrote the retry loop' .fridge/notes/ | head -1)"
```

Do this for the longest entry in each file. The `summary` field is truncated on
purpose; `data.body` is not, and that is the one that must be complete.

### 4. Confirm the originals are untouched

Migration must not have modified your source files, apart from the FROZEN header
if you used `--freeze`:

```bash
git status --porcelain To-do.done.md shared-development-updates.md
git diff To-do.done.md shared-development-updates.md
```

Without `--freeze`, both should be clean. With `--freeze`, the only diff should
be the four added header lines at the top.

### 5. Confirm the notes are actually committed

The whole point is that the history is durable. Check that the allowlist in
`.fridge/.gitignore` is letting notes through:

```bash
git add .fridge
git status --porcelain .fridge | head
```

You should see `.fridge/notes/...` and `.fridge/actors/...` staged, and you
should **not** see `.fridge/claims/`, `.fridge/leases/`, `.fridge/sessions/`,
`.fridge/locks/`, or `.fridge/DOOR.md`. Confirm the ignore rules directly:

```bash
git check-ignore -v .fridge/DOOR.md .fridge/claims .fridge/sessions .fridge/locks
```

```
.fridge/.gitignore:3:/*	.fridge/DOOR.md
.fridge/.gitignore:3:/*	.fridge/claims
.fridge/.gitignore:3:/*	.fridge/sessions
.fridge/.gitignore:3:/*	.fridge/locks
```

That is the correct split, and it is explained in
[../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#22-git-behaviour).

---

## Rolling back

If day 2 goes badly, the rollback is complete and takes one command:

```bash
git checkout -- AGENTS.md CLAUDE.md .github/copilot-instructions.md
rm -rf .fridge
```

Your original Markdown files were never modified without `--freeze`, and if you
did freeze them, the header is four lines you can delete. Nothing else in the
repository was touched. See the uninstall answer in [./faq.md](./faq.md).

---

## Migrating from something else

`fridge migrate` only knows the two-file pattern. For any other source, the
generic path is a loop that pins one note per entry. Notes are write-once, so
this is safe to run from several processes at once, and re-running it produces
duplicates rather than losing anything:

```bash
#!/usr/bin/env bash
# Import one note per line of an arbitrary log file.
set -euo pipefail
export FRIDGE_ACTOR=migration

while IFS= read -r line; do
  [ -z "$line" ] && continue
  fridge pin "$line" --kind imported >/dev/null
done < old-status.txt

fridge log --limit 20 --type note.imported
```

`--kind imported` gives the notes the type `note.imported`, so you can filter
them out later. If a line trips the credential heuristic, `fridge pin` refuses
with exit `2` rather than committing a secret to history, which is the behaviour
you want during a bulk import; fix the line or pass `--allow-secret-like`.

---

## Next

- [./quickstart.md](./quickstart.md) - what the agents do after day 2.
- [./concepts.md](./concepts.md) - why write-once notes fix the race.
- [./adapters.md](./adapters.md) - the instruction blocks installed on day 2.
- [./faq.md](./faq.md) - "should I commit `.fridge/notes/`?" (yes).
- [../README.md](../README.md) - the incident this all comes from.
