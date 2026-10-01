---
status: accepted
---

# A peer host is answered without rewriting `Host`

A request from a colleague arrives at Traefik carrying a peer name, which is not
under the machine's TLD. Something has to make the route answer it. The tempting
answer is to have the transport rewrite `Host` to the `.test` name at its edge, so
that proximo changes nothing.

**Decision.** proximo writes a **second HTTP router** for a shared route, on the
existing `websecure` entrypoint, matching the route's peer names. It points at the
**same service** as the local router and carries the **same middleware chain**.
`Host` is never rewritten, on any layer.

- **The transport was never a candidate for the job.** A layer-3 mesh delivers
  packets and terminates no HTTP. Rewriting `Host` would need a proxy proximo
  introduced and owned, TLS would terminate outside Traefik, and the application
  would be lied to about the name that was requested — the same lie the
  design refuses for response bodies
  ([constraint 4](../sharing.md#constraints)), one layer down.
- **Same service, same chain.** `proximo.auth`, `proximo.cors`, the custom headers
  and Inspection apply to a colleague's request exactly as to a local one. An
  inspected route's service *is* the Inspection hop, so excluding Inspection from
  the peer router would need a second service for the same container. A colleague
  seeing different behaviour from the same service would mean sharing had stopped
  sharing and started simulating.
- **Peer hosts are a distinct field, never merged into the local hosts.** The
  local host list feeds the router rule, the SANs the local CA signs and the
  Collision detector. A peer name reaching the local CA's certificate yields one
  that is valid and untrusted, silently, until a colleague's browser refuses it.
  Making the separation a property of the type means no fourth call site can
  forget it.
- **No trusted chain, no peer router.** The router is emitted only when the
  machine label, the Peer suffix, the team root and the intermediate are all
  configured. A peer name served under an untrusted chain warns exactly as an
  interception does.
- **proximo stays unaware that a mesh exists.** Traefik already publishes `:443`
  on every interface, the mesh client runs on the host, and Docker Desktop's
  forwarder serves the mesh interface. proximo calls no mesh API and names no
  vendor.
- **HTTP routes only.** An SNI route has no HTTP layer for a peer router to be, and
  a `passthrough` backend cannot present a peer certificate.

What an interactive session gets, measured on the relayed path: `X-Forwarded-Proto:
https` and `X-Forwarded-Host` set to the peer name, so an app that builds URLs from
the request needs no change; a login that holds across navigation; a WebSocket
upgrade proxied as any other, with `Origin` and `Host` agreeing.

## Considered options

**The transport rewrites `Host`.** Rejected for the reasons above
([#78](https://github.com/filippolmt/proximo/issues/78)).

**TLS terminated outside Traefik**, by a proxy or by the transport. Rejected: it is
the same extra edge, and it would split certificate handling between two
components.

**A dedicated entrypoint bound to the mesh address**, so "this request came from a
colleague" would be a property of the listener. Rejected: Traefik runs in a
container and cannot see the host's mesh interface, so it would need a second
published port and a host-side bind.

**A mesh client inside the stack**, or a routing peer advertising the Docker
network. Rejected on ownership: proximo would own mesh enrolment, which belongs to
the person at the machine, and the machine's other mesh traffic would need a
second client anyway.

**A second service that skips Inspection for peer requests.** Rejected: the peer
request would stop being the local one.

## Consequences

- **Local and peer requests are told apart only at the HTTP layer**, by the router
  that matched and the host that arrived. `proximo errors` shows the arrived peer
  name and never attributes a request to a machine or a person; on Docker Desktop
  the source address is not even the colleague's.
- **A session does not follow from `.test` to the peer name**, nor back: they are
  different sites. One login per name.
- **Removing `proximo.share` withdraws the router at the next reconcile**, with no
  draining.
- **A name the machine does not serve gets the nameless default certificate**,
  whether it is an unshared route or an invented name, so a colleague cannot tell
  the two apart. The default is signed by the local CA and names no route.
