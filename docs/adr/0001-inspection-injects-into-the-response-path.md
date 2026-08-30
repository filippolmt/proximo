---
status: accepted
---

# Client reports are captured by a proximo hop that rewrites the response

To tell a developer — or an agent — whether a broken page is the backend's fault
or the front-end's, proximo needs both halves of an Exchange, and the two halves
only join if one component mints the correlation id, writes it into the Access
record, and hands it to the page. So Inspection is done by a small proximo-owned
Go service placed in the request path of routes that opt in: it injects a
reporting agent into HTML responses, serves the reserved path `/.proximo/`,
ingests what the agent sends, and owns both ends of the id.

The binding constraint is that this must work on **any** stack that serves HTML —
Rails, Django, Laravel, PHP, Go templates, a static build, any SPA — not only on
the ones with a JavaScript dev server. That single requirement is what puts the
mechanism in the proxy: the proxy is the only place every project already passes
through.

## Considered Options

- **A framework dev-server hook.** Vite 8 ships `forwardConsole`, which sends
  browser console output and unhandled errors to the dev-server terminal with
  source-mapped stack traces, explicitly for AI agents that cannot see the
  browser. Rejected as the mechanism: it exists only for Vite projects, and it
  cannot see the server side of the exchange. It remains the lighter option for
  anyone whose projects are all Vite, and the docs should say so.
- **A Traefik body-rewrite plugin.** These do exist for v3, including one with
  gzip support, so injection alone is solvable this way — but injection was the
  easy half. A plugin cannot mint the correlation id, cannot record the Access
  record, and cannot serve the ingest endpoint, so the hop would be needed
  anyway, with a Yaegi plugin to configure and version on top of it.
- **A third-party collector — Bugsink or GlitchTip — in the observability
  profile.** This is the pattern proximo already uses for Dozzle and Beszel, and
  it would supply a UI, grouping and search for free. Rejected because the
  third-party half does not cover the reason to build this at all: such a
  collector knows nothing about the backend, has nowhere to put an Access record,
  and Bugsink ignores replay events, so the Snapshot would be lost too. Buying it
  means buying a human UI and giving up correlation — the two things that would
  otherwise argue for just running a local Sentry.
- **OpenReplay.** Rejected on scale: 2 vCPU, 8 GB RAM and 50 GB of disk minimum,
  ClickHouse plus microservices, against a stack that today is three small
  containers.
- **A `<script>` tag the developer adds to their own app.** Rejected: it destroys
  the only reason to build this inside proximo rather than reaching for a local
  Sentry — that a developer's own projects stay untouched.
- **`@sentry/browser` as the injected agent.** Chosen first, then reversed, and
  the reversal is the more useful record. The argument for it was that
  normalising errors across engines is fiddly work already done. That turned out
  to be work proximo does not need: `window.onerror` hands over the message, file,
  line, column and the `Error` object, and `error.stack` is a string the engine
  already formatted — and the consumer here is a person or an agent reading text,
  for whom the raw stack is at least as good as normalised frames. What the choice
  did cost was concrete: its npm package ships no browser bundle, so the artifact
  had to be built with esbuild and **committed**, then kept in step with a pin, a
  Renovate manager and two guard tests; the hop had to parse an envelope format
  proximo does not own; and two couplings could break in silence — one of which
  already had, dropping every breadcrumb because the SDK sends a bare array where
  the documented format has a wrapper. Writing the agent removed all of it and
  made the codebase smaller. The lighter Sentry-compatible clients were checked
  too — `@micro-sentry/browser` is 2 kB but ESM-only across two packages, targets
  the legacy `/store/` endpoint, and installs no global handlers, so it keeps
  every cost and removes none of the work.

## Consequences

- proximo modifies the HTML a project produced. This is why Inspection is opt-in
  per container (`proximo.inspect`) and never opt-out: everything else in proximo
  that is opt-in merely adds routing, whereas this changes content.
- The injected agent is proximo's own, and the report format is proximo's own.
  It is served from a URL carrying a digest of its content, so it is cached
  immutably and a page pays for it once per proximo version. Chrome is the
  supported browser: the agent leans on what `window.onerror` already hands over,
  including the `Error` whose `stack` the engine has formatted, and the raw stack
  is always kept so one proximo cannot parse into frames is still shown.
- Because it must work on any stack, the hop cannot assume a permissive page. It
  drops the browser's `Accept-Encoding` so the only encoding offered upstream is
  the gzip Go's own transport adds and unwraps, which keeps injection working
  even against a backend that compresses regardless, and it reconciles the
  response's
  `Content-Security-Policy` on two axes, because a policy can defeat Inspection by
  refusing to load the agent *or* by refusing to let it report. For loading it
  carries the page's own nonce onto the injected tag where there is one, and
  otherwise **relaxes `script-src` with a minted nonce** — a nonce, not a source,
  because it is the one thing that works under `'strict-dynamic'` and under a
  hash-only policy alike. For reporting it widens `connect-src` with `'self'`,
  the tunnel being same-origin. Either way it **says so**: on the Exchange, which
  `proximo errors` prints, and against the host, which `proximo status` prints for
  as long as the route is inspected — the second is kept outside the ring buffer
  precisely so eviction cannot hide it. Silently editing a security header was
  rejected outright: the relaxation is confined to routes the developer opted in,
  disappears with the label, and is never invisible.
- Inspected routes gain an extra hop, so they pay latency and acquire a new way to
  fail. Both are bounded to the routes the developer explicitly opted in.
- The label is refused, with a warning, where it cannot be honoured: on a TCP
  (SNI) route, which has no response body to inject into, and on a merged replica
  set, because the hop is told one backend by one header and inventing a
  multi-backend header format is not worth it for the rare case. A refusal is
  never silent — `proximo status` shows it against the route.
- proximo reserves `/.proximo/` on the project's own origin. A project serving that
  prefix collides with the collector.
- Exchanges live in an in-memory ring buffer bounded by bytes, not by count, and
  are lost on `proximo up`. That is deliberate: a Client report carries exception
  messages, Breadcrumbs and a Snapshot of the page, which in development routinely
  hold tokens, session data and whatever the developer was working on. Keeping
  that off the developer's disk removes any need for retention, rotation or
  cleanup on `uninstall`. Moving to disk later is possible; un-writing data from
  users' disks is not.
- Interactive inspection — a DOM tree to click through, an element picker, step
  debugging, screenshots — is not provided and is not delegated elsewhere either.
  It is simply out of reach of an injected script, and proximo does not pretend
  otherwise.
