---
status: accepted
---

# proximo's DNS server answers the peer subtree

A colleague's resolver has to turn `api.shop.<machine>.<suffix>` into the sharing
machine's address on the mesh. A mesh nameserver configuration *forwards*; it
hosts no records. So something has to hold the answer, and it has to answer both
peer names of every route — at whatever depth the declared host has — without
learning in advance which routes exist.

**Decision.** proximo's DNS server answers every name under `<machine>.<suffix>`
with a **configured address**, and the mesh routes each publishing machine's
subtree to that address on port 5354.

1. **A separate, peer-only listener.** `A` queries under the subtree, at any
   depth, get the configured address; other types get NOERROR with no records;
   every name outside the subtree gets **`REFUSED`** — no upstream forwarding and
   no `.test`, so a publishing machine is never a recursive resolver for the mesh.
   `REFUSED` rather than `NXDOMAIN`, because the listener is not authoritative
   outside its subtree. The loopback handler does not change.
2. **Its own Compose service, published only on `<address>:5354`, UDP and TCP.**
   Docker Desktop rewrites the source address of a published port, so the handlers
   can only be kept apart by container port. A publish on an address no interface
   holds leaves the container `created` and the restart policy never retries it: in
   the `dns` service that would take `.test` down whenever the mesh is down at
   start. The watcher retries starting this service, because the mesh client and
   Docker Desktop both start at login in no fixed order.
3. **It exists if and only if** the machine label, the Peer suffix and the address
   are all configured. Otherwise the materialised stack is identical to today's.

This puts new code in the one component whose failure takes down every local
route on the machine, and that is why it carries two conditions as part of the
decision, not as advice to the implementer:

- **Inert when unconfigured.** A machine that has not opted in executes no new
  behaviour at all.
- **`.test` asserted under both states.** The build tests the `.test` answers with
  the peer values set and unset, rather than a reviewer taking it on trust.

Nothing about the mesh reaches proximo beyond an address it is told: it calls no
API, discovers nothing and names no vendor.

## Considered options

**A hosted zone with one wildcard record per machine**, distributed by the mesh's
control plane ([#102](https://github.com/filippolmt/proximo/issues/102)). It
changes proximo not at all, which is its strongest argument and is why the two
conditions above exist. Rejected on measurement: the zone implementation
synthesises one label only, so the Bare peer name resolved and the Qualified one
did not. Serving the Qualified form would need a wildcard per project per machine,
written by proximo into the control plane as projects come and go — a credential
on every laptop. It also left a central zone answering a stale address for a
machine that is off, and depended on an invariant about the zone's contents whose
violation misroutes a peer name silently.

**A single nameserver group for the whole account**, forwarding the suffix to one
central resolver that holds the records. Rejected: a new hosted component, and
state about developers' machines living somewhere other than those machines. "One
object, configured once" does not survive under any mechanism that answers both
names; the honest statement is per machine either way.

**The same handler on a second publish**, so the mesh gets what the loopback gets.
Rejected: the machine would answer every `.test` name to colleagues and forward
everything else upstream for them.

**Publishing on every interface.** Rejected: Docker refuses a second publish of the
loopback port, so it would mean changing the `.test` publish, and it would expose
the listener on the LAN.

## Consequences

- **Authority sits where the knowledge is.** A machine's routes are known only to
  it, and answered only by it, at any depth, with nothing to synthesise.
- **A machine that is off stops answering for its names**, instead of something
  else answering with a stale address.
- **The operator's order matters.** proximo answers on the port first; the mesh's
  routing of the subtree is created after `doctor`'s `peer-dns` passes. Created
  first, it would exist with the right flags and resolve nothing.
- **`doctor` can state that peer names resolve** without reaching another machine:
  `peer-dns` asks the listener directly, `mesh` asks the system resolver, and the
  pair splits "proximo does not answer" from "the mesh does not route" — the same
  pair `dns-server` and `dns-resolver` form for `.test`. Neither of those two
  changes, configured or not.
- **The mesh's access policy admits port 5354, UDP and TCP, to the publishing
  machines**, beside 443.
- **`up` never fails for the peer listener.** When it cannot start, `up` warns,
  names the cause and exits 0: `.test` works.
- Whether the listener answers again after the mesh drops and returns while it
  runs is not measured; it is to be verified on real machines
  ([#131](https://github.com/filippolmt/proximo/issues/131)).
