---
name: proximo
description: proximo makes local Docker containers reachable at https://<name>.test with trusted HTTPS and local DNS. Use when exposing a container through proximo.* labels, when a .test host does not answer or returns 502/503, when a browser distrusts a local certificate, when a page loads and then breaks in the browser, when a request through a .test host fails and you need what the container itself wrote, or when the developer says something is not progressing — a queue not draining, a worker or job that keeps dying — and no page is broken.
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
*nothing is wrong*. Under `--service` the listing then prints the **readings**
(running since when, healthcheck, restarts, when its output last moved) and
deliberately draws no conclusion: identical readings come from a consumer with
nothing to do and one that is stuck. Report the readings as readings, and read the
output for the window before concluding anything:
`proximo errors transcript --service <service> --since 30m`. To make a stuck
worker visible next time, propose a healthcheck that fails when it stops
advancing — Docker then says *unhealthy*, which is an Incident.

**Ask on the qualified host.** Every route answers on two names, and only the
qualified one stays put: a Collision can move the bare host to another
container. Put the qualified host in `--host` and in `curl`.
