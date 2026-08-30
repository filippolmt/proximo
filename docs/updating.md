# Updating proximo

[← back to docs index](README.md)

proximo ships as **two artifacts that must stay in lockstep**: the **CLI binary**
and the **stack containers** (`traefik`, `dns`, `watcher`, `inspector`). A single
release tag governs both — the CLI is stamped with its version, and it asks for
the stack image published under that same version.

## Mental model

- **The CLI is the unit you upgrade** (`brew upgrade`, `go install`, or a release
  tarball). The stack's Go services come from **one published multi-arch image**,
  `ghcr.io/filippolmt/proximo`, holding all three binaries; compose picks one per
  service with an `entrypoint`. Nothing is compiled on your host.
- **The image tag is the CLI's own version**, never `latest`. A binary installed
  at `v0.4.0` asks for `:v0.4.0` for as long as it stays installed, so it can
  never pull a watcher speaking a label contract it has never seen. (`latest` is
  published for a human browsing the package page; proximo never reads it.)
- **The running stack is a projection of the CLI.** Upgrading the CLI does *not*
  touch the running containers, so a freshly upgraded CLI can speak a label
  contract the old watcher does not honor — routing then breaks silently.
- **`proximo update` converges the projection.** It re-materializes the embedded
  assets, pulls the stack image pinned to the CLI version, and re-pulls Traefik,
  so the running stack matches the installed CLI.

## proximo update

```sh
proximo update
```

- **Idempotent** — when the stack already matches the CLI it reports
  "up to date" and recreates nothing.
- **Never needs sudo** — it performs only Docker operations (no resolver/CA
  changes), so it is the safe escape hatch for any stack problem.
- **Soft no-op** — when Docker is unreachable or no stack is running it prints
  that the update will apply on the next `proximo up` and exits 0 (so it is safe
  to call from automated hooks — the Homebrew cask calls it on install).
- **Re-pulls Traefik** — the pinned Traefik image is re-pulled on every converge
  (best-effort, so an offline host still converges from what it has), because
  upstream moves that tag for security patches.
- **Legacy stacks converge too** — a stack deployed by a pre-0.4.0 CLI carries
  no `proximo.version` label; `update` treats it as skewed (not as "no stack")
  and converges it, and `status` warns about it.
- `proximo update --force` **pulls even when the tag is already cached**. It is
  a benign no-op on an immutable `vX.Y.Z`, and the only way to advance a mobile
  ref that has not changed name.

### Mobile vs. release refs

A **release tag** (`vX.Y.Z`) and a digest are published once and never moved, so
a cached copy is necessarily current: `update` pulls it only when it is missing.
A **mobile ref** (`main`, `sha-…`, `latest`, a locally built tag) can change
under a fixed name, so it is pulled on every converge.

### Nothing is pruned

Images of superseded versions stay on disk. `update` deletes nothing — implicitly
removing an image during a routine convergence is not a thing proximo does, and
keeping the previous version cached makes a downgrade instant.
[`proximo uninstall`](cli.md#proximo-uninstall) removes them with the rest — the
proximo images only. Traefik and the dashboard images are left alone: proximo
did not author them, and you may be sharing them with another project.

## Running a different image

```sh
proximo up --image ghcr.io/filippolmt/proximo:sha-1a2b3c4
proximo update --image proximo:src      # e.g. an image you built locally
```

The escape hatch for testing an unreleased build. It takes a ref **verbatim**, so
a tag, a digest or a locally built image all work, and it replaces the **whole
stack** — never one component, because `watcher` and `inspector` hold a live
contract with each other and a mixed pair is a footgun no CI has ever built.

It is **sticky**: the ref is written into the materialized `.env`, so containers
restarting at boot keep it. While it is in effect:

- `proximo doctor` reports the overridden ref as a failure, with `proximo up`
  as the remedy,
- `proximo update` never reports "up to date" — the stack is not running the
  CLI's image,
- the next `up` or `update` **without** `--image` clears it and prints the
  reversal.

Any other command that converges the stack — `proximo install`,
`proximo config tld` — clears it too, since neither takes the flag. They print
the same reversal line, so the swap is never silent.

`proximo install` has no `--image`: first-run host setup installs the canonical
thing.

## When does an update apply?

| Trigger | What happens |
| --- | --- |
| `proximo update` | Converge now (the manual path). |
| `proximo up` | Applies any pending convergence on start — same code path as `update`. So a stack that was stopped at upgrade time is brought to the installed CLI version when next started. |
| [`proximo doctor`](cli.md#proximo-doctor) | **Read-only.** Reports a stack version differing from the CLI, and an `--image` override in effect, each with its remedy. It never converges. |
| Homebrew upgrade | The cask's post-install hook runs `proximo update`, so the stack converges with the CLI. It cannot fail the upgrade: if Docker was down, the caveat says it applies on the next `proximo up`. |

Outside Homebrew this is deliberately **not** push auto-update: after a CLI
upgrade the stack stays on the old version until you run `proximo update` or
restart with `proximo up`. The `doctor` skew check makes the pending update
visible. proximo installs no scheduled unit and runs no in-stack updater — the
stack is pinned to the version of the binary that started it, so an in-stack
updater would have to overtake its own CLI.

## Linux

Linux has no Homebrew cask, so the manual model applies: this guide's nudge, the
`proximo doctor` skew check, and `proximo update` (or the next `proximo up`)
converge the stack.

See the [CLI reference](cli.md#proximo-update) for the command summary,
[Architecture](architecture.md) for how the stack is wired, and
[ADR 0002](adr/0002-stack-services-ship-as-one-published-image.md) for why the
services ship as one image pinned to the CLI version.
