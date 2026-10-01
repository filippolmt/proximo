# Specification — sharing a route with colleagues

[← back to docs index](../README.md)

> **Status: not built.** This document describes a capability the binary does
> not have. Nothing in it is honoured today: `proximo.share` is not a label,
> and the `config` subcommands, Checks and columns below do not exist. It lives
> outside the guides on purpose — the label table in `docs/routing.md` is copied
> into the published Skill, and a row written there now would tell every agent
> to use a label the binary ignores.
>
> **Every section below is headed by the guide it lands in.** Building the
> capability means moving each section into that guide, not rewriting it. The
> document is deleted once it has been emptied. The four decision records it
> relies on are written and marked `proposed`; they become `accepted` in the
> commit that builds the capability.

A developer labels a route, and a small group of colleagues reach it from their
own machines — in a browser, with a real login, cookies and WebSockets — at a
stable name that says whose machine it is on. The transport between the
machines is a mesh the team operates. proximo never installs it, never
configures it and never talks to it.

## Scope and assumptions

This is **development tooling for a small, trusted group**. The colleagues
already work together, every one of them may share their own routes with the
others, and what a colleague reaches is somebody's work in progress on their own
machine. The feature exists to *simulate* a production environment, not to be
one. People outside the group are not served: a public link for a client or an
external tester is a different product with a different threat model.

The security posture is proportionate to that. A ceremony nobody performs, a
custody rule nobody follows, or a hardware dependency standing between a machine
and its enrolment is a failure of the design, not caution. The one place this
does not relax is a colleague's trust store, which serves their real browsing
(constraint 7).

A **relayed path is a normal operating condition**. Colleagues sit behind home
routers, phone hotspots and carrier-grade NAT, and the design is judged on the
relayed path (constraint 10).

Out of scope: developing *against* a colleague's app as a backend of your own
stack, reaching machines in the mesh that do not run proximo, operating the mesh
control plane, and rewriting response bodies.

## Constraints

Each part of this specification cites the constraints that bound it. Each is a
property the implementation must keep, not a description of how it keeps it.

1. **A colleague reaches a route by a peer-qualified name, never by the local
   one.** `.test` cannot be delegated, proximo's DNS server answers every name
   under the TLD with `127.0.0.1`, and two machines running a project with the
   same name is the ordinary case. The local name and the peer name are two names
   for one service.
2. **Sharing is opt-in per route, by a label**, in the grammar proximo already
   uses for its labels. Nothing is shared because the mesh happens to be up.
3. **Sharing is durable, not a session.** The label is the switch. No link
   expires, and nothing materialises during an incident.
4. **proximo never rewrites a response body**, nor a `Set-Cookie`, nor the `Host`
   header. An app that builds URLs from a configured base, or pins a cookie to a
   `Domain`, is documented as unshareable as it stands, not patched.
5. **No real domain name is ever written into this repository.** The Peer suffix,
   the machine label and the address are configuration supplied by whoever
   installs proximo, with **no default**. Documentation uses placeholders.
6. **Nothing the mesh configures may become a machine's default resolver.**
   proximo's DNS server answers `.test` on the loopback, so a resolver that
   captures unmatched queries takes down every local route on that machine. The
   test is the same whatever the transport: `.test` still resolves locally while
   the mesh is up.
7. **What a colleague installs into their trust store can sign only beneath the
   Peer suffix, in every name type.** An anchor without that limit exposes a
   colleague's whole browsing, not the shared app. Proportionality governs how
   the signing key is *kept*, never what it may *sign*.
8. **No wildcard certificate may cover the Peer suffix or the label immediately
   beneath it.** A wildcard sitting immediately above a Public Suffix List
   boundary is refused by Chrome once that boundary is listed, and the suffix's
   parent may be listed in the future. Peer leaves carry exact names.
9. **No part of this capability may put the local `.test` path at risk.** A
   change made for sharing is measured against local breakage first. Every new
   path is **inert when unconfigured** — a machine that has not opted in executes
   no new behaviour — and `.test` behaviour is **asserted under both states**,
   configured and not, rather than trusted. This orders the risk; it does not
   veto the work.
10. **A relayed path is normal, and no per-network remedy is required.** No port
    forward, UPnP, advertised external address or static endpoint is a
    prerequisite or a documented fix. Every requirement here, the interactive one
    included, is judged on the relayed path. A direct path is an improvement the
    transport may deliver, never something the design relies on.

## Terms

These enter `CONTEXT.md` in the commit that builds the capability, not before:
the glossary carries no term for a capability that does not exist. Each is
defined by the domain, never by the mechanism that currently implements it.

- **Peer suffix** — the multi-label DNS suffix every peer name lives under,
  supplied as configuration. It is not a TLD: proximo claims nothing for it on
  the host resolver. (`zone` stays on the glossary's *Avoid* list.)
- **Machine label** — the single label that names one machine in the mesh. It
  names a **machine, not a person**, and colleagues write it down, so it is
  neutral and stable.
- **Peer name** — a name a route answers on for colleagues:
  `<…>.<machine>.<suffix>`.
  - **Bare peer name** — derived from the route's Bare host. The convenient
    form, and like the Bare host it is the one a Collision can move.
  - **Qualified peer name** — derived from the route's Qualified host. Never
    moved by a Collision. The one to send a colleague.
- **Peer host** — any host a route answers on that is a peer name, as opposed to
  its `.test` hosts.
- **Publishing peer** — a machine that exposes routes for others to reach. Being
  one means its own names must resolve for everyone else.
- **Resolving peer** — a machine that must resolve other people's peer names:
  anyone who opens those pages. A different and larger set than the publishing
  peers. *You resolve to consult; you publish to offer.* A publishing peer is
  always a resolving peer too.
- **Team root** — the certificate authority a colleague installs to trust peer
  names. Name-constrained to the Peer suffix, kept offline.
- **Intermediate** — the certificate authority one machine holds, signed by the
  team root and constrained to `<machine>.<suffix>`. proximo signs that machine's
  peer leaves with it.

## Lands in `docs/routing.md`

### The label table row

| Label | Required | Default | Meaning |
| --- | --- | --- | --- |
| `proximo.share` | no | `false` | Also serve the container's HTTP routes on their [peer names](#proximoshare--share-a-route-with-colleagues), for colleagues on the team's mesh. Truthy: `true`/`1`/`yes`. Declares intent, not reachability. HTTP-only; ignored with a warning on TCP routes. |

### proximo.share — share a route with colleagues

*A `##` section of `docs/routing.md`.*

*Constraints 1, 2, 3.*

`proximo.share` is a **pure switch**: not required, default `false`, truthy
`true`/`1`/`yes` (case-insensitive), anything else leaves sharing off — the
grammar of `proximo.redirect`, `proximo.inspect` and `proximo.transcript`. It
carries no name, no list of people and no suffix:

- **No name.** The peer names are derived. A label that could set one would let
  two routes on one machine claim the same peer name.
- **No policy.** The mesh grants access to a *machine*, never to a route
  ([who can reach a shared route](#who-can-reach-a-shared-route)), and proximo
  holds no identity a list could be matched against. A list in the label would
  look enforced and would not be. A route that must be narrower than its machine
  carries [`proximo.auth`](../routing.md#proximo-middlewares--auth-cors-custom-headers).
- **No suffix.** The machine label, the Peer suffix and the certificates are
  machine configuration ([`docs/cli.md`](#lands-in-docsclimd)).

A shared route keeps its Bare and Qualified `.test` hosts, unchanged in name and
in meaning. It **gains two peer names**, always both:

| | Derivation | `api.test` in project `shop` |
| --- | --- | --- |
| **Bare peer name** | the declared host, with `.<tld>` replaced by `.<machine>.<suffix>` | `api.<machine>.<suffix>` |
| **Qualified peer name** | the Qualified host, with `.<tld>` replaced by `.<machine>.<suffix>` | `api.shop.<machine>.<suffix>` |

The rule is the local one with a longer suffix, so it holds wherever the local
rule holds:

- **A multi-label declared host keeps its whole base.** `api.v2.test` in project
  `shop` answers at `api.v2.<machine>.<suffix>` and
  `api.v2.shop.<machine>.<suffix>`.
- **A route whose host already carries its Namespace** (`api.shop.test` in
  project `shop`) has no Qualified host, so it gets one peer name,
  `api.shop.<machine>.<suffix>`.
- **A container outside a Compose project** has no Namespace and gets the Bare
  peer name only — the same missing safety net
  [the two hosts every route gets](../routing.md#the-two-hosts-every-route-gets)
  already declares.
- **A declared host outside the TLD** (`api.example.com`) produces no peer names,
  with a watcher warning. The route's other hosts are still shared.
- **A container that loses a Collision** for its Bare host keeps only its
  Qualified peer name: there is no Bare host to derive the other from. A
  Collision between peer names on one machine is the local Collision carried
  over, reported by the same machinery. Two machines never collide: the machine
  label separates them.
- **A peer name longer than 253 octets, or with a label longer than 63**, is not
  served, with a watcher warning — a label fault, like every other.

Everything else about the route travels with it unchanged. The peer names go to
the **same service** through the **same middleware chain** — `proximo.auth`,
`proximo.cors`, `proximo.header.*` and `proximo.inspect` included — so a
colleague's request is the request a local browser makes, through another name.
`proximo.redirect` applies to the peer names with identical semantics: opt-in,
`302`, and no `:80` route without it. **Replica sets are shared normally**: the
peer names point at the service, not at one backend.

`proximo.share` is per container, so a host split across containers with
`proximo.path` is shared per container. Sharing only the container that serves
`/api` gives a peer name that answers `404` at its root and serves `/api`. That
is the contract working: a peer name is the union of the routes that opted in,
not a proxy for the whole site.

**TCP routes are not shared.** A route declaring `proximo.tcp.port` is routed by
SNI and has no HTTP layer, and a `passthrough` backend could not present a peer
certificate anyway. The label is ignored, with a watcher warning and a
[`proximo status`](#proximo-status--the-peer-column) row.

**The label declares intent, never reachability.**

> `proximo.share` declares that this route is to be served on its peer names.
> Whether a colleague can actually reach it additionally depends on this
> machine's machine label, Peer suffix, team root and intermediate being
> configured, and on the mesh being up — none of which the label controls.

With any of the four missing, proximo emits no peer route: the label is set,
correctly honoured, and the route does not answer on its peer names.
`proximo status` says which value is missing. With the mesh down, proximo
cannot tell, and says nothing.

**An unshared route is unreachable from the mesh by construction.** Without the
label there is no peer route at all, so a colleague who guesses an unshared
route's peer name gets exactly what a name nobody declared gets
([a shared link fails with a certificate error](#a-colleague-sees-a-certificate-error-on-a-shared-link)).
Removing the label withdraws the peer route at the next reconcile, with no
draining and no grace period: a colleague holding the page open holds a page,
not a session.

**Send the Qualified peer name.** `proximo status` prints both, because it
reports facts, but the Bare peer name is the one a Collision can move.

#### What an app needs to be shareable

*A `###` inside the section above.*

*Constraint 4.* proximo detects nothing here. An app is shareable when it has two
properties, and the self-test below checks both:

1. **Every absolute URL is derived from the request** — `Host`, or
   `X-Forwarded-Host` — never from a configured base URL (`APP_URL`,
   `SITE_URL`, `ROOT_URL` and the like). Traefik passes `Host` through
   unrewritten and sets `X-Forwarded-Host` to the peer name and
   `X-Forwarded-Proto` to `https`, so an app that builds URLs from the request
   needs no change. One built from a configured base sends a colleague's browser
   to a `.test` host that does not resolve on their machine.
2. **Cookies are host-only**, with no `Domain` attribute. A cookie pinned to
   `Domain=<name>.test` is refused on the peer name, so the login appears to
   succeed and never sticks. A cookie pinned to the Peer suffix is sent to every
   shared route of every colleague, because the whole mesh is one site.

**The self-test**: open your own route's **Qualified** peer name in your own
browser, log in, and navigate. If you stay on the peer name and the session
holds, the app is shareable. It needs no colleague, because this machine's
resolver reaches its own peer names like anyone else's.

A WebSocket needs no caveat: Traefik proxies the upgrade on the same route, and
`Origin` and `Host` carry the same peer name, so an origin check comparing them
passes.

A session started on the `.test` host does not follow onto the peer name, and
the reverse: they are different sites. You log in once per name.

## Lands in `docs/cli.md`

The `### proximo config …` sections below each become a `##` section of
`docs/cli.md`, with its row in `docs/README.md` and in the command table at the
top of that guide.

All of the configuration below is persisted in `config.json` beside the TLD, has
**no default**, and is set one value per subcommand, the way
[`proximo config tld`](../cli.md#proximo-config-tld) is. Partial configuration is a
legitimate state, not an error: `status` and `doctor` report what is missing.

The validation rule is the same everywhere, and it is the one the next value
added must follow: **proximo refuses what the machine can decide, reports what
it cannot, and does both when a person sets the value** — not at reconcile, and
not as a refusal to start. A label is read with nobody watching, so a bad label
degrades with a watcher warning; a configuration value is typed by a person who
will read a refusal. Every rule a person has to apply is printed in terms of the
**property required, never the mechanism that currently satisfies it**.

### `proximo config machine`

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
  intermediate, which is constrained to the old label.

### `proximo config peer-suffix`

```sh
proximo config peer-suffix <suffix>
```

Set the Peer suffix every peer name lives under.

- **Refused**: anything that does not normalise to lowercase `[a-z0-9-]` labels,
  or that has fewer than two labels. This is not `config tld`'s normaliser,
  which accepts exactly one label.
- **Warned at set time, never refused**: a suffix whose right-most label is not
  reserved from delegation for private use. The list is the one `config tld`
  already advises from. The value is stored either way.
- **The rule, in prose.** A suffix is admissible when all three hold:
  1. **No resolver on any colleague's machine claims it ahead of the nameserver
     the mesh configures.** `.local` fails this: mDNS answers it first on macOS
     and Linux.
  2. **No Public Suffix List entry can reclassify it underneath a shipped
     design.** `home.arpa` is the precedent. Constraint 8 is what makes a future
     listing survivable.
  3. **Nobody can delegate it in the future.** A single unreserved label works on
     the day it is chosen and stops resolving the day someone registers it.
     *Unclaimable*, not merely unclaimed.

  The rule is deliberately not "avoid reserved names": proximo depends on `.test`
  being reserved.

### `proximo config address`

```sh
proximo config address <ip>
```

Set the address proximo answers this machine's peer names with — this machine's
own address on the mesh.

- **Refused**: anything that is not a parsable IP address.
- **Warned at set time, never refused**: an address that no interface of this
  machine currently holds. The check compares the **exact** address against the
  addresses of **every** interface: a subnet test would assert nearly nothing,
  and interface names are not stable. It is a report, because a correct address
  is held by nothing whenever the mesh client is down.
- **Printed on every set**: the rule. The check separates "an address this
  machine holds" from "an address that exists nowhere on it". It catches a typo
  and a stale value. It cannot tell the mesh address from the LAN address or
  `127.0.0.1`, and that part is the person's to get right.

### `proximo config team-root`

```sh
proximo config team-root <path>
```

Point proximo at the team root certificate. The file is public and is
distributed however the team likes — never from this repository.
[`proximo trust`](../cli.md#proximo-trust) and `install` then install it as a
second anchor beside the local CA, and `uninstall` removes it.

- **Refused, hard** — the one value whose rule is checked completely, and the
  most consequential (constraint 7): a certificate whose permitted DNS subtree
  does not cover the configured Peer suffix, whose name constraints are not
  marked critical, or which lacks exclusions for every IP address (`0.0.0.0/0`,
  `::/0`), every email address and every URI. Constraining `dNSName` alone leaves
  every other name type unrestricted.
- **Refused with a Remedy** naming `proximo config peer-suffix` when the suffix
  is not set yet, since coverage cannot be judged without it. That is an order of
  setting, not a completeness requirement.

### `proximo config csr`

```sh
proximo config csr > machine.csr
```

Print the certificate signing request for this machine's intermediate on stdout.
It creates the machine key only if none exists. Run again, it prints the same CSR,
and it **never replaces a key that already has an intermediate**. A reinstalled
machine has no key, so it gets a new one — the ceremony is the same as a first
enrolment. The key never leaves the machine; the CSR is a public object, so any
channel may carry it. See
[the team root and the intermediates](#the-team-root-and-the-intermediates).

### `proximo config intermediate`

```sh
proximo config intermediate <file>
```

Install the intermediate a custodian signed from this machine's CSR.

- **Refused, hard**: a certificate that does not chain to the configured team
  root, whose permitted subtree is not exactly `<machine>.<suffix>`, whose
  `MaxPathLen` is not 0, or that does not match the machine key.
- **Refused with a Remedy** naming the missing value when the team root, the
  machine label or the Peer suffix is not set.

### `proximo config mesh-remedy`

```sh
proximo config mesh-remedy '<command>'
```

Set the command the [`mesh` Check](#proximo-doctor--the-peer-checks) offers as
its Remedy — typically the transport's own status command. Stored verbatim, with
no validation and no warning: proximo cannot judge a transport's command, and a
public CLI names no vendor. Unset, `mesh` still runs and offers a platform
command instead. It is an override, never a prerequisite.

### `proximo status` — the `PEER` column

*Lands inside the existing `## proximo status` section.* A `PEER` column appears
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

There is no clipboard flag and no `status --json`.

### `proximo doctor` — the peer Checks

*Lands inside the existing `## proximo doctor` section.* Four Checks. On a machine
that has not opted in, every one of them is Skipped, and `dns-server` and
`dns-resolver` return exactly the Result they return today (constraint 9).

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

### `proximo errors` — a request that arrived on a peer name

*Lands inside the existing `## proximo errors` section.*

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
  it ([ADR 0006](../adr/0006-the-transcript-is-quoted-never-stored.md)). **An
  Incident is never attributed to a peer**: the runtime declares nothing about
  the browser that caused a request
  ([ADR 0007](../adr/0007-proximo-remembers-what-the-runtime-declares.md)).
- **`proximo.share` with `proximo.inspect` is allowed, silently.** A colleague's
  Client report and Snapshot are kept like any other.

### `proximo up`, `proximo trust`, `proximo uninstall`

*Each lands inside its existing section.*

- **`up`**: when the peer DNS service cannot start, `up` succeeds, prints a
  warning naming `peer-dns` and the cause (the address is held by no
  interface), and exits 0, because `.test` works. A peer-side failure never fails
  the command that serves `.test`.
- **`trust`** and **`install`**: with `team-root` set, also install the team root
  in the system and NSS stores, as a second anchor; without it, behave exactly as
  today.
- **`uninstall`**: also removes the team root.

## Lands in `docs/troubleshooting.md`

Each `###` below becomes a `##` section, with its row in `docs/README.md`. The
first six are written for a colleague who reports a symptom; the rest are the
sections `doctor`'s peer Checks point at. None of them names a vendor or a
hostname: where the mesh is operated and by whom is outside this repository.

### A shared link does not resolve or times out

A timeout with no page is what a machine that has not admitted you looks like:
nothing listens below TLS. The causes, none of them a proximo fault and none of
them affecting the sharing machine's own `.test` routes:

- the mesh's access policy does not admit your machine to theirs;
- the sharing machine's mesh login has lapsed — ask them to log in, retrying
  will not help;
- the sharing machine is off, or its mesh client is down;
- your machine is not a resolving peer, so the name does not resolve for you.

On the sharing machine, `proximo doctor` reports `mesh`.

### A shared link lands on a `.test` address that does not resolve

The browser shows `DNS_PROBE_FINISHED_NXDOMAIN` for a `.test` host. The app
built an absolute URL — a redirect, usually after login — from a configured base
URL instead of from the request. See property 1 of
[what an app needs to be shareable](#what-an-app-needs-to-be-shareable).

### Login on a shared link does not stick

The login appears to succeed and the next page is anonymous. The app pinned its
session cookie to `Domain=<name>.test`, which the browser silently refuses on the
peer name. In the other direction, a session started on the `.test` host never
follows onto the peer name: one login per name. A colleague's app that pins a
cookie to the Peer suffix can also overwrite your session, because the whole mesh
is one site. See property 2 of
[what an app needs to be shareable](#what-an-app-needs-to-be-shareable).

### A shared link answers 404

The route is shared and the request reached it:

- only a `proximo.path` prefix is shared, so the root answers `404`, and
  `proximo status` does not print paths;
- the app itself answers `404`;
- with `proximo.inspect` on, `/.proximo/` is reserved on the peer names too;
- the Bare peer name moved to another container after a Collision — use the
  Qualified peer name.

An unshared route or an invented name does **not** answer `404`; it fails at the
certificate (next section).

### A colleague sees a certificate error on a shared link

Read what the certificate names:

- **It carries no name at all.** The name is not shared on that machine, or
  nobody declared it. The two are deliberately indistinguishable: there is no
  catch-all that says "not shared", because one would let a colleague tell
  "exists but unshared" from "does not exist". Possible reasons: the route lacks
  `proximo.share`; the label is set and a value is missing (the sharing machine's
  `proximo status` names it); the declared host is outside the TLD; the container
  is not running.
- **It carries the right name under an unknown issuer.** The team root is not
  installed on the colleague's machine: macOS reports `CSSMERR_TP_NOT_TRUSTED`,
  Chrome shows a warning that cannot be clicked through, Firefox
  `SEC_ERROR_UNKNOWN_ISSUER`. Install it ([`docs/sharing.md`](#lands-in-docssharingmd)).
- **The intermediate has expired.** proximo keeps issuing valid leaves under it,
  so only the colleague's browser sees the failure. On the sharing machine
  `peer-intermediate` fails a month ahead.
- **Chrome older than 126.** Below 126 a policy could switch off enforcement of
  the team root's constraints, so it is not a supported browser for peer names.
  No `doctor` Check reports it: the browser that matters is on the colleague's
  machine.
- **A name-constraint violation**, which no platform names as such: macOS reports
  the certificate as *"not standards compliant"* (status `-2147409643`, the same
  for a DNS and an IP violation), SecureTransport flattens it to `unable to get
  local issuer certificate`, Chrome shows the generic `ERR_CERT_INVALID`, Firefox
  `SEC_ERROR_CERT_NOT_IN_NAME_SPACE` — even before the root is installed — and
  OpenSSL `permitted subtree violation` or `excluded subtree violation`. Any
  diagnostic that matches one alert string is wrong on some client. It means a
  certificate was issued outside the machine's subtree, which the configuration
  refusals exist to prevent.

### A shared link is slow

The path between the two machines is relayed — behind a phone hotspot or
carrier-grade NAT it usually is, and stays so. That is a normal operating
condition and there is no per-network fix to apply. On a relayed path a new
connection costs from a few hundred milliseconds up to about a second, with
visible jitter. Later requests reuse the HTTP/2 connection and skip the
handshake, so it feels like a distant server, not like localhost.

**Lines inside the sections above, not sections of their own:**

- A colleague cannot read the Inspection of their own request: the read API is
  on the sharing machine's loopback, so the diagnosis is asked of the publisher.
- Inspection is Chrome-only, so a colleague on another engine produces no Client
  report; the Access record and the Transcript are still there.

### A shared route is not served on its peer names

`peer-routes` failed: a label on a shared route cannot take effect. Either a
declared host is outside the TLD, so it has no peer names, or the route is a TCP
route, which is never shared. `docker inspect <container>` shows the labels; drop
`proximo.share` or the TCP label, or move the host under the TLD.

### proximo does not answer on the mesh address

`peer-dns` failed. The peer DNS service is not answering on `<address>:5354`:

- **The address is held by no interface** (the Detail says so): the mesh client
  was down when the service last tried to start. The watcher retries on its own
  once the address appears; `proximo up` retries now.
- The stack's watcher is down, so nothing retries: `proximo up`.
- `config address` is not this machine's address on the mesh.

Until this passes, the nameserver group that routes the subtree to this machine
resolves nothing — which is why it is created only after this passes.

### This machine's peer names do not resolve

`mesh` failed while `peer-dns` passed: proximo answers, and the system resolver
does not send it the query. The Remedy shows which resolver handles the Peer
suffix and where it points. The causes are the mesh's: its client is down or
logged out, the nameserver group for `<machine>.<suffix>` does not exist or
forwards to the wrong port, or this machine is not in the group it is
distributed to — see
[a publishing machine is not a resolving peer](#a-publishing-machine-is-not-a-resolving-peer).

### A publishing machine is not a resolving peer

`mesh` resolves the machine's own names, so a publishing machine outside the
resolving peers fails it even while colleagues reach it. Being a resolving peer
is a requirement for publishing ([`docs/sharing.md`](#lands-in-docssharingmd)):
add the machine to the set the subtrees are distributed to.

### The machine's intermediate is about to expire

`peer-intermediate` failed: the intermediate expires in under 30 days, or has.
Run the ceremony again — `proximo config csr`, have it signed, `proximo config
intermediate <file>`. The machine label does not change, so every bookmark keeps
working.

### A line added to the existing `VPN or corporate DNS overrides the resolver`

A mesh nameserver configured as the machine's **primary** resolver is one more
such cause. It captures every query no rule matches, `.test` included, and it
breaks every local route on the machine. The mesh's nameserver configuration must
never be primary (constraint 6).

## Lands in `docs/architecture.md`

### The peer DNS service

*Constraints 6, 9. [ADR 0013](../adr/0013-proximo-answers-the-peer-subtree.md).*

proximo's DNS server answers this machine's own peer subtree, `<machine>.<suffix>`,
and each colleague's resolver is routed to it by the mesh. Nothing central holds
records about the machines: a machine's routes are known only to that machine,
and are answered only by it, at any depth. A machine that is off stops answering
for its names; nothing else answers for it with a stale address.

- **A separate, peer-only listener.** The loopback DNS handler does not change.
  The peer handler answers `A` for any name under `<machine>.<suffix>`, at any
  depth, with the configured `<address>`; `AAAA` and every other type with
  NOERROR and no records, as the loopback handler does for the TLD. Every name
  outside the subtree gets **`REFUSED`**: no upstream forwarding and no `.test`,
  so the publishing machine is never a recursive resolver for the mesh.
- **Its own container port.** Docker Desktop rewrites the source address of a
  published port, so the server cannot tell a mesh query from a loopback one by
  its sender. The two handlers are kept apart by container port.
- **Its own Compose service**, published only on `<address>:5354`, UDP and TCP.
  A publish bound to an address no interface holds leaves its container
  `created`, and Docker's restart policy never retries it. Inside the `dns`
  service that would take `.test` down on every machine whose mesh is down at
  start; in a service of its own it fails alone. Every interface is not an
  option: Docker refuses a second publish of the loopback port, and it would
  expose the listener on the LAN.
- **Present if and only if** `machine`, the Peer suffix and `address` are all
  configured. In any other state the service is absent from the materialised
  Compose file, which is then identical to today's. The certificates are not
  required: a name that resolves while TLS fails is a stated state, not an error.
- **The watcher restarts it.** Docker Desktop and the mesh client both start at
  login, in no fixed order. Whenever the peer DNS service is `created` or
  `exited`, the watcher runs `docker start` on it at a fixed interval. It never
  inspects host interfaces, touches no other service, and does nothing on a
  machine where the service does not exist.
- **Not a core service.** It is not one of the services `stack` requires, so a
  mesh problem never fails `stack` and never skips the rest of `doctor`.

### The peer route

*Constraints 1, 4. [ADR 0012](../adr/0012-a-peer-host-is-answered-without-rewriting-host.md).*

For a shared HTTP route the watcher writes a **second router** on the
`websecure` entrypoint. Its rule is the alternation of the route's peer names
(with the route's path prefix, when it has one); it points at the **same
service** as the local router — the Inspection hop's, when the route is
inspected — and carries the **same middleware chain**. `Host` is never rewritten.
With `proximo.redirect` it gets the same redirect on `web`.

- **Peer hosts are a distinct field** of the routed container, never merged into
  its local hosts. The local host list feeds the router rule, the SANs the local
  CA signs and the Collision detector; a peer name reaching the local CA's
  certificate would be valid and untrusted, and silent until a colleague's
  browser refused it.
- **The peer router is emitted only when** the machine label, the Peer suffix,
  the team root and the intermediate are all configured. Without them the route
  stays local and fully working. A peer router served under an untrusted chain
  would warn exactly as an interception does.
- **proximo does not know a mesh exists.** Traefik publishes `:443` on every
  interface already, and the mesh client runs on the host beside the stack.
  Docker Desktop's forwarder serves the mesh interface. proximo calls no mesh API,
  discovers nothing and names no vendor: it is told a label, a suffix and an
  address.

### TLS and trust

*Constraints 7, 8. [ADR 0010](../adr/0010-peer-certificates-come-from-a-name-constrained-team-root.md).*

- **The team root** is a second trust anchor beside the machine's local CA, never
  in place of it. It is installed and removed under a **name of its own**:
  today one constant is both the NSS nickname and the macOS deletion selector of
  the local CA, so removing one anchor must never be able to remove the other.
- **The intermediate** is held in the TLS state directory with the machine key
  that never leaves the machine.
- **Peer leaves** are signed locally with the intermediate, exactly as the local
  CA signs `.test` leaves: one per shared container, its peer names as exact
  SANs, 397 days, reissued when its peer names change and removed when the
  container stops being shared. The certificate file presents the intermediate
  after the leaf, since a colleague's machine holds only the root. Traefik reads
  them through the same file provider. SNI selects between a route's two leaves.
- **No wildcard**, at any level (constraint 8).
- **The default certificate is nameless.** Traefik has one TLS store and one
  default certificate, served whenever no leaf matches the SNI. proximo writes a
  certificate with **no SAN at all**, signed by the local CA, as that default —
  always, with zero routes and whether or not the machine is configured. A name
  this machine does not serve therefore gets the same certificate whether it is
  an unshared route or an invented name, and the certificate names none of the
  machine's local routes. A local `.test` name with no route stays in the error
  class it has today: a trusted issuer and the wrong name. `sniStrict` is not
  used: it is global, and would turn local clients without SNI into handshake
  failures.

## Lands in `docs/sharing.md`

A new how-to guide, for a person setting up sharing on a machine or for the team.

### Who can reach a shared route

The mesh grants access to a **machine**, never to a route: every shared route on
a machine answers on one address and one port, and the route is chosen by a
`Host` header inside a TLS session the mesh never opens. A mesh can enforce
application-level access only for the protocols it terminates. So:

- **Whoever the mesh admits to a machine reaches every route that machine
  shares.** "Shared" means shared with everyone allowed to reach you.
- **The mesh admits exactly HTTPS and the peer DNS port** between the machines
  that take part — nothing else they listen on.
- **A route narrower than its machine carries `proximo.auth`**, which answers
  `401` without credentials. proximo neither requires nor warns about that
  pairing: sharing without it is the model working.

What a refused colleague sees: a **timeout** from a machine that has not admitted
them, a **`401`** from `proximo.auth`, and **the same nameless certificate** for
an unshared name and an invented one.

### What the mesh must provide

These are requirements on whatever transport the team runs, stated as
properties. [Appendix — one transport that satisfies them](#appendix--one-transport-that-satisfies-them)
maps them onto one product.

1. **A layer-3 mesh.** Each machine holds an address the others can reach, and
   reaches theirs, over relays when no direct path exists (constraint 10).
2. **The subtree `<machine>.<suffix>`, at any depth, is routed to that machine's
   configured address on port 5354** for every resolving peer — and the mesh
   **never becomes a machine's primary resolver** (constraint 6). It forwards; it
   hosts no records about the machines.
3. **Between the machines that take part, it admits exactly TCP 443 and port 5354
   over UDP and TCP** to the publishing peers.
4. **Membership follows the identity provider.** Disabling a person removes their
   machines from the mesh within the login expiry the mesh is configured with.

### Taking part

**To reach colleagues' routes** — every colleague:

1. Be enrolled in the mesh, and be a resolving peer.
2. `proximo config peer-suffix <suffix>`, then `proximo config team-root <path>`,
   then `sudo proximo trust`. Fully restart the browser.
3. Use Chrome 126 or later, or Firefox.

**To share your own routes** — additionally, in this order:

1. `proximo config machine <label>` and `proximo config address <ip>`.
2. `proximo config csr > machine.csr`, send it to a custodian, and install what
   comes back with `proximo config intermediate <file>`.
3. `proximo up`, then `proximo doctor`: `peer-dns` passes.
4. **Only then** does the mesh operator create the routing of
   `<machine>.<suffix>` to `<address>:5354`. Created first, it would exist,
   carry the right flags and resolve nothing — configuration that looks live and
   is not. `mesh` now passes.
5. Label a route `proximo.share=true`; `proximo status` shows its peer names. Run
   the [self-test](#what-an-app-needs-to-be-shareable), then send the Qualified
   peer name.

### The team root and the intermediates

*Constraint 7, and the proportionate posture in [Scope and assumptions](#scope-and-assumptions).*

- **The root key is an encrypted file on offline physical media, in two copies
  held by two people.** No service of any kind holds it: not a password manager,
  not a secret on a cluster, not the machine that runs the mesh — the last would
  put the signing key and mesh access, the two compromises that must both occur,
  behind one door. Losing both copies is an **accepted recovery path**: generate a
  new root, re-sign the intermediates, re-install the anchor on every machine.
  So custody needs secrecy, not durability.
- **Both certificates are name-constrained, critically, in every name type.** The
  root is permitted the Peer suffix; each intermediate is permitted
  `<machine>.<suffix>` and has `MaxPathLen` 0. Both exclude every IP range
  (`0.0.0.0/0`, `::/0`), every email address and every URI: a name type absent
  from the permitted subtrees is unrestricted.
- **Validity**: root 10 years, intermediate 5 — in practice one ceremony per
  machine in its life — leaf 397 days, unchanged.
- **The ceremony.** The machine runs `proximo config csr`; the CSR reaches a
  custodian over any channel, since it is public; the custodian decrypts the root
  into a temporary directory on an ordinary laptop, signs with the script checked
  into this repository, and removes the plaintext; the intermediate, also public,
  returns over any channel; the machine runs `proximo config intermediate`. The
  script takes the Peer suffix and the machine label as arguments and names
  neither. A reinstalled machine repeats the ceremony with no special case, under
  the same label; its old intermediate is not revoked and lapses on its own.
- **No revocation.** Presenting a peer certificate requires mesh membership, which
  offboarding removes, so real harm needs a valid certificate *and* mesh access. A
  revocation list would be a service in continuous operation for a development
  tool. Someone who leaves and regains mesh membership is a failure of mesh
  offboarding that no certificate mechanism here would fix.

### Limits for whoever runs the team

- Identity-provider offboarding covers machines enrolled through the identity
  provider, within the login expiry the mesh is configured with — nothing else.
- Machines enrolled without a person (a headless server) and any local
  break-glass account of the mesh are always revoked by hand, and nothing will
  remind anyone.
- A shared route answers only while the sharing machine's mesh login is live.
  When it lapses, the machine's routes stop answering for colleagues until its
  owner logs in again. The expiry trades that against how fast revocation
  happens, and the choice is the team's.
- When group membership is declared in infrastructure-as-code rather than derived
  from the identity provider, a person who changes team keeps their access until
  the change is merged.
- There is no certificate revocation, for the reason above.
- The intermediate expires after five years; `peer-intermediate` fails 30 days
  ahead.

### Appendix — one transport that satisfies them

*Lands as the last section of `docs/sharing.md`.* NetBird, self-hosted, verified
end to end between two machines, one of them relayed. It is one transport that
satisfies the requirements, not a requirement. Every value is a placeholder.

| Requirement | NetBird |
| --- | --- |
| 1. Layer-3 mesh | the NetBird client on each machine; its relay carries peers no direct path reaches |
| 2. Subtree routing | **one nameserver group per publishing peer**: domain `<machine>.<suffix>`, nameserver `<address>` with **`port = 5354`**, **`primary = false`**, **`search_domains_enabled = false`**, distributed to the group of resolving peers. Write all three explicitly and confirm them in the plan: an omitted optional field can carry a default that silently substitutes port 53 or turns the group primary. Leave the account's DNS domain empty. NetBird's compiled-in search domain still appears in the search list; it appends only to unqualified names and cannot capture a `.test` query |
| 3. Access | one group holding the machines that take part, one group-to-group policy from it to itself, `bidirectional`, TCP 443 and TCP+UDP 5354, nothing else. Declaring a user's groups replaces membership not declared; verify membership per peer afterwards |
| 4. Membership | developers' machines enrol through SSO with interactive login, under a tenant identity; `peer_login_expiration` is the login expiry above; setup keys only for headless machines; the local break-glass account holds no peers |

Enumerating a nameserver group is not a test that it resolves: test with
`dscacheutil -q host -a name <name>` on macOS (never `dig`, which bypasses the
scoped resolvers) and always beside a control name that should not resolve.

## Decision records

Written from the standing positions, `status: proposed`:

- [0010 — Peer certificates come from a name-constrained team root](../adr/0010-peer-certificates-come-from-a-name-constrained-team-root.md)
- [0011 — A shared route answers on two peer names, derived from its local hosts](../adr/0011-a-shared-route-answers-on-two-derived-peer-names.md)
- [0012 — A peer host is answered without rewriting `Host`](../adr/0012-a-peer-host-is-answered-without-rewriting-host.md)
- [0013 — proximo's DNS server answers the peer subtree](../adr/0013-proximo-answers-the-peer-subtree.md)

ADR 0003 gains a *See also* line pointing at 0011, and its text is otherwise
unchanged: a machine *answers* a second suffix, but claims no second TLD on the
host resolver, which is what "exactly one TLD per machine" is about.

## What the published Skill will owe

Nothing changes in `skills/proximo/` until the capability is built. Then:

- the `proximo.share` row, which arrives through the generated label-table block;
- a "share a route with a colleague" trigger in `SKILL.md`'s description;
- the symptoms above that an agent must recognise — a shared link that times out,
  lands on `.test`, loses its login, or fails at the certificate.

## Acceptance

What the implementation must demonstrate before it is accepted. The first block
is what was never measured by hand; the rest guard constraint 9.

**Never measured, verified on real machines:**

1. The **Qualified** peer name works in a browser: login, navigation and the
   session holding, as the Bare one was shown to.
2. A shared session survives the sharing machine passing through `Login
   required` and back.
3. Whether the mesh address stays assigned while the machine is in `Login
   required`, as opposed to after a deliberate disconnect.
4. On Ubuntu, the mesh address is held by the mesh interface, so the address
   check has something to read.
5. With the mesh down at `proximo up` and brought up later, `peer-dns` passes with
   no intervention.
6. With the mesh dropping and returning while the peer DNS service runs, whether
   the listener answers again. If it does not, widening the watcher's restart is
   a new decision, not part of this specification.
7. From a colleague's machine, `openssl s_client -servername` on an unshared
   name and on an invented name returns the same certificate, with no SAN.

**Constraint 9, asserted by the build:**

8. With no peer value set, the materialised Compose file is identical to
   today's.
9. The DNS server's `.test` answers are tested with the peer values set and
   unset, and are the same.
10. The default TLS store references a certificate with no SAN, and a served
    `.test` route still presents its own leaf — with the peer values set and
    unset.
11. With `machine`, `peer-suffix` and `address` unset, `peer-dns` and `mesh` are
    Skipped, and `dns-server` and `dns-resolver` return the same Result
    configured and unconfigured.
12. With only `mesh-remedy` unset, `mesh` runs, and fails with the platform
    Remedy when the suffix is not routed.
13. Stopping the peer DNS service leaves `stack` passing.

**Contract:**

14. Each refusal of `config machine`, `config peer-suffix`, `config address`,
    `config team-root` and `config intermediate`, and each warning, is tested on
    the case it exists for — the team root on a certificate constrained only in
    `dNSName`, the intermediate on one with an IP SAN.
15. Removing `proximo.share` withdraws the peer router and the peer leaf at the
    next reconcile.

## Left to implementation

Choices nothing above depends on, deliberately not fixed here: the peer DNS
service's container port and the watcher's retry interval (30 s is the figure
the design assumed); whether setting a value converges a running stack at once,
as `config tld` does, or at the next `up`; how a value is unset; the file names
in the TLS state directory; and the team root anchor's name, provided it cannot
collide with the local CA's.
