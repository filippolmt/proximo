# The agent skill ships in the CLI, and the CLI keeps it current

proximo already answers two audiences well and a third not at all. `docs/` is
written for a person who is reading; `proximo doctor` and `proximo errors` are
written for a developer who is typing. But the developer increasingly asks an
*agent* — "why is `web.test` returning 502", "what broke on this page" — and the
agent has neither. It guesses at the `proximo.*` labels, reaches for
`proximo errors` before checking that anything is under Inspection, and, handed
`proximo errors dom`, reads a several-hundred-kilobyte Snapshot into its context
window: the exact thing the Snapshot's own definition forbids, for the exact
reason it forbids it. None of that is a documentation gap. The knowledge that is
missing is procedural — an order of questions and a list of things not to do —
and prose that a person skims is the wrong shape for it.

A distribution channel for that knowledge already exists: a Claude Code skill is
a folder with a `SKILL.md`, Codex reads the identical format, and
`filippolmt/skills` already references external skills as pinned `git-subdir`
entries. What the existing channels cannot do is the part that matters here.
They install per user, so they cannot put the skill into a team's repository
where the whole team gets it; and they pin to an upstream commit, which has no
relationship to the version of proximo actually on the machine — a skill
describing labels the installed binary does not implement is worse than no skill,
because it is confidently wrong.

**Decision.** There is one Skill, its single source is `skills/proximo/`, and it
is compiled into the CLI binary. `proximo skill install` writes it, choosing
destination by `--agent claude|codex|all` and `--scope project|global` — four
destinations, defaulting to every agent detected, at project scope. The command
prints the plan before writing and lists what it wrote; `--dry-run` prints and
stops; outside a git repository, `--scope project` is an error naming
`--scope global`, never a silent fallback. `proximo skill uninstall` takes the
same flags and takes back what proximo wrote, so the command that installs is
reversible on its own terms rather than only as a side effect of uninstalling
proximo itself.

A **Managed copy** is marked by a `.proximo-skill.json` written beside the
`SKILL.md`, carrying the CLI version and a hash of the content. `install`, `up`
and `update` bring every managed copy they can see level with the binary, and the
Skill's own first step re-checks it, so a developer who upgrades through a
package manager and never runs a lifecycle command is repaired at the next
session. A copy whose hash no longer matches is **skipped and reported, never
overwritten** — with `--scope project` that content belongs to a team, not to
proximo. The single exception is `--force`, which exists because the report
would otherwise be a dead end: it is never reached by a lifecycle command, only
by a developer typing the flag the report itself names, having read which copy
it is about. No flag reaches a copy proximo did not write. A project-scope write
is announced as a tracked diff to commit, not performed quietly — by the
lifecycle commands as much as by the typed one, since the diff is the team's
either way.

**The absence of the side file is itself the answer**: a copy installed from the
marketplace, or by Codex's own `skill-installer`, has none, is therefore
unmanaged, and the Skill says so rather than claiming a version it cannot know.
The marketplace entry stays — a `git-subdir` on `skills/proximo`, which is why
that path is public and stable — as the unmanaged channel for someone who wants
the skill without the binary.

The `agent-skill` Check closes what auto-update deliberately cannot: copies
skipped for a diverging hash, and project copies in a repository proximo is not
being run from. It is **skipped, not failed, when no managed copy exists** — a
developer who uses no agent must never see a red line about one.

## Considered options

**The marketplace alone.** The cheapest option, and it was the starting point.
Rejected on two counts, either of which is fatal: a marketplace plugin installs
per user, so a team cannot commit it and get one answer for everyone; and its
pinned `sha` tracks the skill's repository, not the binary on the machine, so
the skill and the CLI drift apart with nothing able to notice. Kept as a
secondary, explicitly unmanaged channel rather than dropped.

**A second marketplace for Codex.** Codex has its own plugin manifest and
marketplace file, so parity was available. Rejected: Codex's system
`skill-installer` already installs from any GitHub repository and path, so the
route exists at zero cost. A second marketplace is a second pinned commit to
bump, a second catalogue to regenerate, and a second place to go stale, to serve
one skill.

**A `__PROXIMO_VERSION__` sentinel in the `SKILL.md`**, substituted on write the
way `__TLD__` already is for the stack assets. Attractive, because the version
would then be visible to the agent reading the file. Rejected because it breaks
precisely in the channel it cannot control: a copy installed from the marketplace
never passes through our installer, so the agent would read the raw placeholder.
Putting the version in a side file instead keeps the source byte-identical to
every installed copy, and turns the file's absence into information.

**Two skills, or three** — one per pillar: exposing a container, diagnosing a
route, reading what the browser reported. Rejected because it inverts the
developer's actual state of knowledge. "The page is blank" may be a 502, a
container that never became healthy, or a `TypeError` in a bundle; splitting the
skill demands the choice be made *before* the answer is known, which is the one
moment it cannot be. One entry point that triages, loading a single reference
once the answer is narrowed, is the same information at a fraction of the
metadata every session pays for.

**Two variants of the skill, one per agent.** Rejected: the formats coincide, and
the only divergence is a `metadata.short-description` key that Codex uses and
Claude Code ignores without complaint. Two files to keep in step for no gain.

**A Check and a Remedy instead of auto-update** — the shape the rest of proximo
already uses for skew, and the option originally recommended here. Overruled
deliberately: stack skew is repaired by a command the developer runs *at* proximo,
while skill skew is discovered by an agent already midway through answering a
question, where a Remedy is a dead end rather than a next step. The objection
that survived — that a project-scope copy is a tracked file, so updating it
produces a diff nobody asked for — is answered by announcing the diff, not by
declining to make it: a skill frozen at an old version misinforms the whole team,
which is worse than a diff that names itself.

**A hook on the root command**, so that every `proximo` invocation reconciles.
Rejected: `proximo status` is an inventory and must not write to `~/.claude/`.
Auto-update belongs to the commands that already materialise proximo's own
artefacts onto disk.

**A hook in the agent** — a `SessionStart` hook for Claude Code, its counterpart
for Codex. Rejected as disproportionate: it requires editing the settings of two
different agents, which is a far larger claim on a developer's machine than the
skill it would be maintaining.

**Overwriting a modified copy.** Rejected as an automatic behaviour: at project
scope it destroys a team's work, silently, in the middle of an unrelated
`proximo up`. The hash check that prevents it mirrors the `computedHash` this
repository already relies on for the skills it vendors. What survives is the
deliberate form — `--force`, typed by a developer who has just been told which
copy diverged — because "silently" is what the objection is about, not
"ever".

## Consequences

- `skills/` becomes the directory of what proximo **publishes**, distinct from
  `.claude/skills/`, which holds what proximo **consumes** while being developed.
  The word "skill" now means two things in this tree and the distinction is
  documented in `docs/development.md`, not left to be inferred.
- The skill's `references/` are **generated** from `docs/` by `make skill-refs`,
  with a CI check that fails when they drift — the label contract and the
  Inspection semantics have exactly one source. Nothing in the skill may use a
  repository-relative link: a globally installed copy has no `docs/` beside it,
  so every reference out is an absolute URL to the published site.
- Auto-update reaches global copies always, and a project copy only while proximo
  is run inside that project. A repository left alone for six months holds a stale
  copy until someone returns to it. This is a stated limit, and the `agent-skill`
  Check names it where it can see it.
- `proximo uninstall` removes managed copies whose hash is intact and lists the
  ones it skipped, keeping the promise that install is reversible without
  extending it into deleting work proximo did not write.
- The skill's version is the CLI's version, with no independent release cadence
  to reason about — and no way to ship a skill fix without shipping a binary.
