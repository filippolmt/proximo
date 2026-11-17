# Plain TCP and UDP routing

proximo routes HTTP(S) and TLS-with-SNI TCP. It does not route plaintext TCP or
UDP, and it does not give each host its own loopback address to make them
routable. A port that carries either is published with `ports:` and reached
through the free `*.test` DNS name.

## What already exists

Two parts of the original request are built:

- **TCP over TLS** routed by SNI on the shared `:443`, terminated by proximo or
  passed through (`proximo.tcp.port`, `proximo.tcp.tls`):
  [docs/routing.md — proximo.tcp.port](../docs/routing.md#proximotcpport--route-tcp-services-by-name-sni).
- **Round-robin across replicas** that declare the same host and port, HTTP or
  TCP: [docs/routing.md — Round-robin across replicas](../docs/routing.md#round-robin-across-replicas).

## Why the rest is out of scope

Plaintext TCP and UDP carry no hostname: no `Host` header, no SNI. Every name
under the TLD resolves to `127.0.0.1`, so a connection to `app.test:5027` cannot
say which stack it meant. The only way to route it is to make the *address* the
key: one loopback IP per host, a DNS answer per IP, and an entrypoint per
IP and port. That is a different system:

- DNS answers become an allocation that must stay stable across restarts and
  never collide with an address something else holds.
- On macOS each address needs an `lo0` alias, a privileged change to the host
  that proximo would own, repair, and remove on `uninstall`.
- Traefik would bind one entrypoint per (address, port, protocol), declared
  statically, so a new L4 port means rewriting the static configuration and
  restarting the proxy, not a watcher reconcile.
- [ADR 0014](../docs/adr/0014-the-stack-listens-on-the-loopback.md) keeps the
  stack listening on `127.0.0.1` alone; a range of addresses widens that surface.

The cost lands on every user for a case that `ports:` already covers. Parallel
stacks that clash on a published port can pick different host ports.

## Prior requests

- #42: "Support L4 (TCP/UDP) routing and multi-backend load balancing"
