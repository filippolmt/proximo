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

## `proximo down`

Stop and remove the stack containers. Host configuration (resolver, trust) is
left untouched, so a later `up` brings everything back.

```sh
proximo down
```

A no-op if the stack was never materialized.

## `proximo status`

List the containers that are currently routed and the URL each is reachable at.
Hosts come from the `proximo.hosts` label when present, otherwise from native
Traefik router rules.

```sh
proximo status
```

```
CONTAINER  URL
whoami     https://whoami.test
api        https://api.test
```

Prints `No routed containers.` when nothing is exposed.

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
4. Delete the local CA material from disk.

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
