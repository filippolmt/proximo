# Every route answers on a qualified host

Two containers claiming `api.test` is the ordinary case in local development —
two worktrees of one repository, or a stale container nobody stopped. proximo's
glossary already said a Collision is *reported, never silently resolved*, but the
watcher drops the loser by lexicographic order, logs it where nobody looks, and
`proximo status` omits it entirely. Every fix for that stood on the same
unanswered question: if proximo may not pick a winner, what does the loser get?

**Decision.** Every route answers on two hosts. The **bare host** (`api.test`) is
the short name a developer writes, and it is the only contested one. The
**qualified host** (`api.shop.test`) inserts the route's Namespace — the
container's Compose project name — before the TLD, is always present, and is
never moved by a Collision. A Collision therefore costs a bare host, never a
service: both claimants stay reachable, and proximo reports the contest instead
of hiding it.

## Considered options

**Refuse to serve a contested bare host at all.** The most literal reading of
"reports, never resolves", and rejected: it turns a forgotten container into a
broken environment, punishing the developer for a leftover.

**Serve the winner and report the loser, with no qualified host.** Honest, but it
leaves the arbitrariness intact — the winner is whoever sorts first, so renaming a
container moves traffic, and the report has no remedy to offer. It is this option
plus the qualified host that makes the report actionable.

**Qualify only on collision** (`api.test` becomes `api.shop.test` when a second
claimant appears). Rejected: a name that materializes during an incident is a name
nobody puts in a README or an `.env`, so nobody relies on it — which was the whole
point.

**A second TLD instead of a namespace** (`api.test` and `api.loc`). Rejected: it
is a second mechanism for what the Namespace already means, and the worse one — a
privileged resolver per TLD, multiplied SANs and routers, and an ambiguous
qualified host. The TLD stays exactly one per machine.

**`api-shop.test` instead of `api.shop.test`.** Rejected for readability, at a
real cost recorded below.

## Consequences

- **Cookie isolation inside a Project changes.** `test` is a single-label TLD, so
  browsers reject `Domain=test` and `api.test` and `db.test` are isolated origins
  today. Under `api.shop.test`, an app serving `shop.test` can set
  `Domain=shop.test` and reach every qualified host of that project. In local
  development a session shared between a project's `web` and `api` is more often
  wanted than not, but it is a change in isolation and was accepted knowingly.
  Choosing `api-shop.test` would have avoided it.
- **The qualified host derives from the declared host, not the container name.**
  Replicas are containers with different names and the same `proximo.hosts`;
  deriving from the name would give them different qualified hosts, diverge
  `replicaKey`, and break round-robin merging for every scaled service. Deriving
  from the declared host also means containers of different Projects can no longer
  merge as replicas — a merge across projects is always an accident, so making it
  impossible by construction is the point, not a side effect.
- **Collision resolution moves from the container to the host.** A container losing
  one host keeps the others, which it does not today.
- **proximo stands down before an explicit declaration.** Where a generated
  qualified host would collide with a host a developer wrote by hand, or where a
  native `traefik.*` rule already claims a host, proximo withdraws its own router
  and reports the withdrawal. This is not arbitration between two developers: one
  of the two claims is proximo's own.
- **A container outside a Compose project gets no qualified host**, and remains the
  one case with no safety net. The remedy is real and short: put it in a Compose
  project.
- **The safe name derives from the Namespace** (`shop-api`) rather than from a
  container-ID suffix, so a file in the certificate directory can be traced back to
  a container by reading it.
- **Cost at the routing layer is close to nothing.** `syncCerts` issues one
  certificate per container with its hosts as SANs, and `renderRouter` emits one
  router with one rule; the qualified host is one more SAN and one more term. The
  visible cost is a one-time reissue of every certificate on the update that
  introduces it — signed by the already-trusted CA, so no browser re-approval.
- **Always on, with no opt-out.** A stable name behind a flag is not stable: if it
  can be switched off, nothing in a README may depend on it.
