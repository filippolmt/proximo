# Installation

[← back to docs index](README.md)

`proximo` installs in two parts: the **binary** (the `proximo` CLI) and the
**one-time host setup** (`proximo install`) that wires DNS and certificate
trust. Both are fully reversible.

## Requirements

- **Docker** — Docker Desktop, Colima, or Lima on macOS; Docker Engine on
  Linux. The daemon must be running and reachable from your user.
- **OS** — macOS, or Ubuntu/Debian Linux using `systemd-resolved` as the active
  resolver. Other Linux resolver stacks (NetworkManager, plain `resolvconf`) are
  not supported in v1 and `proximo install` aborts with a clear message.
- **`sudo` once** — `proximo install` needs administrator rights to write the
  host resolver file and to trust the local CA. Everything it changes is undone
  by `proximo uninstall`.
- **Network access to `ghcr.io`** — the stack's services are pulled as one
  published image, `ghcr.io/filippolmt/proximo`, tagged with the CLI version.
  It is a public package, so no `docker login` is needed. See
  [Updating](updating.md#mental-model).
- **Free ports** — Traefik publishes `80` and `443`; the DNS server publishes
  `127.0.0.1:5354/udp`. `proximo install` checks the DNS port is free before
  touching anything.

## Step 1 — install the binary

### macOS (Homebrew cask)

```sh
brew install filippolmt/tap/proximo
```

The cask strips the `com.apple.quarantine` attribute on install (the binary is
unsigned), so there is no Gatekeeper "is damaged" alert.

### Linux

Download a release archive from the [releases page] and put `proximo` on your
`PATH`, or build from source:

```sh
go install github.com/filippolmt/proximo@latest
```

The Homebrew cask carries a Linux archive and Linuxbrew will install it, but
the quarantine step it exists for is macOS-only — on Linux prefer the release
binaries or `go install`.

[releases page]: https://github.com/filippolmt/proximo/releases

### Verify

```sh
proximo version
```

## Step 2 — one-time host setup

```sh
proximo install
```

This runs, in order:

1. **Preflight** — the subset of [`proximo doctor`](cli.md#proximo-doctor)'s
   checks that is meaningful before the host has been changed: the Docker daemon
   is reachable, nothing but proximo holds `:80`, `:443` or
   `127.0.0.1:5354/udp`, and the browser (NSS) trust store this install is about
   to write can be written at all. A port held by proximo's own stack is not a
   failure, so `install` can be re-run while the stack is up; a port held by
   anything else stops the command before it touches your host, and says which
   command names the holder.
2. **Prime sudo** — prompts once so the following privileged steps don't each
   ask for a password.
3. **Generate the local CA** — a P-256 ECDSA CA created on first run and reused
   afterwards, stored under your [state home](#state-home-proximo).
4. **Configure the host resolver** — routes `*.<tld>` lookups to the local DNS
   server (see [what it changes](#what-install-changes-on-your-host)).
5. **Install CA trust** — adds the CA to the OS system trust store and, when
   present, the NSS store (Firefox / Chromium on Linux) via `certutil`,
   installing the NSS tooling if it is missing. Preflight has already confirmed
   that it can be, which is why this step does not fail after the host has been
   changed.
6. **Start the stack** — materializes and `docker compose up -d --build`s the
   embedded Traefik + DNS + watcher stack.
7. **Save config** — persists the chosen TLD.

When it finishes, containers labeled with a host under the TLD are reachable at
`https://<host>` with trusted HTTPS.

## What install changes on your host

Everything below is created by `install` and removed by `uninstall`.

| Platform | Resolver change | Trust change |
| --- | --- | --- |
| **macOS** | `/etc/resolver/<tld>` → `nameserver 127.0.0.1` + `port 5354` | CA added to the system keychain trust store + NSS DBs (if any) |
| **Linux** | `/etc/systemd/resolved.conf.d/proximo-<tld>.conf` → `DNS=127.0.0.1:5354`, `Domains=~<tld>`, then `systemd-resolved` is restarted | CA added to the system trust store + NSS DBs via `certutil` |

`install` also refreshes any [agent Skill](skill.md) copy proximo itself wrote,
bringing it level with the binary. It never creates one: a Skill appears only
where you ran `proximo skill install`.

## State home (~/.proximo)

All of proximo's per-user state lives under a single user-owned home directory,
**`~/.proximo`** (literally `$HOME/.proximo` on both macOS and Linux — not the
platform `os.UserConfigDir()` location). Nothing of proximo's lives in a
Docker-managed named volume, so a `docker volume prune` can't wipe it.

| Path | Holds |
| --- | --- |
| `~/.proximo/config.json` | the persisted TLD |
| `~/.proximo/tls/` | the local CA certificate and key (`ca.pem`, `ca-key.pem`) — external tools should query the CA path via [`proximo config ca-path`](cli.md#proximo-config-ca-path) instead of hardcoding it |
| `~/.proximo/stack/` | the materialized `docker compose` stack (compose file, Traefik config, a copy of the CA for the watcher) |
| `~/.proximo/data/traefik/` | **bind-mounted** into Traefik + the watcher: the dynamic routes and per-container certificates the watcher generates |
| `~/.proximo/data/beszel/` | **bind-mounted** into the Beszel metrics hub when observability is enabled (`up --observability`): metrics history and hub users |

**Back it up** by copying the folder — `cp -a ~/.proximo ~/proximo-backup` (or
add it to your backup set). It is the one place all proximo state lives.

- **Bind mounts, not named volumes.** `data/traefik` (and `data/beszel` when
  observability is enabled) are host directories mounted into the containers, so
  they are plainly visible and survive `docker volume rm` / `docker volume
  prune`. The watcher regenerates routes and certs into `data/traefik` on its
  first reconcile, so even an empty `data/traefik` self-heals.
- **Linux file ownership.** The stack containers write into `data/` as root, so
  on Linux those files appear **root-owned** under your home. They are still
  readable for backup; `proximo uninstall` removes them (it tears the stack down
  first, then deletes the home).
- **Stale legacy volume.** If you ran a pre-bind-mount build, a now-unused
  `proximo_dynamic` named volume may linger in Docker. It is harmless (nothing
  references it); reclaim the space with `docker volume rm proximo_dynamic`.
- **No migration from the old location.** Earlier builds stored state under
  `os.UserConfigDir()/proximo`. There is no automatic move — if you have such an
  install, run `proximo install` again (it regenerates the CA and re-trusts it
  once).
- **macOS Docker Desktop file sharing.** `$HOME` is shared by default, so the
  bind mount works out of the box; a non-default file-sharing configuration that
  excludes `$HOME` would block it.

## Uninstall

```sh
proximo uninstall
```

Reverses the whole setup: stops the stack, removes the resolver config, untrusts
and deletes the local CA from the system and NSS stores, and **deletes the
`~/.proximo` state home** (config, CA, stack, and the bind-mounted data) so no
proximo state is left behind. The host returns to its prior state. (See the
[CLI reference](cli.md#proximo-uninstall) for details.)

## Next

- [CLI reference](cli.md) — all commands.
- [Routing](routing.md) — how to expose a container.
