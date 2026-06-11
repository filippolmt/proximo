# proximo documentation

`proximo` makes any running Docker container reachable at
**`https://<name>.test`** — with automatic local DNS and a trusted HTTPS
certificate — on macOS and Linux. No per-container published ports, no
`/etc/hosts` edits, no long-running host daemon.

This folder is the full guide, and this page is the **canonical section-level
map** of it — every `##` section of every guide is linked below.

> **Editors:** GitHub anchors are generated from headings, so **renaming a
> heading breaks links** — and when you add a `##` section to a guide, add it
> to this map. CI link-checks every anchor.

| Guide | Type | What it covers |
| --- | --- | --- |
| [Installation](installation.md) | how-to | Requirements, install on macOS/Linux, exactly what is changed on your host, and how to fully reverse it. |
| [CLI reference](cli.md) | reference | Every `proximo` command, what it does, and example sessions. |
| [Updating](updating.md) | how-to | Keeping the stack in lockstep with the CLI: `proximo update`, skew detection, and how updates apply. |
| [Architecture](architecture.md) | explanation | How it works under the hood: the embedded stack, the DNS server, the local CA and trust, the watcher. |
| [Routing](routing.md) | reference | How to expose a container: the `proximo.*` labels, port auto-detection, multiple hosts, and native Traefik compatibility. |
| [Dev-time observability](observability.md) | how-to | The opt-in `up --observability` logs (Dozzle) + metrics (Beszel) dashboards — credential-less and no-secret. |
| [Troubleshooting](troubleshooting.md) | how-to | Common issues, one anchored section per failure mode. |
| [Development](development.md) | how-to | Contributing: build/test from source, local stack builds (`PROXIMO_SRC`), versioning, embedded assets, releases. |

### [Installation](installation.md)

[Requirements](installation.md#requirements) ·
[Step 1 — install the binary](installation.md#step-1--install-the-binary) ·
[Step 2 — one-time host setup](installation.md#step-2--one-time-host-setup) ·
[What `install` changes on your host](installation.md#what-install-changes-on-your-host) ·
[State home (`~/.proximo`)](installation.md#state-home-proximo) ·
[Uninstall](installation.md#uninstall) ·
[Next](installation.md#next)

### [CLI reference](cli.md)

[`proximo install`](cli.md#proximo-install) ·
[`proximo up`](cli.md#proximo-up) ·
[`proximo down`](cli.md#proximo-down) ·
[`proximo update`](cli.md#proximo-update) ·
[`proximo status`](cli.md#proximo-status) ·
[`proximo config tld`](cli.md#proximo-config-tld) ·
[`proximo config ca-path`](cli.md#proximo-config-ca-path) ·
[`proximo uninstall`](cli.md#proximo-uninstall) ·
[`proximo version`](cli.md#proximo-version) ·
[Typical sessions](cli.md#typical-sessions)

### [Updating](updating.md)

[Mental model](updating.md#mental-model) ·
[`proximo update`](updating.md#proximo-update) ·
[When does an update apply?](updating.md#when-does-an-update-apply) ·
[Linux](updating.md#linux)

### [Architecture](architecture.md)

[The big picture](architecture.md#the-big-picture) ·
[The CLI is a one-shot orchestrator](architecture.md#the-cli-is-a-one-shot-orchestrator) ·
[The stack: three services](architecture.md#the-stack-three-services) ·
[DNS](architecture.md#dns) ·
[TLS and trust](architecture.md#tls-and-trust) ·
[The watcher](architecture.md#the-watcher) ·
[Source map](architecture.md#source-map)

### [Routing](routing.md)

[The proximo labels](routing.md#the-proximo-labels) ·
[`proximo.hosts` — opt in and pick the host(s)](routing.md#proximohosts--opt-in-and-pick-the-hosts) ·
[`proximo.port` — usually you can omit it](routing.md#proximoport--usually-you-can-omit-it) ·
[`proximo.enable` — temporary opt-out](routing.md#proximoenable--temporary-opt-out) ·
[`proximo.redirect` — opt in to the HTTP→HTTPS redirect](routing.md#proximoredirect--opt-in-to-the-httphttps-redirect) ·
[What happens behind the scenes](routing.md#what-happens-behind-the-scenes) ·
[Native Traefik labels (backward compatible)](routing.md#native-traefik-labels-backward-compatible) ·
[Multiple networks](routing.md#multiple-networks) ·
[Quick reference](routing.md#quick-reference)

### [Dev-time observability](observability.md)

[Start it](observability.md#start-it) ·
[How it is wired](observability.md#how-it-is-wired) ·
[Credential-less access (local only)](observability.md#credential-less-access-local-only) ·
[No hardcoded secret](observability.md#no-hardcoded-secret) ·
[Tear it down](observability.md#tear-it-down) ·
[Logs, metrics & retention](observability.md#logs-metrics--retention) ·
[Notes & limits](observability.md#notes--limits)

### [Troubleshooting](troubleshooting.md)

[DNS name does not resolve](troubleshooting.md#dns-name-does-not-resolve) ·
[DNS port already in use](troubleshooting.md#dns-port-already-in-use) ·
[Port 443 or 80 already in use](troubleshooting.md#port-443-or-80-already-in-use) ·
[macOS UDP forwarding](troubleshooting.md#macos-udp-forwarding) ·
[Certificate warnings in Firefox or Chrome](troubleshooting.md#certificate-warnings-in-firefox-or-chrome) ·
[macOS Gatekeeper blocks the binary](troubleshooting.md#macos-gatekeeper-blocks-the-binary) ·
[Where to read watcher warnings](troubleshooting.md#where-to-read-watcher-warnings) ·
[Container not routed](troubleshooting.md#container-not-routed) ·
[VPN or corporate DNS overrides the resolver](troubleshooting.md#vpn-or-corporate-dns-overrides-the-resolver) ·
[Degraded stack](troubleshooting.md#degraded-stack)

### [Development](development.md)

[Build and test](development.md#build-and-test) ·
[Lifecycle targets](development.md#lifecycle-targets) ·
[Local source builds (`PROXIMO_SRC`)](development.md#local-source-builds) ·
[Version and module ref](development.md#version-and-module-ref) ·
[Embedded stack assets](development.md#embedded-stack-assets) ·
[Releases](development.md#releases)

## New here?

The install-label-open walkthrough lives in the
[README quick start](https://github.com/filippolmt/proximo#quick-start); the mental model (one-shot CLI,
no daemon, DNS + TLS produced natively in Go, opt-in routing) is in
[Architecture — The CLI is a one-shot orchestrator](architecture.md#the-cli-is-a-one-shot-orchestrator)
and [The big picture](architecture.md#the-big-picture).
