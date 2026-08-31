# Transcript — implementation brief

Domain model: `CONTEXT.md` (`Transcript`, `Access record`, `Exchange`).
Rationale and rejected alternatives: `docs/adr/0006-the-transcript-is-quoted-never-stored.md`.

This file holds only the decisions that belong in neither — the command surface,
the output rules and the code-level consequences agreed in the design session.

## Code-level consequences

- `internal/inspect/store.go` — `Exchange` has no backend identity today. It must
  carry the backend the request was served by. The value already transits:
  `internal/inspect/inspect.go` reads `X-Proximo-Backend`, which the watcher writes
  into Traefik's dynamic config (`internal/docker/watcher.go:913`) and Traefik
  always overwrites, so it cannot be forged from outside.
- Traefik's access log must be enabled to stdout, JSON per line, in
  `internal/docker/assets/traefik/traefik.yml`. The compose `*proximo-logging`
  anchor already bounds and rotates that stream — do not add a file or a rotation
  policy.
- `internal/docker/routes.go:133` (`inspection off: route balances across
  replicas`) stays as it is. A Transcript needs no injection, so replicas get one
  even though they get no Inspection.
- The join lives in the CLI. Do not mount the Docker socket into `inspector`: it
  is the only stack service the browser can reach, and it deliberately has none.
  The `watcher` has the socket; the CLI has Docker directly.
- Backend address to container is resolved at join time, comparing the container's
  birth time against the request instant. If the address belongs to no live
  container, or to one created after the request, say the container is gone —
  never quote another container's output.

## Output rules

- **Bounded, elision declared.** Cap in bytes, keep head and tail, state how many
  lines were dropped in between. The head carries a panic's message, the tail its
  most recent lines; a silent truncation is the failure mode this design refuses
  everywhere else.
- **Inline only where there is something to say** — the same test the listing
  already applies to decide what to show: a failing status, a Client report, or a
  warning. One predicate, so a listed Exchange is never one whose Transcript is
  withheld. Tightly capped. Clean Exchanges show no Transcript.
- **Replicas**: quote only the container that served, and say how many replicas the
  service has. Without the count, an agent reads "happens only sometimes" as a race
  condition when the cause is one replica running stale config.
- **Never a silence without a named cause.** Three places, one principle:
  - no Exchange for a host → name both causes ("nothing called it" / "it does not
    resolve here") and point at `proximo doctor`;
  - empty Transcript → tell apart "wrote nothing in this window" from "wrote
    nothing since it started, so it probably logs elsewhere" from "log driver not
    readable";
  - no access log at all → version skew, see the Check below.
- **Credentials**: state once, in the CLI and in the Skill, that a Transcript is
  raw application output and may carry secrets. No redaction. If a knob is ever
  added it must be `--redact`, never `--no-redact`, so nobody can read the default
  as protected.

## Command surface

- `proximo errors --since` accepts an **absolute instant** as well as a duration.
  No cursor, no persisted state, no blocking watch: the agent knows when it saved
  the file, proximo does not.
- `proximo errors transcript <id>` — the whole Transcript, symmetric with the
  existing `proximo errors dom <id>`. Prints to **stdout** (unlike `dom`, which
  writes a file: a capped Transcript is text to read and pipe, not hundreds of
  kilobytes to grep), with `-o` available.
- `proximo errors` warns when the host asked about is a contested bare host.

## The Check

One new Check in `internal/checks/registry.go`: the running stack records access
logs. Prerequisite: the stack is up. Remedy: `proximo update`. Needs its anchor in
`docs/troubleshooting.md` — a test asserts it. Follow
`docs/adr/0004-checks-report-remedies.md`.

`proximo errors` also names this inline, and that is not redundancy: the Check
answers whoever opens `proximo doctor` before having a problem, the inline line
answers the agent asking now, which will never run `proximo doctor` on its own.

## The Skill

One new row in the triage table of `skills/proximo/SKILL.md`: *the route answers,
the page renders, an endpoint 500s* → read the Transcript. Two new rules beside the
two that exist:

- ask on the **qualified host**, never the bare one — it is the name a Collision
  cannot move;
- ask for the whole Transcript only after the capped one has shown where to look.

**Do not reorder the three existing rows.** A Transcript is mute when the symptom
is "does not answer at all", which stays the most frequent local failure; a triage
that reads it first sends the agent into the log of a container that never got the
request.

`references/` under the skill is generated — `make skill-refs`, checked in CI.

## Also

`CLAUDE.md` doc map gains a row for the Transcript once the docs section exists.
