# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What proximo is

A single Go CLI that makes any running Docker container reachable at `https://<name>.test`
on macOS/Linux, bundling `Host`-based routing (Traefik), local wildcard DNS, and a trusted
local CA. Docker is the only mandatory prerequisite — DNS and certificates are produced
natively in Go. Full picture: [docs/architecture.md](docs/architecture.md); section-level
index of all guides: [docs/README.md](docs/README.md).

## Engineering standard

Always apply best practices, even when it means writing new code or rewriting
existing code. Do the right thing now rather than deferring quality — shortcuts
become gaps we have to close later. This holds for production code, tests, and
docs alike.

## Build / test / run

All Go work runs **inside Docker** via the Makefile — no local Go toolchain is assumed.
Make targets, the single-test invocation, and the lifecycle targets
(`install`/`up`/`down`/`status`/`uninstall`/`e2e`) are in
[docs/development.md — Build and test](docs/development.md#build-and-test) and
[Lifecycle targets](docs/development.md#lifecycle-targets).

## Architecture

The architecture is fully documented in `docs/` — do not duplicate it here. Component
ownership: [docs/architecture.md — Source map](docs/architecture.md#source-map).

### Doc map (question → section)

| Question | Read |
| --- | --- |
| Routing label contract & port resolution rules | [docs/routing.md — The proximo labels](docs/routing.md#the-proximo-labels) |
| Which hosts are reserved for the stack | [docs/routing.md — proximo.hosts](docs/routing.md#proximohosts--opt-in-and-pick-the-hosts) |
| HTTP→HTTPS redirect semantics (opt-in, 302) | [docs/routing.md — proximo.redirect](docs/routing.md#proximoredirect--opt-in-to-the-httphttps-redirect) |
| Health-gated routing & the `proximo.health=false` opt-out | [docs/routing.md — proximo.health](docs/routing.md#proximohealth--wait-for-the-container-to-be-healthy) |
| Curated middleware labels (auth/CORS/custom headers) & escape hatch | [docs/routing.md — proximo middlewares](docs/routing.md#proximo-middlewares--auth-cors-custom-headers) |
| Where watcher warnings appear | [docs/troubleshooting.md — Where to read watcher warnings](docs/troubleshooting.md#where-to-read-watcher-warnings) |
| 502/503 right after a container restarts (health gating fix) | [docs/troubleshooting.md — 502/503 right after a container restarts](docs/troubleshooting.md#502503-right-after-a-container-restarts) |
| Version skew & how updates apply | [docs/updating.md — proximo update](docs/updating.md#proximo-update) |
| What `install` changes on the host (sudo, reversal) | [docs/installation.md — What install changes on your host](docs/installation.md#what-install-changes-on-your-host) |
| Observability bootstrap (Beszel hub/agent) | [docs/observability.md — How it is wired](docs/observability.md#how-it-is-wired) |

## Conventions & gotchas

Code-level contracts live in the development guide — read the relevant section
before touching the corresponding code:

- Version → module ref (`moduleRef()` re-adds the `v` GoReleaser strips):
  [docs/development.md — Version and module ref](docs/development.md#version-and-module-ref)
- Embedded stack assets & sentinels (edits need rebuild **and** re-materialize):
  [docs/development.md — Embedded stack assets](docs/development.md#embedded-stack-assets)
- `PROXIMO_SRC` local-source builds vs published module:
  [docs/development.md — Local source builds](docs/development.md#local-source-builds)
- Releases (tag → GoReleaser) and the CI workflows:
  [docs/development.md — Releases](docs/development.md#releases)
- **OpenSpec** lives in `openspec/` — this repo uses a spec-driven change workflow for
  proposing features (`openspec/changes/`, `openspec/specs/`). Local-only tooling: the
  directory is gitignored and its artifacts never land in PRs.
