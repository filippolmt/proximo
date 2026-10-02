# CLI reference

[← back to docs index](README.md)

`proximo` is a **one-shot orchestrator**: every command performs its action and
exits. There is no background proximo process on your host — only the stack
containers keep running between commands.

```
proximo <command> [args]
```

| Command | Summary | Needs sudo | Needs Docker |
| --- | --- | --- | --- |
| [`install`](#proximo-install) | Full host setup + start the stack | yes | yes |
| [`up`](#proximo-up) | Start the stack only (`--observability` adds the dashboards) | no | yes |
| [`down`](#proximo-down) | Stop the stack only | no | yes |
| [`update`](#proximo-update) | Converge the running stack to the installed CLI | no | yes |
| [`trust`](#proximo-trust) | Re-trust the local CA (system + NSS), stack-safe | yes | no |
| [`status`](#proximo-status) | List routed containers and URLs | no | yes |
| [`doctor`](#proximo-doctor) | Report every check, with a remedy per failure | no | no |
| [`config tld <tld>`](#proximo-config-tld) | Change the routed TLD | yes | yes |
| [`config ca-path`](#proximo-config-ca-path) | Print the local CA certificate path | no | no |
| [`config machine <label>`](#proximo-config-machine) | Set this machine's label in its peer names | no | no |
| [`config peer-suffix <suffix>`](#proximo-config-peer-suffix) | Set the suffix every peer name lives under | no | no |
| [`config address <ip>`](#proximo-config-address) | Set the mesh address this machine's peer names answer with | no | no |
| [`config team-root <path>`](#proximo-config-team-root) | Point proximo at the team root certificate | no | no |
| [`config csr`](#proximo-config-csr) | Print this machine's CSR for its intermediate | no | no |
| [`config intermediate <file>`](#proximo-config-intermediate) | Install the intermediate signed from that CSR | no | no |
| [`config mesh-remedy <command>`](#proximo-config-mesh-remedy) | Set the Remedy the `mesh` Check offers | no | no |
| [`config unset <value>`](#proximo-config-unset) | Return a peer-sharing value to unconfigured | no | no |
| [`skill install`](#proximo-skill-install) | Install the agent Skill for the coding agents on this host | no | no |
| [`skill uninstall`](#proximo-skill-uninstall) | Remove the Skill copies proximo installed | no | no |
| [`uninstall`](#proximo-uninstall) | Reverse all host changes + stop the stack | yes | yes |
| [`version`](#proximo-version) | Print version, commit, build date | no | no |

---

## proximo install

Preflight, generate the CA, configure the host resolver, install CA trust, and
start the stack. The widest-reaching privileged command — it is the only one
that touches the host resolver (`trust` and `config tld` also need sudo, but for
narrower changes).

```sh
proximo install
```

Preflight is the subset of [`proximo doctor`](#proximo-doctor)'s checks that is
meaningful before the host has been changed — Docker, and who holds `:80`,
`:443` and the DNS port — so a failure stops the command before it touches
anything. `up` runs the same gate.

Idempotent on the parts that allow it: the CA is generated once and reused; the
resolver and trust steps re-apply cleanly. See
[Installation](installation.md#step-2--one-time-host-setup) for the full step
list and exactly what it writes to your host.

## proximo up

Start (or rebuild) the embedded stack **without** touching host configuration.
Use it after a reboot or a `down`.

```sh
proximo up
```

Requires that the Docker daemon is reachable. If you run `up` before `install`,
the CA may not exist yet — the watcher then runs without issuing certificates
until you `install`.

With the stack up, Traefik's own **dashboard** is always served at
`https://traefik.<tld>` — read-only and local-only (no `api.insecure`, no extra
published port, no credentials), trusted by the local CA like every other
proximo host. `traefik.<tld>` is **reserved** for the stack; do not assign it to
your own containers.

`up` shares the convergence path with [`update`](#proximo-update), so it also
applies any pending update — pulling the stack image pinned to the installed CLI
version and re-pulling Traefik. See [Updating](updating.md).

### --image

```sh
proximo up --image ghcr.io/filippolmt/proximo:sha-1a2b3c4
```

Run the stack from this image ref instead of the version-pinned one. Takes the
ref **verbatim** (a tag, a digest, or a locally built image) and replaces the
**whole stack**, never one component. It is **sticky** — written into the
materialized `.env`, so containers restarting at boot keep it — and the next
`up` or `update` without the flag clears it and says so. See
[Running a different image](updating.md#running-a-different-image).

### --observability

```sh
proximo up --observability
```

Bring up the core stack **and** the opt-in logs (Dozzle) + metrics (Beszel)
dashboards in one command, run the metrics bootstrap, and print the dashboard
URLs (`https://logs.<tld>`, `https://metrics.<tld>`). Both dashboards opt into the
HTTP→HTTPS redirect, so `http://logs.<tld>` / `http://metrics.<tld>` auto-redirect
to the trusted https host. Off by default: a plain `up` starts neither. `down` /
`uninstall` tear them down too. See [Dev-time observability](observability.md).

**Peer sharing.** When the peer DNS service cannot start, `up` succeeds, prints a
warning naming `peer-dns` and, when it is the one it can see, the cause — the
address is held by no interface of this machine — and exits 0, because `.test`
works. A peer-side failure never fails
the command that serves `.test`.

## proximo down

Stop and remove the stack containers — the core services **and** the opt-in
observability dashboards (the profile is enabled on teardown, so they do not
linger). Host configuration (resolver, trust) is left untouched, so a later `up`
brings everything back.

```sh
proximo down
```

A no-op if the stack was never materialized.

### --observability

Stop **only** the observability dashboards (Dozzle + Beszel), leaving the core
stack running:

```sh
proximo down --observability
```

## proximo update

Converge the running stack to the **installed CLI version**: re-materialize the
embedded assets, pull the stack image tagged with the CLI version, and re-pull
Traefik (security patches). Run it after upgrading the CLI (`brew upgrade` /
`go install`) — it is also the safe escape hatch for any stack problem.

```sh
proximo update
proximo update --force            # pull even when the tag is already cached
proximo update --image <ref>      # same escape hatch as `up --image`
```

- **Idempotent**: prints "up to date" and recreates nothing when the stack
  already matches the CLI — and never says so while an `--image` override is in
  effect, because then the stack is not running the CLI's image.
- **Never needs sudo**: Docker operations only — no resolver or CA changes.
- **Soft no-op**: when Docker is unreachable or no stack is running it reports
  that the update will apply on the next `proximo up` and exits 0, so calling it
  from automation cannot fail a build.
- **Prunes nothing**: superseded images stay on disk, so a downgrade is instant.
  `uninstall` removes them.
- Shares the convergence code path with `proximo up`, so "update now" and
  "update on next start" cannot drift.

See [Updating proximo](updating.md) for the full model.

## proximo trust

Re-add the local CA to the OS system trust store and, when present, the NSS
store (Firefox / Chromium). It is the trust step of `install` on its own:

```sh
proximo trust
```

Use it when a browser stops trusting `https://<name>.<tld>` (an
`ERR_CERT_AUTHORITY_INVALID` / "issuer not trusted" warning) — typically because
the CA never made it into the browser's store or was regenerated.

- **Stack-safe**: it runs no checks and never touches DNS or the Docker stack,
  so it works while proximo is up — no `down`/`up` cycle.
- **Idempotent**: the system-store add is a no-op when already trusted; the NSS
  add removes any stale entry first. Re-run it freely.
- **Needs sudo, no Docker**: it only writes host trust stores.
- Reuses the existing CA (it never rotates it), so already-issued certificates
  stay valid. **Fully restart the browser afterwards** to pick up the CA.

**Peer sharing.** `trust` and `install`: with `team-root` set, also install the team root
in the system and NSS stores, as a second anchor, under names of its own; without
it, they behave exactly as they would on a machine that does not share. The file
is validated again first, and the anchor installed is recorded, so a replaced root
takes the old one out and `uninstall` removes it even once the file is gone.

## proximo status

List the **effective** routing state — the routes the watcher actually serves,
not just declared intent. It uses the same classifier the watcher uses, so the
two never disagree. Hosts come from the `proximo.hosts` label when present,
otherwise from native Traefik router rules; for a `proximo.hosts` route the
backend port is resolved the same way the watcher resolves it (explicit
`proximo.port`, else the single exposed TCP port).

```sh
proximo status
```

```
CONTAINER          URL
shop-api-1         https://api.test  + api.shop.test
proximo-traefik-1  https://traefik.test
whoami             https://whoami.test  + whoami.proximo-demo.test
```

Each route lists its **bare host** as the URL and, after `+`, the **qualified
host** it also answers on — one row per declared host, never two. See
[the two hosts every route gets](routing.md#the-two-hosts-every-route-gets); a
container outside a Compose project has no qualified host and shows none, and
neither do the stack's own routes.

A host a container did **not** get, because another container claims it, is a row
of its own carrying the reason and naming the winner:

```
CONTAINER   URL
shop-api-1  https://api.test  + api.shop.test
work-api-1  ⚠ api.test is served by shop-api-1; this container answers at api.work.test
```

A collision costs a bare host, not a service — see
[a host collision is reported](troubleshooting.md#a-host-collision-is-reported).

The `traefik.<tld>` route is the stack's own
[dashboard](#proximo-up) — listed whenever the stack is running, since the
watcher serves it unconditionally.

[TCP routes](routing.md#proximotcpport--route-tcp-services-by-name-sni) appear
alongside HTTP ones, showing the SNI host, backend port(s), and TLS mode; a route
served by several replica containers is marked `(balanced ×N)`:

```
CONTAINER  URL
db         tcp://db.test:5432 (terminate)  + db.shop.test
web        https://app.test (balanced ×2)  + app.shop.test
```

The port in a `tcp://` line is the **backend** port; clients still connect on
`:443` with SNI (`db.test`), and the proxy routes the stream to that port.

A `proximo.hosts` container whose backend port is **ambiguous** (no
`proximo.port`, and the image exposes zero or several ports) is not served by the
watcher, so it is **not** shown as a working route — it is flagged instead so you
know why it is missing:

```
CONTAINER  URL
multi      ⚠ set proximo.port (exposes 2 TCP ports)
```

A container that carries
[`proximo.transcript`](routing.md#the-proximo-labels) and no host is in the
inventory too, marked as having no route. It is not a warning — nothing is wrong
with a worker that is not reachable — and it is listed so the label can be
verified at all: the only other way to learn it took effect would be to wait for
something to go wrong. The row names the service to pass to
[`proximo errors --service`](#proximo-errors) and carries no command of its own:
`status` is an inventory, and a command in it would read as a Remedy.

```
CONTAINER       URL
shop-web-1      https://app.test  + app.shop.test
shop-worker-1   no route — observed for Incidents (proximo.transcript), service shop/worker
```

Prints `No routed containers.` when nothing is exposed — which implies the
stack is down, since a running stack always serves the dashboard route.

`status` is an **inventory**: it answers *what is running*, and it never prints
a Remedy. Version skew, an `--image` override and a broken resolver are
diagnoses — [`proximo doctor`](#proximo-doctor) reports those. A collision shows
up in both by design: `status` shows it because the route's reachable URL
changed, `doctor` because there is something to do about it.

### The PEER column

A `PEER` column appears
**only when at least one route carries `proximo.share`**, the way `MIDDLEWARES`
appears only when needed. Order: `CONTAINER | URL | PEER | MIDDLEWARES`. The cell
mirrors the `URL` cell — the Bare peer name with `https://`, the Qualified one
bare after `  + `:

```
CONTAINER      URL                                          PEER
shop-api-1     https://api.test  + api.shop.test            https://api.studio-01.<suffix>  + api.shop.studio-01.<suffix>
shop-worker-1  no route — observed for Incidents (proximo.transcript), service shop/worker
db             tcp://db.test:5432 (terminate)  + db.shop.test  ⚠ proximo.share ignored on a TCP route
web            https://app.test  + app.shop.test            ⚠ proximo.share is set; not served on its peer names (team-root and intermediate are not configured)
```

- **Shared and served**: both peer names. Both are printed because both answer;
  which one to send is prose (send the Qualified one), not an omitted column.
- **Shared but not served**: `⚠` and the missing values, in the fixed order
  `machine`, `peer-suffix`, `address`, `team-root`, `intermediate` — the words of
  the `config` subcommands, so `proximo config <word>` is findable directly.
  Joined with commas and a final "and": `(machine, address and intermediate are
  not configured)`; one alone reads `(machine is not configured)`. The `⚠` shows
  whenever any of the five is missing, including `address` alone, where the peer
  route is emitted but its names do not resolve. It is a fact, not a Remedy: the
  cell prints no command. An **expired** intermediate is not a missing value —
  the cell is unaffected and `doctor` reports it. `mesh-remedy` is never a cause.
- **Shared, served, mesh down**: indistinguishable from served. proximo cannot
  observe the mesh and does not imply reachability it never measured.
- **Not shared**: an empty cell.
- **A TCP route with the label**: `⚠ proximo.share ignored on a TCP route`, in
  addition to the watcher warning.
- **A container that lost a Collision**: its row carries only the Qualified peer
  name; the `⚠` stays in `URL`, where the Collision already speaks.
- `(balanced ×N)` is never repeated in `PEER`: it is a property of the service.
- `proximo.path` is not printed, here or in `URL`.

There is no clipboard flag.

### --json

`proximo status --json` is the same inventory for a tool rather than a person:
one entry per table row, with every name the route answers on as its own field
instead of display text. It exists for tools that make proximo's names reachable
somewhere proximo's DNS does not answer — a dev container pinning them in its
`/etc/hosts`, for one. Deriving a
[qualified host](routing.md#the-two-hosts-every-route-gets) is proximo's job,
so such a tool reads the names here rather than from labels.

```json
{"routes": [
  {"container": "shop-api-1", "scheme": "https", "bare": "api.test", "qualified": "api.shop.test",
   "peer": {"bare": "api.studio-01.mesh.internal", "qualified": "api.shop.studio-01.mesh.internal"}},
  {"container": "work-api-1", "qualified": "api.work.test",
   "collision": {"host": "api.test", "served_by": "shop-api-1"}},
  {"container": "multi", "claimed": "multi.test", "warning": "set proximo.port (exposes 2 TCP ports)"},
  {"container": "shop-worker-1", "note": "no route — observed for Incidents (proximo.transcript), service shop/worker"}
]}
```

- `bare` and `qualified` are **only names the route answers on**; a name it
  claims and does not get is kept apart. A container that lost a
  [Collision](troubleshooting.md#a-host-collision-is-reported) carries the host
  under `collision`, with the claimant that kept it, and keeps `qualified` when
  it still answers there — an entry with neither is a container that lost every
  host it declared. A flagged row (starting, unhealthy, an ambiguous port)
  carries the host as `claimed` with its `warning`; an observed container
  carries only its `note`.
- `scheme` is `https` or `tcp` (an [SNI route](routing.md#proximotcpport--route-tcp-services-by-name-sni)),
  on a served entry; `path` is the [`proximo.path`](routing.md#proximopath--split-one-host-across-containers)
  prefix, when the route has one.
- `peer` holds the [peer names](sharing.md) only when the route is served on
  them, kept apart from the `.test` hosts so a tool pins only the names it means
  to.
- **The contract**: a field is omitted when it has no value; fields are only ever
  added, never renamed or removed. Exit codes and stderr are the table's, and on
  exit 0 stdout is always one JSON document — `{"routes": []}` when nothing is
  exposed. Inspection notes are not part of it.

## proximo doctor

Report every [Check](../CONTEXT.md#diagnosis-and-observation) on this host in one
pass, and hand back a [Remedy](../CONTEXT.md#diagnosis-and-observation) for each
failure — the cure where one exists, and otherwise the command whose own output
names the cause.

```sh
proximo doctor
```

```
✔ The Docker daemon is reachable
✔ Nothing but proximo holds :80/tcp — held by the proximo stack (proximo-traefik-1)
✔ Nothing but proximo holds :443/tcp — held by the proximo stack (proximo-traefik-1)
✔ Nothing but proximo holds :5354/udp — held by the proximo stack (proximo-dns-1)
✔ Browser trust can be installed
✔ proximo is installed on this host — CA and host resolver are in place
✔ The local CA is in the system trust store
✔ The local CA is in the browser (NSS) trust stores — 2 NSS database(s) hold the CA
✔ The proximo stack is running — traefik, dns, watcher, inspector
✔ The stack matches the installed CLI version — 0.4.0
✔ The stack runs the image this CLI pins — ghcr.io/filippolmt/proximo:v0.4.0
✔ The proximo DNS server answers — proximo-doctor.test answers 127.0.0.1 on 127.0.0.1:5354
✔ The host resolver uses the proximo DNS server — proximo-doctor.test resolves to 127.0.0.1
✔ Every routed container is served — 3 route(s)
✔ The agent skill matches the installed CLI — 1 copy at 0.4.0
```

It prints the checks that **passed** too: those say where *not* to look, and
narrow the search as much as a failure does. A failure spends the lines it
needs, because it is the one being read:

```
✘ The host resolver uses the proximo DNS server
    proximo-doctor.test resolves to "", not 127.0.0.1
    Remedy: resolvectl status
    See:    https://github.com/filippolmt/proximo/blob/main/docs/troubleshooting.md#vpn-or-corporate-dns-overrides-the-resolver
```

The two DNS checks are one answer. *The proximo DNS server answers* queries
`127.0.0.1:5354` directly; *the host resolver uses it* asks the OS resolver for
the same name. A VPN produces exactly the pair above — the first passes, the
second fails — and that pair **is** the answer: proximo is healthy, the host is
not sending it the query.

A check that the environment could not answer is **skipped**, naming what it
waited on, so one cause never produces a dozen red lines:

```
✘ proximo is installed on this host
    missing the local CA (~/.proximo/tls/ca.pem) and the host resolver file (/etc/resolver/test)
    Remedy: proximo install
    See:    https://github.com/filippolmt/proximo/blob/main/docs/troubleshooting.md#proximo-is-not-installed-on-this-host
– The local CA is in the system trust store — waiting on: proximo is installed on this host
```

Two properties are worth relying on:

- **It never elevates.** Everything proximo must read is readable unprivileged,
  and it never asks for a password: a check that does is one nobody runs at
  the moment they need it most. Remedies may need `sudo` — you type those.
- **It never repairs.** `doctor` reads the host and reports; every mutation
  stays a verb you typed.

Each failure names the section that explains it, and a check that can fail for
causes documented apart points at the right one: a contested host is sent to
[a host collision is reported](troubleshooting.md#a-host-collision-is-reported),
not to the mislabelled-container checklist, and is offered the command that
lists every claimant rather than a cure proximo may not pick.

Any failure exits **non-zero**, including a failed route (your container rather
than proximo): an exit code that needs a rule to interpret is worse than one
that does not. Each check is bounded: one that runs out of time fails, because a
tool that hangs is worse than one that is wrong.

`up` runs the subset that is meaningful before the host is touched — Docker and
the three ports. `install` runs that same subset plus one more, that browser
trust can be installed at all, since it is about to write a store `up` never
touches. Both print only what failed.

### The peer Checks

Four Checks. On a machine
that has not opted in, every one of them is Skipped, and `dns-server` and
`dns-resolver` return exactly the Result they return on a machine that never
configured a peer value ([constraint 9](sharing.md#constraints)).

| Check | Statement | `Needs` | Skipped when | Remedy |
| --- | --- | --- | --- | --- |
| `peer-intermediate` | The machine's intermediate is valid for at least 30 more days | — | no intermediate installed, or `machine` or `peer-suffix` unset | `proximo config csr` |
| `peer-routes` | Every shared route is served on its peer names | `stack` | `machine` or `peer-suffix` unset | `docker inspect <container>` |
| `peer-dns` | The proximo DNS server answers the Peer suffix | `stack` | `machine`, `peer-suffix` or `address` unset | `proximo up` |
| `mesh` | This machine's peer names resolve through the system resolver | `peer-dns` | only by inheritance from `peer-dns` | the configured `mesh-remedy`, else `scutil --dns` (macOS) / `resolvectl status` (Linux) |

- **`peer-intermediate`** Fails under 30 days and when expired, so `doctor` exits
  non-zero a month ahead. Without it, proximo would go on issuing valid leaves
  under an expired intermediate and only a colleague's browser would see it.
- **`peer-routes`** reads labels and is true or false with the mesh down. It
  fails on the two label mistakes that survive the `status` row and have a cure:
  a declared host outside the TLD, and a TCP route carrying the label. `routes`
  is unchanged — "not served at all" and "served but not shared" read and cure
  differently. It passes when no route is shared.
- **`peer-dns`** queries `<address>:5354` directly for the sentinel
  `proximo-doctor.<machine>.<suffix>` and requires `<address>` back. When it
  fails, its Detail says whether the address is held by any interface of this
  machine — the one cause of a failed bind.
- **`mesh`** asks the **system** resolver for the same sentinel and requires the
  configured `<address>`. That one lookup walks the whole local chain: system
  resolver, the mesh's routing of the subtree, this machine's address on 5354,
  proximo. It runs without `mesh-remedy`: the environment answers, only a
  declared cure would be missing, and the platform command shows whether a
  resolver exists for the suffix and where it points.

`peer-dns` and `mesh` are one answer, as `dns-server` and `dns-resolver` are:
`peer-dns` failing means proximo does not answer on the mesh address; `peer-dns`
passing and `mesh` failing means proximo answers and the mesh does not route the
suffix to it. A publishing machine that is not itself a resolving peer fails
`mesh` while others can reach it; that cause has a section of its own.

`stack` never fails for a peer problem: the peer DNS service is not one of the
stack's core services. There is **no Check that a shared route is reachable**:
proximo cannot observe another machine's view. There is no Check on the
*quality* of a configured value either — whether a label names a person is not a
statement proximo can verify.

## proximo errors

Show recent Exchanges: what the stack served, what the container that served it
wrote while the request was live (the
[Transcript](observability.md#transcripts--what-the-container-said)), and — on
routes labelled
[`proximo.inspect`](routing.md#proximoinspect--see-what-the-browser-saw) — what
the browser reported.

Every route produces an Exchange, labelled or not: a developer learns they need
a diagnosis only after the request they needed it for is over.

Interleaved with the Exchanges, in one time order, are the
[Incidents](observability.md#incidents--what-the-runtime-declared) the runtime
declared about the containers proximo knows — an exit, a restart, an OOM kill.
There is one listing rather than two sections: the question is never "was it the
request or the worker", and time order is the only thing tying the 14:05:09
checkout to the worker that died seven seconds earlier.

```sh
proximo errors                       # what went wrong in the last 15 minutes
proximo errors --host web.test       # one host
proximo errors --service worker      # one service: its Exchanges and its Incidents
proximo errors --service shop/worker # qualified, when two projects both have one
proximo errors --since 1h --limit 50
proximo errors --since 2026-08-31T10:30:00Z   # an absolute instant
proximo errors --all                 # the clean Exchanges too, and quiet breadcrumbs
proximo errors --json                # structured, for tooling
```

By default it lists only the rows with something to say: a client report, a
warning, a failing status, an Incident. The clean ones are hidden because
otherwise the one page that broke is buried under every request that did not —
and `--limit` then cuts the interesting one first. Ordering follows the **most
recent activity**, not the page load: a page served ten minutes ago that threw a
moment ago sorts above a request served since, and `--since` follows the report
rather than the load. An Incident sorts by the instant the runtime declared it.

An Incident row carries the same instant and id as an Exchange row, then the
service and what the runtime declared where the method, path and status would be.
The request columns are not padded out with blanks: a hole in a column is a
question, not information.

```
14:05:02  9b3e1a7c5d2f8e04  shop/worker  exited 137 (OOM-killed)
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--host` | all | Only this host, e.g. `web.test`. An Incident carries no host, so this keeps the Incidents of the services that served that host's requests rather than dropping every one. |
| `--service` | all | Only this Compose service: its Exchanges **and** its Incidents. Qualified (`shop/worker`) or bare when nothing contests it (`worker`); a contested bare name has its candidates reported rather than one of them chosen. |
| `--since` | `15m` | A duration back from now (`15m`, `2h`) **or** an absolute RFC 3339 instant (`2026-08-31T10:30:00Z`). There is no cursor and no persisted state: an agent knows when it last looked, proximo does not. |
| `--limit` | `20` | Most recent N Exchanges, and N Incidents. |
| `--all` | `false` | Hold nothing back: the Exchanges with nothing wrong, and the `debug`/`info`/`log` breadcrumbs hidden by default so framework chatter does not bury the report. |
| `--json` | `false` | Emit `{"exchanges": [...], "incidents": [...], "transcripts": {"<id>": {...}}}` instead of the reading layout — the Transcripts keyed by the Exchange **or Incident** they belong to. Under a `--service` it also carries `"readings"`: one [reading](observability.md#readings--what-the-runtime-says-right-now) per running container of that service, with anything that could not be read named under `"unread"`. A service with nothing running carries no `"readings"` and a `"notes"` entry saying so — an omitted member cannot say which absence it is. |

A `--service` always ends with the
[readings](observability.md#readings--what-the-runtime-says-right-now) — one per
running container of that service: running since when, what its healthcheck says,
how many restarts, when its output last moved. They print after the listing,
Incident or no Incident, because *what happened* and *how it is now* are two
questions. When the listing is empty the readings also state that the conclusion
is not proximo's to draw: a container that is alive and stuck declares no
Incident, and an unexplained silence is the answer most likely to be read as *all
fine*.

The command reads two sources — the Inspection hop, for what browsers reported,
and the watcher, for Incidents — so either can fail on its own. When the Incident
store cannot be asked, the listing says so and hands over the Remedy on the spot
(`proximo update`) rather than looking like a quiet machine: an absent Incident
and an unreachable Incident store are indistinguishable from the output, and one
of them means a restart-looping worker is going unreported.

`proximo status` lists which routes are under Inspection, and anything proximo had
to relax on them to get there — that belongs with the route, which is why it is
not only in `proximo errors`.

The default layout has a stable field order on purpose — it is read as often by
an agent as by a person. See
[Inspection](observability.md#inspection--what-the-browser-saw) for what is
captured and where it lives.

A Transcript is quoted inline beside every Exchange the listing shows, and is
**raw application output quoted with no redaction** — it may carry credentials
or personal data. The listing says so once.

### A request that arrived on a peer name

- **The reading row appends the host the request arrived on**, verbatim, only
  when it is a peer name. A local row is unchanged.
- **`--json` gains `"peer": true`** on such an Exchange, and omits it otherwise.
  An agent reading the JSON knows neither the TLD nor the Peer suffix, so it
  could not classify the host without a second command. The suffix itself is
  not added: that would be configuration inside an observation.
- **`--host` stays an exact match.** When the named host has peer names and the
  window holds Exchanges on them, the listing says how many it excluded and names
  the command that includes them, `--service`, which already unions local and
  peer Exchanges because it selects by backend.
- **No attribution to a machine or a person.** The client address is not read;
  on Docker Desktop it is not even the colleague's.
- **The Transcript carries no peer marker**, ever — proximo authors nothing inside
  it ([ADR 0006](adr/0006-the-transcript-is-quoted-never-stored.md)). **An
  Incident is never attributed to a peer**: the runtime declares nothing about
  the browser that caused a request
  ([ADR 0007](adr/0007-proximo-remembers-what-the-runtime-declares.md)).
- **`proximo.share` with `proximo.inspect` is allowed, silently.** A colleague's
  Client report and Snapshot are kept like any other.

### proximo errors transcript

Print the whole of what a container wrote in one window. Unlike `dom`, it goes to
**stdout**: a transcript is text to read and pipe, not hundreds of kilobytes to
grep.

The window comes from whatever fixed it, and the id from the listing decides
which: an Exchange id quotes what the container wrote while that request was
live, an Incident id quotes from the previous Incident of that service up to this
one. With `--service` and no id there is no anchor at all, and the window is
plainly the one `--since` names — the fallback for a service the runtime has
declared nothing about.

```sh
proximo errors transcript 1f0c9a2b3d4e5f60             # an Exchange's window
proximo errors transcript 9b3e1a7c5d2f8e04             # an Incident's window
proximo errors transcript --service worker --since 30m # no anchor: a plain window
proximo errors transcript 1f0c9a2b3d4e5f60 -o /tmp/web-1.transcript.txt
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--service` | — | Quote this service's plain `--since` window, when no id is given. Same resolution as `proximo errors --service`. |
| `--since` | `15m` | The window the Exchange or Incident is looked for in, same forms as `proximo errors --since`. With `--service` and no id, it *is* the window. |
| `--limit` | `1048576` | Cap the transcript at this many bytes. An elision is always declared. |
| `-o`, `--out` | stdout | Write to this path instead. |

Identities are derived rather than minted — an Exchange from host, instant and
backend; an Incident from service, instant and kind — so the same thing has the
same id in two invocations, which is what lets an agent say "that one". An
Incident id stays computable from what is on screen even after proximo has
forgotten the Incident.

An Incident can outlive what it would quote. proximo then says it remembers the
Incident and cannot show what was written — the container is gone, or the one
answering to that name now started after it — rather than quoting whichever
container holds the name today.

### proximo errors dom

Write the DOM captured for one Exchange to a file and print the path. It is never
dumped into the terminal: a page's DOM is hundreds of kilobytes.

```sh
proximo errors dom 9f3a21ab
proximo errors dom 9f3a21ab -o /tmp/broken.html
```

A missing snapshot means either the Exchange was evicted, or no client report on
that page carried one.

## proximo config tld

Change the top-level domain routed to the local proxy. Updates the host resolver
for the new TLD, persists it, and restarts the stack so routing follows.

```sh
proximo config tld internal    # containers become reachable at <name>.internal
```

- The TLD must be a single DNS label of `[a-z0-9-]` (a leading dot is stripped,
  the value is lowercased).
- `.local` is **rejected** — it is reserved for mDNS (Bonjour/Avahi) and
  overriding it breaks real `.local` devices on your network.
- No-op (with a message) when the TLD is already set.

Default TLD is `.test` (reserved by RFC 6761, never collides with mDNS).

**Pick a TLD nobody else owns.** `.test` is the only value with a guarantee: RFC
6761 reserves it, so it can never be delegated and no public resolver will ever
answer for it. `.internal` is reserved for private use as well. Every other label
is accepted but unguaranteed, and some are actively harmful: `.dev`, `.app` and
`.zip` are real gTLDs in the browsers' HSTS preload list, so claiming one shadows
names that exist on the public internet. A label that is merely undelegated today
(`.loc`, `.lan`) works, but nothing stops it from being delegated tomorrow.

## proximo config ca-path

Print the absolute path of the local CA certificate (PEM):

```sh
proximo config ca-path
# /Users/you/.proximo/tls/ca.pem
```

This is the **stable contract for external tools** that want to trust proximo's
CA (e.g. mounting it into a dev container) — shell out to this command instead
of hardcoding the state-home layout. The path is printed even when the file
does not exist yet (proximo not installed yet), so callers must check existence
themselves; the command itself is side-effect free and never creates
directories.

## proximo config machine

All of the configuration below is persisted in `config.json` beside the TLD, has
**no default**, and is set one value per subcommand, the way
[`proximo config tld`](#proximo-config-tld) is. Partial configuration is a
legitimate state, not an error: `status` and `doctor` report what is missing.
A value reaches the peer routes and peer certificates at once: a running stack's
watcher picks it up at its next reconcile, with no `up`. The peer DNS service is
the exception — it is added, changed or removed by the next `proximo up`. [`proximo config unset`](#proximo-config-unset) returns
one to its unconfigured state.

The validation rule is the same for every value: **proximo refuses what the machine can decide, reports what
it cannot, and does both when a person sets the value** — not at reconcile, and
not as a refusal to start. A label is read with nobody watching, so a bad label
degrades with a watcher warning; a configuration value is typed by a person who
will read a refusal. Every rule a person has to apply is printed in terms of the
**property required, never the mechanism that currently satisfies it**.

```sh
proximo config machine <label>
```

Set this machine's label in its peer names.

- **Refused**: anything that is not a single DNS label of `[a-z0-9-]` (lowercased,
  as `config tld` does), or longer than 63 octets.
- **Printed on every set, unconditionally**: the rule — the label names a
  machine, not a person; colleagues bookmark it and write it into READMEs, so it
  must be neutral and stable (`studio-01`, never a person's name, a model or an
  office). A person can own two machines, and a machine outlives its owner's
  tenure. proximo cannot tell whether a label names a person, so it does not
  pretend to check.
- Changing it renames every peer name on the machine and requires a new
  intermediate, which is constrained to the old label: with one installed, the
  set warns and names `proximo config csr`.

## proximo config peer-suffix

```sh
proximo config peer-suffix <suffix>
```

Set the Peer suffix every peer name lives under.

- **Refused**: anything that does not normalise to lowercase `[a-z0-9-]` labels,
  or that has fewer than two labels. This is not `config tld`'s normaliser,
  which accepts exactly one label. Also refused, because the machine can decide
  them: a suffix ending in `.local` (rule 1 below), and one under the configured
  TLD, which proximo's own DNS server answers with `127.0.0.1`.
- **Warned when it leaves something behind**: a configured team root that does
  not cover the new suffix (Remedy `proximo config team-root <path>`), and an
  installed intermediate constrained to the old one (Remedy `proximo config csr`).
- **Warned at set time, never refused**: a suffix whose right-most label is not
  reserved from delegation for private use. The list is the one `config tld`
  already advises from. The value is stored either way.
- **The rule, in prose.** A suffix is admissible when all three hold:
  1. **No resolver on any colleague's machine claims it ahead of the nameserver
     the mesh configures.** `.local` fails this: mDNS answers it first on macOS
     and Linux.
  2. **No Public Suffix List entry can reclassify it underneath a shipped
     design.** `home.arpa` is the precedent. [Constraint 8](sharing.md#constraints) is what makes a future
     listing survivable.
  3. **Nobody can delegate it in the future.** A single unreserved label works on
     the day it is chosen and stops resolving the day someone registers it.
     *Unclaimable*, not merely unclaimed.

  The rule is deliberately not "avoid reserved names": proximo depends on `.test`
  being reserved.

## proximo config address

```sh
proximo config address <ip>
```

Set the address proximo answers this machine's peer names with — this machine's
own address on the mesh.

- **Refused**: anything that is not a parsable IPv4 address. The peer DNS
  service answers `A` records only, so an IPv6 address could never be served.
- **Warned at set time, never refused**: an address that no interface of this
  machine currently holds. The check compares the **exact** address against the
  addresses of **every** interface: a subnet test would assert nearly nothing,
  and interface names are not stable. It is a report, because a correct address
  is held by nothing whenever the mesh client is down.
- **Printed on every set**: the rule. The check separates "an address this
  machine holds" from "an address that exists nowhere on it". It catches a typo
  and a stale value. It cannot tell the mesh address from the LAN address or
  `127.0.0.1`, and that part is the person's to get right.

## proximo config team-root

```sh
proximo config team-root <path>
```

Point proximo at the team root certificate. The file is public and is
distributed however the team likes — never from this repository.
[`proximo trust`](#proximo-trust) and `install` then install it as a
second anchor beside the local CA, and `uninstall` removes it.

- **Refused, hard** — the one value whose rule is checked completely, and the
  most consequential ([constraint 7](sharing.md#constraints)): a certificate whose permitted DNS subtree
  does not cover the configured Peer suffix, whose name constraints are not
  marked critical, or which lacks exclusions for every IP address (`0.0.0.0/0`,
  `::/0`), every email address and every URI. Constraining `dNSName` alone leaves
  every other name type unrestricted. Covering the suffix is not enough either: a
  root permitting an ancestor of it, or a second unrelated subtree, could sign
  outside it, and is refused. So is a certificate that is not a CA.
- **Refused, hard**: a certificate whose common name contains the local CA's
  (`proximo local CA`). macOS removes the local CA by a common-name match,
  which matches a substring, so that removal would take the team root with
  it. The team root itself is removed by its fingerprint.
- **Refused with a Remedy** naming `proximo config peer-suffix` when the suffix
  is not set yet, since coverage cannot be judged without it. That is an order of
  setting, not a completeness requirement.

## proximo config csr

```sh
proximo config csr > machine.csr
```

Print the certificate signing request for this machine's intermediate on stdout.
It creates the machine key only if none exists. Run again, it prints the same CSR,
and it **never replaces a key that already has an intermediate**. A reinstalled
machine has no key, so it gets a new one — the ceremony is the same as a first
enrolment. The key never leaves the machine; the CSR is a public object, so any
channel may carry it. See
[the team root and the intermediates](sharing.md#the-team-root-and-the-intermediates).

## proximo config intermediate

```sh
proximo config intermediate <file>
```

Install the intermediate a custodian signed from this machine's CSR.

- **Refused, hard**, each in the words of the property it misses: a certificate
  not signed by the configured team root, whose permitted subtree is not exactly
  `<machine>.<suffix>`, whose `MaxPathLen` is not 0, whose name constraints are
  not critical or leave an IP, email or URI name open, that carries such a name
  itself, that has expired or is not yet valid, or that does not match the
  machine key. The team root is validated again first.
- **Refused with a Remedy** naming the missing value when the team root, the
  machine label or the Peer suffix is not set.

## proximo config mesh-remedy

```sh
proximo config mesh-remedy '<command>'
```

Set the command the [`mesh` Check](#the-peer-checks) offers as
its Remedy — typically the transport's own status command. Stored verbatim, with
no validation and no warning: proximo cannot judge a transport's command, and a
public CLI names no vendor. Unset, `mesh` still runs and offers a platform
command instead. It is an override, never a prerequisite.

## proximo config unset

```sh
proximo config unset <machine|peer-suffix|address|team-root|intermediate|mesh-remedy>
```

Return one peer-sharing value to its unconfigured state; a materialized stack
follows as it does when a value is set: the peer routes at its next reconcile,
the peer DNS service at the next `proximo up`. `intermediate`
removes the installed intermediate and keeps the machine key, the one thing a new
intermediate must match. `team-root` leaves the anchor in the trust stores:
removing it needs `sudo`, and `uninstall` removes what was trusted.

## proximo skill install

Write the [agent Skill](skill.md) where a coding agent will read it. Needs
neither Docker nor sudo: the Skill is compiled into the binary, and the
destinations are your own files.

```sh
proximo skill install                          # every agent detected, this repository
proximo skill install --scope global           # follow you instead of the repo
proximo skill install --agent claude --dry-run # print the plan and stop
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--agent` | every agent detected | `claude`, `codex`, a comma-separated list, or `all` |
| `--scope` | `project` | `project` (this repository) or `global` (`~/.claude`, `$CODEX_HOME`) |
| `--dry-run` | off | Print the plan and stop |
| `--force` | off | Overwrite a copy edited after proximo wrote it |

The plan is printed before anything is written, and a project-scope write is
announced as a tracked diff to review and commit. Outside a git repository,
`--scope project` is an error naming `--scope global`, never a silent fallback.

`install`, `up` and `update` refresh every copy proximo wrote and can see, so
this is normally run once per repository. What auto-update cannot reach is
reported by `doctor`:
[The agent skill is out of date](troubleshooting.md#the-agent-skill-is-out-of-date).

## proximo skill uninstall

Remove the Skill copies proximo installed. Takes the same flags as
`skill install`.

```sh
proximo skill uninstall --agent all
```

A copy edited after proximo wrote it, and a copy proximo did not write at all,
are **listed and left alone** — `--force` removes an edited one, and nothing
removes an unmanaged one. `proximo uninstall` does the same sweep across every
destination it can see.

## proximo uninstall

Reverse everything `install` did and tear down the stack:

```sh
proximo uninstall
```

1. Stop the stack (this also removes the profiled observability containers)
   **and proximo's own images**, current and superseded — the one place proximo
   deletes them; a plain `down` keeps them cached, and Traefik and the dashboard
   images are never touched — and delete the generated observability secret +
   env files
   ([Dev-time observability](observability.md)). proximo uses no Docker named
   volume, so there is nothing to volume-remove here — the data goes with the
   home in step 4.
2. Remove the host resolver config for the TLD (and reload the resolver on
   Linux).
3. Remove CA trust from the NSS and system stores.
4. Remove the [agent Skill](skill.md) copies proximo installed and left
   untouched, listing the edited and unmanaged ones it may not delete.
5. Delete the `~/.proximo` state home — config, CA, the materialized stack, and
   the bind-mounted Traefik data (plus the Beszel metrics data, if observability
   was used) — so no proximo state is left on the host.

The host is restored to its prior state.

**Peer sharing.** `uninstall` also removes the team root.

## proximo version

Print the build metadata (version, commit, build date). Works without Docker.

```sh
proximo version
```

---

## Typical sessions

**First run**

```sh
proximo install            # one-time host setup + stack
docker compose up -d       # your own stack, with proximo.hosts labels
open https://whoami.test
```

**Day to day**

```sh
proximo status             # what's exposed right now
proximo doctor             # when something is broken: every check + its remedy
proximo down               # free ports 80/443 when you're done
proximo up                 # bring the proxy back later
```

**Switch domain / clean up**

```sh
proximo config tld internal   # move everything under .internal
proximo uninstall          # remove all host changes
```

See [Routing](routing.md) for how to label your containers and
[Architecture](architecture.md) for what each command is orchestrating.
