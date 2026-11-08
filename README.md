# proximo

Make any running Docker container reachable at **`https://<name>.test`** — with
automatic local DNS and trusted HTTPS — on macOS and Linux. No per-container
published ports, no `/etc/hosts` edits.

`proximo` gives you automatic `Host`-based routing plus the two things that are
usually left to you: local DNS and a trusted certificate. The only mandatory
prerequisite is Docker; DNS and certificates are produced natively in Go.

## Documentation

📖 **[Read the full guide](https://filippolmt.github.io/proximo/)** — the docs
below, rendered and searchable.

Full guides live in [`docs/`](docs/README.md):

- [Installation](docs/installation.md) — requirements, install steps, what it
  changes on your host, and how to reverse it.
- [CLI reference](docs/cli.md) — every command with examples.
- [Updating](docs/updating.md) — `proximo update`, skew detection, and how
  updates apply to the stack.
- [Architecture](docs/architecture.md) — the stack, DNS, the local CA, the
  watcher.
- [Routing](docs/routing.md) — the `proximo.*` labels, the
  [two hosts every route gets](docs/routing.md#the-two-hosts-every-route-gets),
  and Traefik compatibility.
- [Dev-time observability](docs/observability.md) — `proximo errors` shows,
  for every route and with no label, the failing requests and what the
  container itself wrote around them (the **Transcript**), and the
  **Incidents** the runtime declared: a container that exited, restarted or
  was OOM-killed. Label a container `proximo.inspect=true` and it also shows
  what the browser reported (**Inspection** — Chrome is the supported browser;
  other engines are likely to work and are not tested). Opt-in
  `up --observability` adds logs (Dozzle) + metrics (Beszel) dashboards,
  credential-less and over trusted HTTPS.
- [Sharing a route with colleagues](docs/sharing.md) — label a route
  `proximo.share` and a small group of colleagues reach it from their own
  machines, in a browser with a real login, over a mesh the team already
  operates. Team setup, in order: [sharing setup](docs/sharing-setup.md).
- [The agent skill](docs/skill.md) — `proximo skill install` puts a Skill in
  your repository (or your home) that teaches Claude Code and Codex the label
  contract and how to diagnose a broken route. It ships inside the binary, so
  the version an agent reads is always the one you have installed.
- [Troubleshooting](docs/troubleshooting.md) — common issues. Something broken?
  [`proximo doctor`](docs/cli.md#proximo-doctor) reports every check on the host
  and hands back the exact command to run for each failure. Container labeled
  but unreachable? Start with the
  [container not routed checklist](docs/troubleshooting.md#container-not-routed).
- [Development](docs/development.md) — building, testing from source,
  versioning, releases.

## Quick start

Install the binary (macOS via Homebrew; Linux via release binary or
`go install` — see [Installation](docs/installation.md)):

```sh
brew install filippolmt/tap/proximo          # macOS
go install github.com/filippolmt/proximo@latest   # Linux/from source
```

One-time host setup (CA, resolver, trust, and the stack — needs `sudo` once):

```sh
proximo install
```

Label any container with the host you want, then bring it up:

```yaml
# docker-compose.yml
services:
  whoami:
    image: traefik/whoami
    labels:
      - "proximo.hosts=whoami.test"   # the port is auto-detected (single EXPOSE)
```

```sh
docker compose up -d
open https://whoami.test   # macOS — trusted HTTPS, no warning
proximo status             # see what's routed
open https://traefik.test  # Traefik's own dashboard, always on (traefik.<tld> is reserved)
```

Every route also answers on a **qualified host** — the Compose project name
inserted before the TLD, so `whoami.test` in project `shop` is served at
`whoami.shop.test` too. It is never taken by another container, which makes it
the name to put in a README or an `.env`; `proximo status` prints it beside the
short one. See
[the two hosts every route gets](docs/routing.md#the-two-hosts-every-route-gets).

A ready-to-run sample is in [`examples/whoami/`](examples/whoami/). For the label
contract (multiple hosts, explicit port, opt-out, native Traefik labels) see
the [routing label table](docs/routing.md#the-proximo-labels). If something
does not work, the [troubleshooting checklist](docs/troubleshooting.md#container-not-routed)
walks through the usual causes.

## Development

No local Go toolchain is required — every Make target runs Go inside the
`golang` image, so **Docker is the only prerequisite**:

```sh
make build      # build bin/proximo-<os>-<arch> for the host (Go runs in Docker)
make test       # run the test suite
make check      # everything CI checks, plus the links: run it before a commit
```

Everything else — all Make targets, the lifecycle targets, running the stack
from local source (`PROXIMO_SRC`), versioning, embedded assets, releases — is
in the [development guide](docs/development.md).

## License

[MIT](LICENSE) — free to use, modify, and distribute.
