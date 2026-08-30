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

## The vendored browser agent

The [Inspection](observability.md#inspection--what-the-browser-saw) hop injects
`@sentry/browser` into inspected pages. Its npm package ships only ESM/CJS, so
the browser bundle is **built here and committed** at
`internal/inspect/assets/sentry.min.js`, then embedded: building proximo — and
building the stack images from the published module — needs nothing but the
module itself, and an inspected page works with no network.

```sh
make vendor-agent                        # rebuild at the pinned versions
make vendor-agent SENTRY_VERSION=10.73.0 # bump the SDK
```

The target runs npm + esbuild in `node:22-alpine` (both versions pinned in the
Makefile) over an entry that re-exports only `init`, so tree-shaking drops
tracing, replay, feedback and the AI integrations — about 89 KB raw, 30 KB
gzipped. The hop serves it gzipped from a content-addressed, immutable URL, so an
inspected page fetches it once per proximo version. The output carries a provenance banner; it is marked `-diff
linguist-vendored` and is never hand-edited.

`make build` depends on the file, so a checkout that somehow lacks it rebuilds it
before compiling. If it is missing at runtime the `inspector` container refuses
to start and names the command, rather than serving a script that silently does
nothing.

### Renovate keeps the pins, you rebuild the artifact

`renovate.json` watches all four pins in the Makefile — `SENTRY_VERSION`,
`ESBUILD_VERSION`, `NODE_IMAGE` and `PUPPETEER_IMAGE` — so an update arrives as a
PR like any other. What Renovate **cannot** do is rebuild a committed binary
artifact: on its own, a `SENTRY_VERSION` bump would change one line and leave the
shipped bundle untouched.

`TestVendoredSDKMatchesMakefile` is what closes that. It compares the pins against
the provenance banner stamped into the bundle, so a Renovate PR fails CI until:

```sh
make vendor-agent        # rebuild the bundle at the new pin
make capture-envelope    # re-record the parser's fixture from it
```

pushed onto the same PR. `ESBUILD_VERSION` is checked the same way. The `node` and
`puppeteer` images only affect how the artifact is produced, so their bumps need
nothing.

### After a version bump

The hop parses the Sentry envelope format, which proximo does not own, and
attaches the DOM Snapshot through `hint.attachments` in `beforeSend`, which the
SDK documents loosely. Both can change without any error — an inspected page
would keep working and quietly report less. So the parser is tested against an
envelope captured from the vendored agent in a real browser:

```sh
make vendor-agent SENTRY_VERSION=<new>
make capture-envelope     # re-record the fixture, then run the tests
```

`TestFixtureMatchesVendoredSDK` fails while the fixture and the bundle name
different versions, so the second command cannot be forgotten. (This is not
theoretical: the first capture immediately showed the SDK sends `breadcrumbs` as
a bare array, not the `{"values": …}` wrapper the store format documents.)

## Releases

Push a `vX.Y.Z` tag → `.github/workflows/release.yml` runs GoReleaser
(Homebrew cask `filippolmt/tap/proximo`, release archives). CI on PRs and
`main`: `.github/workflows/ci.yml` (build, vet, gofmt, test) and
`.github/workflows/docs.yml` (Markdown link + anchor check).
