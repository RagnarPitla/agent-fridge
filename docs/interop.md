# Interop

Where FridgeBoard runs, how to set up each environment, and where it is honestly degraded.

The integration surface is one process that exits with a number, so most of this
page is short. The interesting parts are the per-shell identity setup and the
two degraded cases at the end.

Two rules carry across every environment on this page:

1. **Each participant needs its own `FRIDGE_ACTOR`,** or must pass `--agent` on
   every command. FridgeBoard never guesses.
2. **State is per checkout.** `.fridge/` lives beside `.git/`, so it travels
   with the repository into a container, an SSH session, or a CI runner without
   any extra configuration.

---

## Terminal multiplexers

### tmux: one pane per agent

The classic setup. Each pane runs one agent with one identity.

Copy-pasteable, creates a window with three panes and sets `FRIDGE_ACTOR` in
each:

```bash
#!/usr/bin/env bash
# fridge-tmux.sh - one pane per agent, each with its own identity.
set -euo pipefail

REPO="${1:-$PWD}"
SESSION="fridge"

cd "$REPO"
[ -d .fridge ] || fridge init

for a in claude copilot codex; do
  fridge join --agent "$a" --vendor "$a" >/dev/null
done

tmux new-session  -d -s "$SESSION" -c "$REPO" -n agents
tmux split-window -h -t "$SESSION:agents" -c "$REPO"
tmux split-window -v -t "$SESSION:agents.1" -c "$REPO"

tmux send-keys -t "$SESSION:agents.0" 'export FRIDGE_ACTOR=claude'  C-m
tmux send-keys -t "$SESSION:agents.1" 'export FRIDGE_ACTOR=copilot' C-m
tmux send-keys -t "$SESSION:agents.2" 'export FRIDGE_ACTOR=codex'   C-m

tmux send-keys -t "$SESSION:agents.0" 'clear; fridge whoami' C-m
tmux send-keys -t "$SESSION:agents.1" 'clear; fridge whoami' C-m
tmux send-keys -t "$SESSION:agents.2" 'clear; fridge whoami' C-m

tmux new-window -t "$SESSION" -n door -c "$REPO"
tmux send-keys -t "$SESSION:door" 'fridge status --watch --interval 2000' C-m

tmux select-window -t "$SESSION:agents"
tmux attach -t "$SESSION"
```

```bash
chmod +x fridge-tmux.sh
./fridge-tmux.sh /path/to/repo
```

The fourth window is a live board. `fridge status --watch` reprints on an
interval; `--interval` is milliseconds and the floor is 250.

To set the identity for a pane you did not create with the script:

```bash
tmux send-keys -t fridge:agents.2 'export FRIDGE_ACTOR=codex' C-m
```

If you would rather not rely on the environment, drop `FRIDGE_ACTOR` and pass
`--agent codex` on every command. Both are supported, and `--agent` wins when
both are present.

### Zellij

Same idea. In a layout file:

```kdl
layout {
    tab name="agents" {
        pane split_direction="vertical" {
            pane {
                command "bash"
                args "-lc" "export FRIDGE_ACTOR=claude; exec bash"
            }
            pane {
                command "bash"
                args "-lc" "export FRIDGE_ACTOR=copilot; exec bash"
            }
        }
    }
    tab name="door" {
        pane {
            command "fridge"
            args "status" "--watch"
        }
    }
}
```

```bash
zellij --layout ./fridge.kdl
```

### GNU screen

```bash
screen -dmS fridge -t claude
screen -S fridge -p claude -X stuff 'export FRIDGE_ACTOR=claude\n'
screen -S fridge -X screen -t copilot
screen -S fridge -p copilot -X stuff 'export FRIDGE_ACTOR=copilot\n'
screen -r fridge
```

### Herdr-style orchestrators

Any orchestrator that spawns one agent per pane, tab, or process works the same
way, because FridgeBoard's only requirement is that each spawned agent has a
distinct identity. Two options:

- **Per-process environment.** Set `FRIDGE_ACTOR` in the environment the
  orchestrator gives each agent. This is the cleanest, because the agent's own
  instruction block then works unmodified.
- **Explicit flag.** Have the orchestrator inject `--agent <name>` into the
  commands it generates.

If the orchestrator names its workers, reuse those names. Names are free-text
and are the only identity the protocol has:

```bash
fridge join --agent "worker-3" --vendor other
```

An orchestrator can read the board without joining, because `board`, `status`,
`render`, `log`, and `version` do not need a session:

```bash
fridge status --json
fridge log --limit 50 --json
```

Both print one JSON envelope on stdout and nothing else, which is what you want
in a supervisor loop.

### Plain terminals

Nothing special is required. bash, zsh, fish, Nushell, Alacritty, iTerm2,
Windows Terminal, Ghostty: all fine. Output is pure ASCII with no ANSI escape
sequences, so it survives being piped, logged, or read by a screen reader.

fish uses different syntax for the environment variable:

```fish
set -x FRIDGE_ACTOR claude
```

### VS Code integrated terminal

Works with no configuration. Each integrated terminal is its own shell, so
export a different `FRIDGE_ACTOR` in each.

To make VS Code do it for you, in `.vscode/settings.json`:

```json
{
  "terminal.integrated.profiles.osx": {
    "claude": {
      "path": "/bin/zsh",
      "args": ["-l"],
      "env": { "FRIDGE_ACTOR": "claude" }
    },
    "copilot": {
      "path": "/bin/zsh",
      "args": ["-l"],
      "env": { "FRIDGE_ACTOR": "copilot" }
    }
  }
}
```

Use `terminal.integrated.profiles.linux` or `.windows` as appropriate. Then pick
the profile from the terminal dropdown.

---

## Windows

FridgeBoard is tested on Windows in CI. The two things that make it behave there
are deliberate: the registry mutex is built on `mkdir`, which is atomic on
Windows as well as POSIX, and **all output is pure ASCII with no ANSI colour**,
so a PowerShell 5.1 transcript or a CI log stays readable.

`--no-color` is accepted and documented as a no-op. There was never any colour
to turn off.

### PowerShell 5.1 and PowerShell 7

Set the identity:

```powershell
$env:FRIDGE_ACTOR = "claude"
```

Make it stick for future sessions:

```powershell
[Environment]::SetEnvironmentVariable("FRIDGE_ACTOR", "claude", "User")
```

Read it back:

```powershell
$env:FRIDGE_ACTOR
```

PowerShell does not set `$?` to a numeric exit code, so **branch on
`$LASTEXITCODE`**, not on `$?`. The switch pattern:

```powershell
fridge claim "src/api/**" --task "refactor the router"
switch ($LASTEXITCODE) {
  0  { "the chore is yours" }
  10 { "somebody else has it; pick another chore" }
  7  { "run: fridge join --agent <name>" }
  3  { "run: fridge init" }
  default { throw "fridge failed with $LASTEXITCODE" }
}
```

Capture the exit code immediately. Any command in between, including `Write-Host`,
overwrites `$LASTEXITCODE`:

```powershell
fridge check src/api/routes.ts
$code = $LASTEXITCODE          # capture first
if ($code -eq 10) { Write-Host "theirs" }
```

`--json` plus `ConvertFrom-Json` is the robust option when you want data:

```powershell
$result = fridge status --json | ConvertFrom-Json
$result.data.claims | ForEach-Object { "$($_.actorName) holds $($_.include -join ', ')" }
```

To make a whole script stop on the first failure:

```powershell
$ErrorActionPreference = "Stop"
fridge claim "src/api/**" --task "codemod"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
```

`fridge run` is often simpler, because it propagates the child's exit code:

```powershell
fridge run --claim "src/api/**" --task "tests" -- npm test
exit $LASTEXITCODE
```

### cmd.exe

```
set FRIDGE_ACTOR=claude
fridge claim "src/api/**" --task "refactor"
if errorlevel 11 goto other_error
if errorlevel 10 goto conflict
if errorlevel 1  goto other_error
echo the chore is yours
```

`errorlevel N` in `cmd.exe` means "N or greater", so test descending, highest
first. Quote glob patterns so `cmd.exe` does not expand them.

For a persistent variable:

```
setx FRIDGE_ACTOR claude
```

`setx` affects new shells only, not the current one.

### Git Bash and MSYS2

Behaves like any other bash. One thing to watch: MSYS path conversion can turn a
glob argument into a Windows path. Quote patterns, and if a pattern still gets
mangled, disable conversion for the call:

```bash
export FRIDGE_ACTOR=claude
MSYS_NO_PATHCONV=1 fridge claim "src/api/**" --task "refactor"
```

Backslash separators in claim paths are normalised to forward slashes
internally, so `src\api\**` and `src/api/**` mean the same thing. UNC paths
(`\\server\share\...`) are rejected with `E_PATH_INVALID`, exit 40.

### WSL 1 and WSL 2

Supported. **Keep the checkout on the Linux filesystem** (`~/code/repo`), not on
the Windows drive mount (`/mnt/c/...`).

The reason is not performance, although that matters too. The `9p` and `drvfs`
translation layers behind `/mnt/c` have weaker atomicity and coarser timestamp
resolution than the protocol assumes, which is the same class of problem as a
network filesystem. Both `mkdir` mutex acquisition and lease expiry depend on
those being sharp.

```bash
# good
cd ~/code/myrepo && fridge init

# avoid
cd /mnt/c/Users/me/code/myrepo && fridge init
```

A repository on the Linux side is still reachable from Windows tools through
`\\wsl$\`, so you rarely need the other layout.

Do not run agents in Windows and in WSL against the same checkout at the same
time. Those are effectively two hosts sharing one filesystem, which is the
degraded case below.

---

## Containers, remote, and CI

### Devcontainers

`.fridge/` lives inside the repository, so it is present in the container
automatically. Add the identity to `devcontainer.json`:

```json
{
  "name": "myrepo",
  "image": "mcr.microsoft.com/devcontainers/javascript-node:20",
  "containerEnv": {
    "FRIDGE_ACTOR": "devcontainer"
  },
  "postCreateCommand": "npm install -g fridgeboard && fridge join --agent devcontainer --vendor other"
}
```

Note that a devcontainer is a different host from your laptop, even though it
shares the files through a bind mount. If you run agents on the host **and**
inside the container against the same checkout, read the multi-host section
below. Pick one side and stay there if you can.

### SSH and remote development

Nothing special. The state is in the checkout on the remote machine, so
everything works exactly as it does locally. Export `FRIDGE_ACTOR` in the remote
shell, or per-host in `~/.ssh/config` with `SendEnv` if your server has
`AcceptEnv` configured.

If several people SSH into one box and share one checkout, that is the intended
case and it works well: same host, same filesystem, different actor names.

### CI

The useful CI checks are the drift checks. They write nothing and exit `30` when
something is out of date:

```yaml
name: fridgeboard
on: [push, pull_request]

jobs:
  coordination:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - run: npm install -g fridgeboard
      - name: Instruction blocks are current
        run: fridge adapters check
      - name: Generated views are current
        run: fridge render --check
      - name: Workspace is healthy
        run: fridge doctor --check
```

On Windows runners the same checks work; branch with `$LASTEXITCODE` if you need
to treat exit `30` as a warning rather than a failure:

```yaml
  coordination-windows:
    runs-on: windows-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - run: npm install -g fridgeboard
      - shell: pwsh
        run: |
          fridge adapters check
          if ($LASTEXITCODE -eq 30) {
            Write-Host "::warning::instruction blocks are stale; run fridge adapters install"
          } elseif ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
          }
```

Claims are not useful in CI. A CI job is a fresh checkout with nobody else in
it, so there is nothing to coordinate with; `claims/` is git-ignored and will
always be empty. If a job does want to be explicit, `fridge run` is the tidy
form because it always releases:

```bash
fridge join --agent "ci-$GITHUB_RUN_ID" --vendor other
fridge run --claim "src/**" --task "build" -- npm run build
```

Set `FRIDGE_JSON=1` in a CI environment to get JSON from every command without
touching each call site.

---

## Degraded cases, stated plainly

Two environments do not give FridgeBoard the guarantees it needs. Both are
detected and reported rather than silently tolerated.

### Two machines sharing one checkout over NFS or SMB

**Status: degraded, and it tells you.**

On NFS, SMB, and similar network filesystems, `mkdir` atomicity and timestamp
resolution are weaker than the protocol assumes. Worse, a claim taken on another
host cannot have its liveness verified from here: `processAlive(pid)` is
meaningless across machines, so the only signal left is the lease.

What that means in practice:

- Lease expiry still works, because it is wall-clock arithmetic on a timestamp
  in a file. This is why leases, not pid checks, are the primary liveness
  mechanism ([./concepts.md](./concepts.md)).
- The one-minute grace period cannot be skipped for a dead owner, so a crashed
  remote agent's card takes slightly longer to sweep.
- The registry mutex is weaker. If your NFS mount does not implement atomic
  `mkdir`, two processes can in principle both believe they hold it.

`fridge doctor` reports every foreign-host claim as an informational finding:

```
INFO   Card clm_01M0D3Z1WDG81DRPWQDTVCQMTG was taken on another machine; liveness cannot be checked here.
```

The protocol reserves exit `41` (`E_FOREIGN_HOST`) and an `--allow-multihost`
override for operating on another host's claim
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#125-shared-and-networked-filesystems)).
As of 0.1.0 that refusal is specified but not yet enforced by the CLI: foreign
claims are reported by `doctor` and are otherwise treated like local ones. Do
not rely on exit `41` to stop you.

**The recommendation:** do not do this. Multi-machine coordination is an
explicit non-goal for v0.1
([../spec/protocol-v0.1.md](../spec/protocol-v0.1.md#13-non-goals-for-v01)).
Give each machine its own clone and coordinate through Git, or run all the
agents on the machine that owns the filesystem, over SSH.

### A repository inside Dropbox, OneDrive, iCloud Drive, or Google Drive

**Status: degraded, and it tells you.**

Cloud sync clients do not preserve the properties the protocol relies on. They
delay writes, re-order them, resurrect deleted files, create conflicted copies
with names like `config (conflicted copy).json`, and in some cases replace a
file after an atomic rename has already completed. A lease file that arrives
three minutes late describes ownership that no longer exists.

`fridge doctor` warns when the checkout path contains a known sync folder:

```
WARN   This repository lives under Dropbox. File sync can duplicate or delay .fridge/ writes; coordination guarantees are weaker.
```

The checked names are Dropbox, OneDrive, Google Drive, iCloud Drive, and
Creative Cloud Files. The check is a path-substring heuristic, so it will miss a
sync folder you renamed. It is a hint, not a guarantee.

**The recommendation:** move the checkout out of the synced folder. Use Git for
the sync; that is what Git is for. If you genuinely cannot move it, exclude
`.fridge/` from sync in the client's settings, accept that history in `notes/`
is then local-only, and treat coordination as best-effort.

If a conflicted copy does appear inside `.fridge/`, `fridge doctor --fix` moves
unparseable records to `.fridge/quarantine/` rather than deleting them, so
nothing is lost silently.

---

## Quick reference

| Environment | Status | The one thing to know |
| --- | --- | --- |
| bash, zsh, fish | Supported | `export FRIDGE_ACTOR=<name>` per terminal |
| tmux, Zellij, screen | Supported | One pane per agent; set the variable per pane |
| Herdr-style orchestrators | Supported | Per-process env, or inject `--agent` |
| VS Code integrated terminal | Supported | Terminal profiles can set the variable for you |
| PowerShell 5.1 and 7 | Supported | Branch on `$LASTEXITCODE`, not `$?` |
| cmd.exe | Supported | `if errorlevel N` tests "N or greater"; test descending |
| Git Bash, MSYS2 | Supported | Quote globs; `MSYS_NO_PATHCONV=1` if a path is mangled |
| WSL 1 and WSL 2 | Supported | Keep the checkout on the Linux filesystem |
| Devcontainers | Supported | `containerEnv`; the container is a different host |
| SSH and remote | Supported | State travels with the checkout |
| CI | Supported | Use the `--check` drift commands; claims are pointless there |
| macOS, Linux, Windows | Supported | ASCII-only output, no ANSI colour, ever |
| NFS/SMB shared checkout | Degraded, reported | Liveness across hosts cannot be verified |
| Dropbox/OneDrive/iCloud | Degraded, reported | `fridge doctor` warns; move the checkout |

---

## Next

- [./quickstart.md](./quickstart.md) - the two-terminal walkthrough.
- [./adapters.md](./adapters.md) - instruction blocks and optional hooks.
- [./concepts.md](./concepts.md) - why liveness is lease-driven.
- [./comparison.md](./comparison.md) - when separate checkouts are better.
- [../spec/exit-codes.md](../spec/exit-codes.md) - the exit-code contract.
