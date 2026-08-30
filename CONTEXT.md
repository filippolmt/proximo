# proximo

proximo makes a locally running service reachable at a stable, trusted name
(`https://<name>.<tld>`) on the developer's own machine, and answers what that
service did when a developer opens it. Docker is the current implementation
vehicle, not the purpose: the domain is the local development environment — the
names it answers to, and what it can be made to say about itself.

Terms marked _Debt_ are normative: they fix the language proximo must use, and
the absence of an implementation is a declared gap, never a description of
today's behaviour.

## Language

### The environment

**Stack**:
The set of long-running services proximo itself manages — `traefik`, `dns`,
`watcher`, `inspector`, plus the opt-in observability services. There is exactly one per host:
it owns `:80`, `:443`, the DNS port, and the compose project name `proximo`.
_Avoid_: proxy, infrastructure, services (when the user's containers are meant)

**Project**:
A developer's own set of containers, the things proximo routes *to*. Never part
of the stack. Identified by its Docker Compose project name.
_Avoid_: stack, app, deployment

**Route**:
The binding of one host name to one backend, live only while its container is
running. Routes are derived from container labels, never declared by hand.
_Avoid_: mapping, rule, proxy entry

**Host**:
A single name proximo answers for, e.g. `api.test`. Always the full name
including the TLD.
_Avoid_: domain, hostname, URL, address

**TLD**:
The single DNS label proximo claims on the host resolver, `test` by default.
One per machine; changing it re-points the entire environment.

### Naming and collisions

**Namespace**:
The qualifier that distinguishes two routes claiming the same base name — for
example the same service running from two worktrees of one repository. Derived
from the project's Compose project name, never invented by the developer.
_Avoid_: profile, environment, workspace, instance
_Debt_: no implementation. Today a name clash is disambiguated by container ID,
not by namespace.

**Collision**:
Two running containers claiming the same host. A collision is a condition
proximo reports, not a state it silently resolves in favour of one claimant.
_Avoid_: conflict, duplicate, override
_Debt_: the code both reports *and* resolves. Host collisions warn and then drop
the loser by iteration order; safe-name collisions are resolved silently with a
container-ID suffix and never reported at all.

### Diagnosis and observation

**Check**:
One independently verifiable statement about the environment — the resolver is
installed, `:443` is free, the CA is trusted, this container's labels are
well-formed. A check reports; it never repairs.
_Avoid_: test, validation, probe
_Debt_: exists only as scattered preflight functions, not as a first-class
concept with a uniform report.

**Remedy**:
The exact command a developer runs to clear a failed check. proximo prints
remedies; the developer runs them, so every mutation of the host stays a verb
the developer typed.
_Avoid_: fix, autofix, repair
_Debt_: one implementation only — a converge that leaves the stack image absent
prints the `docker pull` that diagnoses it. Every other failed check still
reports an error, not a remedy.

**Access record**:
The metadata of one request that passed through the stack — host, method,
status, latency, size. Deliberately excludes request and response bodies. One
half of an Exchange: what the stack saw from outside the application.
_Avoid_: trace, transaction, log entry
_Debt_: recorded only for routes under Inspection, which is where the hop sees
the exchange. Every other route produces none: Traefik's access log is off.

**Inspection**:
The collection of Exchanges for one route, live only while its container carries
the `proximo.inspect` label. Inspection is never on by omission: it rewrites the
responses a project produced, so it is always something the developer asked for.
_Avoid_: monitoring, tracing, debugging, instrumentation

**Reserved path**:
A path prefix on a project's *own* origin that proximo answers for instead of the
backend, so that a page can report to proximo same-origin. There is one,
`/.proximo/`, and it is unavailable to the project for as long as the route is
under Inspection.
_Avoid_: internal route, hook, callback, endpoint

**Client report**:
What the browser observed about one page — an uncaught exception, a rejected
promise, a policy violation — together with the Breadcrumbs and the Snapshot that
led up to it. The other half of an Exchange. Capture is deliberately wide and
filtering happens at display: a report is never dropped at collection time
because it looked redundant.
_Avoid_: error, log, event, exception (alone) — as names for the concept. The
`proximo errors` command is exempt: it is the question a developer asks, not the
name of the thing they get back.

**Breadcrumb**:
One thing that happened before a Client report — a console call, a request the
page made, a click, a navigation. Breadcrumbs are the sequence; the report is the
end of it.
_Avoid_: history, timeline, trace, span

**Snapshot**:
The page's DOM as it stood when the *first* Client report of an Exchange was
raised. One per Exchange, not one per report: a page rarely changes shape between
two errors of the same load, and a copy of the DOM is the heaviest thing proximo
holds. Never printed to a
terminal: it is handed over as a file, because it is meant to be read by whoever
is diagnosing, not scrolled past.
_Avoid_: dump, capture, replay

**Exchange**:
One Access record joined to the Client reports that arose while the page it
served was live. The unit proximo hands to a developer or an agent, and the
reason both halves are collected: neither half alone says whether a broken page
is the backend's fault or the front-end's.
_Avoid_: session, request, correlation
