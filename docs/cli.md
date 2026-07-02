# CLI reference

[← back to docs index](README.md)

`proximo` is a **one-shot orchestrator**: every command performs its action and
exits. There is no background proximo process on your host — only the stack
containers keep running between commands.

```
proximo <command> [args]
```

| Command | Summary | Needs sudo | Needs Docker |
| --- | --- | --- | --- |
| [`install`](#proximo-install) | Full host setup + start the stack | yes | yes |
| [`up`](#proximo-up) | Start the stack only (`--observability` adds the dashboards) | no | yes |
| [`down`](#proximo-down) | Stop the stack only | no | yes |
| [`update`](#proximo-update) | Converge the running stack to the installed CLI | no | yes |
| [`trust`](#proximo-trust) | Re-trust the local CA (system + NSS), stack-safe | yes | no |
| [`status`](#proximo-status) | List routed containers and URLs | no | yes |
| [`config tld <tld>`](#proximo-config-tld) | Change the routed TLD | yes | yes |
| [`config ca-path`](#proximo-config-ca-path) | Print the local CA certificate path | no | no |
| [`uninstall`](#proximo-uninstall) | Reverse all host changes + stop the stack | yes | yes |
| [`version`](#proximo-version) | Print version, commit, build date | no | no |

---

## proximo install

Preflight, generate the CA, configure the host resolver, install CA trust, and
start the stack. The widest-reaching privileged command — it is the only one
that touches the host resolver (`trust` and `config tld` also need sudo, but for
narrower changes).

```sh
proximo install
```

Idempotent on the parts that allow it: the CA is generated once and reused; the
resolver and trust steps re-apply cleanly. See
[Installation](installation.md#step-2--one-time-host-setup) for the full step
list and exactly what it writes to your host.

## proximo up

Start (or rebuild) the embedded stack **without** touching host configuration.
Use it after a reboot or a `down`.

```sh
proximo up
```

Requires that the Docker daemon is reachable. If you run `up` before `install`,
the CA may not exist yet — the watcher then runs without issuing certificates
until you `install`.

With the stack up, Traefik's own **dashboard** is always served at
`https://traefik.<tld>` — read-only and local-only (no `api.insecure`, no extra
published port, no credentials), trusted by the local CA like every other
proximo host. `traefik.<tld>` is **reserved** for the stack; do not assign it to
your own containers.

`up` shares the convergence path with [`update`](#proximo-update), so it also
applies any pending update — rebuilding the in-stack images at the installed CLI
version and re-pulling Traefik. See [Updating](updating.md).

### --observability

```sh
proximo up --observability
```

Bring up the core stack **and** the opt-in logs (Dozzle) + metrics (Beszel)
dashboards in one command, run the metrics bootstrap, and print the dashboard
URLs (`https://logs.<tld>`, `https://metrics.<tld>`). Both dashboards opt into the
HTTP→HTTPS redirect, so `http://logs.<tld>` / `http://metrics.<tld>` auto-redirect
to the trusted https host. Off by default: a plain `up` starts neither. `down` /
`uninstall` tear them down too. See [Dev-time observability](observability.md).

## proximo down

Stop and remove the stack containers — the core services **and** the opt-in
observability dashboards (the profile is enabled on teardown, so they do not
linger). Host configuration (resolver, trust) is left untouched, so a later `up`
brings everything back.

```sh
proximo down
```

A no-op if the stack was never materialized.

### --observability

Stop **only** the observability dashboards (Dozzle + Beszel), leaving the core
stack running:

```sh
proximo down --observability
```

## proximo update

Converge the running stack to the **installed CLI version**: re-materialize the
embedded assets, rebuild the `dns` / `watcher` images at the CLI version, and
re-pull Traefik (security patches). Run it after upgrading the CLI
(`brew upgrade` / `go install`) — it is also the safe escape hatch for any stack
problem.

```sh
proximo update
proximo update --force   # rebuild images without the build cache
```

- **Idempotent**: prints "up to date" and recreates nothing when the stack
  already matches the CLI.
- **Never needs sudo**: Docker operations only — no resolver or CA changes.
- **Soft no-op**: when Docker is unreachable or no stack is running it reports
  that the update will apply on the next `proximo up` and exits 0.
- Shares the convergence code path with `proximo up`, so "update now" and
  "update on next start" cannot drift.

See [Updating proximo](updating.md) for the full model.

## proximo trust

Re-add the local CA to the OS system trust store and, when present, the NSS
store (Firefox / Chromium). It is the trust step of `install` on its own:

```sh
proximo trust
```

Use it when a browser stops trusting `https://<name>.<tld>` (an
`ERR_CERT_AUTHORITY_INVALID` / "issuer not trusted" warning) — typically because
the CA never made it into the browser's store or was regenerated.

- **Stack-safe**: unlike `install` it skips the DNS port check and never touches
  DNS or the Docker stack, so it runs while proximo is up — no `down`/`up` cycle.
- **Idempotent**: the system-store add is a no-op when already trusted; the NSS
  add removes any stale entry first. Re-run it freely.
- **Needs sudo, no Docker**: it only writes host trust stores.
- Reuses the existing CA (it never rotates it), so already-issued certificates
  stay valid. **Fully restart the browser afterwards** to pick up the CA.

## proximo status

List the **effective** routing state — the routes the watcher actually serves,
not just declared intent. It uses the same classifier the watcher uses, so the
two never disagree. Hosts come from the `proximo.hosts` label when present,
otherwise from native Traefik router rules; for a `proximo.hosts` route the
backend port is resolved the same way the watcher resolves it (explicit
`proximo.port`, else the single exposed TCP port).

```sh
proximo status
```

```
CONTAINER          URL
api                https://api.test
proximo-traefik-1  https://traefik.test
whoami             https://whoami.test
```

The `traefik.<tld>` route is the stack's own
[dashboard](#proximo-up) — listed whenever the stack is running, since the
watcher serves it unconditionally.

[TCP routes](routing.md#proximotcpport--route-tcp-services-by-name-sni) appear
alongside HTTP ones, showing the SNI host, backend port(s), and TLS mode; a route
served by several replica containers is marked `(balanced ×N)`:

```
CONTAINER  URL
db         tcp://db.test:5432 (terminate)
web        https://app.test (balanced ×2)
```

A `proximo.hosts` container whose backend port is **ambiguous** (no
`proximo.port`, and the image exposes zero or several ports) is not served by the
watcher, so it is **not** shown as a working route — it is flagged instead so you
know why it is missing:

```
CONTAINER  URL
multi      ⚠ set proximo.port (exposes 2 TCP ports)
```

Prints `No routed containers.` when nothing is exposed — which implies the
stack is down, since a running stack always serves the dashboard route.

When the running stack version differs from the installed CLI, `status` prints a
read-only **skew warning** recommending `proximo update` (it never rebuilds):

```
⚠ stack is running 0.1.0 but the CLI is 0.2.0; run `proximo update` to converge
```

## proximo config tld

Change the top-level domain routed to the local proxy. Updates the host resolver
for the new TLD, persists it, and restarts the stack so routing follows.

```sh
proximo config tld dev    # containers become reachable at <name>.dev
```

- The TLD must be a single DNS label of `[a-z0-9-]` (a leading dot is stripped,
  the value is lowercased).
- `.local` is **rejected** — it is reserved for mDNS (Bonjour/Avahi) and
  overriding it breaks real `.local` devices on your network.
- No-op (with a message) when the TLD is already set.

Default TLD is `.test` (reserved by RFC 6761, never collides with mDNS).

## proximo config ca-path

Print the absolute path of the local CA certificate (PEM):

```sh
proximo config ca-path
# /Users/you/.proximo/tls/ca.pem
```

This is the **stable contract for external tools** that want to trust proximo's
CA (e.g. mounting it into a dev container) — shell out to this command instead
of hardcoding the state-home layout. The path is printed even when the file
does not exist yet (proximo not installed yet), so callers must check existence
themselves; the command itself is side-effect free and never creates
directories.

## proximo uninstall

Reverse everything `install` did and tear down the stack:

```sh
proximo uninstall
```

1. Stop the stack (this also removes the profiled observability containers) and
   delete the generated observability secret + env files
   ([Dev-time observability](observability.md)). proximo uses no Docker named
   volume, so there is nothing to volume-remove here — the data goes with the
   home in step 4.
2. Remove the host resolver config for the TLD (and reload the resolver on
   Linux).
3. Remove CA trust from the NSS and system stores.
4. Delete the `~/.proximo` state home — config, CA, the materialized stack, and
   the bind-mounted Traefik data (plus the Beszel metrics data, if observability
   was used) — so no proximo state is left on the host.

The host is restored to its prior state.

## proximo version

Print the build metadata (version, commit, build date). Works without Docker.

```sh
proximo version
```

---

## Typical sessions

**First run**

```sh
proximo install            # one-time host setup + stack
docker compose up -d       # your own stack, with proximo.hosts labels
open https://whoami.test
```

**Day to day**

```sh
proximo status             # what's exposed right now
proximo down               # free ports 80/443 when you're done
proximo up                 # bring the proxy back later
```

**Switch domain / clean up**

```sh
proximo config tld dev     # move everything under .dev
proximo uninstall          # remove all host changes
```

See [Routing](routing.md) for how to label your containers and
[Architecture](architecture.md) for what each command is orchestrating.
