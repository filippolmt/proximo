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
  seeds a fixed local user (`proximo@<tld>`) and enables **auto-login**, landing
  you straight in the dashboard.

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
proximo down        # stops the observability containers with the core stack
proximo uninstall   # removes them and the generated secret, then reverses all
                    # host changes and deletes the ~/.proximo home (which holds
                    # the bind-mounted metrics data)
```

## Notes & limits

- **Pinned images.** `amir20/dozzle`, `henrygd/beszel`, and
  `henrygd/beszel-agent` are pinned in the embedded compose (like Traefik). The
  Beszel hub env vars and API endpoints are upstream behaviors tied to that tag —
  they are verified when the pin is bumped.
- **Host-level metrics reflect the Docker VM**, not your laptop, because the
  agent runs on the compose network (no host networking). The signal that matters
  for development — **per-container** metrics — is unaffected.
- Not intended for production monitoring, alerting, or long-term retention.
