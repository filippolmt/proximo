# Read what the container said

Reached from [`SKILL.md`](../SKILL.md) when the route answers, the page renders,
and an endpoint fails — a 500, a 502, an API call that comes back wrong.

## What a Transcript is

A **Transcript** is what the container that served a request wrote while that
request was live, quoted verbatim and never interpreted. It is the third part of
an **Exchange**, beside the **Access record** (what the stack saw) and, on
inspected routes, the **Client reports** (what the browser saw).

Three things follow, and each one changes how you read it:

- **Every route has one.** No label is needed, and no browser: Traefik records an
  access log, so any request produces an Exchange. You can provoke one yourself
  with `curl` and read the result.
- **proximo stores none of it.** It is read back from the container's own output
  at the moment it prints, and cut to the window of the Exchange. Nothing is
  retained, so there is nothing to page through and no cursor to keep.
- **The cut is temporal, not causal.** It says *what the container wrote in this
  window*, never *what this request caused*. When proximo reports that other
  requests overlapped, the lines are interleaved and cannot be separated —
  reproduce the problem on its own before drawing a conclusion from them.

## Ask for it

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://<qualified-host>/<path>
proximo errors --json --host <qualified-host> --since 5m
```

The listing carries a capped Transcript per Exchange: both ends, and a declared
count of the lines elided between them. Read that first. When the head and the
tail are not enough to place the failure, ask for the whole thing:

```sh
proximo errors transcript <exchange-id>
```

It prints to stdout. `--since` takes a duration or an absolute RFC 3339 instant,
and identities are derived rather than minted, so the same Exchange keeps the
same id across invocations.

## When there is nothing to quote

proximo never returns an unexplained silence. Each of these is a different
answer, and only two of them are about the project's code:

| What it says | What it means |
| --- | --- |
| *wrote nothing while this request was live* | The container was up and quiet. Look at the Access record's status instead. |
| *has written nothing at all since it started, so it probably logs elsewhere* | The application logs to a file inside the container, or to a collector. Only stdout and stderr can be quoted. Point its logger at stdout. |
| *log driver cannot be read back* | The container runs a driver Docker cannot replay (`syslog`, `fluentd`, `gelf`). |
| *the container that served this request is gone* | It was stopped or replaced, or the address now answers for a container started after the request. proximo refuses to quote whichever container holds the address now. |
| *this stack records no access log* | Version skew, not an absence of errors. The Remedy is `proximo update`, and it is the developer's to run. |

When a Transcript names a replica count — *1 of 3 replicas* — read "it only
happens sometimes" as a fact about **that** container before reading it as a
race: one replica running stale config produces exactly that symptom.

## Credentials

A Transcript is raw application output. **proximo redacts nothing**, because
redacting is interpreting and a redactor that misses is worse than none. It may
carry tokens, connection strings or personal data, and quoting it into your
context sends it to a model API. Say so to the developer before pasting one into
a file, an issue, or a message.
