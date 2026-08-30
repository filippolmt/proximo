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

The binary is named per OS/arch (`bin/proximo-darwin-arm64`,
`bin/proximo-linux-amd64`, …) so a macOS and a Linux build never overwrite each
other in a shared working tree. Override the host target with
`make build GOOS=linux GOARCH=amd64`.

Run a **single test** (no Make target — invoke `go test` directly in the build
image):

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.26-alpine go test ./internal/docker/ -run TestModuleRef -v
```

If a local Go ≥1.26 toolchain is present you can run `go build/test/vet ./...`
directly; the Docker path is just the no-toolchain-required default.

## Lifecycle targets

These build first (so the binary always matches the host) and run the host
binary, which talks to the host Docker socket. They pass `PROXIMO_SRC=$(pwd)`
automatically so the in-stack images build from
[local source](#local-source-builds):

```sh
make install    # host setup (CA, resolver, trust) + start stack — asks for sudo
make up         # start the stack (no host changes)
make status     # list routed containers
make errors     # Exchanges from inspected routes (ARGS="--host web.test")
make down       # stop the stack
make uninstall  # reverse host changes + tear down
make e2e        # install + start the whoami demo + open https://whoami.test
make e2e-down   # stop demo + uninstall
```

## Local source builds

The stack's `dns` and `watcher` images are normally built with
`go install github.com/filippolmt/proximo/cmd/...@<ref>`, which needs the
module to be published. Setting `PROXIMO_SRC` to the checkout path (the Make
lifecycle targets do this) generates a `docker-compose.override.yml` that
builds those images **from local source** via `Dockerfile.dev` — no push, no
module fetch. Run the binary without `PROXIMO_SRC` to use the published module
instead.

## Version and module ref

Build metadata is injected via ldflags into `internal/version` (`Version`,
`Commit`, `Date`). GoReleaser's `{{ .Version }}` strips the leading `v`, so
`version.Version` is a **bare semver** (`0.1.0`). The in-stack images are built
with `go install …@${PROXIMO_REF}`, which requires a **canonical module
version** — so `internal/docker/stack.go:moduleRef()` re-adds the `v`
(`0.1.0`→`v0.1.0`; `dev`/empty→`main`). Keep `version.Version` usable as a
display string and normalize at the module-ref consumer, not at the goreleaser
source.

## Embedded stack assets

The compose project lives in `internal/docker/assets/` and is compiled into
the binary (`//go:embed`), then materialized to `~/.proximo/stack/` with the
`__TLD__`, `__DNSPORT__`, `__DATADIR__`, `__OBS_HUBPORT__` and `__INSPECTPORT__`
sentinels substituted. Two
consequences:

- **Editing an asset has no effect until the binary is rebuilt AND the stack
  re-materialized** (re-`install`/`up`/`update`).
- Installed users only get an asset fix on the **next release** — the binary
  carries its own asset copy.

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
(Homebrew cask `filippolmt/tap/proximo`, release archives). CI on PRs and
`main`: `.github/workflows/ci.yml` (build, vet, gofmt, test) and
`.github/workflows/docs.yml` (Markdown link + anchor check).
