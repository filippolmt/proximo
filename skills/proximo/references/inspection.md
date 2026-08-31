# Read what the browser reported

Reached from [`SKILL.md`](../SKILL.md) when the host answers and the page itself
is wrong.

## What an Exchange is

An **Exchange** is one **Access record** — host, method, status, latency, size,
never bodies — with the **Transcript** of the container that served it and the
**Client reports** raised while the page it served was live. No one part says on
its own whether a broken page is the backend's fault or the front end's, which is
the whole reason all three are collected. A Client report carries the
**Breadcrumbs** that led up to it (console calls, requests, clicks, navigations)
and, once per Exchange, the **Snapshot**: the DOM as it stood when the first
report of that load was raised.

Every route produces an Exchange. **Client reports** are the part that needs
`proximo.inspect=true`, and they are what this branch is about; the backend half
needs no label — [`transcript.md`](transcript.md). **Chrome is the supported
browser** for Client reports — say so early when the developer is on another
engine, rather than after a round of probing.

## Before running `proximo errors`

`proximo status` lists which routes are under Inspection, and anything proximo
had to relax to get there — a `Content-Security-Policy` it rewrote, most often.
When the route is not under Inspection, `proximo errors` still lists its
Exchanges — with no Client report on any of them. The answer is then the label,
not the query.

## Read the structured form, narrowed

```sh
proximo errors --json --host <host> --since 30m
```

- **`--json` is the form to read.** The default layout has a stable field order
  because it is read as often by an agent as by a person, but the structured form
  is the one to parse.
- **Narrow with `--host` and `--since` before raising `--limit`.** Ordering
  follows the *most recent activity*, not the page load: a page served ten
  minutes ago that threw a moment ago sorts above a request served since, and
  `--since` follows the report rather than the load. Widen `--since` when the
  reproduction is older than the window; raise `--limit` last.
- **Reach for `--all` to answer one question**: is this route being served
  through the hop at all? It adds the clean Exchanges and the `debug`/`info`/`log`
  breadcrumbs that are hidden by default — hidden because framework chatter buries
  the report otherwise.

`proximo errors --help` lists the flags and their defaults.

## The Snapshot is a file, and stays one

```sh
proximo errors dom <exchange-id>          # writes it, prints the path
proximo errors dom <exchange-id> -o /tmp/broken.html
```

Query it for the selector, id, or text the Client report already named:

```sh
grep -n 'data-testid="cart-total"' /tmp/broken.html
```

A page's DOM is hundreds of kilobytes, which is why `proximo errors dom` prints a
path and never the content. **Hold to that: query the file, and hand the path to
the developer when the whole page needs an eye on it.** A missing snapshot means
the Exchange was evicted, or no Client report on that page carried one.

## Nothing shows for an inspected route

<!-- generated:start source=docs/troubleshooting.md#proximo-errors-shows-nothing-for-an-inspected-route -->
1. **The label is on and the route is HTTP.** `proximo status` lists the route;
   `proximo.inspect` is ignored on a TCP (SNI) route and on a
   [replica set](https://filippolmt.github.io/proximo/routing.html#round-robin-across-replicas). The watcher logs why —
   see [Where to read watcher warnings](https://filippolmt.github.io/proximo/troubleshooting.html#where-to-read-watcher-warnings).
2. **The page is HTML with a `</head>`.** The agent is inserted before the
   closing head tag. A response without one is left untouched, and the Exchange
   for it carries a warning saying so.
3. **The stack is recent enough to have the hop.** An older stack has no
   `inspector` container, and an inspected route then 502s. `proximo update`.
4. **The page really loaded the agent.** View source and look for
   `/.proximo/agent.js`. If it is in the HTML but the browser did not run it, the
   page's `Content-Security-Policy` blocked it — the Exchange will say proximo
   relaxed the policy, and if it does not, the policy came from somewhere proximo
   cannot rewrite (a `<meta http-equiv>` tag in your own markup).
5. **The Exchange is still held.** The buffer is bounded and in memory, so
   `proximo up` — including the one you ran to pick up a change — throws away
   everything recorded before it. This catches people out: reproduce the problem
   *after* the restart, not before. `proximo errors` says so when the hop came up
   in the last ten minutes.
6. **You are looking in the right window.** `--since` defaults to 15 minutes and
   follows the report, not the page load, so a page opened an hour ago that threw
   a moment ago still appears. `--all` adds the Exchanges with nothing wrong, which
   is worth a look when you want to confirm the route is being served through the
   hop at all.
<!-- generated:end -->

Reference:
<https://filippolmt.github.io/proximo/observability.html#inspection--what-the-browser-saw>
·
<https://filippolmt.github.io/proximo/cli.html#proximo-errors>
