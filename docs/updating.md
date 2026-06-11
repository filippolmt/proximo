# Updating proximo

[← back to docs index](README.md)

proximo ships as **two artifacts that must stay in lockstep**: the **CLI binary**
and the **stack containers** (`traefik`, `dns`, `watcher`). A single release tag
governs both — the CLI is stamped with its version, and the in-stack `dns` /
`watcher` images are built locally at that same version.

## Mental model

- **The CLI is the unit you upgrade** (`brew upgrade`, `go install`, or a release
  tarball). proximo publishes no container images — every user builds `dns` /
  `watcher` locally inside Docker.
- **The running stack is a projection of the CLI.** Upgrading the CLI does *not*
  touch the running containers, so a freshly upgraded CLI can speak a label
  contract the old watcher does not honor — routing then breaks silently.
- **`proximo update` converges the projection.** It re-materializes the embedded
  assets, rebuilds the in-stack images at the CLI version, and re-pulls Traefik,
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
  to call from automated hooks).
- **Re-pulls Traefik** — the pinned Traefik image is re-pulled so security
  patches are picked up.
- **Legacy stacks converge too** — a stack deployed by a pre-0.4.0 CLI carries
  no `proximo.version` label; `update` treats it as skewed (not as "no stack")
  and converges it, and `status` warns about it.
- `proximo update --force` rebuilds the in-stack images **without the build
  cache** (the nuclear option).

### Mobile vs. release refs

For a **release tag** (`vX.Y.Z`) the build cache is reused — a new tag changes
the build inputs and cache-misses naturally, so updates are fast. For a **mobile
ref** (`main` / a dev build) `update` rebuilds with `--no-cache`, because
BuildKit would otherwise reuse a stale `go install …@main` layer and the new
source would never land.

## When does an update apply?

| Trigger | What happens |
| --- | --- |
| `proximo update` | Converge now (the manual path). |
| `proximo up` | Applies any pending convergence on start — same code path as `update`. So a stack that was stopped at upgrade time is brought to the installed CLI version when next started. |
| `proximo status` | **Read-only.** Warns when the running stack version differs from the CLI and points you to `proximo update`. It never rebuilds. |
| Homebrew upgrade | The cask shows a **caveat** nudging you to run `proximo update`. It runs **no** Docker build in the hook (Homebrew discourages heavy/network work, and Docker may be down). |

This is deliberately **not** push auto-update: after a CLI upgrade the stack
stays on the old version until you run `proximo update` or restart with
`proximo up`. The `status` skew warning and the cask nudge make the pending
update visible.

## Linux

Linux has no Homebrew cask, so the same model applies uniformly: this guide's
nudge, the `proximo status` skew warning, and `proximo update` (or the next
`proximo up`) converge the stack.

See the [CLI reference](cli.md#proximo-update) for the command summary and
[Architecture](architecture.md) for how the stack is built.
