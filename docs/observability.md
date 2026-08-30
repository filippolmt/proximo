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

The injected agent is [`@sentry/browser`](https://docs.sentry.io/platforms/javascript/),
pointed at proximo instead of at Sentry, so what it collects is what that SDK
collects: uncaught exceptions, unhandled rejections, policy violations, and the
breadcrumbs before them — every `console.*` call, every `fetch`/XHR, clicks,
navigations. proximo adds the correlation id that joins the two halves and a
snapshot of the DOM at the moment of the report.

Capture is deliberately wide and **filtering happens at display time**: `proximo
errors` hides breadcrumbs below warning level so framework chatter does not bury
the report, and `--all` shows everything. Nothing is dropped at collection.

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
oldest evicted first — and lost when the stack restarts. That is deliberate:
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
- **The `Content-Security-Policy`, when it has to.** A page whose policy would
  block the injected script has `script-src` relaxed with a nonce — and every
  Exchange for that route carries a warning saying so. proximo never edits a
  security header silently, and the relaxation disappears with the label.

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
