---
name: proximo
description: proximo makes local Docker containers reachable at https://<name>.test with trusted HTTPS and local DNS. Use when exposing a container through proximo.* labels or giving a service a local hostname, when calling or testing a containerized service (curl, wget, a test suite), when a .test host does not answer or returns 502/503, when a browser distrusts a local certificate, when a page loads and then breaks in the browser, when a request through a .test host fails and you need what the container itself wrote, when the developer says something is not progressing — a queue not draining, a worker or job that keeps dying — and no page is broken, or when sharing a route with a colleague over the team's mesh (proximo.share) or a colleague reports a shared link that times out, lands on .test, loses its login, or fails at the certificate.
metadata:
  short-description: Expose a container at https://<name>.test, and diagnose one that is broken
---

# proximo

A container carries `proximo.*` labels; proximo gives it a name, local DNS, a
trusted certificate, and a `Host`-based route. Reference:
<https://filippolmt.github.io/proximo/>.

## Before anything else

Run `proximo version`.

- **Not on PATH** — proximo is not in play here. Say so and stop.
- **`.proximo-skill.json` sits beside this file and its version differs** — run
  `proximo skill install` (add `--scope global` when this copy is under the
  developer's home directory rather than in the repository), then ask the
  developer to restart the session so the refreshed skill loads.
- **No `.proximo-skill.json`** — this copy came from a marketplace rather than
  from the CLI, so nothing keeps it level with the installed binary. Confirm
  anything surprising against `proximo --help` before acting on it.

## Triage

`proximo status` is an inventory of routes, and it never prints a Remedy. Run it
first: what it shows picks the branch.

| `proximo status` shows | Read |
| --- | --- |
| No route for the container, or a route flagged with a warning | [`references/expose.md`](references/expose.md) |
| A route, but the host does not answer, answers 502/503, or the browser distrusts the certificate | [`references/routing.md`](references/routing.md) |
| A route, the host answers, and the page itself is wrong | [`references/inspection.md`](references/inspection.md) |
| A route, the host answers, and a request fails on the server — a 500, or an API call that comes back wrong | [`references/transcript.md`](references/transcript.md) |
| Nothing is visibly broken, and the developer says something is **not progressing** — a queue not draining, a job that never finishes, a worker that keeps dying | [`references/transcript.md`](references/transcript.md) — ask for **Incidents** |
| A route carries `proximo.share`, or a **colleague** reports a shared link that times out, lands on a `.test` address, loses its login, or fails at the certificate | The `PEER` column says whether the route is served on its peer names; then [sharing](https://filippolmt.github.io/proximo/sharing.html) and the matching section of [troubleshooting](https://filippolmt.github.io/proximo/troubleshooting.html#a-shared-link-does-not-resolve-or-times-out). `proximo doctor` checks this machine's side (`peer-intermediate`, `peer-routes`, `peer-dns`, `mesh`); nothing here can see the colleague's |

When the developer's account does not settle which row it is, ask the host
itself and let the status code decide:

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://<host>
```

## Rules, in every branch

**A Remedy that changes the host is the developer's to run.** `proximo doctor`
reports Checks, and every failed Check carries a Remedy — the exact command.
Hand over the ones that install, trust, or ask for a password: proximo's contract
is that every mutation of a machine stays a command its owner typed. Run the
read-only commands yourself, and say which one you are running.

**`proximo up` empties the Exchange buffer.** Exchanges are held in memory and
bounded, so restarting the stack discards every Client report recorded before it
— including the restart a developer runs to pick up a label change. Collect what
you need before restarting, and reproduce the problem after.

**No Incident does not mean no problem.** `proximo errors` reports what the
*runtime* declared about a container — an exit, a restart, an OOM kill — and
never what a container wrote. A worker that is alive and blocked on a slow query
declares nothing, so an empty listing means *proximo saw nothing happen*, never
*nothing is wrong*. Under `--service` the listing always ends with the
**readings** — one per running container (running since when, healthcheck,
restarts, when its output last moved) — and deliberately draws no conclusion:
identical readings come from a consumer with nothing to do and one that is stuck.
A scaled service gives you one reading per replica; do not generalise one
replica's reading to the service. Report the readings as readings, and read the
output for the window before concluding anything:
`proximo errors transcript --service <service> --since 30m`. To make a stuck
worker visible next time, propose a healthcheck that fails when it stops
advancing — Docker then says *unhealthy*, and a healthcheck that was passing and
stopped is an Incident. It has to pass once first — a check that never passed
declares nothing when it fails — so propose a `start_period` with it, which keeps
a slow first job from being reported unhealthy on the way there.

**A call to a local container goes through its route.** A request that checks a
service running in a local container — `curl`, `wget`, a test suite, a script —
goes to `https://<qualified host>`, resolved by
proximo's DNS and verified against its CA: the path the developer's browser takes,
so the result covers the name, the certificate, the router and the health gate.
Read the host from `proximo status --json`. A name that does not resolve or a
certificate that is refused is itself the finding: run `proximo doctor`, read
[`references/routing.md`](references/routing.md), and keep the call unchanged.
`localhost:<port>`, the container's IP, `-k`, `--resolve` and `/etc/hosts` each
answer a different question and hide the one you were asked. Any other host —
`google.com`, a staging API — is outside proximo: call it as it is.

**A hostname is a proximo label.** When a service needs a name, declare it with
`proximo.hosts` under the configured TLD (`proximo config tld`), or
`proximo.tcp.port` for a TCP service: proximo issues the DNS record, the
certificate and the route together, and it owns the local zone. The host
resolver, `/etc/hosts` and DNS containers stay as they are. How:
[`references/expose.md`](references/expose.md).

**Ask the binary.** For anything about proximo — routes and their names, the CA
(`proximo config ca-path`), the inventory (`proximo config inventory-dir`), what a
container wrote (`proximo errors`) — the `proximo` CLI is the interface, and
`proximo --help` lists it. The files under `~/.proximo` and the stack's
containers are its implementation. The first rule still holds: a command that
changes the host is the developer's to run.

**Ask on the qualified host.** Every route answers on two names, and only the
qualified one stays put: a Collision can move the bare host to another
container. Put the qualified host in `--host` and in `curl`, and read it from
`proximo status --json` (`qualified`) rather than out of the table. A tool that
must follow routes without running the CLI — a dev container's hosts sync — reads
the same document from `routes.json` in `proximo config inventory-dir`.
