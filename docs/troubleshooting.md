# Troubleshooting

[← back to docs index](README.md)

One section per failure mode. If your container is labeled but unreachable,
start with [Container not routed](#container-not-routed). To have proximo look
for you, run [`proximo doctor`](cli.md#proximo-doctor): it reports every check
on this host, and each failure carries the command that clears it.

## The Docker daemon is not reachable

Docker is the one mandatory prerequisite: without it there is no stack, no
routes, and nothing for proximo to read. Every command that needs it stops with
`cannot reach the Docker daemon (is Docker running?)`.

```sh
docker version
```

Its own output names the cause — Docker Desktop not started, a `DOCKER_HOST`
pointing at a context that is gone, or a user not in the `docker` group. Start
Docker (or point `DOCKER_HOST` at a context that exists) and re-run.

## proximo is not installed on this host

Trust and DNS are host changes, and they are made by one verb the developer
types. Until then the local CA is not on disk and nothing points the TLD at
proximo's DNS server, so names do not resolve and certificates are not trusted —
however healthy the stack is.

```sh
proximo install
```

`proximo doctor` reports this as one failure and **skips** everything that
depends on it, rather than repeating the same cause a dozen times. The
pre-install checks still run, so a machine that is also missing Docker says so
before the install that would fail on it. What `install` changes is listed in
[What install changes on your host](installation.md#what-install-changes-on-your-host).

## DNS name does not resolve

`https://<name>.test` does not resolve — confirm the stack is up
(`proximo status`) and the resolver is wired: on macOS check
`/etc/resolver/<tld>`; on Linux check
`/etc/systemd/resolved.conf.d/proximo-<tld>.conf` and that `systemd-resolved` is
active (`resolvectl status`). Only `systemd-resolved` is supported in v1.

To test the DNS server directly, bypassing the host resolver:

```sh
dig @127.0.0.1 -p 5354 whoami.test
```

On macOS, a name looked up *before* `proximo install` wired the resolver can sit
in the system cache as a failure. Flush it and ask again:

```sh
sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder
scutil --dns | grep -A 3 "domain.*test"        # the resolver is in effect
```

If that answers `127.0.0.1` but the browser still fails, the host resolver is
the problem — see also
[VPN or corporate DNS overrides the resolver](#vpn-or-corporate-dns-overrides-the-resolver).

## DNS port already in use

`proximo install` and `proximo up` abort before changing anything if
`127.0.0.1:5354/udp` is held by something other than proximo. (Port `5353` is
deliberately not used: macOS `mDNSResponder`/Bonjour binds it.) The port held by
proximo's own DNS service is the healthy case and stops nothing.

The check names the holder it can name, and hands over the question for the one
it cannot:

```sh
docker ps --filter publish=5354    # another container publishes it
sudo lsof -nP -iUDP:5354           # a process on this host holds it
```

## Port 443 or 80 already in use

Traefik publishes `443` and `80` on the host. If another service already
listens there (another reverse proxy, a local web server, an old Traefik), the
stack fails to start with a Docker error like
`Bind for 0.0.0.0:443 failed: port is already allocated`.

`proximo install` and `proximo up` check this before touching anything, and
`proximo doctor` reports it any time. All three ask *who* holds the port rather
than whether it is free — a healthy machine has `:443` bound, by proximo — so
the answer also picks the command that names the holder:

```sh
docker ps --filter publish=443       # another container publishes it
sudo lsof -nP -iTCP:443 -sTCP:LISTEN # a process on this host holds it
```

Stop the conflicting listener, then bring the stack back up with `proximo up`.

## macOS UDP forwarding

`127.0.0.1:5354/udp` relies on the Docker VM forwarding UDP. It works on
current Docker Desktop; if a setup proves unreliable, that is the first thing
to check.

## Certificate warnings in Firefox or Chrome

A browser that flags `https://<name>.<tld>` as untrusted
(`ERR_CERT_AUTHORITY_INVALID` / "issuer not trusted") almost always means the
local CA is not in the store the browser reads — Firefox and Chrome on Linux use
NSS, not the system store; Chrome and Safari on macOS use the system keychain.

First **fully restart the browser** (NSS loads CAs only at process start). If the
warning persists, re-trust the CA:

```sh
proximo trust
```

proximo checks both stores separately, so
[`proximo doctor`](cli.md#proximo-doctor) says which one is missing the CA —
and it compares the certificate, not its name, so a CA that was regenerated
while an older namesake is still in the store is reported as untrusted rather
than as fine.

The NSS store needs `certutil`. Where it is absent, proximo installs it through
Homebrew or apt; on a host with neither, `install` says so **before** changing
anything, and the remedy is to install the NSS tooling with your own package
manager (`libnss3-tools` on Debian-family distributions, `nss-tools` elsewhere).

[`proximo trust`](cli.md#proximo-trust) re-adds the CA to the system and NSS
stores via `certutil` (installing `nss-tools` if needed). It touches neither DNS
nor the stack, so it works while proximo is up, without a `down`/`up` cycle.
Restart the browser again afterwards. To confirm the CA landed in both stores,
run [`proximo doctor`](cli.md#proximo-doctor).

## Traefik logs failed to find any PEM data

Older versions logged a recurring, harmless error on every container restart:

```
ERR Unable to parse certificate .../certs/<name>.crt error="... tls: failed to find any PEM data in certificate input"
```

It came from a race: the watcher rewrote a `.crt` with a plain truncate-then-write
while Traefik's file provider reloaded mid-write, briefly reading an empty file.
Certificate materialization is now atomic (write-temp-then-rename), so the proxy
only ever sees a complete file and this error no longer appears on reissue.

If you still see it, the cert is genuinely malformed — a corrupted CA or a
hand-edited file under `certs/`. Re-run `proximo install` to regenerate the CA,
then restart the stack.

## macOS Gatekeeper blocks the binary

Gatekeeper's "is damaged" dialog is handled by the cask. For a manually
downloaded binary: `xattr -dr com.apple.quarantine ./proximo`.

## Where to read watcher warnings

The watcher logs every routing decision it skips (invalid hostnames, ambiguous
ports, duplicate hosts across label schemes). When a doc says "a warning is
logged", this is where to look:

```sh
cd ~/.proximo/stack && docker compose logs watcher
```

With [observability](observability.md) enabled (`proximo up --observability`),
the same logs are browsable at `https://logs.<tld>` (Dozzle) — select the
`watcher` container.

Every [Incident](observability.md#incidents--what-the-runtime-declared) the
watcher records is logged there too, prefixed `Incident:` — the same facts
`proximo errors` lists, in the order they arrived:

```sh
cd ~/.proximo/stack && docker compose logs watcher | grep Incident:
```

## Container not routed

A container with proximo labels is not reachable. Check in order:

1. **Hosts label syntax** — `proximo.hosts` must be present (its presence is
   the opt-in) and contain valid hostnames under the configured TLD,
   comma-separated. Invalid entries are rejected and logged.
2. **Enable flag** — `proximo.enable=false`/`0`/`no` parks the container.
   Remove the label or set it truthy.
3. **Port ambiguity** — if the image `EXPOSE`s zero or several ports and no
   `proximo.port` is set, the container is skipped; `proximo status` shows it
   flagged (`⚠ set proximo.port`). Add an explicit `proximo.port`.
4. **Host taken by another container** — `proximo status` lists it with the
   reason and the winner: see
   [A host collision is reported](#a-host-collision-is-reported).
5. **Watcher warnings** — anything else (network attach failures, invalid
   middleware values) is explained in the watcher log: see
   [Where to read watcher warnings](#where-to-read-watcher-warnings).

The full label contract is in [Routing](routing.md#the-proximo-labels).

## A host collision is reported

Two containers claim one host — two worktrees of a repository, or a stale
container nobody stopped. proximo does not pick a winner quietly: the container
that did not get the host is listed by `proximo status` with the reason and the
name of the container serving it.

```
CONTAINER      URL
shop-api-1     https://api.test  + api.shop.test
work-api-1     ⚠ api.test is served by shop-api-1; this container answers at api.work.test
work-api-1     https://admin.test  + admin.work.test
```

Read it as three facts:

- **Nothing is unreachable** — as long as the two are in different projects. The
  losing container keeps every other host it declared (`admin.test` above) and
  still answers at its own qualified host: a collision costs a bare host, never a
  service. Two containers of **one** project share the qualified host too, so
  there the loser is left with nothing and the note says so instead — give one of
  them a different `proximo.hosts`.
- **The bare host went to one claimant.** Which one is decided by name order,
  which is why it is reported rather than relied on.
- **The remedy is yours to pick.** Stop the container you forgot about, give one
  of the two a different `proximo.hosts`, or simply use the qualified host and
  leave both running.

Two variants of the same line say proximo stood down rather than arbitrated —
one of the two claims was its own:

- `qualified host api.shop.test withdrawn: <name> serves it` — someone declared
  by hand the name proximo would have generated. The hand-written declaration wins.
- `api.test is matched by a traefik.* rule on <name>; proximo withdrew its
  router` — a native Traefik label already routes that host, whether on that
  container or on another. Use one scheme per host
  ([native Traefik labels](routing.md#native-traefik-labels-backward-compatible)).

A container **outside a Compose project** has no Namespace, so it has no
qualified host to fall back on and the note says so. Put it in a Compose project
and it gets one.

## 502/503 right after a container restarts

A container that is up but still booting (DB migrations, JIT warmup, slow start)
gets traffic the instant it is running and answers `502`/`503` until it is
actually ready — most visible on a frequently-restarting container, which
repeats the failed-request window on every cycle.

Declare a Docker `HEALTHCHECK` on the image (or `healthcheck:` in Compose).
proximo then gates the route on health: it publishes the router + certificate
only once the container reports `healthy` and withdraws them when it turns
`unhealthy`, so requests never reach a backend that cannot serve. While the
container is starting, `proximo status` lists it as `starting (waiting for
healthy)` rather than as a working URL.

- A container with **no** healthcheck is unchanged — it routes on running, so if
  you still see the 502 window, the fix is to add a healthcheck.
- A healthcheck stricter than "can serve HTTP" can hold the route off
  indefinitely; `proximo.health=false` opts out and routes on running. See
  [proximo.health](routing.md#proximohealth--wait-for-the-container-to-be-healthy).

## An error I typed in the browser console never shows up

It never will, and nothing is broken. An expression that throws in Chrome's
console is caught by DevTools itself, so `window.onerror` never fires — and that
is what the agent listens to. The same is true of anything you `try`/`catch`
yourself: a handled error is not an uncaught one.

Throw from a real task instead, which is what a broken app does:

```js
setTimeout(function(){ null.foo }, 0)
```

## proximo errors shows nothing at all

Every route produces an Exchange, whether or not it is inspected, because
Traefik records an access log. A stack brought up before that existed records
none, and `proximo errors` is then silent for a reason that has nothing to do
with your code — it says so inline, and `proximo doctor` reports it as *The
running stack records access logs*.

```console
$ proximo update
```

Traefik reads its static configuration once, at startup, so a stack keeps
running without the access log however new the file on disk is. `proximo update`
restarts it; `proximo doctor` confirms.

If the check passes and a route you called still shows nothing, the request
never reached proximo. `proximo doctor` tells apart "nothing called it" from
"the name does not resolve here", which are the two causes and look identical
from the listing.

## proximo errors reports no Incident

An [Incident](observability.md#incidents--what-the-runtime-declared) is what the
runtime declared about a container: a non-zero exit, a restart, an OOM kill. The
watcher records them and publishes them on a loopback port; a stack brought up
before that existed publishes nothing, and `proximo errors` is then silent about
a container that keeps dying for a reason that has nothing to do with your code.
It says so inline, and `proximo doctor` reports it as *The running stack records
Incidents*.

```console
$ proximo update
```

If the Check passes and a container you expected still produces no Incident,
work through these in order:

1. **Is the container one proximo knows?** A container with no host is invisible
   until it says otherwise: add `proximo.transcript=true`. `proximo status` lists
   it as *no route — observed for Incidents* once the label takes effect, which
   is how you verify the label at all.
2. **Did the runtime actually declare anything?** A clean exit (code 0) is not an
   Incident, and neither is a healthcheck that went to unhealthy without ever
   having passed — a container that never reached *healthy* declares nothing when
   it fails.
3. **Is it still in the window?** `--since` defaults to 15 minutes, and the
   watcher keeps a bounded number of Incidents per service. `proximo up` discards
   them all: they are held in memory.
4. **Is the container alive and simply stuck?** Then it declares no Incident by
   itself: a worker blocked on a slow query is healthy and silent — **no Incident
   does not mean no problem**. Two things follow, in this order:
   - `proximo errors --service <service>` answers with the
     [readings](observability.md#readings--what-the-runtime-says-right-now) —
     running since when, what the healthcheck says, how many restarts, when its
     output last moved — and stops there rather than concluding, because an idle
     consumer and a stuck one read identically. Read the output for the window
     yourself: `proximo errors transcript --service <service> --since 30m`.
   - To make it visible *next time*, give the container a healthcheck that fails
     when it stops advancing:
     [making "not progressing" an Incident](observability.md#making-not-progressing-an-incident).
     Docker then declares it unhealthy, and a healthcheck that was passing and
     stopped is an Incident whose window quotes exactly what the worker wrote
     before it stalled. The container has to reach *healthy* once for that to
     work, which is what `start_period` is for.

## A transcript is empty or says the container is gone

A [Transcript](observability.md#transcripts--what-the-container-said) is quoted
from the container's own output at the moment you ask, never stored. Five
outcomes are not failures, and each says which it is:

- **"wrote nothing while this request was live"** — the container was up and
  quiet. Nothing to fix.
- **"has written nothing at all since it started, so it probably logs
  elsewhere"** — the project writes to a file inside the container, or to a
  collector. Only stdout and stderr can be quoted. Point the application's
  logger at stdout and the transcripts fill in.
- **"log driver cannot be read back"** — the container runs a logging driver
  Docker cannot replay (`syslog`, `fluentd`, `gelf`). Docker's own `json-file`
  and `local` drivers can; the rest cannot, by design.
- **"the container that served this request is gone"** — it was *removed*
  since, or the address now answers for a container started *after* the request.
  A container that merely stopped is still quoted, because Docker still answers
  `docker logs` for it. proximo says so rather than quoting whichever container
  holds the address now: attributing another container's stack trace to your
  request is the one failure worth being silent about.

- **"proximo remembers this Incident, not what … wrote"** — the Incident is
  runtime metadata proximo kept; the output it would quote is the project's, and
  proximo holds none of it. The container is gone, or the one answering to that
  name now started after the Incident, so there is nothing left to read back.
  This is the declared price of remembering only what the runtime says, and
  proximo says it rather than quoting whichever container holds the name today.

A transcript can also say *N other request(s) overlapped this one*. The cut is
temporal — it is what the container wrote in this window, never what this request
caused — so when two requests to one container overlap, their lines interleave
and nothing after the fact can separate them. Reproduce the problem on its own,
or read the whole transcript with `proximo errors transcript <id>`.

## proximo errors shows nothing for an inspected route

If you were testing by hand from the console, read the entry above first — that
accounts for most of it. Otherwise work down this list; each step is visible
without guessing.

This entry is about the *Client reports* half — what the browser saw. The
backend half needs no label: read the [Transcript](cli.md#proximo-errors) of any
route.

1. **The label is on and the route is HTTP.** `proximo status` lists the route;
   `proximo.inspect` is ignored on a TCP (SNI) route and on a
   [replica set](routing.md#round-robin-across-replicas). The watcher logs why —
   see [Where to read watcher warnings](#where-to-read-watcher-warnings).
2. **The page is HTML with a `</head>`.** The agent is inserted before the
   closing head tag. A response without one is left untouched, and the Exchange
   for it carries a warning saying so.
3. **The stack is recent enough to have the hop.** An older stack has no
   `inspector` container, and an inspected route then 502s. `proximo update`.
4. **The page really loaded the agent.** View source and look for
   `/.proximo/agent.js`. If it is in the HTML but the browser did not run it, the
   page's `Content-Security-Policy` blocked it — the Exchange will say proximo
   relaxed the policy, and if it does not, the policy came from somewhere proximo
   cannot rewrite (a `<meta http-equiv>` tag in your own markup).
5. **The Exchange is still held.** The buffer is bounded and in memory, so
   `proximo up` — including the one you ran to pick up a change — throws away
   everything recorded before it. This catches people out: reproduce the problem
   *after* the restart, not before. `proximo errors` says so when the hop came up
   in the last ten minutes.
6. **You are looking in the right window.** `--since` defaults to 15 minutes and
   follows the report, not the page load, so a page opened an hour ago that threw
   a moment ago still appears. `--all` adds the Exchanges with nothing wrong, which
   is worth a look when you want to confirm the route is being served through the
   hop at all.

## An inspected route 404s on part of my app

proximo reserves the path prefix `/.proximo/` on the origin of an inspected route
— that is where the page reports to, same-origin. If your project serves anything
under that prefix, it is unreachable for as long as the route carries the label.
Remove `proximo.inspect` and the prefix is yours again.

## VPN or corporate DNS overrides the resolver

VPN clients and corporate DNS setups often install their own resolver
configuration with higher precedence, so `.test` queries never reach proximo's
DNS server even though the stack is healthy.

- **macOS** — inspect the resolver order with `scutil --dns`: the
  `domain : test` entry must point at `127.0.0.1` port `5354`. Some VPN clients
  install scoped resolvers that shadow `/etc/resolver/<tld>`.
- **Linux** — check `resolvectl status`: the VPN interface may claim a routing
  domain of `~.` (everything), which outranks the proximo drop-in. Restrict the
  VPN's DNS scope, or re-run `proximo install` after connecting.

In both cases `dig @127.0.0.1 -p 5354 <name>.test` answering correctly proves
the problem is resolver precedence, not proximo's DNS server.

## Degraded stack

Routes stop updating (new containers are not picked up, removed containers
still resolve) when the watcher is not running — it is the reconcile loop, so
without it the routing files and certificates go stale.

```sh
proximo doctor                            # names the service that is missing
docker ps --filter "label=proximo.role"   # traefik, dns and watcher should be up
proximo up                                # converges the stack back to healthy
```

Stale routes left behind by a crash are cleaned up on the next reconcile after
the watcher restarts. If `proximo doctor` reports a version skew between the
CLI and the stack, run `proximo update` — see [Updating](updating.md#proximo-update).

## The stack runs an overridden image

[`--image`](cli.md#--image) is sticky: it is recorded in the stack's `.env` and
survives until an `up` or `update` without it. A stack running an overridden ref
is running one thing while the CLI pins another — fine while you are testing a
build, and a surprise weeks later.

```
✘ The stack runs the image this CLI pins
    the stack runs proximo:src, this CLI pins ghcr.io/filippolmt/proximo:v0.4.0
    Remedy: proximo up
```

`proximo up` (without `--image`) restores the pinned image. To keep the
override, keep passing the flag — see
[Running a different image](updating.md#running-a-different-image).

## The stack image cannot be pulled

A converge that leaves the stack image absent ends with a **Remedy** naming it:

```
the stack image ghcr.io/filippolmt/proximo:v0.4.0 is not on this host.
Remedy: docker pull ghcr.io/filippolmt/proximo:v0.4.0
```

Run that pull. proximo does not claim the pull is what broke — other services
can fail the same converge — but the pull's own output settles it, and it is
almost always one of three:

- **No route to `ghcr.io`** (offline, VPN, proxy). Then the pinned Traefik and
  dashboard images cannot be fetched either; nothing else is wrong, and the next
  `proximo up` converges once the registry is reachable.
- **`denied` / `unauthorized`.** The image tag for this CLI version has not been
  published yet (a release still building), or the package is no longer public.
  A published version tag is never deleted, so a tag that used to work and now
  denies is a package-visibility problem, not a missing image.
- **`manifest unknown`.** You are on a CLI built from an unreleased ref. Use a
  published version, or point the stack at an image you have:
  `proximo up --image <ref>` — see
  [Running a different image](updating.md#running-a-different-image).
- **The pull succeeds.** Then the image was never the problem — read the error
  above the Remedy for the service that actually failed.

proximo never falls back to building the image on your host: that retry needs
the same network that just failed, and on the rare success it would leave you
running an image nobody else has.

## The agent skill is out of date

`proximo doctor` reports the copies of the [agent skill](skill.md) proximo
installed and found behind the binary, or edited since:

```
✘ The agent skill matches the installed CLI
    ~/.claude/skills/proximo was written by another version of proximo
    Remedy: proximo skill install --scope global
    See:    https://github.com/filippolmt/proximo/blob/main/docs/troubleshooting.md#the-agent-skill-is-out-of-date
```

`install`, `up` and `update` already refresh every copy they can see, so a copy
this Check names is one they could not reach:

- **A project copy in a repository proximo is not being run from.** Auto-update
  reaches a `--scope project` copy only while proximo runs inside that
  repository, so a repository left alone holds a stale copy until someone
  returns to it. Run `proximo skill install` there.
- **A global copy no lifecycle command has run beside.** `skill install`
  defaults to project scope, so the Remedy names `--scope global` when the copy
  it is about lives under your home. A failure spanning both scopes takes two
  runs, and the next report names what is left.
- **A copy edited after proximo wrote it.** proximo never overwrites one: at
  project scope that content belongs to a team. The Remedy is
  `proximo skill install --force`, which discards the local edits — the only
  command that does.

Two answers are not failures. A copy with no `.proximo-skill.json` beside it is
**unmanaged** — installed from a marketplace or by an agent's own installer — so
proximo neither updates nor removes it, and the Check names it rather than
claiming a version it cannot know:

```
– The agent skill matches the installed CLI — proximo installed no agent skill
  on this host (~/.claude/skills/proximo came from another channel, and proximo
  neither updates nor removes it)
```

And a host with no copy at all **skips** the Check with nothing to name: a
developer who uses no coding agent should never see a red line about one.

After any of these, restart the agent session — a skill is read when the session
starts.
