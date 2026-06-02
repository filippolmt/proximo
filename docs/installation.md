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

The Homebrew cask is macOS-only; Linux installs via release binaries or
`go install`.

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

1. **Preflight** — confirms the OS is supported, a package manager is available
   (Homebrew or apt), and the Docker daemon is reachable.
2. **DNS port check** — confirms `127.0.0.1:5354/udp` is free.
3. **Prime sudo** — prompts once so the following privileged steps don't each
   ask for a password.
4. **Generate the local CA** — a P-256 ECDSA CA created on first run and reused
   afterwards, stored under your per-user config dir.
5. **Configure the host resolver** — routes `*.<tld>` lookups to the local DNS
   server (see [what it changes](#what-install-changes-on-your-host)).
6. **Install CA trust** — adds the CA to the OS system trust store and, when
   present, the NSS store (Firefox / Chromium on Linux) via `certutil`.
7. **Start the stack** — materializes and `docker compose up -d --build`s the
   embedded Traefik + DNS + watcher stack.
8. **Save config** — persists the chosen TLD.

When it finishes, containers labeled with a host under the TLD are reachable at
`https://<host>` with trusted HTTPS.

## What `install` changes on your host

Everything below is created by `install` and removed by `uninstall`.

| Platform | Resolver change | Trust change |
| --- | --- | --- |
| **macOS** | `/etc/resolver/<tld>` → `nameserver 127.0.0.1` + `port 5354` | CA added to the system keychain trust store + NSS DBs (if any) |
| **Linux** | `/etc/systemd/resolved.conf.d/proximo-<tld>.conf` → `DNS=127.0.0.1:5354`, `Domains=~<tld>`, then `systemd-resolved` is restarted | CA added to the system trust store + NSS DBs via `certutil` |

Per-user state (not privileged) lives under your OS config dir
(`os.UserConfigDir()/proximo`):

- `config.json` — the persisted TLD.
- `tls/` — the CA certificate and key (`ca.pem`, `ca-key.pem`).
- `stack/` — the materialized `docker compose` stack (compose file, Traefik
  config, a copy of the CA for the watcher to mount).

## Uninstall

```sh
proximo uninstall
```

Reverses the whole setup: stops the stack, removes the resolver config, untrusts
and deletes the local CA from the system and NSS stores. The host returns to its
prior state. (See the [CLI reference](cli.md#proximo-uninstall) for details.)

## Next

- [CLI reference](cli.md) — all commands.
- [Routing](routing.md) — how to expose a container.
