# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What proximo is

A single Go CLI that makes any running Docker container reachable at `https://<name>.test`
on macOS/Linux, bundling three things developers normally wire by hand: `Host`-based
routing (Traefik), local wildcard DNS, and a trusted local CA. Docker is the only
mandatory prerequisite — DNS and certificates are produced natively in Go.

## Build / test / run

All Go work runs **inside Docker** via the Makefile — no local Go toolchain is assumed
(a persistent module/build cache volume is reused across runs). Docker is the only prerequisite.

```sh
make build      # compile bin/proximo-<os>-<arch> for the host (override GOOS/GOARCH=…)
make build-all  # cross-compile darwin,linux × amd64,arm64
make test       # full suite (go test ./...) in the golang image
make vet        # go vet
make tidy       # go mod tidy
```

Run a **single test** (no Make target — invoke `go test` directly in the build image):

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.26-alpine go test ./internal/docker/ -run TestModuleRef -v
```

If a local Go ≥1.26 toolchain is present you can run `go build/test/vet ./...` directly;
the Docker path is just the no-toolchain-required default.

### Lifecycle targets (build the host binary, then run it)

These pass `PROXIMO_SRC=$(pwd)` so the in-stack images build from **local source** (see below).

```sh
make install    # host setup (CA, resolver, trust) + start stack — asks for sudo once
make up         # start stack (no host changes)   make down       # stop stack
make status     # list routed containers          make uninstall  # reverse host changes + tear down
make e2e        # install + whoami demo + open https://whoami.test
```

## Architecture (the big picture)

Full diagram and per-service detail: `docs/architecture.md`. Routing label contract: `docs/routing.md`.

- **The CLI is a one-shot orchestrator — there is no proximo daemon.** Each command
  (`install`/`up`/`down`/`update`/`config tld`/`uninstall`) performs its action and exits. The only
  long-running processes are the **stack containers**.
- **The stack is a `docker compose` project embedded in the binary** via `//go:embed`
  (`internal/docker/assets/`: `docker-compose.yml`, `traefik/`, `dns.Dockerfile`,
  `watcher.Dockerfile`). On `install`/`up` it is *materialized* to the per-user config dir
  (`os.UserConfigDir()/proximo/stack/`) with the TLD (`__TLD__`) and DNS port (`__DNSPORT__`)
  sentinels substituted, then brought up via the shared `docker.Converge()`
  (`docker compose up -d --build --pull always`, plus a `--no-cache` rebuild for mobile refs).
  **Editing an embedded asset has no effect until the binary is rebuilt AND the stack
  re-materialized (re-`install`/`up`/`update`).**
- **Three stack services:** `traefik` (HTTPS on :443, routes by `Host`, two providers: Docker
  labels + file provider on `/etc/traefik/dynamic`), `dns` (miekg/dns wildcard: `*.<tld>` →
  `127.0.0.1`, published on `127.0.0.1:5354/udp`), `watcher` (the reconcile loop). They share a
  `dynamic` volume — watcher writes, Traefik's file provider reads.
- **The watcher (`internal/docker/watcher.go`) is the engine.** Reconciles on start, on Docker
  events, and every 30s: selects routed containers, resolves the backend port, attaches Traefik
  to backend networks, writes one Traefik router/service per container targeting
  `http://<container-name>:<port>` (name, not IP — survives restarts), and issues one CA-signed
  cert per container with its hosts as SANs. Containers labeled `proximo.role=*` (the stack
  itself) are never routed.
- **TLS:** a P-256 ECDSA local CA (`internal/tls`) generated on first install, added to the OS
  system trust store + NSS (Firefox/Chromium) via `certutil`. Leaf certs are per-container,
  capped under 398 days. No `*.<tld>` wildcard (browsers reject TLD-level wildcards).
- **Routing is opt-in via labels:** `proximo.hosts` (comma-separated, presence opts in),
  `proximo.port` (omit when the image EXPOSEs exactly one port; ambiguous → skipped + warning),
  `proximo.enable=false` (park), `proximo.redirect=true` (opt in to the HTTP→HTTPS redirect;
  off by default — `:80` for a non-opted host 404s, it is not redirected). Native `traefik.*`
  labels still work in parallel.

### Source map

| Path | Responsibility |
| --- | --- |
| `main.go`, `internal/cli/` | Cobra command surface |
| `internal/config/` | Persisted TLD + per-user paths (config dir, DNS port constant) |
| `internal/dns/` | Wildcard DNS server + host-resolver wiring (macOS `/etc/resolver`, Linux systemd-resolved drop-in) |
| `internal/tls/` | Local CA, leaf issuance, system + NSS trust |
| `internal/docker/` | Embedded stack (`assets/`), `compose` driver (`stack.go`), the watcher |
| `internal/platform/` | OS / package-manager detection, privileged host ops |
| `cmd/dnsserver/`, `cmd/watcher/` | Entrypoints for the two in-stack services |

## Conventions & gotchas

- **Version → module ref.** Build metadata is injected via ldflags into `internal/version`
  (`Version`, `Commit`, `Date`). GoReleaser's `{{ .Version }}` strips the leading `v`, so
  `version.Version` is a **bare semver** (`0.1.0`). The in-stack images are built with
  `go install …@${PROXIMO_REF}`, which requires a **canonical module version** — so
  `internal/docker/stack.go:moduleRef()` re-adds the `v` (`0.1.0`→`v0.1.0`; `dev`/empty→`main`).
  Keep `version.Version` usable as a display string and normalize at the module-ref consumer,
  not at the goreleaser source.
- **`PROXIMO_SRC` selects how the dns/watcher images build.** Set (the `make` lifecycle targets
  do this) → a generated `docker-compose.override.yml` builds them from the local checkout via
  `Dockerfile.dev` (no publish needed). Unset → the base compose builds them with
  `go install <module>@<ref>`, which needs the module/tag published.
- **Binaries are named per OS/arch** (`bin/proximo-<os>-<arch>`) so macOS and Linux builds never
  clobber each other in a shared tree.
- **Releases:** push a `vX.Y.Z` tag → `.github/workflows/release.yml` runs GoReleaser (Homebrew
  cask `filippolmt/tap/proximo`, release archives). A fix to embedded stack assets only reaches
  installed users on the **next release** (the binary carries its own asset copy).
- **`install` mutates the host** (resolver file + CA trust, needs sudo once); `uninstall`
  reverses everything. Linux requires `systemd-resolved` as the active resolver.
- **OpenSpec** lives in `openspec/` — this repo uses a spec-driven change workflow for proposing
  features (`openspec/changes/`, `openspec/specs/`).
