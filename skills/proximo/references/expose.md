# Get a container routed

Reached from [`SKILL.md`](../SKILL.md) when `proximo status` shows no route for
the container, or shows one flagged with a warning.

## Steps

1. **Find where the container is declared.** A Compose service is the normal
   case, and the Namespace that gives the container its qualified host comes from
   the Compose project name — so labels go on the service, not on a
   `docker run` line, whenever there is a Compose file to put them in.
2. **Pick the host.** One label opts the container in:
   `proximo.hosts=<name>.test`. Use the TLD the machine is actually configured
   for — `proximo status` prints it, and `proximo config tld` changes it.
3. **Settle the port.** Omit `proximo.port` when the image `EXPOSE`s exactly one
   port. When it exposes none or several, proximo skips the container and
   `proximo status` flags it `⚠ set proximo.port`; add the label explicitly.
4. **Apply and confirm.** `docker compose up -d`, then `proximo status` — the
   route appears with both its hosts.
5. **Verify against the qualified host, not the bare one.** The bare host is the
   only name that can be contested; the qualified host always belongs to this
   container.

   ```sh
   curl -sS -o /dev/null -w '%{http_code}\n' https://<name>.<project>.test
   ```

Done when the qualified host returns the container's own response over HTTPS
with no certificate warning. A 502 or 503 at this point is a routing question,
not a labelling one: [`routing.md`](routing.md).

## Choosing among the optional labels

- The container declares a Docker healthcheck and the developer wants it routed
  before it is healthy → `proximo.health=false`.
- Several containers must share one host on different prefixes →
  `proximo.path=/api`, plus `proximo.path.strip=true` when the backend expects
  the prefix already removed.
- A plain `http://` visit should land on HTTPS → `proximo.redirect=true`. It is
  opt-in and answers 302.
- The service is not HTTP — a database, gRPC, MQTT → `proximo.tcp.port`. SNI
  routes by host alone, so give each TCP service its own host.
- The developer wants to see what the browser reports on this page →
  `proximo.inspect=true`, then [`inspection.md`](inspection.md).

## The label contract

<!-- generated:start source=docs/routing.md#the-proximo-labels -->
| Label | Required | Default | Meaning |
| --- | --- | --- | --- |
| `proximo.hosts` | **yes** | — | Comma-separated hostname(s) under the configured TLD. **Its presence opts the container in.** |
| `proximo.port` | no | auto-detected | Backend port. Omit when the image `EXPOSE`s exactly one port. |
| `proximo.enable` | no | `true` | Opt-out switch. Set to `false`/`0`/`no` to park the container. |
| `proximo.redirect` | no | `false` | Opt in to an HTTP→HTTPS redirect for the container's hosts. Truthy: `true`/`1`/`yes`. |
| `proximo.path` | no | — | Path **prefix** (must start with `/`) scoping the routes, so several containers can share one host on distinct prefixes. Invalid values skip the container. |
| `proximo.path.strip` | no | `false` | Strip the matched prefix before the backend (so `/api/users` arrives as `/users`). Truthy: `true`/`1`/`yes`. |
| `proximo.health` | no | `true` | Gate routing on the container's Docker healthcheck: a container that declares one is routed only while `healthy`. Set to `false`/`0`/`no` to route as soon as it is running. No effect on containers without a healthcheck. |
| `proximo.auth` | no | — | Require HTTP basic auth. Comma-separated `user:password` pairs; plaintext passwords are hashed on disk. A pair missing `:` is skipped with a warning. |
| `proximo.cors` | no | — | Add CORS response headers. `true` for permissive CORS, or a comma-separated allowed-origin list. A blank value is skipped with a warning. |
| `proximo.header.<Name>` | no | — | Add a custom response header `<Name>: <value>`. Repeatable; an invalid header name is skipped with a warning. |
| `proximo.inspect` | no | `false` | Serve the container's HTTP routes through the Inspection hop, which injects a reporting agent into HTML responses and records what the browser reports. Truthy: `true`/`1`/`yes`. HTTP-only; ignored on TCP routes and on replica sets. See [Inspection](https://filippolmt.github.io/proximo/observability.html#inspection--what-the-browser-saw). |
| `proximo.tcp.port` | no | — | Route the container's hosts over **TCP-over-TLS by SNI** on the given backend port (for DBs, gRPC, MQTT, HTTPS backends). Invalid values are skipped with a warning. |
| `proximo.tcp.ports` | no | — | Comma-separated form of `proximo.tcp.port`. Note: SNI routes by host only, so several ports on one host cannot be told apart — give each TCP service its own host. |
| `proximo.tcp.tls` | no | `terminate` | TLS mode for TCP routes: `terminate` (proxy terminates with the per-host proximo cert, forwards plaintext) or `passthrough` (proxy routes the raw TLS stream by SNI; the backend terminates). |
<!-- generated:end -->

Full contract, with examples:
<https://filippolmt.github.io/proximo/routing.html#the-proximo-labels>.

## Every route answers on two hosts

| | Example | Contested? |
| --- | --- | --- |
| **Bare host** — what the developer declared | `api.test` | yes: one container serves it |
| **Qualified host** — with the Namespace inserted | `api.shop.test` | never |

The Namespace is the Compose project name, with `_` rewritten to `-`, derived
from the *declared host* rather than the container name — so every replica of a
scaled service shares it. A container outside a Compose project has no namespace,
and therefore no qualified host.

Both hosts go into one router rule and one certificate. Prefer the qualified host
in anything you write down or hand back: it survives a Collision, and the bare
one may not.
<https://filippolmt.github.io/proximo/routing.html#the-two-hosts-every-route-gets>
