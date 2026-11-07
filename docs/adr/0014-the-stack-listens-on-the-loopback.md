---
status: accepted
---

# The stack listens on the loopback

Traefik published `:80` and `:443` on every interface, while every other stack
service was published on `127.0.0.1`
([#122](https://github.com/filippolmt/proximo/issues/122)). A route has no
authentication unless it opts into `proximo.auth`, and a local-dev app usually
has none. The point of proximo is that a container stops publishing ports of
its own, so the developer stops thinking about exposure — and the proxy made it
broader than the `ports:` it replaced: every routed container answered, by
`Host`, to anyone on the same network. Nothing a container declares could
narrow it.

**Decision.** Traefik is published on `127.0.0.1:80` and `127.0.0.1:443` only.
A shared route reaches it through the **peer relay**: a Compose service of its
own, beside the peer DNS service, published only on `<address>:443`, that copies
TCP to Traefik over the Stack network.

1. **Loopback is the default, not an option.** `.test` resolves only to
   `127.0.0.1` (`A` records, no `AAAA`), so nothing local needs another address.
   There is no `bind` setting: its one proposed use — opening a page on a phone
   on the same Wi-Fi — does not work anyway, because the phone neither resolves
   `.test` nor trusts the local CA.
2. **The relay copies bytes, nothing else.** TLS terminates at Traefik, so the
   SNI a colleague's browser sends arrives intact and the peer router of
   [ADR 0012](0012-a-peer-host-is-answered-without-rewriting-host.md) is
   unchanged. No PROXY protocol: nothing in proximo reads the client address,
   and Docker Desktop already rewrites it.
3. **It lives and fails with the peer DNS service.** Same profile, same
   if-and-only-if (machine, Peer suffix and address configured), started after
   the core converge, restarted by the watcher on its 30-second tick — for the
   reason [ADR 0013](0013-proximo-answers-the-peer-subtree.md) gives: a publish
   on an address no interface holds leaves the container `created`.
4. **Only `:443`.** The mesh admits TCP 443 and 5354 between peers, nothing else
   ([sharing.md — Constraints](../sharing.md#constraints)).

## Considered options

**Keep every interface, add an opt-out.** It breaks no existing setup, which is
its whole argument. Rejected: the developer who needs the opt-out is exactly the
one who does not know they are exposed.

**A `bind` setting, defaulting to the loopback.** Rejected for now: no use case
it would serve works, and the one non-loopback consumer that does — sharing —
already has its own address.

**Add `<address>:443` to Traefik's own publishes.** Rejected: whenever the mesh
is down at start, Traefik would stay `created` and `.test` would go down with it.

**Keep every interface on a machine that shares.** Rejected: a sharing machine
would put every route — shared or not — on the LAN, the exposure ADR 0013
refused for the DNS listener.

## Consequences

- **Breaking for anyone who reached routes from another host** without the mesh
  (a VM, another machine with an `/etc/hosts` entry). The converge that moves a
  stack off the wildcard says so in one line.
- **`proximo status` says where routed traffic is accepted**, as Docker reports
  it, and warns when that is an unspecified address.
- **A shared request's client address is the relay's** in Traefik's access log.
- **`doctor` gains `peer-relay`**, a TLS handshake on `<address>:443`. It inherits
  the macOS limit of `peer-dns`: a machine that cannot reach its own mesh address
  ([#144](https://github.com/filippolmt/proximo/issues/144)) fails it while
  colleagues are served.
- **The router a shared route gets on `web`** for `proximo.redirect` is reached by
  nothing over the mesh; whether to remove it is a separate question.
