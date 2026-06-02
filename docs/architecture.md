# Architecture — how it works

[← back to docs index](README.md)

`proximo` combines three things developers usually wire up by hand —
`Host`-based routing, local DNS, and a trusted certificate — into one tool. This
page explains how the pieces fit together.

## The big picture

```
            ┌─────────────────────────── your host ───────────────────────────┐
            │                                                                   │
  browser   │   /etc/resolver/<tld>            system + NSS trust stores        │
  https://  │   (macOS)  ─┐                    └─ contain the proximo local CA  │
  app.test  │             │  *.test → 127.0.0.1:5354/udp                        │
     │      │   resolved  ┘                                                     │
     │      │   drop-in (Linux)                                                 │
     │      │                                                                   │
     ▼      │                  ┌──────────── docker compose stack ───────────┐ │
  127.0.0.1 │   :5354/udp ─────┼──▶ dns      (miekg/dns: *.tld → 127.0.0.1)   │ │
  :443 ─────┼──▶ traefik ◀─────┼─── watcher  (labels → dynamic config + certs)│ │
            │      │           │       │  attaches traefik to backend nets    │ │
            │      ▼           └───────┼──────────────────────────────────────┘ │
            │   your container  ◀──────┘  http://<name>:<port>                   │
            │   (proximo.hosts=app.test)                                         │
            └───────────────────────────────────────────────────────────────────┘
```

1. The browser resolves `app.test` through the host resolver, which sends
   `*.<tld>` queries to the local DNS server → `127.0.0.1`.
2. The browser opens `https://app.test`; Traefik (publishing `:443`) terminates
   TLS with a CA-signed certificate your system already trusts.
3. Traefik matches the `Host` header to a router and forwards to your container
   over the Docker network the watcher attached it to.

## The CLI is a one-shot orchestrator

There is **no proximo daemon** on your host. Each CLI command performs its action
and exits:

- `install` → generate CA, configure resolver, install trust, `compose up`.
- `up` / `down` → start / stop the stack.
- `update` → converge the running stack to the installed CLI version (rebuild
  the in-stack images, re-pull Traefik); a soft no-op when Docker/stack is down.
- `config tld` → rewrite the resolver and restart the stack.
- `uninstall` → reverse all host changes, `compose down`.

The only long-running processes are the **stack containers**. They are an
embedded `docker compose` project, materialized to `~/<config>/proximo/stack/`
from assets compiled into the binary (`//go:embed`), with the TLD and DNS port
substituted in.

## The stack: three services

| Service | Image / build | Role |
| --- | --- | --- |
| **traefik** | `traefik:v3.7` | Reverse proxy. Terminates HTTPS on `:443`, redirects `:80` → `:443`, routes by `Host`. Two providers: the **Docker provider** (native `traefik.*` labels) and the **file provider** watching `/etc/traefik/dynamic`. |
| **dns** | built from this repo | Wildcard DNS server (`miekg/dns`). Answers `*.<tld>` → `127.0.0.1`, forwards everything else upstream. Published on `127.0.0.1:5354/udp`. |
| **watcher** | built from this repo | Reads container labels, writes Traefik dynamic config + per-container certificates, and attaches Traefik to backend networks. Mounts the Docker socket and the CA. |

The three share a `dynamic` volume: the watcher writes to it, Traefik's file
provider reads from it.

## DNS

The DNS server (`internal/dns`) is intentionally tiny:

- A query for the TLD or any subdomain of it (`app.test`, `api.test`) →
  authoritative `A` record `127.0.0.1`. Non-`A` types return `NOERROR` with no
  records (the TLD is IPv4 loopback only).
- Every other query is forwarded to an upstream resolver (Cloudflare/Google by
  default).

It publishes on host port **5354/udp** (not 53, to avoid a privileged bind; not
5353, which macOS mDNSResponder already owns). The host resolver points the TLD
at it:

- **macOS** — `/etc/resolver/<tld>` with `nameserver 127.0.0.1` and `port 5354`.
- **Linux** — a `systemd-resolved` drop-in
  (`/etc/systemd/resolved.conf.d/proximo-<tld>.conf`) with
  `DNS=127.0.0.1:5354` and `Domains=~<tld>`, then `systemd-resolved` is
  restarted.

Because DNS answers `*.<tld>`, **which hostnames exist is driven entirely by the
labels you set** — there is no per-host DNS registration.

## TLS and trust

`proximo` runs its own certificate authority so HTTPS is trusted with no browser
warning and no public ACME round-trip.

- **Local CA** (`internal/tls`) — a P-256 ECDSA CA generated on first
  `install` (10-year validity, `IsCA`, path-len 0) and reused afterwards. Stored
  as `tls/ca.pem` + `tls/ca-key.pem` in your config dir.
- **Trust** — the CA is added to the **OS system trust store** (via built-in OS
  tooling) and the **NSS store** (Firefox/Chromium DBs, via `certutil`) when
  present. `uninstall` removes both.
- **Leaf certificates** — the watcher issues **one certificate per routed
  container**, with that container's hostnames as SANs, signed by the CA. Leaf
  validity is capped under **398 days** to satisfy the Apple/Chrome policy.
- **No wildcard** — a single `*.<tld>` is deliberately avoided: browsers reject
  TLD-level wildcards like `*.test`. Exact SANs are used instead.

## The watcher

The watcher (`internal/docker/watcher.go`) runs a reconcile loop — once at
start, then on Docker events, with a 30s safety resync. Each reconcile:

1. **Finds Traefik** (the container labeled `proximo.role=traefik`).
2. **Selects routed containers** — opted in via `proximo.hosts` (and not
   `proximo.enable=false`), or via native `traefik.enable=true`. Stack
   containers (`proximo.role`) are never routed.
3. **Resolves the backend port** — `proximo.port` if set; otherwise the single
   exposed TCP port (auto-detected via `ContainerInspect`); ambiguous cases are
   skipped with a warning.
4. **Attaches Traefik to backend networks** so it can reach the container by
   name, and detaches from networks no longer needed.
5. **Writes Traefik dynamic config** — one HTTP router + service file per
   proximo-labeled container, targeting `http://<container-name>:<port>`. Stale
   files are removed.
6. **Issues per-container certificates** — reissuing only when a container's host
   set changes, and removing certs when a container stops being routed.

The backend URL uses the **container name**, not its IP: once Traefik is on the
container's network, Docker's embedded DNS resolves the name, and names survive
restarts whereas IPs change.

See [Routing](routing.md) for the label contract that drives all of this.

## Source map

| Path | Responsibility |
| --- | --- |
| `main.go`, `internal/cli/` | The `proximo` command surface (Cobra). |
| `internal/config/` | Persisted config (TLD), per-user paths. |
| `internal/dns/` | The wildcard DNS server + host-resolver wiring. |
| `internal/tls/` | Local CA, leaf issuance, system + NSS trust. |
| `internal/docker/` | Embedded stack (`assets/`), `compose` driver, the watcher. |
| `internal/platform/` | OS / package-manager detection, privileged host ops. |
| `cmd/dnsserver/`, `cmd/watcher/` | Entrypoints for the two in-stack services. |
