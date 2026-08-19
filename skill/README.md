# The bundled Agent Skill

[`SKILL.md`](SKILL.md) is the vendor-neutral, open Agent Skill that ships in the
box with the `fridge` binary. Apache-2.0, no vendor's proprietary format, no
account, no registry.

It is layer 2 of the [package](../README.md#what-ships-and-why-it-is-shaped-this-way),
and it holds no behaviour that is not already in the protocol. A skill that knows
something the protocol does not would be a second protocol, and two protocols is
the problem this project exists to remove.

---

## Installing it

### Any agent that reads a Markdown skill file

Copy or symlink the directory. Most runtimes look for `SKILL.md` in a skills
folder:

```sh
mkdir -p ~/.config/agent-skills/agent-fridge
cp "$(fridge version --json | grep -o '"skillPath":"[^"]*"' | cut -d'"' -f4)/SKILL.md" \
   ~/.config/agent-skills/agent-fridge/
```

Or simply, from a checkout:

```sh
cp -r skill/ ~/.config/agent-skills/agent-fridge/
```

The front matter carries `name`, `version`, `protocol`, `license`, `homepage`,
`description`, and `keywords`, which is the common subset every skill format
understands.

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
