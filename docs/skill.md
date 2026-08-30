# The agent skill

[← back to docs index](README.md)

proximo answers a developer who reads ([these guides](README.md)) and a developer
who types (`proximo doctor`, `proximo errors`). The **Skill** is the third
audience: the coding agent the developer asks instead. It is a folder with a
`SKILL.md` — the format Claude Code defines and Codex reads unchanged — holding
the procedural knowledge prose is the wrong shape for: which question to ask
first, and which commands never to run.

It is **compiled into the CLI**, so the Skill an agent reads is always the one
that matches the binary on the machine. Why that, and not a marketplace pin:
[ADR 0005](adr/0005-the-agent-skill-ships-in-the-cli.md).

## Install it

```sh
proximo skill install
```

With no flags this writes the Skill for **every agent detected on the host**, at
**project scope** — into the repository you are standing in, where the whole
team gets the same answer. It prints the plan before writing anything:

```
Skill: proximo 0.5.0

  claude  project  .claude/skills/proximo  install
  codex   project  .codex/skills/proximo   up to date

Done. Restart your agent session for the change to take effect.
These are tracked files: review and commit the diff under .claude/skills/proximo.
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--agent` | every agent detected | `claude`, `codex`, a comma-separated list, or `all` (both, detected or not) |
| `--scope` | `project` | `project` — this repository; `global` — `~/.claude` and `$CODEX_HOME` (default `~/.codex`) |
| `--dry-run` | off | Print the plan and stop |
| `--force` | off | Overwrite a copy that was edited after proximo wrote it |

Outside a git repository, `--scope project` is an **error naming
`--scope global`** — never a silent write somewhere else.

`proximo skill uninstall` takes the same flags and removes what proximo wrote.

## Managed and unmanaged copies

A copy proximo wrote carries a `.proximo-skill.json` beside its `SKILL.md`,
recording the version that wrote it and a digest of what it wrote. That file is
what makes a copy **Managed**:

- **Managed and untouched** — `install`, `up` and `update` bring it level with
  the binary automatically, and say so. Restart the agent session afterwards.
- **Managed and edited** — the digest no longer matches, so proximo **skips and
  reports it, never overwrites it**. At project scope that content belongs to a
  team. `proximo skill install --force` is the only thing that replaces it.
- **Unmanaged** — no side file, so the copy came from somewhere proximo does not
  control (a marketplace, or an agent's own skill installer). proximo neither
  updates nor removes it. The Skill's own first step reads the same file, and
  says it cannot know its version rather than guessing.

Auto-update reaches global copies always, and a project copy only while proximo
is being run inside that repository. A repository left alone holds a stale copy
until someone returns to it — which is what the `agent-skill` Check reports:
[The agent skill is out of date](troubleshooting.md#the-agent-skill-is-out-of-date).

## What the Skill knows

One entry point that triages, and one reference loaded once the answer is
narrowed — because "the page is blank" may be a 502, a container that never
became healthy, or a `TypeError` in a bundle, and the choice cannot be made
before the answer is known.

| The agent sees | It reads |
| --- | --- |
| No route for the container, or one flagged with a warning | how to expose it: the label contract |
| A route that does not answer, 502s, or a distrusted certificate | how to locate the failure between browser and container |
| A route that answers, and a page that is wrong | what the browser reported, through Inspection |

Two rules run through every branch, and they are the reason the Skill exists at
all rather than the docs being enough:

- **A Remedy that changes the host is the developer's to run.** proximo's
  contract is that every mutation of a machine stays a command its owner typed,
  so an agent hands over anything that installs, trusts, or asks for a password.
- **`proximo errors dom` prints a path, never the page.** A DOM Snapshot is
  hundreds of kilobytes; it is there to be grepped, not read into a context
  window.

## Without the binary

The Skill's source is [`skills/proximo/`](https://github.com/filippolmt/proximo/tree/main/skills/proximo)
— a public, stable path, so it can also be installed straight from the
repository by an agent's own installer, or referenced as a pinned entry in a
skills marketplace. Copies installed that way are unmanaged: nothing keeps them
level with the binary, which is the trade for not needing one.
