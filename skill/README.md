# The bundled Agent Skill

[`SKILL.md`](SKILL.md) is the vendor-neutral, open Agent Skill published with
every Agent Fridge release. Apache-2.0, no vendor's proprietary format, no
account, no registry. It is a separate, checksummed release asset rather than
an opaque file hidden inside the `fridge` binary.

It is layer 2 of the [package](../README.md#what-ships-and-why-it-is-shaped-this-way),
and it holds no behaviour that is not already in the protocol. A skill that knows
something the protocol does not would be a second protocol, and two protocols is
the problem this project exists to remove.

---

## Installing it

### From the latest release

Pick the skills directory for the runtime you use:

| Runtime | Destination |
| --- | --- |
| GitHub Copilot CLI | `~/.copilot/skills/agent-fridge` |
| Claude Code | `~/.claude/skills/agent-fridge` |
| Codex | `~/.codex/skills/agent-fridge` |
| Generic Agent Skills directory | `~/.config/agent-skills/agent-fridge` |

macOS or Linux:

```sh
DEST="$HOME/.copilot/skills/agent-fridge" # choose a destination from the table
mkdir -p "$DEST"
curl -fsSL https://github.com/RagnarPitla/agent-fridge/releases/latest/download/SKILL.md \
  -o "$DEST/SKILL.md"
curl -fsSL https://github.com/RagnarPitla/agent-fridge/releases/latest/download/SKILL.md.sha256 \
  -o "$DEST/SKILL.md.sha256"
(cd "$DEST" && if command -v shasum >/dev/null 2>&1; then
  shasum -a 256 -c SKILL.md.sha256
else
  sha256sum -c SKILL.md.sha256
fi)
```

Windows PowerShell:

```powershell
$dest = Join-Path $HOME '.copilot\skills\agent-fridge' # choose a destination from the table
New-Item -ItemType Directory -Force -Path $dest | Out-Null
$base = 'https://github.com/RagnarPitla/agent-fridge/releases/latest/download'
Invoke-WebRequest "$base/SKILL.md" -OutFile (Join-Path $dest 'SKILL.md')
Invoke-WebRequest "$base/SKILL.md.sha256" -OutFile (Join-Path $dest 'SKILL.md.sha256')
$want = ((Get-Content (Join-Path $dest 'SKILL.md.sha256') -Raw) -split '\s+')[0]
$got = (Get-FileHash -Algorithm SHA256 (Join-Path $dest 'SKILL.md')).Hash.ToLower()
if ($want.ToLower() -ne $got) { throw 'SKILL.md checksum mismatch' }
```

Or copy it from a checkout:

```sh
cp -r skill/ ~/.config/agent-skills/agent-fridge/
```

The front matter contains exactly `name` and `description`, the portable subset
used by strict Agent Skills loaders. Release, protocol, license, and homepage
metadata remain visible in the skill body.

### Agents that read repository instructions instead

Most coding agents do not load external skills at all. They read a file in the
repository. That is what the adapters are for, and it is the recommended path
because it works everywhere:

```sh
fridge adapters install
```

This splices one canonical, marker-delimited, content-hashed block into every
vendor instruction file present: `AGENTS.md`, `CLAUDE.md`,
`.github/copilot-instructions.md`, the Codex instruction file, and a generic
snippet. Your own text above and below the block survives, and
`fridge adapters check` exits `30` if a block has drifted.

See [../docs/adapters.md](../docs/adapters.md).

---

## Which one do I need?

| Situation | Use |
| --- | --- |
| Agent reads repository instructions (Claude Code, Copilot CLI, Codex, most others) | `fridge adapters install` |
| Agent supports external skills and you want it available in every repository | Copy `skill/` into your skills directory |
| You want both | Do both. They say the same thing, generated from the same source |
| Agent supports neither | Nothing to install. Tell it the five commands, or let it read `fridge --help` |

---

## Keeping it honest

The skill, the adapter blocks, and the protocol must agree. They are checked:

- `npm run lint` enforces ASCII across the skill file, so it renders in every
  terminal and every instruction parser.
- The adapter blocks are generated from one canonical text, hashed, and
  drift-checked by `fridge adapters check`.
- Anything the skill claims about exit codes is verified against
  [`../spec/exit-codes.md`](../spec/exit-codes.md), which is itself generated
  from the implementation and checked by `npm run gen:check`.

If you find the skill telling an agent something the CLI does not do, that is a
bug worth reporting, not a documentation nit.
