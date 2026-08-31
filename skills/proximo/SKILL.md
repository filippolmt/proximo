---
name: proximo
description: proximo makes local Docker containers reachable at https://<name>.test with trusted HTTPS and local DNS. Use when exposing a container through proximo.* labels, when a .test host does not answer or returns 502/503, when a browser distrusts a local certificate, or when a page loads and then breaks in the browser.
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
| A route, the page renders, and an endpoint fails — a 500, a 502, an API call that comes back wrong | [`references/transcript.md`](references/transcript.md) |

When the developer's account does not settle which row it is, ask the host
itself and let the status code decide:

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://<host>
```

## Four rules, in every branch

**A Remedy that changes the host is the developer's to run.** `proximo doctor`
reports Checks, and every failed Check carries a Remedy — the exact command.
Hand over the ones that install, trust, or ask for a password: proximo's contract
is that every mutation of a machine stays a command its owner typed. Run the
read-only commands yourself, and say which one you are running.

**`proximo up` empties the Exchange buffer.** Exchanges are held in memory and
bounded, so restarting the stack discards every Client report recorded before it
— including the restart a developer runs to pick up a label change. Collect what
you need before restarting, and reproduce the problem after.

**Ask on the qualified host, never the bare one.** Every route answers on both,
but the bare host is the one a collision can move to another container. The
qualified host is the name that stays put, so it is the one to put in `--host`
and in `curl`.

**Read the capped transcript before asking for the whole one.** The inline quote
carries both ends of the container's output and a declared count of what was
elided between them; that is usually enough to place the failure. Ask for the
whole transcript once you know where to look — and remember it is raw
application output, quoted with no redaction, so it may carry credentials.
