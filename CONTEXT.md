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
The binding of one host name to the backends serving it, live only while their
containers are running. Routes are derived from container labels, never declared
by hand.
_Avoid_: mapping, rule, proxy entry

**Host**:
A single name proximo answers for, e.g. `api.test`. Always the full name
including the TLD. Every route has both a Bare host and a Qualified host.
_Avoid_: domain, hostname, URL, address

**Bare host**:
The short, unqualified name a developer actually writes — `api.test`. It is a
convenience, and the only name that can be contested: when two containers claim
it, one of them is not served under it.
_Avoid_: canonical host, primary host

**Qualified host**:
The bare host with the Namespace inserted before the TLD — `api.shop.test`.
Derived from the *declared* host and never from the container name, so replicas
of one service share it. Always present and never moved by a Collision, which is
what makes it the name a developer can rely on.
_Avoid_: alias, secondary host, fallback

**TLD**:
The single DNS label proximo claims on the host resolver, `test` by default.
Exactly one per machine: the Namespace, never a second TLD, is how two groups of
projects are kept apart. `test` is the only value carrying a guarantee (RFC 6761
reserves it); any other label is a name someone else may one day own.
_Avoid_: domain suffix, zone

### Naming and collisions

**Namespace**:
The qualifier that distinguishes two routes claiming the same bare host — for
example the same service running from two worktrees of one repository. Derived
from the project's Compose project name, never invented by the developer: a
hand-picked namespace would be a second host name, and host names are already
declared by `proximo.hosts`. A container outside a Compose project has no
namespace, and therefore no qualified host.
_Avoid_: profile, environment, workspace, instance

**Collision**:
Two containers claiming the same host. proximo reports a collision; it never
resolves one in favour of a claimant. A collision costs a bare host, not a
service: every claimant stays reachable at its own qualified host, and only the
contested bare host is served by one of them. It is scoped to the host, so a
container losing one host keeps every other host it declared.
_Avoid_: conflict, duplicate, override

**Replica**:
One of several containers of a single Project producing the same route,
differing only in the backend they point at. Replicas are not claimants and
there is nothing to report about them: they are one route with several backends,
balanced round-robin. Replicas live inside one Project — containers of different
Projects are never replicas.
_Avoid_: collision, duplicate, instance

### Diagnosis and observation

**Check**:
One independently verifiable statement about the environment — the resolver is
installed, nothing else holds `:443`, the CA is trusted, this container's labels
are well-formed. A check reports; it never repairs. It also never elevates:
anything that needs a privilege the developer must grant is a Remedy, not a
check, because a diagnosis that asks for a password is one nobody runs at the
moment they need it most.
_Avoid_: test, validation, probe

**Remedy**:
The exact command a developer runs after a failed Check, and every failed check
carries one. Where a cure exists the remedy is the cure; where the cause is
unknown it is the command whose own output names it — a port held by an unnamed
process has no fix, but it has a question. proximo prints remedies; the
developer runs them, so every mutation of the host stays a verb the developer
typed.
_Avoid_: fix, autofix, repair

**Report**:
The outcome of one complete pass of Checks. Each check passed, failed, or was
skipped — skipped when the environment could not answer it, never when the
answer was inconvenient, and a skip names what it waited on. A report is one
whole thing rather than a stream of errors, which is why it shows the checks
that passed too: those say where *not* to look, and narrow the search as much as
a failure does. Only a report carries Remedies. An inventory of what is running
is not a report, and `proximo status` is an inventory.
_Avoid_: diagnosis, output, summary, health check

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
