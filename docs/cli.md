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
  that the update will apply on the next `proximo up` and exits 0. This is what
  makes it safe in the Homebrew cask's post-install hook.
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
| `--service` | all | Only this Compose service: its Exchanges **and** its Incidents. Qualified (`shop/worker`) or bare when nothing contests it (`worker`); a contested bare name has its candidates reported rather than one of them chosen. Under an explicit `--service` nothing is held back, so `turned unhealthy` becomes visible too. |
| `--since` | `15m` | A duration back from now (`15m`, `2h`) **or** an absolute RFC 3339 instant (`2026-08-31T10:30:00Z`). There is no cursor and no persisted state: an agent knows when it last looked, proximo does not. |
| `--limit` | `20` | Most recent N Exchanges, and N Incidents. |
| `--all` | `false` | Hold nothing back: the Exchanges with nothing wrong, and the `debug`/`info`/`log` breadcrumbs hidden by default so framework chatter does not bury the report. |
| `--json` | `false` | Emit `{"exchanges": [...], "incidents": [...], "transcripts": {"<id>": {...}}}` instead of the reading layout — the Transcripts keyed by the Exchange **or Incident** they belong to. |

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
