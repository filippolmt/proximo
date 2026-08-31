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
| [Updating](updating.md) | how-to | Keeping the stack in lockstep with the CLI: the published stack image, `proximo update`, skew detection, and how updates apply. |
| [Architecture](architecture.md) | explanation | How it works under the hood: the embedded stack, the DNS server, the local CA and trust, the watcher. |
| [Routing](routing.md) | reference | How to expose a container: the `proximo.*` labels, port auto-detection, multiple hosts, and native Traefik compatibility. |
| [Dev-time observability](observability.md) | how-to | The opt-in `up --observability` logs (Dozzle) + metrics (Beszel) dashboards — credential-less and no-secret. |
| [The agent skill](skill.md) | how-to | The Skill proximo ships to coding agents: installing it, the Managed copy that keeps it level with the binary, and what it knows. |
| [Troubleshooting](troubleshooting.md) | how-to | Common issues, one anchored section per failure mode. |
| [Development](development.md) | how-to | Contributing: build/test from source, local stack builds (`PROXIMO_SRC`), versioning, embedded assets, releases and the stack image pipeline. |

### [The domain model](../CONTEXT.md)

The project's glossary: the terms proximo uses and the ones it deliberately
avoids. Normative — a term with no implementation is marked as a declared debt,
not described as if it worked. Read it before naming anything new.

### [Decision records](adr/)

Why a design is the way it is, and what was rejected on the way.

[0001 — Client reports are captured by a proximo hop that rewrites the response](adr/0001-inspection-injects-into-the-response-path.md)

[0002 — The stack's Go services ship as one published image, pinned to the CLI version](adr/0002-stack-services-ship-as-one-published-image.md)

[0003 — Every route answers on a qualified host](adr/0003-every-route-answers-on-a-qualified-host.md)

[0004 — Checks are a first-class concept, with a report and a remedy](adr/0004-checks-report-remedies.md)

[0005 — The agent skill ships in the CLI, and the CLI keeps it current](adr/0005-the-agent-skill-ships-in-the-cli.md)

[0006 — The Transcript is quoted, never stored](adr/0006-the-transcript-is-quoted-never-stored.md)

[0007 — proximo remembers what the runtime declares, never what the project wrote](adr/0007-proximo-remembers-what-the-runtime-declares.md)

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
[`proximo trust`](cli.md#proximo-trust) ·
[`proximo status`](cli.md#proximo-status) ·
[`proximo doctor`](cli.md#proximo-doctor) ·
[`proximo errors`](cli.md#proximo-errors) ·
[`proximo errors transcript`](cli.md#proximo-errors-transcript) ·
[`proximo config tld`](cli.md#proximo-config-tld) ·
[`proximo config ca-path`](cli.md#proximo-config-ca-path) ·
[`proximo skill install`](cli.md#proximo-skill-install) ·
[`proximo skill uninstall`](cli.md#proximo-skill-uninstall) ·
[`proximo uninstall`](cli.md#proximo-uninstall) ·
[`proximo version`](cli.md#proximo-version) ·
[Typical sessions](cli.md#typical-sessions)

### [Updating](updating.md)

[Mental model](updating.md#mental-model) ·
[`proximo update`](updating.md#proximo-update) ·
[Running a different image](updating.md#running-a-different-image) ·
[When does an update apply?](updating.md#when-does-an-update-apply) ·
[Linux](updating.md#linux)

### [Architecture](architecture.md)

[The big picture](architecture.md#the-big-picture) ·
[The CLI is a one-shot orchestrator](architecture.md#the-cli-is-a-one-shot-orchestrator) ·
[The stack: four services](architecture.md#the-stack-four-services) ·
[DNS](architecture.md#dns) ·
[TLS and trust](architecture.md#tls-and-trust) ·
[The watcher](architecture.md#the-watcher) ·
[Source map](architecture.md#source-map)

### [Routing](routing.md)

[The proximo labels](routing.md#the-proximo-labels) ·
[The two hosts every route gets](routing.md#the-two-hosts-every-route-gets) ·
[`proximo.hosts` — opt in and pick the host(s)](routing.md#proximohosts--opt-in-and-pick-the-hosts) ·
[`proximo.port` — usually you can omit it](routing.md#proximoport--usually-you-can-omit-it) ·
[`proximo.enable` — temporary opt-out](routing.md#proximoenable--temporary-opt-out) ·
[`proximo.redirect` — opt in to the HTTP→HTTPS redirect](routing.md#proximoredirect--opt-in-to-the-httphttps-redirect) ·
[`proximo.health` — wait for the container to be healthy](routing.md#proximohealth--wait-for-the-container-to-be-healthy) ·
[`proximo.path` — split one host across containers](routing.md#proximopath--split-one-host-across-containers) ·
[proximo middlewares — auth, CORS, custom headers](routing.md#proximo-middlewares--auth-cors-custom-headers) ·
[`proximo.inspect` — see what the browser saw](routing.md#proximoinspect--see-what-the-browser-saw) ·
[`proximo.tcp.port` — route TCP services by name (SNI)](routing.md#proximotcpport--route-tcp-services-by-name-sni) ·
[Round-robin across replicas](routing.md#round-robin-across-replicas) ·
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
[Transcripts — what the container said](observability.md#transcripts--what-the-container-said) ·
[Incidents — what the runtime declared](observability.md#incidents--what-the-runtime-declared) ·
[Readings — what the runtime says right now](observability.md#readings--what-the-runtime-says-right-now) ·
[Inspection — what the browser saw](observability.md#inspection--what-the-browser-saw) ·
[Notes & limits](observability.md#notes--limits)

### [The agent skill](skill.md)

[Install it](skill.md#install-it) ·
[Managed and unmanaged copies](skill.md#managed-and-unmanaged-copies) ·
[What the Skill knows](skill.md#what-the-skill-knows) ·
[Without the binary](skill.md#without-the-binary)

### [Troubleshooting](troubleshooting.md)

[The Docker daemon is not reachable](troubleshooting.md#the-docker-daemon-is-not-reachable) ·
[proximo is not installed on this host](troubleshooting.md#proximo-is-not-installed-on-this-host) ·
[DNS name does not resolve](troubleshooting.md#dns-name-does-not-resolve) ·
[DNS port already in use](troubleshooting.md#dns-port-already-in-use) ·
[Port 443 or 80 already in use](troubleshooting.md#port-443-or-80-already-in-use) ·
[macOS UDP forwarding](troubleshooting.md#macos-udp-forwarding) ·
[Certificate warnings in Firefox or Chrome](troubleshooting.md#certificate-warnings-in-firefox-or-chrome) ·
[Traefik logs failed to find any PEM data](troubleshooting.md#traefik-logs-failed-to-find-any-pem-data) ·
[macOS Gatekeeper blocks the binary](troubleshooting.md#macos-gatekeeper-blocks-the-binary) ·
[Where to read watcher warnings](troubleshooting.md#where-to-read-watcher-warnings) ·
[Container not routed](troubleshooting.md#container-not-routed) ·
[A host collision is reported](troubleshooting.md#a-host-collision-is-reported) ·
[502/503 right after a container restarts](troubleshooting.md#502503-right-after-a-container-restarts) ·
[An error I typed in the browser console never shows up](troubleshooting.md#an-error-i-typed-in-the-browser-console-never-shows-up) ·
[proximo errors shows nothing at all](troubleshooting.md#proximo-errors-shows-nothing-at-all) ·
[proximo errors reports no Incident](troubleshooting.md#proximo-errors-reports-no-incident) ·
[A transcript is empty or says the container is gone](troubleshooting.md#a-transcript-is-empty-or-says-the-container-is-gone) ·
[proximo errors shows nothing for an inspected route](troubleshooting.md#proximo-errors-shows-nothing-for-an-inspected-route) ·
[An inspected route 404s on part of my app](troubleshooting.md#an-inspected-route-404s-on-part-of-my-app) ·
[VPN or corporate DNS overrides the resolver](troubleshooting.md#vpn-or-corporate-dns-overrides-the-resolver) ·
[Degraded stack](troubleshooting.md#degraded-stack) ·
[The stack runs an overridden image](troubleshooting.md#the-stack-runs-an-overridden-image) ·
[The stack image cannot be pulled](troubleshooting.md#the-stack-image-cannot-be-pulled) ·
[The agent skill is out of date](troubleshooting.md#the-agent-skill-is-out-of-date)

### [Development](development.md)

[Build and test](development.md#build-and-test) ·
[Lifecycle targets](development.md#lifecycle-targets) ·
[Local source builds (`PROXIMO_SRC`)](development.md#local-source-builds) ·
[Version and image ref](development.md#version-and-image-ref) ·
[Embedded stack assets](development.md#embedded-stack-assets) ·
[The published skill (`skills/`)](development.md#the-published-skill-skills) ·
[The injected agent](development.md#the-injected-agent) ·
[Releases](development.md#releases)

## New here?

The install-label-open walkthrough lives in the
[README quick start](https://github.com/filippolmt/proximo#quick-start); the mental model (one-shot CLI,
no daemon, DNS + TLS produced natively in Go, opt-in routing) is in
[Architecture — The CLI is a one-shot orchestrator](architecture.md#the-cli-is-a-one-shot-orchestrator)
and [The big picture](architecture.md#the-big-picture).
