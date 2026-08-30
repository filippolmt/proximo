# Development — building, testing, releasing

[← back to docs index](README.md)

Contributor guide: how to build and test from source, run the stack from a
local checkout, and how versioning and releases work.

## Build and test

No local Go toolchain is required — every Make target runs Go inside the
`golang` image (with a persistent module/build cache volume reused across
runs), so **Docker is the only prerequisite**:

```sh
make build      # build bin/proximo-<os>-<arch> for the host (Go runs in Docker)
make build-all  # cross-compile all targets (darwin,linux × amd64,arm64)
make test       # run the test suite (always Linux, in the golang image)
make vet        # go vet
make tidy       # go mod tidy
make check-links # validate Markdown links + anchors (lychee, offline — same check as CI)
```

Run `make tidy` whenever a change touches imports, and commit the resulting
`go.mod`/`go.sum`. CI (`.github/workflows/ci.yml`) runs the same checks on every
PR and on `main` — build, `go vet`, `gofmt -l`, `go mod tidy -diff` and the test
suite — and fails on an untidy module graph the same way it fails on unformatted
code.

The binary is named per OS/arch (`bin/proximo-darwin-arm64`,
`bin/proximo-linux-amd64`, …) so a macOS and a Linux build never overwrite each
other in a shared working tree. Override the host target with
`make build GOOS=linux GOARCH=amd64`.

Run a **single test** (no Make target — invoke `go test` directly in the build
image):

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.27-alpine go test ./internal/docker/ -run TestImageRef -v
```

If a local Go toolchain satisfying the `go` directive in `go.mod` is present you
can run `go build/test/vet ./...` directly; the Docker path is just the
no-toolchain-required default.

## Lifecycle targets

These build first (so the binary always matches the host) and run the host
binary, which talks to the host Docker socket. They pass `PROXIMO_SRC=$(pwd)`
automatically so the in-stack images build from
[local source](#local-source-builds):

```sh
make install    # host setup (CA, resolver, trust) + start stack — asks for sudo
make up         # start the stack (no host changes)
make status     # list routed containers
make doctor     # every check on this host, with a remedy per failure
make errors     # Exchanges from inspected routes (ARGS="--host web.test")
make down       # stop the stack
make uninstall  # reverse host changes + tear down
make e2e        # install + start the whoami demo + open https://whoami.test
make e2e-inspect # prove Inspection end to end against the running stack
make e2e-down   # stop demo + uninstall
```

## Local source builds

The stack's `dns`, `watcher` and `inspector` services normally run the
**published image** `ghcr.io/filippolmt/proximo:<version>` (one image, three
binaries, selected per service with an `entrypoint`). Setting `PROXIMO_SRC` to
the checkout path (the Make lifecycle targets do this) generates a
`docker-compose.override.yml` that builds that image **from local source** —
the same root `Dockerfile` the release pipeline uses, so the two paths cannot
drift — tags it `proximo:src` and points all three services at it. No push, no
pull. Run the binary without `PROXIMO_SRC` to use the published image instead.

To run a *published* image other than the pinned one — a `sha-…` build, a
digest, an image you built by hand — use
[`up --image` / `update --image`](updating.md#running-a-different-image) rather
than `PROXIMO_SRC`.

## Version and image ref

Build metadata is injected via ldflags into `internal/version` (`Version`,
`Commit`, `Date`) — for the CLI *and*, through the root `Dockerfile`, for the
three in-stack binaries. GoReleaser's `{{ .Version }}` strips the leading `v`,
so `version.Version` is a **bare semver** (`0.1.0`) while the published image
tag keeps it — so `internal/docker/stack.go:imageRef()` re-adds it
(`0.1.0`→`:v0.1.0`; `dev`/empty→`:main`). Keep `version.Version` usable as a
display string and normalize at the image-ref consumer, not at the goreleaser
source.

The in-stack binaries are `go build`-ed from the checkout, not `go install`-ed
from the module, so `debug.ReadBuildInfo().Main.Version` is empty in them:
their build-identity log line reads `internal/version` instead.

## Embedded stack assets

The compose project lives in `internal/docker/assets/` and is compiled into
the binary (`//go:embed`), then materialized to `~/.proximo/stack/` with the
`__TLD__`, `__DNSPORT__`, `__DATADIR__`, `__OBS_HUBPORT__` and `__INSPECTPORT__`
sentinels substituted. The image ref, the TLD and the CLI version are **not**
sentinels: they go through the generated `.env` (`PROXIMO_IMAGE`, `PROXIMO_TLD`,
`PROXIMO_VERSION`), which is what makes an `--image` override survive a
boot-time container restart. Two consequences:

- **Editing an asset has no effect until the binary is rebuilt AND the stack
  re-materialized** (re-`install`/`up`/`update`).
- Installed users only get an asset fix on the **next release** — the binary
  carries its own asset copy.

## The published skill (`skills/`)

The word "skill" means two things in this tree, and they are not the same
directory:

| Directory | What it is |
| --- | --- |
| `skills/proximo/` | What proximo **publishes** — the single source of the [agent Skill](skill.md), compiled into the binary and written out by `proximo skill install`. A public, stable path: a marketplace entry pins a `git-subdir` on it. |
| `.claude/skills/` | What proximo **consumes** while it is being developed — skills this repository vendors for its own contributors. Nothing ships from here. |

`skills/embed.go` exists only so the source has a Go package to be embedded
from: `//go:embed` cannot reach out of its own directory, and the source must
stay at `skills/proximo/`.

The Skill's `references/` quote contracts out of `docs/` verbatim, inside
`<!-- generated:start source=docs/<file>.md#<anchor> -->` markers. Regenerate
them after editing the source section:

```sh
make skill-refs
```

`go test ./...` fails when they drift, and the **Docs** workflow runs the same
check — CI ignores `docs/**`, and a docs edit is exactly what makes the Skill
drift. The generator takes the first table or ordered list in the named section
and rewrites every relative link to the published site: an installed copy has
no `docs/` beside it, so a repository-relative link out of the Skill would
resolve to nothing.

The Skill's version is the CLI's version. There is no independent release
cadence to reason about — and no way to ship a Skill fix without shipping a
binary.

## The injected agent

The [Inspection](observability.md#inspection--what-the-browser-saw) hop injects
`internal/inspect/assets/agent.js`, which is proximo's own — no bundle to build,
no version to pin, no artifact to regenerate. Edit it like any other source file;
`make build` embeds it.

It is one half of a contract whose other half is `internal/inspect/report.go`:
the agent posts JSON proximo defines, and the Go decodes it. Nothing compiles the
two together, so `TestAgent` checks that every field the decoder reads is one the
agent actually sets. Add a field to one side and that test tells you about the
other.

**Chrome is the supported browser.** The agent leans on what
`window.onerror` hands over — the message, the file, the line, the column and the
`Error` object, whose `stack` the browser has already formatted — plus
`unhandledrejection` and `securitypolicyviolation`. All of it is verified on
Chrome; other engines are likely to work and are not tested. The raw stack is
always kept on the report, so a stack proximo cannot parse into frames is printed
as the browser wrote it rather than dropped.

## Releases

Push a `vX.Y.Z` tag → `.github/workflows/release.yml` runs GoReleaser
(Homebrew cask `filippolmt/tap/proximo`, release archives), and
`.github/workflows/image.yml` publishes the stack image. CI on PRs and `main`:
`.github/workflows/ci.yml` ([the checks listed above](#build-and-test)) and
`.github/workflows/docs.yml` (Markdown link + anchor check).

### The stack image

`image.yml` builds the root `Dockerfile` for `linux/amd64` and `linux/arm64`
(cross-compiled from the build platform — Go needs no emulation) and publishes
**two tag families from two triggers**:

| Trigger | Tags |
| --- | --- |
| push to `main` | `main` (mobile), `sha-<short>` (immutable) |
| push of `vX.Y.Z` | `vX.Y.Z`, `latest` |

Mobile `vX.Y` / `vX` tags are deliberately **not** published: proximo pins the
exact version programmatically, and a mobile major tag would reintroduce the
skew the pinning exists to prevent. For the same reason **old version tags must
never be deleted** from GHCR, and the package must stay **public** — an
authenticated pull would put a `docker login` between a user and their first
`proximo up`.

The workflow's `paths:` filter cannot be narrow: the image is built from the
whole Go module, so it triggers on `**.go`, `go.mod`, `go.sum`, the `Dockerfile`
and the workflow itself.

A `smoke` job then brings the stack up from the **just-published digest**,
without `PROXIMO_SRC` and without logging in to GHCR. It is the only place the
pull path is exercised (every Make lifecycle target sets `PROXIMO_SRC`) and the
only place the watcher meets a real Docker socket — so a package that quietly
went private fails there, not on a user's first install.
