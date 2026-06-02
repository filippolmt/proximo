# Troubleshooting

[← back to docs index](README.md)

- **`https://<name>.test` does not resolve** — confirm the stack is up
  (`proximo status`) and the resolver is wired: on macOS check
  `/etc/resolver/<tld>`; on Linux check
  `/etc/systemd/resolved.conf.d/proximo-<tld>.conf` and that `systemd-resolved` is
  active (`resolvectl status`). Only `systemd-resolved` is supported in v1.
- **DNS port already in use** — `proximo install` aborts before making changes
  if `127.0.0.1:5354/udp` is taken. (Port `5353` is deliberately not used: macOS
  `mDNSResponder`/Bonjour binds it.) Free the port and retry, or check it with
  `sudo lsof -nP -iUDP:5354`.
- **macOS UDP forwarding** — `127.0.0.1:5354/udp` relies on the Docker VM
  forwarding UDP. It works on current Docker Desktop; if a setup proves
  unreliable, that is the first thing to check.
- **Certificate warnings in Firefox/Chrome** — these use NSS, not the system
  store. `proximo install` adds the CA via `certutil` (installing `nss-tools` if
  needed). Fully restart the browser after install.
- **Gatekeeper "is damaged" (macOS)** — handled by the cask. For a manually
  downloaded binary: `xattr -dr com.apple.quarantine ./proximo`.
