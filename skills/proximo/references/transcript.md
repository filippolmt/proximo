# Read what the container said

Reached from [`SKILL.md`](../SKILL.md) when the route answers, the page renders,
and an endpoint fails — a 500, a 502, an API call that comes back wrong — or when
nothing is visibly broken and something is not progressing.

## What a Transcript is

A **Transcript** is what a container wrote in a window, quoted verbatim and never
interpreted. What fixes the window is never a reading of the text:

- an **Exchange** — one request: the **Access record** (what the stack saw), the
  Transcript, and on inspected routes the **Client reports** (what the browser
  saw);
- an **Incident** — a fact the runtime declared about the container, for a
  container no request ever reaches (see below);
- `--since` — a plain window, when neither of the two exists.

Three things follow, and each one changes how you read it:

- **Every route has an Exchange.** No label is needed, and no browser: Traefik
  records an access log, so any request produces one. You can provoke one
  yourself with `curl` and read the result.
- **proximo stores no Transcript.** It is read back from the container's own
  output at the moment it prints, and cut to the window. Nothing is retained, so
  there is nothing to page through and no cursor to keep.
- **The cut is temporal, not causal.** It says *what the container wrote in this
  window*, never *what this request caused*. When proximo reports that other
  requests overlapped, the lines are interleaved and cannot be separated —
  reproduce the problem on its own before drawing a conclusion from them.

## Ask for it

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://<qualified-host>/<path>
proximo errors --json --host <qualified-host> --since 5m
```

The listing carries a capped Transcript per Exchange: both ends, and a declared
count of the lines elided between them. Read that first. When the head and the
tail are not enough to place the failure, ask for the whole thing:

```sh
proximo errors transcript <exchange-id>
```

It prints to stdout. `--since` takes a duration or an absolute RFC 3339 instant,
and identities are derived rather than minted, so the same Exchange keeps the
same id across invocations.

## When nothing is progressing: ask for Incidents

A worker, a queue consumer, a migration job has no host, so no request reaches it
and no Exchange exists. What proximo has instead is **Incidents**: facts the
runtime declared — `exited <code>`, `exited <code> (OOM-killed)`, `restarted`,
`stopped being healthy`. They appear in the same listing as the Exchanges, in one
time order, and each one anchors a Transcript window running from the previous
Incident of that service to itself. Nothing is held back: every Incident is in the
default listing.

**One thing that will catch you out:** the healthcheck Incident is a check that
was *passing and stopped*, never merely one that reports unhealthy. A container
that never reached healthy — a worker waiting on postgres with no `start_period`,
a check whose marker never appears — declares nothing when it fails. So a service
with no such row is not necessarily one whose healthcheck is fine; it may be one
whose healthcheck never passed.

```sh
proximo errors --json --since 30m                 # Exchanges and Incidents together
proximo errors --json --service <service> --since 30m   # one service, plus its readings
proximo errors transcript <incident-id>           # the whole window that Incident closes
```

`--service` takes the qualified name a listing prints (`shop/worker`), or a bare
one when nothing contests it. A contested bare name comes back with its
candidates rather than a guess — pass one of them.

Two rules before you report anything:

- **A container with no route must ask to be observed.** If the developer's worker
  produces no Incident at all, check the label: `proximo.transcript=true` on that
  container, and nothing else. It routes nothing. `proximo status` then lists it
  as *no route — observed for Incidents*.
- **No Incident does not mean no problem.** A worker that is alive and blocked —
  on a slow query, on a lock, on a queue that never delivers — is healthy, silent,
  and declares nothing. Every `--service` listing carries the **readings**, one
  per running container: running since when, what the healthcheck says, how many
  restarts, when its output last moved (`"readings"` in `--json`, with anything
  that could not be read under `"unread"`). They are facts, not a verdict — a
  consumer with nothing to do reads identically to a stuck one, because only the
  project knows whether work was waiting. A scaled service gives one reading per
  replica: never generalise one replica's reading to the service, since the wedged
  one is the one you are looking for. A service with nothing running carries no
  readings at all and a note saying so — read the note, and do not read the
  missing member as a service that is fine. Quote them as facts and read the
  output for the window before concluding:
  `proximo errors transcript --service <service> --since 30m`.
- **Offer the healthcheck.** To make a stuck worker visible next time, the
  container needs a healthcheck that fails when it stops advancing — the worker
  touches a marker on every job, the healthcheck fails once the marker is stale.
  Docker then says *unhealthy*, and a check that was passing and stopped is an
  Incident whose window quotes exactly what the worker wrote before it stalled. It
  is the developer's code to change, so propose it and let them decide; proximo
  neither defines the contract nor reads the marker. Propose a `start_period` with
  it: the check has to pass once, or the stall it is there to catch declares
  nothing.

## When there is nothing to quote

proximo never returns an unexplained silence. Each of these is a different
answer, and they send you to different places:

| What it says | What it means |
| --- | --- |
| *no route matched this host* | Nothing served the request, so there is no container to quote. `proximo status` lists the hosts that are routed — the usual cause is a typo in the host, or a container without the labels. |
| *wrote nothing while this request was live* | The container was up and quiet. Look at the Access record's status instead. |
| *has written nothing at all since it started, so it probably logs elsewhere* | The application logs to a file inside the container, or to a collector. Only stdout and stderr can be quoted. Point its logger at stdout. |
| *log driver cannot be read back* | The container runs a driver Docker cannot replay (`syslog`, `fluentd`, `gelf`). |
| *the container that served this request is gone* | It was removed, or the address now answers for a container started after the request. A container that merely stopped is still quoted — Docker answers `docker logs` for it. proximo refuses to quote whichever container holds the address now. |
| *this stack records no access log* | Version skew, not an absence of errors: no route produces an Exchange at all. The line carries its Remedy, `proximo update`, and it is the developer's to run. |
| *this stack records no Incident* | Version skew again: the running watcher publishes no Incident API, so nothing reports that a container exited or restarted. Remedy `proximo update`, the developer's to run. |
| *proximo remembers this Incident, not what … wrote* | proximo kept the Incident — runtime metadata — and the container's output is gone with the container, or the one answering to that name now started after it. There is nothing left to read back, and proximo will not substitute another container's output. |

When a Transcript names a replica count — *1 of 3 replicas* — read "it only
happens sometimes" as a fact about **that** container before reading it as a
race: one replica running stale config produces exactly that symptom.

## Credentials

A Transcript is raw application output. **proximo redacts nothing**, because
redacting is interpreting and a redactor that misses is worse than none. It may
carry tokens, connection strings or personal data, and quoting it into your
context sends it to a model API. Say so to the developer before pasting one into
a file, an issue, or a message.
