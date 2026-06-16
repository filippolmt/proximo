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

> **Reserved host.** `traefik.<tld>` (e.g. `traefik.test`) is reserved for the
> stack's own Traefik dashboard and must not be claimed via `proximo.hosts` —
> the dashboard route is injected by the watcher on every reconcile, so a
> container claiming it collides with the stack's router.

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
- Two containers claiming the same host **and** the same prefix conflict; the
  lexicographically-first container name wins and the other is logged — same
  deterministic resolution as other host conflicts.

`proximo status` lists each container with its prefix in the URL
(`https://app.test/api`), so you can see the split at a glance.

## What happens behind the scenes

For each routed container the watcher:

- writes a Traefik HTTP router + service targeting `http://<container-name>:<port>`,
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
proximo labels — custom middlewares, exact-`Path` or regex rules, header
matching. Path-**prefix** composition no longer needs them: use
[`proximo.path`](#proximopath--split-one-host-across-containers) for the common
SPA-plus-API split. You can mix schemes across containers freely.

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

# Advanced: native Traefik labels
- "traefik.enable=true"
- "traefik.http.routers.web.rule=Host(`web.test`)"
- "traefik.http.services.web.loadbalancer.server.port=80"
```
