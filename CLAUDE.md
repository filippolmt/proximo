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
[docs/architecture.md](docs/architecture.md). Any other question about a guide: find its
heading in [docs/README.md](docs/README.md), which links every `##` section. The rows below
are the questions a heading does not answer:

| Question | Read |
| --- | --- |
| Which component owns what | [docs/architecture.md — Source map](docs/architecture.md#source-map) |
| What a domain term means, and which words to avoid | [CONTEXT.md](CONTEXT.md) |
| Why a design is the way it is, and what was rejected | [docs/adr/](docs/adr/) |
| Routing label contract & port resolution; which hosts are reserved for the stack | [docs/routing.md — The proximo labels](docs/routing.md#the-proximo-labels), [proximo.hosts](docs/routing.md#proximohosts--opt-in-and-pick-the-hosts) |
| Sharing a route with colleagues, and the team setup steps in order | [docs/sharing.md](docs/sharing.md), [docs/sharing-setup.md](docs/sharing-setup.md) |
| How peer routes, peer leaves and the peer DNS service are built, and why they are inert unconfigured | [docs/architecture.md — Peer sharing](docs/architecture.md#peer-sharing), [docs/adr/0010–0013](docs/adr/) |
| What a Check reports, and why `status` never prints a Remedy | [docs/cli.md — proximo doctor](docs/cli.md#proximo-doctor) |
| The Skill proximo ships to agents, and what makes a copy Managed | [docs/skill.md](docs/skill.md) |
| Transcript, Incident, Reading, Inspection: what each records, and what proximo refuses to store or conclude | [docs/observability.md](docs/observability.md), [docs/adr/0006–0008](docs/adr/) |
| The `proximo errors` output contract & the DOM snapshot | [docs/cli.md — proximo errors](docs/cli.md#proximo-errors) |

## Conventions & gotchas

Read the matching section **before** touching that code — each one is a contract the
compiler will not enforce:

- Adding a Check (the registry, its prerequisites, and the troubleshooting
  anchor a test asserts): [docs/adr/0004](docs/adr/0004-checks-report-remedies.md),
  `internal/checks/registry.go`
- Recording an Incident (only what the runtime declares, never a line that was
  read; the per-service retention and the derived id):
  [docs/adr/0007](docs/adr/0007-proximo-remembers-what-the-runtime-declares.md),
  `internal/docker/incident.go`
- The healthcheck Incident is a check that was *passing and stopped*, never one
  that merely reports unhealthy — and a Reading is a fact about one container, so
  a Service produces one per running replica:
  [docs/adr/0008](docs/adr/0008-proximo-measures-the-project-concludes.md),
  `internal/docker/incident.go`, `internal/transcript/read.go`
- Adding a `##` section to a guide in `docs/`: add its row to
  [docs/README.md](docs/README.md), the canonical section map —
  `internal/skill/docsindex_test.go` asserts both directions and fails the build
  otherwise
- The published Skill: its source is `skills/proximo/`, its `references/` are
  generated from `docs/` (`make skill-refs`, checked in CI), and no link out of
  it may be repository-relative:
  [The published skill](docs/development.md#the-published-skill-skills),
  [docs/adr/0005](docs/adr/0005-the-agent-skill-ships-in-the-cli.md)
- Any change to a command, a label or an output: check `skills/proximo/SKILL.md`
  against it. Agents outside this repository (e.g. inside toolbox) operate proximo
  through the Skill, and `make skill-refs` guards only the generated `references/`,
  never the hand-written `SKILL.md`
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
