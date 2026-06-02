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
| [`up`](#proximo-up) | Start the stack only | no | yes |
| [`down`](#proximo-down) | Stop the stack only | no | yes |
| [`update`](#proximo-update) | Converge the running stack to the installed CLI | no | yes |
| [`status`](#proximo-status) | List routed containers and URLs | no | yes |
| [`config tld <tld>`](#proximo-config-tld) | Change the routed TLD | yes | yes |
| [`uninstall`](#proximo-uninstall) | Reverse all host changes + stop the stack | yes | yes |
| [`version`](#proximo-version) | Print version, commit, build date | no | no |

---

## `proximo install`

Preflight, generate the CA, configure the host resolver, install CA trust, and
start the stack. This is the only command that changes privileged host state.

```sh
proximo install
```

Idempotent on the parts that allow it: the CA is generated once and reused; the
resolver and trust steps re-apply cleanly. See
[Installation](installation.md#step-2--one-time-host-setup) for the full step
list and exactly what it writes to your host.

## `proximo up`

Start (or rebuild) the embedded stack **without** touching host configuration.
Use it after a reboot or a `down`.

```sh
proximo up
```

Requires that the Docker daemon is reachable. If you run `up` before `install`,
the CA may not exist yet — the watcher then runs without issuing certificates
until you `install`.

`up` shares the convergence path with [`update`](#proximo-update), so it also
applies any pending update — rebuilding the in-stack images at the installed CLI
version and re-pulling Traefik. See [Updating](updating.md).

## `proximo down`

Stop and remove the stack containers. Host configuration (resolver, trust) is
left untouched, so a later `up` brings everything back.

```sh
proximo down
```

A no-op if the stack was never materialized.

## `proximo update`

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

## `proximo status`

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
CONTAINER  URL
whoami     https://whoami.test
api        https://api.test
```

A `proximo.hosts` container whose backend port is **ambiguous** (no
`proximo.port`, and the image exposes zero or several ports) is not served by the
watcher, so it is **not** shown as a working route — it is flagged instead so you
know why it is missing:

```
CONTAINER  URL
multi      ⚠ set proximo.port (exposes 2 TCP ports)
```

Prints `No routed containers.` when nothing is exposed.

When the running stack version differs from the installed CLI, `status` prints a
read-only **skew warning** recommending `proximo update` (it never rebuilds):

```
⚠ stack is running 0.1.0 but the CLI is 0.2.0; run `proximo update` to converge
```

## `proximo config tld`

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

## `proximo uninstall`

Reverse everything `install` did and tear down the stack:

```sh
proximo uninstall
```

1. Stop the stack.
2. Remove the host resolver config for the TLD (and reload the resolver on
   Linux).
3. Remove CA trust from the NSS and system stores.
4. Delete the `~/.proximo` state home — config, CA, the materialized stack, and
   the bind-mounted Traefik data — so no proximo state is left on the host.

The host is restored to its prior state.

## `proximo version`

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
