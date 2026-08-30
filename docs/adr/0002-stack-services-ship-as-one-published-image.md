---
status: accepted
---

# The stack's Go services ship as one published image, pinned to the CLI version

Until now every host compiled the stack for itself: `dns`, `watcher` and
`inspector` each carried a `build:` stanza that ran
`go install github.com/filippolmt/proximo/cmd/<x>@${PROXIMO_REF}` inside
`golang:alpine`, so a first `proximo up` pulled a ~350 MB toolchain image and ran
three module downloads and three compilations before anything served a request.
Instead, the release pipeline builds **one** multi-architecture image,
`ghcr.io/filippolmt/proximo`, holding all three binaries, and compose selects the
service with a per-service `entrypoint:`. The image tag is the CLI's own version —
the same `dev`→`main`, `0.4.0`→`v0.4.0` mapping `moduleRef()` already applied to
the module ref, moved to the image ref.

Pinning to the CLI version rather than tracking `latest` is what keeps the two
halves of a release honest. The binary embeds its own copy of the compose assets
and stamps `proximo.version` on every stack service, and `proximo update` uses
that label to detect skew. A floating tag would let a binary installed weeks ago
pull a watcher that speaks a label contract it has never seen, and would do it
precisely where the existing skew detection cannot look: between the binary and
the image, not between the binary and the materialised stack.

One image rather than three, and the usual argument for that is wrong here. The
bytes are a wash: with three images the containers share the `alpine` base layer
by digest, so the download is 5.374 + 6.554 + 7.275 MB of binaries either way
(measured, `-trimpath -ldflags "-s -w"`, linux/amd64). What actually decides it
is publishing cost — three GHCR packages, three BuildKit cache refs, and a matrix
of 3 components × 2 architectures instead of 2 build legs and one manifest merge
— and version coherence: `watcher` and `inspector` hold a live contract, since
the watcher routes inspected hosts at the hop and hands it the real backend in
`X-Proximo-Backend` while the hop mints the correlation id the watcher must find
again in the Access record. Per-component images would make it easy to run a
combination no CI has ever built, and to break it silently on exactly that
header.

## Considered Options

- **Three images, one per service.** The only axis where it wins is granularity
  of the experimental override — pull an experimental `inspector` under a release
  `watcher` — and that is the axis where the watcher↔inspector contract turns a
  capability into a footgun. It also carries a weak security argument: with one
  image the browser-facing `inspector` container ships the `watcher` binary too.
  The socket that binary needs is not mounted there, so it is inert, and anyone
  running code in that container could fetch it from GHCR in a second. It buys
  hygiene, not a barrier.
- **One multi-call binary** (three `main`s fused, dispatching on `argv[1]`).
  Would save the Go runtime linked three times, roughly 19 → 12 MB, but costs the
  fusion of three thirty-line `main`s that are readable as they are, and makes
  incremental pulls *worse*: a single binary is a single layer that moves whole
  on any change.
- **`:latest`.** Rejected for the skew reason above. `latest` is still published,
  for a human browsing the package page; proximo never reads it.
- **Falling back to a host build when the pull fails.** It is the worst kind of
  silent fallback: it fails on one network path (GHCR) and retries on a path that
  needs the same network (the Go module proxy), so it usually fails twice, ten
  times slower — and in the rare case it succeeds, the developer is running an
  image nobody else has, without knowing. A failed pull reports a **Remedy**
  instead.
- **`go install …@<ref>` inside the published Dockerfile.** Would have kept
  `debug.ReadBuildInfo().Main.Version` populated, but it drags back in the exact
  dependency this change removes — the module must be published and resolvable —
  and adds a race between pushing the tag and the module becoming resolvable. The
  runner already has the commit; `go build` from the checkout with the same
  `-ldflags -X …/internal/version.Version` the CLI already uses is shorter and
  reproducible from the commit alone.
- **A self-updating binary.** Rejected on both install paths: on macOS the binary
  lives in a Homebrew-owned path, and rewriting it underneath breaks the next
  `brew upgrade`; on Linux the tarball sits wherever the user put it, which may
  need sudo — and `proximo update` is documented as never needing sudo, which is
  what makes it the safe escape hatch for a broken stack.
- **An updater inside the stack** (a Watchtower-shaped watcher that polls the
  registry). Excluded by construction: the stack is pinned to the version of the
  binary that started it, so an in-stack updater would have to overtake its own
  CLI, manufacturing the inverse of the skew this ADR exists to prevent.
- **A scheduled launchd/systemd unit** running `proximo update`. Rejected: it
  adds a permanent host mutation to an `install` that today touches only the
  resolver, the CA and trust — all of them enumerated and reversible — and does
  network work unasked on a development machine.
- **Per-component overrides** (`PROXIMO_IMAGE_WATCHER=…`). Same footgun as three
  images, without even the packaging cost to show for it.
- **A registry mirror setting.** Deferred, not rejected: it is purely additive
  and breaks no existing install if added later.

## Consequences

- **Published tags are a contract that outlives the release.** A binary installed
  at v0.4.0 asks for `:v0.4.0` for as long as it stays installed, so old version
  tags must never be deleted or garbage-collected from GHCR, and the package must
  be **public** — an authenticated pull would put a `docker login` between a user
  and their first `proximo up`. Retention policy on that package is now load-bearing.
- **Two tag families, two triggers.** A push to `main` publishes `main` and
  `sha-<short>`; a `v*` tag publishes `vX.Y.Z` and `latest`. Both are needed
  because `moduleRef()`'s `dev`→`main` mapping survives as `imageRef()`'s. Mobile
  `vX.Y` / `vX` tags are deliberately not published: proximo pins the exact
  version programmatically, and a mobile major tag would reintroduce the skew.
  The `paths:` filter cannot mirror the toolbox one — the images depend on the
  whole Go module, so the trigger is `**.go`, `go.mod`, `go.sum`, the Dockerfile
  and the workflows.
- **The pull path has no human who exercises it.** `make up` and `make install`
  set `PROXIMO_SRC`, so a contributor always builds from local source and never
  touches the published path. It is covered by a CI job that brings the stack up
  without `PROXIMO_SRC` after the manifest merge — which is also the only place
  the `watcher` runs against a real Docker socket.
- **Homebrew now converges instead of only nudging.** The cask hook runs
  `proximo update`, which is safe there because it is already a soft no-op that
  exits 0 when Docker is unreachable. The caveat stays, rewritten for exactly
  that case: an upgrade run while Docker is down converges on the next
  `proximo up`. This reverses the reasoning in `docs/updating.md`, which ruled out
  a hook because Homebrew discourages heavy work — the heavy work was the build,
  and it is gone.
- **`proximo update --force` changes meaning**, from "rebuild without the build
  cache" to "pull even when the tag is already cached". It is a benign no-op on
  an immutable `vX.Y.Z` and the only way to advance a mobile ref. The mobile/
  immutable asymmetry `docs/updating.md` already documents for builds carries over
  verbatim to pulls: mobile refs pull always, release refs pull only when missing.
- **Images of superseded versions stay on disk.** `update` prunes nothing —
  implicitly deleting an image during a routine convergence is not a thing proximo
  does, and keeping the previous version cached makes a downgrade instant.
  `uninstall` removes them with the rest.
- **The `--image` escape hatch is a flag, not an environment variable**, on `up`
  and `update` only — `install` is the first-run host setup and installs the
  canonical thing. It takes a ref verbatim, so it also accepts a digest or a
  locally built image, and it replaces the whole stack, never one component. It
  is written into the materialised `.env` so containers restarting at boot keep
  it, which makes it sticky: it is cleared by an `up` or `update` without the
  flag, and that reversal is printed. While it is in effect the stack records the
  effective ref (`proximo.image`), `proximo status` prints it, and `update` never
  reports "up to date" — a stack that runs one thing and declares another is the
  same class of defect as the **Collision** debt already recorded in `CONTEXT.md`.
- **`debug.ReadBuildInfo().Main.Version` goes empty** in the stack binaries, since
  they are no longer produced by `go install <module>@<ref>`. The build-identity
  log line in `cmd/watcher/main.go` moves to `internal/version.Version`, and the
  comment above it stops being true as written.
- **`docs/updating.md` is falsified by this change** and needs rewriting, not
  patching: it states that proximo publishes no container images and that every
  user builds locally, and its closing section justifies the absence of push
  auto-update with a cost that no longer exists.
- **No new glossary term.** Where the watcher's binary comes from is an
  implementation vehicle, and `CONTEXT.md` opens by saying Docker is exactly that
  — not the purpose. No developer describes proximo in these words, so the
  glossary does not move.
- **Two techniques from the sibling `toolbox` image pipeline are deliberately not
  ported**: the Invalidation Floor gate and the `--link`/frozen-mtime layer
  discipline. Both exist there because that image is large and deeply layered.
  Here the whole payload is 19 MB of static binaries, and the most a perfect
  layer split could save is a few MB per release.
