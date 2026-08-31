# proximo measures, the project concludes

A worker blocked on a lock that will never come is running, healthy and silent.
An idle consumer with an empty queue is the same picture from outside the
container, and the readings proximo can take — running for 3h, healthcheck
healthy, last wrote 14m ago — are identical in both cases. Telling them apart
needs to know whether work was waiting, which only the project knows. proximo
therefore states what it measured and stops, and the judgement is something the
project declares in the project's own terms.

This was previously written into [CONTEXT.md](../../CONTEXT.md) as a `_Debt_`:
normative language with the implementation declared missing. That was a category
error. A debt promises a payment, and nobody is going to pay this one — the
reason it is not paid is
[ADR 0006](0006-the-transcript-is-quoted-never-stored.md)'s strongest one, that
proximo is a thing you switch on rather than a dependency of the code it
observes. The `_Debt_` marker and the paragraph explaining the notation both
leave CONTEXT.md with this ADR: what is written down here is a boundary, and a
boundary with a number does not need to be marked as owed.

**Decision.** proximo never deduces that a container is stuck. It reports the
**Reading** it can take and refuses the conclusion, the same stance a **Check**
takes when it reports and never repairs. The project makes "not progressing" a
fact proximo can report by declaring it through a Docker healthcheck — a runtime
feature proximo neither defines nor reads into — whose transition out of healthy
is an **Incident**.

Making that the sanctioned answer settles three things about it that were
previously left to the shape of the code:

- **A transition to unhealthy is an Incident when the container had been healthy,
  and not otherwise.** This is what CONTEXT.md and
  [the Incidents table](../observability.md#incidents--what-the-runtime-declared)
  have always claimed the Incident was, and it is the whole of the noise
  filtering: a container waiting on postgres at boot goes `starting → unhealthy`
  and was never healthy, while a stall is `healthy → unhealthy` by construction.
  Nothing is held back from the default listing.
- **A Reading is taken on every `--service`,** with an Incident in the window or
  without one. An Incident answers *what happened* and a Reading answers *how it
  is now*; the second does not stop having an answer because the first has one.
- **A Reading stays a fact about a container,** and a `--service` produces one
  per running container rather than one container's reading and a count of the
  others.

## Considered options

**Conclude from the readings** — quiet for longer than N while healthy is
"stuck". Rejected because N is the project's number and proximo does not have
it: a batch consumer that wakes hourly and a request worker that should never be
idle for a minute both produce "last wrote 40m ago", and the same threshold is
wrong for one of them in whichever direction it is set. A tool that guesses here
is worse than one that declines, because a wrong "stuck" is read as a
measurement.

**Define a progress contract** — a `proximo.progress` label naming a marker the
project touches and proximo reads, or an endpoint the project exposes. The
cleanest data in the design and the reason it is refused: it makes proximo a
dependency of the code it observes, which is exactly what ADR 0006 rejected when
it turned down a library the project would import. The project that cannot adopt
the contract would be the one project left without the diagnosis, and the
projects that can adopt it already have a Docker healthcheck, which costs them
the same marker and costs proximo nothing.

**Sample the readings over time** and report the delta, on the argument that two
measurements a minute apart are still measurement and never cross the boundary.
True, and still rejected: the developer who runs the command twice gets the same
fact, so the sampling buys convenience rather than a fact otherwise
unobtainable — and it buys it with a polling lifecycle inside proximo for
something the shell already has. It stays available if a developer turns out to
ask for it; it is not what the boundary needed.

**Leave it declared as a `_Debt_`.** Rejected for what the notation promises. A
reader who finds a debt goes looking for the issue that closes it, and there
isn't one. The failure mode the `_Debt_` was guarding against — a developer
reading proximo's silence as *all fine* when it means *I have nothing to say* —
is real, and is answered by the Reading proximo prints and by the sentence that
prints with it, not by a marker in a glossary the developer is not reading at
that moment.

## Consequences

- **The unhealthy Incident is decided when it is recorded, not when it is
  displayed.** Docker's `health_status: unhealthy` event fires on `starting →
  unhealthy` as well, so the event alone cannot say which transition it is: the
  store remembers, per container, that it saw the check pass — from the
  `health_status: healthy` event, and from the health the reconcile's container
  listing reports, or a watcher restart would turn every already-healthy
  container into one that was never healthy. It forgets on a `die`, because a
  container id survives a restart and its healthcheck does not. The noise the
  definition excludes therefore never enters the store, rather than entering it
  and being filtered out of the listing.
- **`Interesting()` loses its only exclusion,** and with it the advice that
  existed to compensate for it: a developer following the healthcheck route no
  longer has to know that `proximo errors` alone will not show the result.
- **A flapping healthcheck produces several Incidents.** That is meaningful
  rather than noisy — flapping is the fact — and the volume is already bounded by
  the per-service cap from
  [ADR 0007](0007-proximo-remembers-what-the-runtime-declares.md).
- **Readings become plural, and the JSON breaks.** `"reading"` becomes
  `"readings"`, and the `Replicas` count disappears: it existed only because
  proximo read one container and counted the rest. The reader is the **Skill**,
  whose **Managed copy** proximo keeps level with the CLI, so the break costs a
  regeneration and no compatibility alias.
- **Non-running containers of a service get no Reading.** A Reading is the
  present tense of something alive; a container that died has already produced an
  Incident, and giving it both says one thing twice in two terms the glossary
  keeps apart. A service with nothing running says that instead.
- **The refusal is printed where it is needed and not where it is wallpaper.**
  The readings print after the listing on every `--service`; the two lines
  explaining that proximo will not conclude print only when the listing is empty,
  because the misreading they exist to stop is silence read as *all fine*, and
  there is no silence to misread on a screen full of Incidents.
