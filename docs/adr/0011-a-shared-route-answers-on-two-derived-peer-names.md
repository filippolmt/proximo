---
status: proposed
---

# A shared route answers on two peer names, derived from its local hosts

> **Proposed.** Part of the [peer-sharing specification](../specs/peer-sharing.md),
> which describes a capability the binary does not have yet. It becomes
> `accepted` in the commit that builds it.

A colleague cannot reach a route by its `.test` name: `.test` is not delegable,
proximo's DNS server answers every name under it with `127.0.0.1`, and two
machines running a project called `shop` is the ordinary case. A shared route
needs a name that says which machine it is on, that a colleague can bookmark, and
that a Collision on the sharing machine cannot quietly redirect.

**Decision.** A shared route answers on **two peer names**, derived from the two
hosts [ADR 0003](0003-every-route-answers-on-a-qualified-host.md) already gives
it, by one rule: replace `.<tld>` with `.<machine>.<suffix>`.

| | Derived from | `api.test` in project `shop` |
| --- | --- | --- |
| **Bare peer name** | the Bare host | `api.<machine>.<suffix>` |
| **Qualified peer name** | the Qualified host | `api.shop.<machine>.<suffix>` |

- **Two names, because ADR 0003's contract is extended across the mesh, not
  reduced.** Locally the Bare host is the convenience and the only name a
  Collision can move; the Qualified host never moves. Offering a colleague only the
  Bare-derived name would hand them the one name that can silently start pointing
  at a different project's service. Both are always present; there is no switch
  for one without the other.
- **Derived, never declared.** The label `proximo.share` is a switch and carries no
  name, so two routes on one machine cannot claim the same peer name by writing it.
- **One rule, not a second dialect.** The local Qualified host is
  `base.<namespace>.<tld>` with the declared base kept whole, so the peer names
  inherit everything the local rule already handles: a multi-label host
  (`api.v2.test` becomes `api.v2.<machine>.<suffix>` and
  `api.v2.shop.<machine>.<suffix>`), a host that already carries its Namespace
  (one peer name), a container outside a Compose project (Bare only), and
  Collisions, which stay one problem with one reporting path.
- **`<machine>` names a machine, not a person**, is mandatory, and has no default
  derived from the host. A person can own two machines, a machine outlives its
  owner's tenure, and colleagues write the label into bookmarks and READMEs, so
  renaming it later breaks everyone who did. It must be chosen, and proximo prints
  that rule every time it is set.
- **The Peer suffix is configuration with no default, chosen by a rule rather than
  named.** A suffix is admissible when no resolver on a colleague's machine claims
  it ahead of the mesh's nameserver (the `.local` failure), no Public Suffix List
  entry can reclassify it underneath a shipped design (the `home.arpa` failure),
  and nobody can delegate it in the future — unclaimable, not merely unclaimed. The
  rule is not "avoid reserved names": proximo depends on `.test` being reserved.
- **No wildcard certificate at or right beneath the suffix.** Chrome refused
  `*.home.arpa` once `home.arpa` entered the Public Suffix List, while
  `*.sub.home.arpa` kept working: the failure needs a wildcard immediately above a
  listed boundary. Exact SANs per route are what make a future listing of the
  suffix's parent survivable.

**The TLD stays exactly one per machine.** This clarifies ADR 0003 rather than
amending it: the machine *answers* a second suffix, but claims nothing for it on
the host resolver.

## Considered options

**The Bare peer name only.** Simpler, one name to explain. Rejected: it is
precisely the name a Collision can move, which is the failure ADR 0003 exists to
remove ([#77](https://github.com/filippolmt/proximo/issues/77)).

**A flattened Qualified name, `<name>--<project>.<machine>.<suffix>`.** Chosen
first, when a per-machine wildcard certificate covered exactly one label; the
double hyphen kept the mapping invertible where a single one would collide with
service names that contain a hyphen. Rejected once the wildcard was gone
([#98](https://github.com/filippolmt/proximo/issues/98)): it was the wildcard's
price, never parity with the local shape, which was always dotted. It defines a
second grammar with its own collision class, can exceed the 63-octet label limit,
and skips the `<project>` cookie scope ADR 0003's trade presumes.

**A suffix under a domain the team owns.** Immune to a future Public Suffix List
entry. Rejected ([#101](https://github.com/filippolmt/proximo/issues/101)): the
exposure it avoids is neutral or beneficial under a private-use suffix — a listing
of the parent would make each machine its own site — while under an owned domain a
cookie scoped to that domain reaches everything else the team serves there. A
signed parent zone would also make privately served answers fail validation.

**Deriving `<machine>` from the hostname or the OS user.** Rejected: both usually
carry a person's name, and both change when the machine does.

## Consequences

- **Cookies do not isolate machines.** The suffix's registrable domain is the
  suffix, so every peer name is same-site with every other, and a cookie pinned to
  the suffix reaches every colleague's shared routes. A cookie pinned to the
  `.test` host is refused on the peer name. Host-only cookies are the documented
  requirement; proximo rewrites nothing (constraint 4 of the specification).
- **`status` prints both names**, and the documentation recommends the Qualified
  one. A container that lost a Collision keeps only its Qualified peer name.
- **A peer name over 253 octets, or with a label over 63, is a label fault**,
  reported by the watcher like every other.
- **Renaming a machine renames every peer name on it** and needs a new
  intermediate, constrained to the new label.
