# Sharing a route with colleagues

[← back to docs index](README.md)

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

Each is a property proximo keeps, stated without the mechanism that currently
keeps it; the guides cite them by number where they bound a behaviour.

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

## Who can reach a shared route

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

## What the mesh must provide

These are requirements on whatever transport the team runs, stated as
properties. [Appendix — one transport that satisfies the requirements](#appendix--one-transport-that-satisfies-the-requirements)
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

## Taking part

**To reach colleagues' routes** — every colleague:

1. Be enrolled in the mesh, and be a resolving peer.
2. `proximo config peer-suffix <suffix>`, then `proximo config team-root <path>`,
   then `proximo trust`, which asks for `sudo` itself. Fully restart the browser.
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
   the [self-test](routing.md#what-an-app-needs-to-be-shareable), then send the Qualified
   peer name.

## The team root and the intermediates

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
  into this repository (below), and removes the plaintext; the intermediate, also public,
  returns over any channel; the machine runs `proximo config intermediate`. The
  script takes the Peer suffix and the machine label as arguments and names
  neither. A reinstalled machine repeats the ceremony with no special case, under
  the same label; its old intermediate is not revoked and lapses on its own.
- **No revocation.** Presenting a peer certificate requires mesh membership, which
  offboarding removes, so real harm needs a valid certificate *and* mesh access. A
  revocation list would be a service in continuous operation for a development
  tool. Someone who leaves and regains mesh membership is a failure of mesh
  offboarding that no certificate mechanism here would fix.

The custodian's script is [`tools/team-ca/team-ca.sh`](https://github.com/filippolmt/proximo/blob/main/tools/team-ca/team-ca.sh),
which needs only Docker. It mints the team root once, and signs each machine's
CSR into its intermediate, in exactly the shape `config team-root` and `config
intermediate` accept:

```sh
tools/team-ca/team-ca.sh root <suffix>        # team-root.crt + team-root.key; encrypt the key
tools/team-ca/team-ca.sh sign <suffix> <machine> team-root.crt team-root.key machine.csr > intermediate.crt
```

## Limits for whoever runs the team

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

## Appendix — one transport that satisfies the requirements

NetBird, self-hosted, verified
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
