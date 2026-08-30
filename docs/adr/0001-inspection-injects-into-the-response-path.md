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

## Consequences

- proximo modifies the HTML a project produced. This is why Inspection is opt-in
  per container (`proximo.inspect`) and never opt-out: everything else in proximo
  that is opt-in merely adds routing, whereas this changes content.
- The injected agent is `@sentry/browser` pointed at `/.proximo/` with its
  `tunnel` option, not code of ours. It already captures exceptions, rejections
  and Breadcrumbs (console, fetch/XHR, clicks, navigation) and is
  source-map-aware. The cost is that the hop must parse the Sentry envelope wire
  format — public and documented — and that ~30 KB over the wire is added to every
  inspected page. The Snapshot rides along as an envelope attachment, since the
  SDK does not capture DOM without its replay product.
- Because it must work on any stack, the hop cannot assume a permissive page. It
  drops the browser's `Accept-Encoding` so the only encoding offered upstream is
  the gzip Go's own transport adds and unwraps, which keeps injection working
  even against a backend that compresses regardless, and it reconciles the
  response's
  `Content-Security-Policy`: it carries the page's nonce onto the injected tag
  where there is one, and where the policy still would not admit the agent it
  **relaxes `script-src` and says so** — a warning in `proximo errors` and in
  `proximo status`, for as long as the route carries the label. Silently editing a
  security header was rejected outright: the relaxation is confined to routes the
  developer opted in, disappears with the label, and is never invisible.
- Inspected routes gain an extra hop, so they pay latency and acquire a new way to
  fail. Both are bounded to the routes the developer explicitly opted in.
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
