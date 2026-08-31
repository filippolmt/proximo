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

**Service**:
One named part of a Project — a Compose service — and the thing a developer
actually asks about: "what did the worker say" is a question about the service,
where "what did worker-2 say" is one you can only ask after seeing all three.
Its qualified form carries the Namespace, `shop/worker`, and a qualified service
and a Namespace are the same concept rather than two: a bare service name is
accepted when nothing contests it and reported with its candidates when
something does, exactly as a Bare host is. A container outside a Compose project
belongs to no service and names itself.
_Avoid_: container, replica, app, process

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
a failure does. A report is where Remedies live, and `proximo doctor` is the only
command that prints a whole one. An inventory of what is running is not a report,
and `proximo status` is an inventory, which is why it never prints a Remedy at
all. A command that is neither may still hand over the Remedy for a Check it
names, when the reason it has nothing to show is the stack itself: `proximo
errors` does, because the reader it exists for is an agent that will not run a
second command to learn a one-word answer.
_Avoid_: diagnosis, output, summary, health check

**Access record**:
The metadata of one request that passed through the stack — host, method,
status, latency, size, and the backend that served it. Deliberately excludes
request and response bodies. One part of an Exchange: what the stack saw from
outside the application. Every route produces one, whether or not it is under
Inspection, because a developer learns they need a diagnosis only after the
request they needed it for is over.
_Avoid_: trace, transaction, log entry

**Transcript**:
What a Project's own container wrote in a window, quoted verbatim and never
interpreted — the only thing proximo hands over that the project itself authored
rather than proximo observed. The window is always fixed from outside the text,
by something that is not a reading of it: an Exchange fixes it most precisely
when there is one, an Incident fixes it for a container no request ever reaches,
and `--since` fixes it when neither exists. proximo never holds a Transcript: it
is read back on demand, because the container's output already exists somewhere
that keeps it. The cut is therefore temporal, not causal, and says only *what the
container wrote in this window* — never *what this request caused*: where two
Exchanges of one container overlap, proximo reports the overlap rather than
attributing a line, the same way it reports a Collision rather than resolving
one. A Transcript is bounded, and an elision is always declared: a truncation
nobody is told about is the one after which a reader stops looking. It is raw
application output, so it may carry credentials and personal data; proximo
redacts nothing, because redacting is interpreting, and a redaction that misses
is worse than none.
_Avoid_: log, logs, stderr, output, stack trace, server error

**Incident**:
A fact the runtime declares about a container: a non-zero exit, a restart, an
OOM kill, a transition to unhealthy. Never a line that was read — the boundary is
the term, because proximo may remember what the runtime declares and never what
the project wrote, and the first matcher for `panic:` breaks nothing else that is
written down. An Incident is dated history, which is what separates it from a
Check: a check is present-tense and repeatable, an Incident happened once, at an
instant, and fixes the window of a Transcript around itself. proximo remembers
Incidents and only Incidents: tens of bytes of runtime metadata per container,
capped per Service so a container restarting every three seconds cannot evict
another's only one. Every container proximo knows about produces them, routed or
not; a container with no Route becomes known by asking to be, and being observed
is a separate thing from being reachable.
_Avoid_: error, event, crash, failure, fault
_Debt_: a container that is alive and doing nothing produces no Incident — a
worker blocked on a slow query is healthy and silent. proximo answers with the
**Reading** it can take instead of with nothing, and refuses the conclusion: an
idle consumer and a stuck one are the same picture from outside the container,
and telling them apart needs to know whether work was waiting, which only the
project knows. Closing that last step needs the project to cooperate, which
[ADR 0006](docs/adr/0006-the-transcript-is-quoted-never-stored.md) rejected with
its strongest reason — proximo is a thing you switch on, not a dependency of your
code — so the gap stays open and stays written down: the failure mode of leaving
it implicit is a developer reading proximo's silence as *all fine* when it means
*I have nothing to say*.

**Reading**:
What the runtime says about a container *right now*, as opposed to a dated
Incident: whether it is running and since when, what its healthcheck says, how
many times it has been restarted, and the instant its output last moved. A
reading is measured, never interpreted — the instant a stream last moved is the
runtime's to declare, while what the line said is the project's — and proximo
states it without drawing the conclusion, the same way a Check reports and never
repairs. A reading that could not be taken is named as such and never reported as
a zero, because a measurement nobody could take is not a measurement of nothing.
It is what proximo has to offer about a container that is alive and may not be
progressing, and its limit is declared with it: no reading distinguishes a
consumer with nothing to do from one that is stuck.
_Avoid_: metric, status, health, probe, sample

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
One Access record, the Transcript of the container that served it, and the
Client reports that arose while the page it served was live. The unit proximo
hands to a developer or an agent for one request, and the most precise of the
windows a Transcript can be cut to rather than the owner of the Transcript: it
fixes a window when there is a request, and a container no request reaches still
has output to quote. The reason all three parts are collected:
no one of them says on its own whether a broken page is the backend's fault or
the front-end's. The parts are joined, never merged: a Client report about a
request the page made carries the identity of the Exchange that request
produced, so a front-end error leads to the serving container's Transcript in
one step, and an Exchange stays one request rather than becoming a tree.
_Avoid_: session, request, correlation

### What proximo hands to an agent

**Skill**:
The artefact proximo hands an agent so the agent can operate proximo on a
developer's behalf: what to ask, in what order, and what not to do. Not
documentation, which is written for a person who is reading, and not a command,
which is written for a shell — it is the procedure an agent follows when the
developer asks *it*, rather than proximo, why a page is broken. There is exactly
one: the developer's question is never "is this a routing problem or a front-end
one", so the answer is never a choice between two skills.
_Avoid_: plugin, integration, prompt, tool, agent instructions

**Managed copy**:
A copy of the Skill that proximo itself wrote, and whose version and content it
therefore knows. proximo keeps a managed copy level with the CLI, and on
uninstall removes the ones it recognises. A copy that arrived by any other route
is not managed: proximo neither updates nor deletes it, because it did not put it
there — and it says so, rather than leaving an agent to assume a copy is current.
A copy a developer has edited stops being current without stopping being theirs:
proximo reports it and leaves it alone.
_Avoid_: installed skill, tracked copy, vendored skill
