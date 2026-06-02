# proximo

Make any running Docker container reachable at **`https://<name>.test`** — with
automatic local DNS and trusted HTTPS — on macOS and Linux. No per-container
published ports, no `/etc/hosts` edits.

`proximo` gives you automatic `Host`-based routing plus the two things that are
usually left to you: local DNS and a trusted certificate. The only mandatory
prerequisite is Docker; DNS and certificates are produced natively in Go.

## Documentation

Full guides live in [`docs/`](docs/README.md):

- [Installation](docs/installation.md) — requirements, install steps, what it
  changes on your host, and how to reverse it.
- [CLI reference](docs/cli.md) — every command with examples.
- [Updating](docs/updating.md) — `proximo update`, skew detection, and how
  updates apply to the stack.
- [Architecture](docs/architecture.md) — the stack, DNS, the local CA, the
  watcher.
- [Routing](docs/routing.md) — the `proximo.*` labels and Traefik compatibility.
- [Dev-time observability](docs/observability.md) — opt-in `up --observability`
  logs (Dozzle) + metrics (Beszel) dashboards: credential-less and over trusted
  HTTPS (`http://` auto-redirects).
- [Troubleshooting](docs/troubleshooting.md) — common issues.

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
```

A ready-to-run sample is in [`examples/whoami/`](examples/whoami/). For the label
contract (multiple hosts, explicit port, opt-out, native Traefik labels) see
[Routing](docs/routing.md).

## Development

No local Go toolchain is required — every Make target runs Go inside the
`golang` image (with a persistent module/build cache), so **Docker is the only
prerequisite**:

```sh
make build      # build bin/proximo-<os>-<arch> for the host (Go runs in Docker)
make build-all  # cross-compile all targets (darwin,linux × amd64,arm64)
make test       # run the test suite (always Linux, in the golang image)
make vet        # go vet
make tidy       # go mod tidy
```

The binary is named per OS/arch (`bin/proximo-darwin-arm64`,
`bin/proximo-linux-amd64`, …) so a macOS and a Linux build never overwrite each
other in a shared working tree. Override the host target with
`make build GOOS=linux GOARCH=amd64`.

### Running it via Make

The lifecycle targets build first (so the binary always matches the host) and
run the host binary, which talks to the host Docker socket:

```sh
make install   # host setup (CA, resolver, trust) + start stack — asks for sudo
make up         # start the stack (no host changes)
make status     # list routed containers
make down        # stop the stack
make uninstall  # reverse host changes + tear down
make e2e         # install + start the whoami demo + open https://whoami.test
make e2e-down    # stop demo + uninstall
```

### Local testing without publishing

The stack's `dns` and `watcher` images are normally built with
`go install github.com/filippolmt/proximo/cmd/...@<ref>`, which needs the module
to be published. The Make lifecycle targets above pass `PROXIMO_SRC=$(pwd)`
automatically, so those images build **from local source** (no push, no module
fetch) via a generated `docker-compose.override.yml`. Run the binary directly
without `PROXIMO_SRC` to use the published images instead.

## License

[MIT](LICENSE) — free to use, modify, and distribute.
