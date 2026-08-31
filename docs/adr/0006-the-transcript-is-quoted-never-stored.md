# The Transcript is quoted, never stored

An Exchange said what the stack saw and what the browser reported, and nothing
about what the application itself said — so a 500 produced an Access record
reading `500` and no stack trace, and the agent a developer asked to fix the code
had to leave proximo and run `docker logs` to find the one thing that mattered.
Closing that hole meant answering where the third part comes from, and what
proximo does with it.

**Decision.** proximo collects no application output of its own. The
**Transcript** is read back on demand from the container's existing output and
cut to the window of the Exchange, so the store of record stays where it already
is and proximo holds nothing. The **Access record** moves out of Inspection and
comes from Traefik's access log, written to stdout like every other stack
service, so every route produces an Exchange rather than only the routes a
developer had already suspected. The join between the two is therefore
**temporal, not causal**, and is made in the CLI at the moment it prints —
because the CLI already has both sources in hand, and the one stack service the
browser can reach deliberately has no Docker socket.

## Considered options

**Collect and keep application output in the stack.** The obvious shape, and the
one that buys a causal join: a hop that owns the output can tag each line with
the request that produced it. Rejected because the cost is a second storage
system — budget, eviction, disk or memory pressure, and a retention policy — for
data that is already retained, bounded and rotated one layer down. The store
proximo already has for Exchanges writes nothing to disk on purpose; adding a
durable one for application logs would be the first time proximo persisted a
project's data, and application logs are the worst possible first case.

**Ask the project to cooperate** — a library or an endpoint the project imports,
sending structured errors with a request id already attached. The cleanest data
in the design, and rejected for what it does to the contract: proximo is a thing
you switch on, not a dependency of the code, and a project that cannot import
the library would be the one project left without a diagnosis.

**Capture 5xx response bodies**, since the Inspection hop is already in the
response path. Rejected because it does not carry what is needed: the body of a
500 is the framework's error page, while the stack trace went to stderr. Bodies
stay excluded from the Access record for the reasons that already excluded them.

**Keep the Access record inside Inspection.** Rejected on the case that matters
most: a route balancing across replicas is refused Inspection today, because one
header cannot name N backends — so the routes with the most containers would have
been the routes with no diagnosis. Traefik's log names the server it picked, which
is exactly what a Transcript needs and all it needs.

**Resolve a backend address to a container at request time**, which is exact by
construction. Rejected: it puts a lookup on every request and a channel between
two stack services, to buy precision over a window measured in minutes. Resolving
at join time and comparing the container's birth time closes the one dangerous
case — an address reassigned after a restart — without either.

## Consequences

- **The join can be wrong, and says so.** Two overlapping requests to one
  container interleave their lines, and a temporal cut cannot tell them apart.
  proximo reports the overlap instead of attributing a line, which is the position
  it already holds on Collisions. Confidently quoting the wrong container's stack
  trace to something about to edit code is the failure this refuses.
- **Traefik's operational log and its access log share one stream.** They are
  told apart because access lines are JSON; the CLI filters on that.
- **An Exchange from a non-inspected route has no minted identity**, because
  Traefik stamps none. The identity is derived at join time from host, instant and
  backend address — stable across two CLI invocations, which is the only property
  an agent needs to say "that one".
- **The link from a Client report to another Exchange exists only under
  Inspection.** It rides a response header, and only the hop can stamp one. This
  is coherent rather than partial: Client reports exist only under Inspection
  anyway.
- **A Transcript may leave the machine.** It is handed to agents, and an agent
  sends its context to a model API. proximo redacts nothing — redacting is
  interpreting, and a redactor covering most patterns produces false confidence
  exactly where an unrecognised format slips through. What proximo owes instead is
  to say so, in the CLI and in the Skill.
- **`curl` closes the loop for the backend half.** With every route producing an
  Exchange, an agent can provoke a request and read the result with no browser and
  no human. A browser stays necessary only for Client reports.
- **A stack that predates this records no access log**, so `proximo errors` would
  be silent for a reason unrelated to the developer's code. That is version skew:
  a Check reports it for whoever runs `proximo doctor`, and `proximo errors` names
  it inline for whoever does not.
