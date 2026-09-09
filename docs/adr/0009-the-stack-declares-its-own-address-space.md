# The stack declares its own address space

The stack's compose asset declared no `networks:` block, so Docker assigned
`proximo_default` a subnet from its default address pool
(`172.17.0.0/16`–`172.31.0.0/16`). Which `/16` the stack landed on was neither
predictable nor visible: it depended on what else the daemon had already
allocated.

That is a routing decision taken by whichever component allocates last. When the
host already routes a subnet inside that pool — a VPN tunnel is the ordinary case
— the stack network can shadow it. From inside a container, packets addressed to
that subnet match the connected route of proximo's own bridge and never leave the
host: the name resolves, then the connection hangs until it times out. On Docker
Desktop the symptom is asymmetric, and that is what makes it expensive: the
host's own terminal is unaffected, because the bridges live inside the Linux VM
and never appear in the host routing table. Only containers lose the route, and
nothing in the failure names proximo.

Remote Docker networks reached over a tunnel are the most likely victims,
because they were assigned from the same default pool on the other side.

**Decision.** The address space the **Stack network** occupies is proximo's to
declare, not Docker's to allocate. The compose asset declares an explicit
subnet, the default is `10.101.0.0/24`, and the developer can move it
(`proximo config subnet <cidr>`, persisted in `config.json` beside the TLD).

Four things follow, and they are the substance of the decision:

- **The default is a `/24`, not a `/16`.** The stack runs a handful of
  containers and does not need 65,534 addresses. Claiming a whole `/16` would
  only maximise the chance of overlapping something else on the host or across a
  tunnel. `10.101.0.0/24` is outside Docker's default pool and outside the ranges
  home LANs habitually use (`192.168.0.0/24`, `192.168.1.0/24`, `10.0.0.0/24`).
- **The default applies to installations that already exist.** A converge that
  finds `proximo_default` on a subnet other than the declared one recreates the
  network (`down` then `up`) and says so in one line. Docker never changes the
  subnet of an existing network, so inheriting it silently would leave every
  affected host exactly as broken after the upgrade as before it — and the
  developer who has the bug is precisely the one who does not know there is a
  subnet to configure. The blast radius is bounded: no Project container is ever
  on the stack network (the watcher joins the Project's networks to reach it), so
  what restarts is the stack, and what is lost is in-memory Exchanges and
  Incidents, which are not durable by design (ADR 0006, ADR 0007).
- **The rejected pool is a static range.** `config subnet` refuses
  `172.17.0.0/16`–`172.31.0.0/16` as a literal, without asking the daemon what
  its `default-address-pools` currently are. A rejection derived from host
  configuration would make a value accepted yesterday invalid today, and would
  leave proximo owing an answer about a config already in use. The rare inverse
  case — a daemon whose pool was moved onto the space proximo declares — is not
  silent either: Docker refuses the network and the converge reports its line
  verbatim.
- **proximo does not look for the collision.** It never reads the host routing
  table to decide whether the declared space overlaps a route. The Check it does
  report compares the *declared* subnet with the *actual* one, which is a
  question about Docker and needs no per-platform routing knowledge.

## Considered options

**Keep the pool allocation, document the failure.** A troubleshooting entry
costs nothing and changes nothing: the developer still has to discover that a
hung connection from inside a container is a routing overlap, and the address
that collides is different on every host and every daemon restart. Documentation
is the right home for the symptom, not for the cause.

**Declare an explicit `/16`.** Symmetrical with what Docker was doing, and
wrong for the same reason: the stack needs a few addresses and a `/16` takes
65,534 out of contention on the host and across every tunnel it reaches.

**Read the daemon's pool and pick a free subnet automatically.** Attractive
until the second run: the chosen subnet must then be remembered or re-derived,
and re-derivation makes the stack's address space change under a developer who
changed nothing. It also cannot see the routes that matter, which live in the
host's routing table (or the tunnel's `AllowedIPs`), not in Docker's pool.

**Declare it, but only for new installations.** Leaves the fix out of reach of
everyone already affected, and makes the stack's address space depend on when
proximo was first installed.

**Detect the overlap against the host routing table.** A Check that reads
`ip route` or `netstat -rn` would name the real problem instead of its proxy. It
is also per-platform, and it belongs to a decision about what proximo may
conclude from the host's configuration, which this one does not settle. Recorded
here as a non-goal, not as a rejected shape.

**Dual-stack the network (IPv6 alongside IPv4).** The reported failure is an
IPv4 route collision. Adding IPv6 would mean choosing a ULA prefix on the
developer's behalf with no symptom asking for it.

## Consequences

The stack's address space is now a configured value with a default, so it is
something `status` can state and `doctor` can dispute: a Check reports the
network's actual subnet — always, not only when it diverges — and its Remedy is
`proximo up` first (the ordinary case is an inherited network the converge will
recreate) and `proximo config subnet <cidr>` second (the declared space itself
collides).

Upgrading proximo can restart the stack once, on hosts whose network came from
the pool. It is announced, not silent, and no Project is touched.

A host whose firewall rules or manual routes name the old, pool-assigned range
has to be updated by hand. This is the price of the previous behaviour, not of
this one: those rules were written against an address proximo never promised.

`config subnet` validates a single CIDR — RFC 1918, prefix between `/30` and
`/16`, outside the static Docker pool — normalises host bits (`10.101.0.5/24`
becomes `10.101.0.0/24`) and prints what it saved. It converges first and
persists only on success, so a valid-but-impossible subnet leaves the config
untouched and the stack on the network it already had. With the stack down there
is nothing to converge: the value is saved and takes effect at the next
`proximo up`.
