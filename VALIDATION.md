# Validation findings

This records the validation for the `add-local-dev-proxy` change (tasks 7.1–7.3).

Some checks are host-specific (real macOS / Ubuntu host, `sudo`, a desktop
browser) and **cannot** run in the Linux CI container used during
implementation. Those are documented with an exact procedure and expected
result; everything that can be verified automatically was run and passed.

## Automated checks (executed, passing)

| Check | Result |
| --- | --- |
| `go build ./...` | ✅ |
| `go vet ./...` | ✅ no issues |
| `go test ./...` | ✅ (DNS wildcard answer, TLS CA reuse + chain verify, name normalization) |
| Cross-compile `darwin,linux × amd64,arm64`, `CGO_ENABLED=0` | ✅ all 4 (proximo + dnsserver + watcher) |
| `goreleaser check` | ✅ config valid, no deprecations |
| `docker compose config` on the embedded stack | ✅ valid; `defaultRule` renders to ``Host(`{{ normalize .Name }}.test`)``; DNS published on `127.0.0.1:5354/udp`, Traefik on `:80`/`:443` |

The DNS server is exercised over a real loopback UDP socket by
`internal/dns/server_test.go` (a `miekg/dns` client `Exchange` against the
running server), confirming `*.test → 127.0.0.1`. The TLS leaf is verified to
chain to the local CA for `web.test` in `internal/tls/tls_test.go`.

## 7.1 — macOS `127.0.0.1:5354/udp` forwarding

**Not executable here** (Linux container, not macOS; no Docker Desktop VM).

Procedure on macOS:

```sh
proximo install
# Docker Desktop:
dscacheutil -flushcache
dig @127.0.0.1 -p 5354 whoami.test            # expect ANSWER 127.0.0.1
scutil --dns | grep -A3 "domain.*test"        # /etc/resolver/test in effect
```

Expected: the query returns `127.0.0.1`, and `ping whoami.test` resolves to
loopback. Spot-check Colima/Lima the same way and record any UDP-forwarding
differences. **Fallback if UDP forwarding is unreliable** (per design): a
host-side DNS shim (LaunchAgent) or TCP-DNS; the host-resolver contract
(`127.0.0.1:5354`) stays the same.

## 7.2 — End-to-end: resolve + route + trusted HTTPS

**Layer checks executed here** (build, DNS unit, TLS chain unit, compose/Traefik
config render) — all green. The **live browser** leg needs a host with the
resolver wired and the CA trusted, so it runs on macOS/Ubuntu:

```sh
proximo install
cd examples/whoami && docker compose up -d
proximo status                       # lists whoami -> https://whoami.test
open https://whoami.test           # macOS  (xdg-open on Linux)
```

Expected: the page loads over HTTPS with **no certificate warning** (system +
NSS trust), served by Traefik and routed to the `whoami` container with no
published ports and no Traefik labels. A headless equivalent of the trust + route
legs:

```sh
curl -v --cacert "$(proximo_tls_dir)/ca.pem" https://whoami.test   # 200, verified chain
```

## 7.3 — `install` → `uninstall` restores the host

**Not executable here** (`sudo` unavailable; `systemd-resolved` inactive in the
container). Procedure on macOS and Ubuntu/Debian:

```sh
# capture prior state
ls /etc/resolver 2>/dev/null; security find-certificate -c "proximo local CA" 2>/dev/null   # macOS
ls /etc/systemd/resolved.conf.d 2>/dev/null; trust list 2>/dev/null | grep -i "proximo"   # linux

proximo install
proximo uninstall

# verify restoration
test ! -f /etc/resolver/test                                  # macOS resolver removed
test ! -f /etc/systemd/resolved.conf.d/proximo-test.conf        # linux drop-in removed
```

Reversibility matrix (what `uninstall` undoes):

| Installed by `install` | Removed by `uninstall` |
| --- | --- |
| macOS `/etc/resolver/<tld>` | ✅ |
| Linux `systemd-resolved` drop-in | ✅ |
| CA in system trust store | ✅ |
| CA in NSS (Firefox/Chrome) | ✅ |
| Local CA/cert files | ✅ (`tls.Purge`) |
| Stack containers | ✅ (`compose down`) |

## Note: stack images and module publication

The DNS and watcher images build with `go install github.com/filippolmt/proximo/cmd/...@<ref>`.
`<ref>` is the release tag for released binaries, and `main` for dev builds — so
the repository must be pushed (public) for `proximo up` to build those images.
Released versions resolve their own tag.
