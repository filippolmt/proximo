# A route exists and the host does not answer

Reached from [`SKILL.md`](../SKILL.md) when `proximo status` lists the route but
the host fails, 502s, 503s, or the browser rejects the certificate.

## Ask proximo before probing

```sh
proximo doctor
```

It runs every Check on the host — resolver installed, `:80`/`:443` free, CA
trusted, stack running and level with the CLI, DNS answering, routes well formed
— and each failure names the exact Remedy. Start here: a report says where *not*
to look as much as where to look, and the passing Checks narrow the search as
much as a failing one.

Remedies that install, trust, or need a password go to the developer to run.

## Locate the failure between browser and container

Work outward, and stop at the first step that fails — everything after it is a
consequence.

| Probe | A failure here means |
| --- | --- |
| `dig +short <host> @127.0.0.1` | the resolver or the DNS service — `proximo doctor` names which |
| `curl -sSI https://<host>` reports a TLS error | the CA is not trusted by this client, see below |
| status is **404** | no route matched: the host is not the one proximo has, or a `proximo.path` prefix excludes this path |
| status is **502/503** | the route matched and the backend did not answer — see the next section |
| status is the container's own **5xx** | routing is fine and the server failed — read its output, [`transcript.md`](transcript.md) |
| status is the container's own, and the page is wrong | routing is fine; the problem is the page — [`inspection.md`](inspection.md) |

Probe the **qualified host** when the bare one fails: if the qualified host works
and the bare one does not, this is a Collision, not a fault.

## 502 or 503

- **Right after a restart, with a healthcheck declared.** Routing is gated on
  Docker health: a container that declares a healthcheck is routed only while
  `healthy`. The window between "running" and "healthy" is exactly when a
  developer refreshes. Wait, or set `proximo.health=false` to route as soon as
  it is running.
- **On a route under Inspection, on an older stack.** The Inspection hop is a
  stack service; a stack predating it 502s every inspected route. `proximo update`.
- **Otherwise** the backend is not listening on the port proximo is sending to.
  Confirm the port the container actually binds, and set `proximo.port`.

## A Collision

Two containers claiming one host — commonly the same service running from two
worktrees. proximo **reports** a collision and never resolves one: `proximo status`
lists the claimants and which of them serves the contested bare host. A collision
costs a bare host, not a service — every claimant stays reachable at its own
qualified host, and every other host it declared is untouched.

So the fix is a choice, and it is the developer's: stop one claimant, or rename
its host. Meanwhile, use the qualified host.
<https://filippolmt.github.io/proximo/troubleshooting.html#a-host-collision-is-reported>

## The browser distrusts the certificate

`proximo trust` re-adds the local CA to the system store and to Firefox's own
NSS store, which is the usual cause when Firefox alone complains. It needs a
password, so it is the developer's command to type.

A Chrome that trusted the certificate yesterday and not today has usually had its
profile or its NSS store rebuilt; the same command fixes it.
<https://filippolmt.github.io/proximo/cli.html#proximo-trust>

## Nothing above accounts for it

The watcher explains every container it skipped and every label it rejected —
network attach failures, invalid middleware values, malformed hosts. Those
warnings are in the watcher's log, not in `proximo status`.
<https://filippolmt.github.io/proximo/troubleshooting.html#where-to-read-watcher-warnings>

## The labelling checklist

<!-- generated:start source=docs/troubleshooting.md#container-not-routed -->
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
   [A host collision is reported](https://filippolmt.github.io/proximo/troubleshooting.html#a-host-collision-is-reported).
5. **Watcher warnings** — anything else (network attach failures, invalid
   middleware values) is explained in the watcher log: see
   [Where to read watcher warnings](https://filippolmt.github.io/proximo/troubleshooting.html#where-to-read-watcher-warnings).
<!-- generated:end -->
