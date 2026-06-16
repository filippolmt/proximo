# Troubleshooting

[← back to docs index](README.md)

One section per failure mode. If your container is labeled but unreachable,
start with [Container not routed](#container-not-routed).

## DNS name does not resolve

`https://<name>.test` does not resolve — confirm the stack is up
(`proximo status`) and the resolver is wired: on macOS check
`/etc/resolver/<tld>`; on Linux check
`/etc/systemd/resolved.conf.d/proximo-<tld>.conf` and that `systemd-resolved` is
active (`resolvectl status`). Only `systemd-resolved` is supported in v1.

To test the DNS server directly, bypassing the host resolver:

```sh
dig @127.0.0.1 -p 5354 whoami.test
```

If that answers `127.0.0.1` but the browser still fails, the host resolver is
the problem — see also
[VPN or corporate DNS overrides the resolver](#vpn-or-corporate-dns-overrides-the-resolver).

## DNS port already in use

`proximo install` aborts before making changes if `127.0.0.1:5354/udp` is
taken. (Port `5353` is deliberately not used: macOS `mDNSResponder`/Bonjour
binds it.) Free the port and retry, or check it with
`sudo lsof -nP -iUDP:5354`.

## Port 443 or 80 already in use

Traefik publishes `443` and `80` on the host. If another service already
listens there (another reverse proxy, a local web server, an old Traefik), the
stack fails to start with a Docker error like
`Bind for 0.0.0.0:443 failed: port is already allocated`.

Find and stop the conflicting listener, then bring the stack back up:

```sh
sudo lsof -nP -iTCP:443 -sTCP:LISTEN
sudo lsof -nP -iTCP:80 -sTCP:LISTEN
proximo up
```

## macOS UDP forwarding

`127.0.0.1:5354/udp` relies on the Docker VM forwarding UDP. It works on
current Docker Desktop; if a setup proves unreliable, that is the first thing
to check.

## Certificate warnings in Firefox or Chrome

These browsers use NSS, not the system store. `proximo install` adds the CA via
`certutil` (installing `nss-tools` if needed). Fully restart the browser after
install.

## Traefik logs failed to find any PEM data

Older versions logged a recurring, harmless error on every container restart:

```
ERR Unable to parse certificate .../certs/<name>.crt error="... tls: failed to find any PEM data in certificate input"
```

It came from a race: the watcher rewrote a `.crt` with a plain truncate-then-write
while Traefik's file provider reloaded mid-write, briefly reading an empty file.
Certificate materialization is now atomic (write-temp-then-rename), so the proxy
only ever sees a complete file and this error no longer appears on reissue.

If you still see it, the cert is genuinely malformed — a corrupted CA or a
hand-edited file under `certs/`. Re-run `proximo install` to regenerate the CA,
then restart the stack.

## macOS Gatekeeper blocks the binary

Gatekeeper's "is damaged" dialog is handled by the cask. For a manually
downloaded binary: `xattr -dr com.apple.quarantine ./proximo`.

## Where to read watcher warnings

The watcher logs every routing decision it skips (invalid hostnames, ambiguous
ports, duplicate hosts across label schemes). When a doc says "a warning is
logged", this is where to look:

```sh
cd ~/.proximo/stack && docker compose logs watcher
```

With [observability](observability.md) enabled (`proximo up --observability`),
the same logs are browsable at `https://logs.<tld>` (Dozzle) — select the
`watcher` container.

## Container not routed

A container with proximo labels is not reachable. Check in order:

1. **Hosts label syntax** — `proximo.hosts` must be present (its presence is
   the opt-in) and contain valid hostnames under the configured TLD,
   comma-separated. Invalid entries are rejected and logged.
2. **Enable flag** — `proximo.enable=false`/`0`/`no` parks the container.
   Remove the label or set it truthy.
3. **Port ambiguity** — if the image `EXPOSE`s zero or several ports and no
   `proximo.port` is set, the container is skipped; `proximo status` shows it
   flagged (`⚠ set proximo.port`). Add an explicit `proximo.port`.
4. **Watcher warnings** — anything else (duplicate host across label schemes,
   network attach failures) is explained in the watcher log: see
   [Where to read watcher warnings](#where-to-read-watcher-warnings).

The full label contract is in [Routing](routing.md#the-proximo-labels).

## VPN or corporate DNS overrides the resolver

VPN clients and corporate DNS setups often install their own resolver
configuration with higher precedence, so `.test` queries never reach proximo's
DNS server even though the stack is healthy.

- **macOS** — inspect the resolver order with `scutil --dns`: the
  `domain : test` entry must point at `127.0.0.1` port `5354`. Some VPN clients
  install scoped resolvers that shadow `/etc/resolver/<tld>`.
- **Linux** — check `resolvectl status`: the VPN interface may claim a routing
  domain of `~.` (everything), which outranks the proximo drop-in. Restrict the
  VPN's DNS scope, or re-run `proximo install` after connecting.

In both cases `dig @127.0.0.1 -p 5354 <name>.test` answering correctly proves
the problem is resolver precedence, not proximo's DNS server.

## Degraded stack

Routes stop updating (new containers are not picked up, removed containers
still resolve) when the watcher is not running — it is the reconcile loop, so
without it the routing files and certificates go stale.

```sh
docker ps --filter "label=proximo.role"   # traefik, dns and watcher should be up
proximo status                            # also reports CLI/stack version skew
proximo up                                # converges the stack back to healthy
```

Stale routes left behind by a crash are cleaned up on the next reconcile after
the watcher restarts. If `proximo status` reports a version skew between the
CLI and the stack, run `proximo update` — see [Updating](updating.md#proximo-update).
