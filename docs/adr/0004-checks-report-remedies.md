# Checks are a first-class concept, with a report and a remedy

proximo diagnoses unevenly. `preflight()` verifies three things and returns bare
errors; `dns.CheckPortFree` guards the DNS port while nothing guards `:80` or
`:443`; `proximo status` prints four different `⚠` lines that nobody calls a
diagnosis; and `docs/troubleshooting.md` documents seventeen failure modes in
prose that no code detects. Underneath all of it sits a sharper fact: the
codebase contains no verification at all. `tls` installs and removes trust,
`dns` configures and removes the resolver, and neither knows how to *look* —
`platform.Has` and `platform.IsActiveService` are the only predicates in the
project. Every attempt to make one failure actionable ran into the same missing
piece: proximo had no way to state what it had observed, only a way to fail.

**Decision.** A Check is one independently verifiable statement, and a Report is
one complete pass of them. A check passes, fails, or is skipped, and **every
failure carries a Remedy** — the cure where one exists, and otherwise the command
whose own output names the cause, generalising the `docker pull` remedy that
`stack.go` already prints for an absent stack image. Checks live in one registry,
`proximo doctor` runs all of them, and `install` and `up` gate on the subset that
is meaningful before the host has been changed. **A check never elevates**: read
the host, never write it, and never ask for a password. The two commands divide
cleanly, and the division is the contract: **`proximo status` never prints a
Remedy, `proximo doctor` always does.**

## Considered options

**Fold the report into `proximo status`.** No new command, and `status` already
prints warnings. Rejected: `status` answers *what is running*, a row per route,
and the common case is a clean table. Growing it into *what is broken* makes the
healthy case noisier for every developer in order to serve the broken one, and
leaves no way to ask for a diagnosis without asking for an inventory.

**Check only at `install` time.** Rejected outright: the moment a developer wants
a diagnosis is never the moment they are installing. It is three weeks later,
when a name stopped resolving.

**Keep `preflight.go` as its own thing, separate from the registry.** Rejected
because the divergence has already happened and is visible in the tree: the DNS
port is guarded and `:443` is not, and neither is ever reported to a developer
who asks. Two lists of the same kind of fact drift apart by default.

**A fourth outcome, `warn`, for things that are wrong but not broken** — a stack
running an older version than the CLI, say. Rejected: that skew has an exact
remedy, `proximo update`, and anything with a remedy is a failure. `warn` would
mean "a failure proximo decided not to insist on", and that decision belongs to
the developer reading the report, not to proximo writing it.

**Let a failed check report without a remedy**, or let the remedy be prose.
Rejected: both turn Remedy into a synonym for "advice", and the term stops
carrying a promise. Holding the line forced the useful reframing below — a
diagnostic command *is* a remedy when the cause is unknown.

**`net.LookupHost` for the end-to-end DNS check.** Rejected, and worth recording
because it is the obvious choice and it is wrong: Go's pure resolver reads
`/etc/resolv.conf` and honours neither `/etc/resolver/<tld>` on macOS nor a
`Domains=~<tld>` drop-in on Linux. It would report a failure on a perfectly
healthy machine — the one outcome a diagnostic tool must never produce.

**Let `doctor` use sudo where a check would need it.** Rejected as a matter of
definition rather than convenience. Everything proximo must read is readable
unprivileged, and a check that genuinely needed elevation would be describing a
repair. The constraint is load-bearing and easy to erode one check at a time.

**Ship `--json` with the first version.** Rejected: the exit code covers the only
named consumer, the internal shape is typed anyway, and nothing in proximo emits
JSON outward today. Adding it later breaks nothing.

## Consequences

- **Nearly every environment check is new code.** Reading the system trust store,
  reading the resolver configuration, and asking who holds a port are all
  capabilities the project does not have. The migration is not a refactor of
  `preflight.go`; it is the first verification proximo has ever done.
- **The DNS check is two checks, not one.** *The proximo DNS server answers* is a
  direct query to `127.0.0.1:<DNSPort>` with `miekg/dns`; *the system uses that
  server* shells out to `dscacheutil` or `resolvectl` against a fixed sentinel
  name, `proximo-doctor.<tld>`, which the wildcard server answers without any
  container running. A corporate VPN produces the pair — the first passes, the
  second fails — and that pair *is* the diagnosis. Neither check alone gives it,
  and their remedies point in opposite directions.
- **The port check asks who holds the port, not whether it is free.** A healthy
  machine has `:443` bound — by proximo. Three outcomes are useful (free, held by
  the stack, held by someone else) and only the third is a failure, which also
  picks the remedy: `docker ps --filter publish=443` when a container holds it,
  `lsof` when a host process does.
- **Checks declare their prerequisites, and a failed prerequisite skips what
  depends on it**, naming what it waited on. Without this, an unreachable Docker
  daemon produces a dozen red lines for one cause. It also answers the
  never-installed machine cleanly: a single gating check (the CA is on disk *and*
  the resolver file exists) fails with `proximo install`, and everything
  downstream skips. The pre-install checks still run, so a developer learns that
  Docker is missing *before* running the install that would fail on it.
- **`proximo status` loses the version-skew and image-override warnings** to
  `doctor`, and keeps its per-route notes. A collision therefore appears in both
  commands by design: `status` shows it because the route's reachable URL changed,
  `doctor` shows it because there is something to do about it. The Remedy is what
  tells them apart.
- **Every check names a section of `docs/troubleshooting.md`, and a test asserts
  the anchor exists.** The seventeen sections were the catalogue all along; making
  the link a compile-and-test constraint stops a documented failure from having no
  check, and a check from having no explanation.
- **A check that runs out of time fails; it does not skip.** `resolvectl` against a
  broken VPN never returns, and that is precisely the machine `doctor` exists for.
  A diagnostic tool that hangs is worse than one that is wrong.
- **Any failure exits non-zero, route failures included.** A route failure is the
  developer's own container rather than proximo, but an exit code that needs a
  rule to interpret is worse than one that does not.
- **Checks take their dependencies as parameters**, the way `hostSteps(r, tld)`
  already does, so the suite can build a machine with no resolver, a port held by
  a stranger, or no NSS store, without touching the host running the tests.
