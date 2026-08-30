# CLAUDE.md

## What proximo is

A single Go CLI that makes any running Docker container reachable at `https://<name>.test`
on macOS/Linux, bundling `Host`-based routing (Traefik), local wildcard DNS, and a trusted
local CA. Docker is the only mandatory prerequisite — DNS and certificates are produced
natively in Go.

## Build / test / run

All Go work runs **inside Docker** via the Makefile — no local Go toolchain is assumed.
Targets, the single-test invocation, and the lifecycle targets
(`install`/`up`/`down`/`status`/`uninstall`/`e2e`):
[docs/development.md — Build and test](docs/development.md#build-and-test),
[Lifecycle targets](docs/development.md#lifecycle-targets).

## Doc map (question → section)

Architecture lives in `docs/`, never here. Whole picture:
[docs/architecture.md](docs/architecture.md); every guide, section by section:
[docs/README.md](docs/README.md).

| Question | Read |
| --- | --- |
| Which component owns what | [docs/architecture.md — Source map](docs/architecture.md#source-map) |
| What a domain term means, and which words to avoid | [CONTEXT.md](CONTEXT.md) |
| Why a design is the way it is, and what was rejected | [docs/adr/](docs/adr/) |
| Routing label contract & port resolution | [docs/routing.md — The proximo labels](docs/routing.md#the-proximo-labels) |
| Which hosts are reserved for the stack | [docs/routing.md — proximo.hosts](docs/routing.md#proximohosts--opt-in-and-pick-the-hosts) |
| The bare + qualified host every route answers on | [docs/routing.md — The two hosts every route gets](docs/routing.md#the-two-hosts-every-route-gets) |
| A host collision: what is reported and what to do | [docs/troubleshooting.md — A host collision is reported](docs/troubleshooting.md#a-host-collision-is-reported) |
| What a Check reports, and why `status` never prints a Remedy | [docs/cli.md — proximo doctor](docs/cli.md#proximo-doctor) |
| The Skill proximo ships to agents, and what makes a copy Managed | [docs/skill.md](docs/skill.md) |
| `skills/` (published) vs `.claude/skills/` (consumed), and `make skill-refs` | [docs/development.md — The published skill](docs/development.md#the-published-skill-skills) |
| HTTP→HTTPS redirect semantics (opt-in, 302) | [docs/routing.md — proximo.redirect](docs/routing.md#proximoredirect--opt-in-to-the-httphttps-redirect) |
| Health-gated routing & the `proximo.health=false` opt-out | [docs/routing.md — proximo.health](docs/routing.md#proximohealth--wait-for-the-container-to-be-healthy) |
| Curated middleware labels (auth/CORS/custom headers) & escape hatch | [docs/routing.md — proximo middlewares](docs/routing.md#proximo-middlewares--auth-cors-custom-headers) |
| Browser stops trusting the cert; re-trust the CA stack-safe | [docs/cli.md — proximo trust](docs/cli.md#proximo-trust) / [troubleshooting.md — Certificate warnings](docs/troubleshooting.md#certificate-warnings-in-firefox-or-chrome) |
| Where watcher warnings appear | [docs/troubleshooting.md — Where to read watcher warnings](docs/troubleshooting.md#where-to-read-watcher-warnings) |
| 502/503 right after a container restarts (health gating fix) | [docs/troubleshooting.md — 502/503 right after a container restarts](docs/troubleshooting.md#502503-right-after-a-container-restarts) |
| A stack image pull that failed (the Remedy) | [docs/troubleshooting.md — The stack image cannot be pulled](docs/troubleshooting.md#the-stack-image-cannot-be-pulled) |
| Version skew & how updates apply | [docs/updating.md — proximo update](docs/updating.md#proximo-update) |
| Where the stack's Go binaries come from; running a different image | [docs/updating.md — Running a different image](docs/updating.md#running-a-different-image) |
| What `install` changes on the host (sudo, reversal) | [docs/installation.md — What install changes on your host](docs/installation.md#what-install-changes-on-your-host) |
| Client-side errors correlated with the response (`proximo.inspect`) | [docs/observability.md — Inspection](docs/observability.md#inspection--what-the-browser-saw) |
| The `proximo errors` output contract & the DOM snapshot | [docs/cli.md — proximo errors](docs/cli.md#proximo-errors) |
| Observability bootstrap (Beszel hub/agent) | [docs/observability.md — How it is wired](docs/observability.md#how-it-is-wired) |

## Conventions & gotchas

Read the matching section **before** touching that code — each one is a contract the
compiler will not enforce:

- Adding a Check (the registry, its prerequisites, and the troubleshooting
  anchor a test asserts): [docs/adr/0004](docs/adr/0004-checks-report-remedies.md),
  `internal/checks/registry.go`
- The published Skill: its source is `skills/proximo/`, its `references/` are
  generated from `docs/` (`make skill-refs`, checked in CI), and no link out of
  it may be repository-relative:
  [The published skill](docs/development.md#the-published-skill-skills),
  [docs/adr/0005](docs/adr/0005-the-agent-skill-ships-in-the-cli.md)
- Version → image ref (`imageRef()` re-adds the `v` GoReleaser strips, and pins
  the stack image to the CLI version):
  [Version and image ref](docs/development.md#version-and-image-ref)
- Embedded stack assets & sentinels (edits need rebuild **and** re-materialize):
  [Embedded stack assets](docs/development.md#embedded-stack-assets)
- `PROXIMO_SRC` local-source builds vs the published image:
  [Local source builds](docs/development.md#local-source-builds)
- The injected agent & its contract with the Go decoder (Chrome-only):
  [The injected agent](docs/development.md#the-injected-agent)
- Releases (tag → GoReleaser) and the CI workflows:
  [Releases](docs/development.md#releases)

## graphify

Knowledge graph at `graphify-out/`.

- Codebase question: run `graphify query "<question>"` first when `graphify-out/graph.json`
  exists — it returns a scoped subgraph, far smaller than `GRAPH_REPORT.md` or raw grep.
  `graphify path "<A>" "<B>"` for relationships, `graphify explain "<concept>"` for one concept.
- Broad navigation: `graphify-out/wiki/index.md` when it exists.
- `GRAPH_REPORT.md`: broad architecture review only, or when query/path/explain fall short.
- After changing code: `graphify update .` (AST-only, no API cost).
