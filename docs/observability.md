# Dev-time observability

[← back to docs index](README.md)

An **opt-in** local observability stack — container **logs** and **metrics** —
surfaced behind the same trusted `https://<name>.<tld>` hostnames proximo already
gives your containers. It is **off by default** and adds nothing to the core
stack unless you ask for it.

| Dashboard | URL | What it is |
| --- | --- | --- |
| Logs | `https://logs.<tld>` | [Dozzle](https://dozzle.dev) — realtime log viewer for every running container. |
| Metrics | `https://metrics.<tld>` | [Beszel](https://beszel.dev) — per-container CPU / memory / network / disk, with history. |

(`<tld>` is your configured TLD, `test` by default — so `https://logs.test` and
`https://metrics.test`.)

Both dashboards opt into the HTTP→HTTPS redirect, so a plain `http://logs.<tld>`
or `http://metrics.<tld>` is redirected to the CA-trusted `https://` host
automatically.

## Start it

```sh
proximo up --observability
```

This brings up the **core stack and the observability services together** in one
one-shot command, then prints the dashboard URLs:

```
Proximo stack started with observability.
  Logs:    https://logs.test
  Metrics: https://metrics.test
```

The first run is heavier (three extra images pulled + a one-time metrics
bootstrap); later runs reuse the cached images and the stored registration.

Without the flag, `proximo up` (and `install`) start only `traefik` / `dns` /
`watcher` — no Dozzle, no Beszel.

## How it is wired

- **No new routing code.** The three services are plain containers carrying the
  ordinary `proximo.hosts` / `proximo.port` labels and **no** `proximo.role`
  label, so the watcher routes them and issues a CA-signed certificate exactly
  like any container you label yourself. They appear in `proximo status` as real
  routes.
- **A compose profile keeps them opt-in.** They live in the same embedded
  compose project under a `observability` profile, so one `up --observability`
  activates them and one `down` tears them down — no second project to manage.

## Credential-less access (local only)

Because everything runs on your own machine, the dashboards open with **no login
to type**:

- **Dozzle** runs with authentication disabled.
- **Beszel** is built on PocketBase and cannot fully disable auth, so proximo
  seeds a fixed local user (`proximo@proximo.<tld>` — a dotted domain PocketBase's
  email validation accepts) and enables **auto-login**, landing you straight in
  the dashboard.

> Dozzle has the Docker socket mounted, so it *can* exec into / stop containers.
> That is accepted for a local dev box.

## No hardcoded secret

The proximo binary ships **zero credentials**. On the first
`up --observability`, the Beszel password is generated with `crypto/rand` and
written to your per-user config dir at `0600` — exactly like the local CA private
key. Later runs reuse the stored secret; they never regenerate it.

The metrics agent registers with the hub automatically: proximo brings the hub
up, authenticates the seeded user, retrieves the hub public key and a universal
registration token, injects them into the agent, and brings the agent up. There
is **no manual "add system" step**, and the registration is idempotent across
repeat runs.

## Tear it down

```sh
proximo down                  # stops the whole stack (core + dashboards)
proximo down --observability  # stops only the dashboards, leaves the core up
proximo uninstall             # removes everything and the generated secret, then
                              # reverses all host changes and deletes the
                              # ~/.proximo home (which holds the metrics data)
```

## Logs, metrics & retention

**Dozzle stores nothing.** It is a live viewer that streams container logs from
the Docker socket — close the tab and nothing is persisted. What you can scroll
back to is whatever the Docker daemon's log driver still holds for that
container; there is no Dozzle-side retention to configure.

**Beszel keeps metrics history** in the `~/.proximo/data/beszel` bind mount, so
it survives `down` / `up` and is removed only by `uninstall`. Beszel
auto-downsamples old records to coarser resolutions internally; those retention
windows are **not configurable via environment variables** (the hub exposes none
for it), so proximo offers no knob either. For a handful of local containers the
on-disk footprint stays small.

**Container log caps.** proximo's own stack containers use a small rotated
`json-file` cap (`max-size: 5m`, `max-file: 3` → ~15 MB per container) so the dev
host's logs cannot grow unbounded. **Your own containers are not covered** —
proximo does not manage them, and Dozzle only reads them. To bound their logs set
a daemon-wide cap in `/etc/docker/daemon.json` (size-based only; `json-file` has
no time-based "keep N days" option) and restart Docker:

```json
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "5m", "max-file": "3" }
}
```

…or per service in your own compose under a `logging:` key.

## Transcripts — what the container said

A **Transcript** is what a container wrote in a window, quoted verbatim. What
fixes the window is never a reading of the text: an **Exchange** fixes it most
precisely — the Access record (what the stack saw), the Transcript, and on
inspected routes the Client reports (what the browser saw) — an
[Incident](#incidents--what-the-runtime-declared) fixes it for a container no
request ever reaches, and `--since` fixes it when neither exists. Every route
produces an Exchange: Traefik records an access log, so a route needs no label
and no browser to be diagnosable.

```console
$ curl -sS -o /dev/null https://web.test/checkout
$ proximo errors --host web.test
```

```
14:05:09  1f0c9a2b3d4e5f60  POST /checkout  →  500  41ms
  (no client report — the failure is the backend's)
  transcript of web-1 (1 of 3 replicas):
      panic: assignment to entry in nil map
      … 412 line(s) elided …
      exit status 2
      whole transcript — `proximo errors transcript 1f0c9a2b3d4e5f60`

A transcript is the application's own output, quoted with no redaction: it may carry credentials or personal data. Check before pasting it anywhere.
```

**proximo stores none of it.** A Transcript is read back from the container's
own output at the moment it is printed, and cut to the window of the Exchange.
The store of record stays where it already is — Docker's log driver, bounded and
rotated — and proximo holds nothing. Nothing about application logs is ever
written to disk by proximo.

The cut is **temporal, not causal**: it says *what the container wrote in this
window*, never *what this request caused*. Where two Exchanges of one container
overlap, proximo reports the overlap rather than attributing a line — see
[A transcript is empty or says the container is
gone](troubleshooting.md#a-transcript-is-empty-or-says-the-container-is-gone)
for that and the three silences it tells apart.

A Transcript is quoted inline only beside a row with something to say — a
failing status, a Client report, a warning proximo raised, or an Incident — and
tightly capped, keeping both ends and declaring what it elided in between.
`proximo errors transcript <id>` prints the whole of it.

### Credentials

A Transcript is raw application output. It may carry tokens, connection strings
or personal data, and **proximo redacts nothing** — redacting is interpreting,
and a redactor covering most patterns produces false confidence exactly where an
unrecognised format slips through. What proximo owes instead is to say so, which
it does in the CLI and in the [agent Skill](skill.md). Transcripts are handed to
agents, and an agent sends its context to a model API: check one before pasting
it anywhere.

## Incidents — what the runtime declared

A worker, a queue consumer, a migration job: no host, no request, therefore no
Exchange — and until something fixes a window there is nothing to quote. What
fixes it is an **Incident**: a fact the runtime declares about a container, and
never a line proximo read.

```yaml
services:
  worker:
    build: .
    command: ./worker
    labels:
      # Observe me. This routes nothing: no host, no certificate, no DNS name.
      - "proximo.transcript=true"
```

Four things are Incidents, and that is the whole list:

| Incident | Where it comes from | In the default listing |
| --- | --- | --- |
| `exited <code>` | the container's main process exited non-zero | yes |
| `exited <code> (OOM-killed)` | the same exit, with the kernel's `oom` event folded into it | yes |
| `restarted` | the container was restarted | yes |
| `turned unhealthy` | the Docker healthcheck went from healthy to unhealthy | only under `--service` |

Unhealthy is out of the default listing on purpose: a worker waiting on postgres
is unhealthy for twenty seconds on every `compose up`, and a listing full of that
noise stops being read exactly when it is needed.

Nothing here reads the text. An exit code, a restart and an OOM kill are
statements *Docker* makes about the container; deciding that a line looks like an
error would mean interpreting the output a Transcript exists to quote. **No
Incident does not mean no problem** — a worker that is alive and blocked on a
slow query produces none, and proximo will be silent about it.

### Reading them

Incidents appear in `proximo errors`, interleaved with Exchanges in one time
order — because time order is the only thing tying the 14:05:09 checkout to the
worker that died seven seconds earlier.

```console
$ proximo errors --since 15m
```

```
14:05:09  1f0c9a2b3d4e5f60  POST /checkout  →  502  3ms
  (no client report — the failure is the backend's)
14:05:02  9b3e1a7c5d2f8e04  shop/worker  exited 137 (OOM-killed)
  container shop-worker-1
  transcript of shop-worker-1:
      picked up job 41871
      … 96 line(s) elided …
      allocating 2.1 GiB for the import batch
      whole transcript — `proximo errors transcript 9b3e1a7c5d2f8e04`
```

The window a Transcript is cut to runs from the **previous Incident of the same
service** to this one — for a restart loop that is exactly one container
lifetime, which no fixed duration could be: a fixed one truncates the worker that
wrote the useful line five minutes before dying and drowns the one that restarts
every three seconds.

`--service` narrows the listing to one service, qualified (`shop/worker`) or bare
when nothing contests it (`worker`); a contested bare name has its candidates
reported rather than one of them chosen. See
[`proximo errors`](cli.md#proximo-errors).

### What proximo remembers, and what it does not

proximo remembers **Incidents and only Incidents**: tens of bytes of runtime
metadata per container, held in the watcher's memory, capped per service (the
last few) and by age. A Transcript is still never stored — it is quoted from the
container's own output at the moment it prints.

That is a declared price, not an oversight: proximo will tell you the worker was
OOM-killed at 14:02 **and that it can no longer show what it wrote**, because the
container is gone or its log has rotated past that window. It says so in those
words rather than printing an empty quote, and it never substitutes the output of
whatever container answers to that name now.

Incidents are held in memory, so `proximo up` discards them along with the
Exchange buffer.

## Inspection — what the browser saw

Dozzle and Beszel watch containers. Inspection watches **pages**: label a
container `proximo.inspect=true` and its HTTP routes are served through a proximo
hop that injects a reporting agent into HTML responses.

```sh
proximo errors --host web.test
```

```
14:05:09  9f3a21ab  GET /checkout  →  200  184ms
  ✗ TypeError: Cannot read properties of undefined (reading 'total')
      at renderSummary (src/checkout/Summary.tsx:47:18)
      at onMount (src/checkout/Page.tsx:12)
      · error    fetch     GET /api/cart 500
  DOM captured — `proximo errors dom 9f3a21ab`

14:05:09  9f3a2417  GET /api/cart  →  500  1.2s
  (no client report — the failure is the backend's)
```

That pairing is the point. proximo is the only component that sees both halves,
so it can answer the question neither half answers alone: *is the page broken
because the backend answered badly, or because the front-end mishandled a good
answer?* An error tracker cannot — it never sees your backend. A browser's
DevTools cannot — it never sees behind the proxy.

### What is captured

The injected agent is proximo's own: about 200 lines of JavaScript with no
dependencies, 7.8 KB — 2.9 KB over the wire, fetched once per proximo version
because its URL carries a digest of its content. It captures uncaught exceptions, unhandled rejections and policy
violations, each with the stack exactly as the browser wrote it, and the
breadcrumbs before them: every `console.*` call, every `fetch` and XHR, clicks,
navigations, and subresources that failed to load. To each report it adds the
correlation id that joins the two halves of an Exchange and — for the first report
of a page — a snapshot of the DOM.

**Chrome is the supported browser.** Everything here is verified on Chrome; other
engines are likely to work and are not tested. Source maps are not resolved, so
frames point wherever the served code says — in development that is usually the
real file already.

Capture is deliberately wide and **filtering happens at display time**: `proximo
errors` hides breadcrumbs below warning level so framework chatter does not bury
the report, and `--all` shows everything. Nothing is dropped at collection — the
agent sends everything it sees. Past fifty reports on one page the hop stops
keeping them and starts counting them instead, so a render loop cannot push every
other Exchange out of the buffer, and the count is shown with the rest.

### Trying it by hand

An error typed into the browser console is caught by DevTools and never reaches
`window.onerror`, so the agent never sees it. Throw from a task instead —
`setTimeout(function(){ null.foo }, 0)` — or just use the app until it breaks,
which is what this is for.

### What is not

No interactive panel: no DOM tree to click through, no element picker, no step
debugging, no screenshots. Those are out of reach of an injected script, and
proximo does not pretend otherwise — for those, the browser's own DevTools is
still the tool. Minified stack traces are also not resolved through source maps:
dev servers overwhelmingly serve unminified code or inline maps, so the frames
usually already point at real files.

### Where the data lives

In memory in the hop, in a ring buffer bounded by bytes (64 MiB by default),
oldest evicted first — and **lost when the stack restarts**, `proximo up`
included. Reproduce a problem after a restart, not before; `proximo errors` tells
you when a recent restart is why it has nothing to show. That is deliberate:
client reports carry exception messages, breadcrumbs and a copy of the page,
which in development routinely means tokens, session data and whatever you were
working on. Keeping it off your disk means there is no retention to configure and
nothing for `uninstall` to clean up.

The read API is published on `127.0.0.1` only, so `proximo errors` can reach it
and an inspected page cannot. The hop's proxy port is never published at all —
only Traefik reaches it, over the stack network.

### Two things it changes about your route

- **The response body.** The agent tag is inserted before `</head>`. A response
  with no `</head>` is left alone and the Exchange says so.
- **The `Content-Security-Policy`, when it has to.** A policy can defeat
  Inspection twice over: by refusing to load the agent, and by refusing to let it
  report. proximo reuses the page's own nonce where there is one; otherwise it
  relaxes `script-src` with a minted nonce, and widens `connect-src` with `'self'`
  so the same-origin report can go out. It never does either silently — the
  warning appears on every Exchange for that route in `proximo errors`, and
  against the route itself in `proximo status`, for as long as it carries the
  label. The relaxation disappears with the label.

An inspected route also gains a hop, so it pays a little latency and has one more
way to fail. Both are confined to the routes you labelled.

### If you only use Vite

Vite 8 ships [`forwardConsole`](https://vite.dev/config/), which sends browser
console output and unhandled errors to the dev-server terminal with source-mapped
stack traces. If every project you run is a Vite project, that is a lighter way
to get the client-side half — it just cannot see the server side of the exchange,
and it does not exist for Rails, Django, Laravel, Go templates or a static build.

## Notes & limits

- **Pinned images.** `amir20/dozzle`, `henrygd/beszel`, and
  `henrygd/beszel-agent` are pinned in the embedded compose (like Traefik). The
  Beszel hub env vars and API endpoints are upstream behaviors tied to that tag —
  they are verified when the pin is bumped.
- **Host-level metrics reflect the Docker VM**, not your laptop, because the
  agent runs on the compose network (no host networking). The signal that matters
  for development — **per-container** metrics — is unaffected.
- Not intended for production monitoring, alerting, or long-term retention.
