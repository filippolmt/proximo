# proximo remembers what the runtime declares, never what the project wrote

> Amended by [ADR 0008](0008-proximo-measures-the-project-concludes.md), which
> settles what this one left as a `_Debt_` in CONTEXT.md — that notation is gone,
> and the two references to it below are the record of when it was still there.

[ADR 0006](0006-the-transcript-is-quoted-never-stored.md) gave every route a
Transcript and left a hole it named out loud: a container with no Route — a
worker, a queue consumer, a migration job — produces no Access record, therefore
no Exchange, therefore no Transcript. The two reasons the gap was declared rather
than filled were that an Exchange without an Access record would hollow out the
term, and that deciding which lines of a routeless container look like errors
would mean interpreting the text a Transcript exists to quote. Both had to be
honoured rather than overridden.

**Decision.** proximo may remember what the runtime declares; never what the
project wrote. Four consequences, one rule:

- The **Transcript stands on its own**. It is what a container wrote *in a
  window*, and an Exchange is the most precise of the things that can fix that
  window rather than the owner of the Transcript. The Exchange is not stretched:
  no optional Access record, no empty sibling term.
- A new term, the **Incident**: a fact the runtime declares about a container — a
  non-zero exit, a restart, an OOM kill, a transition to unhealthy. Never a line
  that was read.
- An Incident **fixes a window**, from the previous Incident of the same service
  to itself. An exit code and a restart are statements *Docker* makes; quoting
  the lines before one interprets a runtime event, never the text. Text matching
  stays excluded, and `--since` remains the fallback when there is no Incident.
- proximo **remembers the Incident and not the Transcript**. An Incident is tens
  of bytes of runtime metadata; the output around it stays where it already is.
- The **store lives in the watcher**, which is the only stack service holding
  both the Docker socket and the event subscription. The hop has neither by
  design: it sits in the response path, and that is the one place proximo works to
  keep dumb.

A container with no host is observed only if it asks to be, with
`proximo.transcript=true` — "you can be quoted", beside `proximo.hosts`'s "this is
how you are reached". Incidents themselves are orthogonal to routing: every
container proximo knows about produces them, and the label only makes an
otherwise invisible one known.

## Considered options

**Match the text.** Read a routeless container's output and treat what looks like
an error — `panic:`, `Traceback`, `ERROR` — as the thing to report. Rejected: it
is the reason ADR 0006 declared the gap instead of filling it. A Transcript
exists to quote output verbatim, and a matcher makes proximo an interpreter of the
project's own words — wrong in both directions, since it invents errors in a
logger that prints `ERROR` at info level and misses every framework that does
not. Naming the Incident as a term is what holds this line: without a word for
"a fact the runtime declared", the first `panic:` matcher breaks nothing that is
written down.

**Query container state instead of remembering.** `docker inspect` reports an
exit code, so proximo could ask at print time and store nothing at all — the
purest form of the ADR 0006 rule. Rejected because it answers a different
question: it reports the *last* exit, not the six before it, which is precisely
the restart loop this exists for. Remembering tens of bytes of runtime metadata
does not weaken the rule; the rule is about the project's data, and an exit code
is Docker's.

**Put the store in the hop**, where the Exchanges already live, so the CLI reads
one source. Rejected twice over: the hop deliberately has no Docker socket, being
the one stack service a browser can reach, and shipping Incidents to it would add
a channel between two stack services — which ADR 0006 rejected elsewhere for the
same reason. The cost, a second way for `proximo errors` to fail, is one the model
already accommodates: a listing hands over a Check's Remedy when the stack itself
is why it has nothing to show.

**A sibling term for the Exchange** — an "Episode", say, holding an Incident and
a Transcript the way an Exchange holds an Access record and one. Rejected: it
doubles the vocabulary to say one thing, and every command, flag and document
would then have to name both. The Transcript standing alone says the same thing
with one term fewer.

**An Exchange with an optional Access record.** The smallest diff on paper, and
rejected on the term: an Exchange *is* one request, and an Exchange whose Access
record may be absent no longer means anything in particular. A term that admits
its own absence has stopped fixing the language.

**Infer the opt-in from the Compose project** — observe every container of a
project that already has a routed one. Rejected: it drags in postgres, redis,
adminer and mailhog, and forces proximo to judge which sidecars matter, a
judgement the model gives it nowhere else. It would also make a container
observable on a fact the developer never wrote down, which is the opposite of how
a Route works.

**A separate command** (`proximo incidents`). Rejected for the reason proximo has
exactly one Skill: the developer's question is never "was it the request or the
worker" — the queue is stuck *because* checkout enqueued a malformed job. Two
commands force them to know the answer before asking. One listing, one time
order, an Incident as a differently-shaped row among the Exchanges.

## Consequences

- **A live worker that is doing nothing gets a Reading, not a verdict.** A worker
  blocked on a slow query is alive, healthy and declares no Incident. proximo
  answers with what it can measure — running since when, what the healthcheck
  says, how many restarts, and the instant its output last moved — and stops
  there, because an idle consumer and a stuck one produce identical readings and
  telling them apart means knowing whether work was queued. That last step needs
  the project to cooperate, which ADR 0006 rejected with its strongest reason —
  proximo is a thing you switch on, not a dependency of your code — and nothing
  here weakens that. **Reading** is therefore a term of its own: present-tense
  and measured, where an Incident is dated and declared. The remaining gap stays a
  normative `_Debt_` under **Incident** in [CONTEXT.md](../../CONTEXT.md), because
  leaving it unwritten is the worst option: it lets a developer read proximo's
  silence as *all fine* when it means *I have nothing to say*. Every place an
  Incident is documented says so.
- **The project can declare it, and that needs nothing new.** A healthcheck that
  fails when a worker stops advancing makes Docker say *unhealthy*, and an
  unhealthy transition is already an Incident — so the window it fixes quotes
  exactly what the worker wrote before it stalled. This is deliberately the whole
  of the answer: the judgement is the project's, expressed in the project's own
  terms through a Docker feature, and proximo neither defines the contract nor
  reads the marker. Nothing is added to proximo, and proximo becomes a dependency
  of nobody's code. Two things it does not do, kept apart on purpose: the
  developer has to write the healthcheck, which is the price of the boundary and
  not a gap in it; and proximo still never *infers* that a container is stuck,
  which is the gap, and stays the `_Debt_`. The docs point at the healthcheck from
  the one place a developer arrives with the question.
- **A fourth silence.** proximo will report that a worker was OOM-killed at 14:02
  and that it can no longer show what the worker wrote, because the container is
  gone or its log has rotated past that window. That is the declared price of
  remembering only the Incident, and it is said in those words rather than shown
  as an empty quote.
- **The window is self-scaling, and unbounded on the left.** The first Incident
  of a service has no previous one, so the quote reaches back as far as the
  container's output goes. The existing byte cap bounds it and declares its own
  elision — the same bound every other Transcript already carries.
- **Retention is per service, not a global budget.** A byte budget is right for
  large heterogeneous items (an Exchange carrying a DOM); an Incident is tens of
  bytes, so the risk is never memory but a looping worker evicting another
  service's only Incident. The cap is spent per service, alongside a maximum age.
- **Nothing is forgotten when a container is removed.** `compose down && compose
  up` is what a developer does *because* something was wrong, so forgetting there
  would destroy the evidence at the moment it is needed.
- **The watcher's store has its own `started`.** An empty listing means "nothing
  happened" or "it was all thrown away", and those are very different answers.
- **A stack that predates this records no Incident**, which is version skew and
  not a quiet machine. It gets its own Check — on the model of *The running stack
  records access logs*, because "your stack is old" does not answer "why is this
  command silent about a worker that keeps dying" — plus the inline line in
  `proximo errors` for whoever never runs `proximo doctor`.
- **"Service" enters the vocabulary.** `--service` names one, and a term a flag
  names cannot stay implicit. Its qualified form and the Namespace are one
  concept: `shop/worker` is what a listing prints, a bare `worker` is accepted
  when nothing contests it, and a contested one has its candidates reported —
  exactly the position proximo holds on a Bare host.
