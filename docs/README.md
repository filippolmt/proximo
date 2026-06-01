# proximo documentation

`proximo` makes any running Docker container reachable at
**`https://<name>.test`** — with automatic local DNS and a trusted HTTPS
certificate — on macOS and Linux. No per-container published ports, no
`/etc/hosts` edits, no long-running host daemon.

This folder is the full guide. Start wherever fits your need:

| Guide | What it covers |
| --- | --- |
| [Installation](installation.md) | Requirements, install on macOS/Linux, exactly what is changed on your host, and how to fully reverse it. |
| [CLI reference](cli.md) | Every `proximo` command, what it does, and example sessions. |
| [Architecture](architecture.md) | How it works under the hood: the embedded stack, the DNS server, the local CA and trust, the watcher. |
| [Routing](routing.md) | How to expose a container: the `proximo.*` labels, port auto-detection, multiple hosts, and native Traefik compatibility. |
| [Troubleshooting](troubleshooting.md) | Common issues: DNS not resolving, port in use, browser cert warnings, macOS Gatekeeper. |

## 60-second tour

```sh
# 1. One-time host setup: CA, resolver, trust, and the stack.
proximo install

# 2. Label any container with the host you want.
#    docker-compose.yml
#    services:
#      whoami:
#        image: traefik/whoami
#        labels:
#          - "proximo.hosts=whoami.test"

# 3. Bring it up and open it — trusted HTTPS, no warning.
docker compose up -d
open https://whoami.test
proximo status
```

## Mental model

- The **`proximo` CLI is a one-shot orchestrator.** Each command performs its
  action (generate the CA, write the host resolver, trust the CA,
  `compose up/down`) and exits. There is no proximo daemon on your host — only
  the stack containers stay running.
- **DNS + TLS are produced natively in Go.** The only mandatory prerequisite is
  Docker.
- **Routing is opt-in.** A container is exposed only when you label it
  (`proximo.hosts=…`, or native `traefik.*` labels).

See [Architecture](architecture.md) for the full picture.
