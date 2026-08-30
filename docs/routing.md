# Routing — exposing a container

[← back to docs index](README.md)

Routing is **opt-in**: a container is exposed only when you label it. The
primary path is the small **proximo-native** label set; native Traefik labels
keep working for advanced cases.

## The proximo labels

| Label | Required | Default | Meaning |
| --- | --- | --- | --- |
| `proximo.hosts` | **yes** | — | Comma-separated hostname(s) under the configured TLD. **Its presence opts the container in.** |
| `proximo.port` | no | auto-detected | Backend port. Omit when the image `EXPOSE`s exactly one port. |
| `proximo.enable` | no | `true` | Opt-out switch. Set to `false`/`0`/`no` to park the container. |
| `proximo.redirect` | no | `false` | Opt in to an HTTP→HTTPS redirect for the container's hosts. Truthy: `true`/`1`/`yes`. |
| `proximo.path` | no | — | Path **prefix** (must start with `/`) scoping the routes, so several containers can share one host on distinct prefixes. Invalid values skip the container. |
| `proximo.path.strip` | no | `false` | Strip the matched prefix before the backend (so `/api/users` arrives as `/users`). Truthy: `true`/`1`/`yes`. |
| `proximo.health` | no | `true` | Gate routing on the container's Docker healthcheck: a container that declares one is routed only while `healthy`. Set to `false`/`0`/`no` to route as soon as it is running. No effect on containers without a healthcheck. |
| `proximo.auth` | no | — | Require HTTP basic auth. Comma-separated `user:password` pairs; plaintext passwords are hashed on disk. A pair missing `:` is skipped with a warning. |
| `proximo.cors` | no | — | Add CORS response headers. `true` for permissive CORS, or a comma-separated allowed-origin list. A blank value is skipped with a warning. |
| `proximo.header.<Name>` | no | — | Add a custom response header `<Name>: <value>`. Repeatable; an invalid header name is skipped with a warning. |
| `proximo.inspect` | no | `false` | Serve the container's HTTP routes through the Inspection hop, which injects a reporting agent into HTML responses and records what the browser reports. Truthy: `true`/`1`/`yes`. HTTP-only; ignored on TCP routes and on replica sets. See [Inspection](observability.md#inspection--what-the-browser-saw). |
| `proximo.tcp.port` | no | — | Route the container's hosts over **TCP-over-TLS by SNI** on the given backend port (for DBs, gRPC, MQTT, HTTPS backends). Invalid values are skipped with a warning. |
| `proximo.tcp.ports` | no | — | Comma-separated form of `proximo.tcp.port`. Note: SNI routes by host only, so several ports on one host cannot be told apart — give each TCP service its own host. |
| `proximo.tcp.tls` | no | `terminate` | TLS mode for TCP routes: `terminate` (proxy terminates with the per-host proximo cert, forwards plaintext) or `passthrough` (proxy routes the raw TLS stream by SNI; the backend terminates). |

Minimal example — the port is auto-detected because `traefik/whoami` exposes a
single port:

```yaml
services:
  whoami:
    image: traefik/whoami
    labels:
      - "proximo.hosts=whoami.test"
```

```sh
docker compose up -d
open https://whoami.test     # trusted HTTPS, no warning
proximo status
```

## The two hosts every route gets

Every proximo route answers on **two** hosts, always, with no label to write and
no switch to turn off:

| | Example | Contested? |
| --- | --- | --- |
| **Bare host** — what you declared | `api.test` | yes: one container serves it |
| **Qualified host** — with the Namespace inserted | `api.shop.test` | never |

The **Namespace** is the container's Compose project name (`shop` above,
`_` rewritten to `-`), so the qualified host needs nothing from you beyond
already using Compose. It is derived from the *declared host*, not the container
name, so every replica of a scaled service shares it.

```sh
$ proximo status
CONTAINER   URL
shop-api-1  https://api.test  + api.shop.test
```

Both hosts go into the same router rule and the same certificate, so both are
trusted HTTPS from the first request. Because the qualified host can never be
taken from a container by another claimant, it is the name to put in a README, a
`.env`, or a colleague's bookmark.

Two cases get **no** qualified host:

- **A container outside a Compose project** — there is no project name to insert,
  and it is the one case with no safety net. The remedy is short: put it in a
  Compose project.
- **A declared host outside the configured TLD** (`api.example.com`) — the local
  resolver answers for `<tld>` only, so a qualified form of it would never
  resolve.

The stack's own routes (`traefik.<tld>`, and `logs`/`metrics` under
[observability](observability.md)) stay unqualified: the stack is not a project.

A collision inside **one** project is the one case the qualified host cannot
soften — two containers of `shop` claiming `api.test` also claim
`api.shop.test`, so the loser is left with nothing and `proximo status` says so.
Give one of the two a different `proximo.hosts`.

> **Cookie scope.** Under `api.shop.test` an app served at `shop.test` can set
> `Domain=shop.test` and be sent that cookie by every qualified host of the
> project — whereas `api.test` and `db.test` are isolated origins, because
> browsers refuse `Domain=test` for a single-label TLD. Inside one project that
> is usually what you want; it is a deliberate trade
> ([ADR 0003](adr/0003-every-route-answers-on-a-qualified-host.md)).

## proximo.hosts — opt in and pick the host(s)

Declaring `proximo.hosts` is the **only mandatory step** to be routed; no
separate enable label is needed.

```yaml
labels:
  - "proximo.hosts=app.test, api.test"   # both route to this container
```

- Multiple hosts are comma-separated; all of them route to the container and all
  land in the container's certificate SANs.
- Surrounding whitespace is trimmed and empty entries are ignored
  (`app.test,,  ,api.test` → `app.test`, `api.test`).
- Hostnames are validated to a hostname character set; invalid entries are
  rejected and a warning is logged (see
  [where to read watcher warnings](troubleshooting.md#where-to-read-watcher-warnings)) —
  this also prevents injection into the generated config.
- Use hostnames under the configured TLD (default `.test`) so DNS resolves them.
- Each host under the TLD also gets its qualified counterpart
  ([the two hosts every route gets](#the-two-hosts-every-route-gets)):
  `app.test, api.test` in project `shop` is served on four names.
- Declaring a host that proximo would have generated (`api.shop.test`) is
  allowed and wins: proximo withdraws its own generated name and reports the
  withdrawal in `proximo status`.

> **Reserved host.** `traefik.<tld>` (e.g. `traefik.test`) is reserved for the
> stack's own Traefik dashboard and must not be claimed via `proximo.hosts` —
> the dashboard route is injected by the watcher on every reconcile, so a
> container claiming it collides with the stack's router.

> **Collisions are reported, not resolved.** When two containers claim one bare
> host, one of them serves it and the other is listed in `proximo status` with
> the reason and the name of the container that won — it keeps every other host
> it declared and stays reachable at its qualified host. See
> [a host collision is reported](troubleshooting.md#a-host-collision-is-reported).

## proximo.port — usually you can omit it

The backend port is resolved as:

1. `proximo.port` if set, else
2. the single exposed TCP port, auto-detected by inspecting the container, else
3. **skipped** — when the container exposes zero or several ports and no
   `proximo.port` is given, the container is not routed and a warning describing
   the ambiguity is logged (see
   [where to read watcher warnings](troubleshooting.md#where-to-read-watcher-warnings)).
   `proximo status` reflects this: it shows the
   container flagged (`⚠ set proximo.port`) rather than as a working route, since
   the watcher does not actually serve it.

```yaml
labels:
  - "proximo.hosts=app.test"
  - "proximo.port=8080"      # needed only when EXPOSE is 0 or many ports
```

## proximo.enable — temporary opt-out

Defaults to `true`. Park a container without deleting its labels:

```yaml
labels:
  - "proximo.hosts=app.test"
  - "proximo.enable=false"   # not routed until you flip it back
```

Falsy values are `false`, `0`, `no` (case-insensitive). Anything else (or
absence) means enabled.

## proximo.redirect — opt in to the HTTP→HTTPS redirect

Defaults to `false`. By default a routed container is served on HTTPS only:
`https://<host>` routes, while a plain `http://<host>` request is **not**
redirected and **not** served — Traefik returns 404 on `:80` for that host
(the proxy still listens on `:80`, it just has no router for the host). Opt in
per container to redirect HTTP to HTTPS:

```yaml
labels:
  - "proximo.hosts=app.test"
  - "proximo.redirect=true"   # http://app.test -> https://app.test (302)
```

Truthy values are `true`, `1`, `yes` (case-insensitive); anything else (or
absence) leaves the redirect off. When enabled, the watcher writes an extra
`web`-entrypoint router with a `redirectScheme` middleware for the container's
hosts; the redirect is a 302 (non-permanent) so removing the label later is not
sticky in browser caches. The HTTPS router is unchanged.

> **BREAKING (behavior change).** Earlier versions redirected **every** host
> from HTTP to HTTPS globally. That global redirect is gone — the redirect is now
> opt-in per container. A host that relied on the automatic redirect must add
> `proximo.redirect=true` to keep it; the one-line fix is the label above. This
> takes effect on the next `proximo update`/`up`.

## proximo.health — wait for the container to be healthy

Defaults to `true`. When a container declares a Docker `HEALTHCHECK`, proximo
publishes its route and certificate **only while it reports `healthy`**, and
withdraws the route when it turns `unhealthy`. This closes the window where a
container is up but still booting (DB migrations, JIT warmup, slow start) and
would otherwise answer `502`/`503` until it is actually ready.

```yaml
services:
  app:
    image: my/app
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/healthz"]
      interval: 5s
      retries: 5
    labels:
      - "proximo.hosts=app.test"   # routed only once health == healthy
```

- A container **without** a healthcheck is unaffected: it routes as soon as it
  is running, exactly as before. The gating is strictly additive.
- While a health-gated container is starting, `proximo status` lists it as
  `starting (waiting for healthy)` — recognized and opted in, just not serving
  yet — so "not ready" is distinct from "misconfigured/absent".
- The watcher reacts to Docker `health_status` events, so the route appears
  within moments of the container turning healthy, not at the next 30s resync.
- Opt out with `proximo.health=false` (truthy off-values `false`/`0`/`no`) when
  the healthcheck is stricter than "can serve HTTP" (e.g. it waits for a warm
  cache); the container then routes on running regardless of health.

```yaml
labels:
  - "proximo.hosts=app.test"
  - "proximo.health=false"   # route on running, ignore the healthcheck
```

> A broken healthcheck never reaches `healthy`, so the route never appears.
> `proximo status` shows the container as `starting` (not silently missing);
> `proximo.health=false` is the escape hatch.

## proximo.path — split one host across containers

By default a host maps wholesale to one container. `proximo.path=<prefix>` scopes
a container's routes to a URL **prefix**, so several containers can share one host
under different prefixes — the classic SPA-plus-API split:

```yaml
services:
  frontend:
    image: my/spa
    labels:
      - "proximo.hosts=app.test"          # serves everything else: app.test/
  backend:
    image: my/api
    labels:
      - "proximo.hosts=app.test"
      - "proximo.path=/api"               # app.test/api/... routes here
      - "proximo.path.strip=true"         # backend sees /users, not /api/users
```

- The match is a **prefix**, not an exact path: `proximo.path=/api` matches
  `/api`, `/api/`, `/api/users`, etc. (`proximo.path=/` is equivalent to no path.)
- The prefix **must start with `/`**; an invalid value skips the container and
  logs a warning (see
  [where to read watcher warnings](troubleshooting.md#where-to-read-watcher-warnings)).
- The most specific (longest) prefix wins automatically — `/api/v2` beats `/api`
  beats a bare host — so you never tune Traefik priorities by hand.
- `proximo.path.strip=true` removes the matched prefix before the request reaches
  the backend (off by default, since many backends expect the full path).
- A bare container with no `proximo.path` (the `frontend` above) keeps matching
  **all** paths for its hosts, so it naturally serves as the fallback.
- Two containers claiming the same host **and** the same prefix collide; the
  lexicographically-first container name serves it and the other is reported by
  `proximo status` — the same host-by-host resolution as any other
  [collision](troubleshooting.md#a-host-collision-is-reported).

`proximo status` lists each container with its prefix in the URL
(`https://app.test/api`), so you can see the split at a glance.

## proximo middlewares — auth, CORS, custom headers

Three curated labels reproduce the edge behavior you usually need in front of a
dev container without hand-writing `traefik.*` middleware blocks. They are opt-in
and compose: a router can carry all three, applied in the fixed order
**auth → CORS → custom headers**. Each validates independently — a malformed
value invalidates only *that* middleware (skipped with a warning), leaving the
container's routing and its other middlewares intact.

```yaml
services:
  api:
    image: my/api
    labels:
      - "proximo.hosts=api.test"
      - "proximo.auth=alice:s3cret, bob:hunter2"  # basic auth, two users
      - "proximo.cors=true"                        # permissive CORS
      - "proximo.header.X-Env=dev"                 # X-Env: dev on responses
```

**`proximo.auth`** — comma-separated `user:password` pairs. Requests without
valid credentials get `401`; valid ones are forwarded. Plaintext passwords are
**bcrypt-hashed when written to the proxy config**, so the dynamic file never
stores a cleartext secret. A value already in htpasswd hash form (`$2y$`/`$2a$`/
`$2b$`/`$apr1$`/`$1$` prefix) is passed through unchanged — use it to keep the
plaintext out of your compose file entirely.

> **Security note.** The password you put in the label is visible in
> `docker inspect` (Docker stores labels verbatim). proximo hashes it *on disk*
> in the proxy config, not in the label. This is a dev-time tool; for a host you
> care about, pass a pre-hashed value so no cleartext lives anywhere.

**`proximo.cors`** — `true` (or `1`/`yes`) emits permissive CORS response
headers (all origins, common methods, any header) and answers `OPTIONS`
preflight. Scope it instead with a comma-separated origin list, e.g.
`proximo.cors=https://app.test`, to advertise only those origins.

**`proximo.header.<Name>`** — adds a custom response header. Repeat the label
for several headers (`proximo.header.X-Env=dev`,
`proximo.header.X-Region=eu`); they accumulate.

`proximo status` shows a **MIDDLEWARES** column listing the active middlewares
per container so you can confirm what is wired.

> **The curated set is deliberately small.** Rate limiting, retries,
> forward-auth, IP allowlists and the rest of Traefik's catalog are out of scope
> — raw `traefik.*` middlewares remain the escape hatch (see
> [Native Traefik labels](#native-traefik-labels-backward-compatible)).

## proximo.inspect — see what the browser saw

An inspected route is served **through a proximo hop** instead of straight to
your container. The hop injects a small reporting agent into HTML responses, and
records the request it served alongside whatever the browser reported while that
page was live — an uncaught exception, a rejected promise, a CSP violation, the
console and network breadcrumbs that led up to it, and a snapshot of the DOM at
the moment it broke.

```yaml
services:
  web:
    image: node:22
    labels:
      - "proximo.hosts=web.test"
      - "proximo.inspect=true"
```

```sh
proximo errors --host web.test
```

It is **opt-in and never opt-out**, because it is the one proximo label that
changes the bytes your project sent: every other label only adds routing. It
applies to HTTP routes only — a TCP (SNI) route has no response body to inject
into, so the label is ignored with a warning — and it is refused for a
[replica set](#round-robin-across-replicas), because the hop forwards to a single
backend; scale the service to one replica to inspect it.

Read [Inspection](observability.md#inspection--what-the-browser-saw) for what is
captured, what is deliberately not, and where the data lives.

## proximo.tcp.port — route TCP services by name (SNI)

HTTP routing multiplexes every host on `:443` by the `Host` header, but raw TCP
services (Postgres, Redis, MySQL, gRPC, MQTT, HTTPS backends) have no such key.
Because those services speak TLS, proximo routes them by the connection's **TLS
SNI** — the hostname in the ClientHello — on the *same* `:443` entrypoint, so a
DB is reachable by name with no extra port and no host-port collisions between
parallel stacks.

```yaml
services:
  db:
    image: postgres:17
    environment: { POSTGRES_PASSWORD: dev }
    labels:
      - "proximo.hosts=db.test"
      - "proximo.tcp.port=5432"     # TCP-over-TLS by SNI, no new port
```

```sh
docker compose up -d
# Connect on :443 (where the SNI router listens), not the backend port 5432.
# db.test resolves to 127.0.0.1; SNI db.test routes the TLS stream to the backend's 5432.
psql "postgresql://postgres:dev@db.test:443/postgres?sslmode=require"
proximo status                       # lists the TCP route + its TLS mode
```

- **The client must use TLS with SNI.** SNI is the only routing key, so the
  client connects to `db.test:443` with SNI `db.test` (e.g. `sslmode=require`).
  Plain TCP without TLS/SNI has no key and is **not** supported (nor is UDP) —
  publish those directly with `-p` and use the free `*.test` DNS name.
- **TLS mode.** By default proximo **terminates** TLS at the proxy using the
  per-host certificate its CA already issues (the same trust as HTTPS) and
  forwards **plaintext** to the backend — zero TLS setup in the container. Set
  `proximo.tcp.tls=passthrough` when the backend must terminate TLS itself.
- **Hosts route, not ports.** Each host in `proximo.hosts` routes by SNI to the
  declared port. Since SNI carries only the hostname, give each TCP service its
  own host; declaring several ports on one host cannot be disambiguated by SNI.
- **Coexists with HTTP.** A connection whose SNI matches a TCP route is served by
  it; every other SNI falls through to the HTTP routers on the same `:443`.

## Round-robin across replicas

Two or more containers declaring the **same host and the same backend port** —
HTTP (`proximo.port`) or TCP (`proximo.tcp.port`) — are treated as replicas of one
service: proximo emits a single router whose load balancer carries one server per
container and distributes traffic round-robin. A lone container is unchanged (one
server). Containers on the same host and the same path that differ in middleware or
redirect are **not** merged — they still resolve deterministically as a collision (see
[proximo.path](#proximopath--split-one-host-across-containers)). `proximo status`
marks a balanced route with `(balanced ×N)`.

## What happens behind the scenes

For each routed container the watcher:

- writes a Traefik HTTP router + service targeting `http://<container-name>:<port>`,
  or, for a TCP-labeled container, a TCP router matching `HostSNI(<host>)` + service
  targeting `<container-name>:<port>` on the shared `:443` entrypoint,
- collapses replicas (same host + port) into one router/service with a server per
  backend,
- issues one CA-signed certificate whose SANs are exactly that container's hosts,
- ensures DNS resolves those hosts to `127.0.0.1`.

So the hostnames you declare are exactly the ones that resolve **and** the ones
the certificate covers — HTTPS is trusted with no browser warning. See
[Architecture](architecture.md#the-watcher) for the reconcile loop.

## Native Traefik labels (backward compatible)

Existing setups using native `traefik.*` labels keep working unchanged — the
Docker provider stays enabled. A container is still routed when it sets
`traefik.enable=true` and a `Host(...)` router rule, and the watcher issues its
certificate the same way.

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.web.rule=Host(`web.test`)"
  - "traefik.http.services.web.loadbalancer.server.port=80"
```

Reach for raw `traefik.*` labels when you need features not expressible with
proximo labels — middlewares beyond the curated set, exact-`Path` or regex
rules, header matching. The common edge needs no longer require them: use
[`proximo.path`](#proximopath--split-one-host-across-containers) for the
SPA-plus-API split and the
[proximo middlewares](#proximo-middlewares--auth-cors-custom-headers) for basic
auth, CORS, and custom response headers. You can mix schemes across containers
freely.

> **Avoid declaring the same host in both schemes.** If a host appears in both a
> `proximo.hosts` label and a native `traefik.*` router rule, Traefik sees a
> duplicate router across providers; the watcher logs a warning (see
> [where to read watcher warnings](troubleshooting.md#where-to-read-watcher-warnings)).
> Use one scheme per host.

## Multiple networks

When a container is attached to several Docker networks, select the one Traefik
should use to reach it:

```yaml
labels:
  - "proximo.hosts=app.test"
  - "traefik.docker.network=<network>"
```

## Quick reference

```yaml
# Single host, auto-detected port
- "proximo.hosts=app.test"

# Multiple hosts
- "proximo.hosts=app.test, api.test"

# Explicit port (image exposes 0 or many)
- "proximo.hosts=app.test"
- "proximo.port=8080"

# Temporarily parked
- "proximo.hosts=app.test"
- "proximo.enable=false"

# Opt in to the HTTP->HTTPS redirect (off by default)
- "proximo.hosts=app.test"
- "proximo.redirect=true"

# Share one host across containers by path prefix
- "proximo.hosts=app.test"
- "proximo.path=/api"          # app.test/api/... -> this container
- "proximo.path.strip=true"    # strip /api before the backend (optional)

# Middlewares: basic auth, CORS, custom response headers (compose in order)
- "proximo.hosts=api.test"
- "proximo.auth=alice:s3cret"  # plaintext hashed on disk; or pass a $2y$ hash
- "proximo.cors=true"          # or a comma-separated allowed-origin list
- "proximo.header.X-Env=dev"   # repeatable

# TCP service by name (SNI) — e.g. a database; client uses TLS+SNI to <host>:443
- "proximo.hosts=db.test"
- "proximo.tcp.port=5432"      # default: proxy terminates TLS, plaintext to backend
- "proximo.tcp.tls=passthrough"  # optional: backend terminates TLS end-to-end

# See client-side errors, correlated with the response that caused them
labels:
  - "proximo.hosts=web.test"
  - "proximo.inspect=true"

# Advanced: native Traefik labels
- "traefik.enable=true"
- "traefik.http.routers.web.rule=Host(`web.test`)"
- "traefik.http.services.web.loadbalancer.server.port=80"
```
