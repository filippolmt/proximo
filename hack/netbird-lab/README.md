# The NetBird lab

[Verify peer sharing on real machines](https://github.com/filippolmt/proximo/issues/141) lists
acceptance items that only two machines and a real mesh can show. This directory is the
runbook for taking them by hand over a self-hosted NetBird, and the script that enrols the
sharing machine. It is a measurement harness, not part of proximo.

Every value below is a placeholder (constraint 5 of [docs/sharing.md](../../docs/sharing.md#constraints)).

## The machines

| Role | What it runs | Placeholder |
| --- | --- | --- |
| **A**, shares | macOS, Docker, proximo at the release under test, NetBird client | mesh address `<A-ip>` |
| **B**, colleague | Ubuntu, NetBird client, Chrome, the proximo binary — **no Docker needed** | mesh address `<B-ip>` |
| **S**, mesh | an existing self-hosted NetBird (management, signal, relay) | `<management-url>` |

Both clients are enrolled through interactive login, not a setup key
(requirement 4 of [What the mesh must provide](../../docs/sharing.md#what-the-mesh-must-provide)).
Read `<A-ip>` and `<B-ip>` from `netbird status --detail`.

**The relay.** When A and B sit on the same LAN, NetBird connects them peer to peer and the
relayed path (constraint 10) is never exercised. `netbird status --detail` shows the
connection type of each peer. For at least one pass of the items below, put B on a phone
hotspot and confirm B's row for A reads `Relayed`.

## 1. Enrol A

On A, from the repository checkout:

```sh
hack/netbird-lab/ceremony.sh studio-01 mesh.internal <A-ip>
```

It mints a throwaway team root under `mesh.internal` with
[`tools/team-ca/team-ca.sh`](../../tools/team-ca/team-ca.sh), sets `peer-suffix`, `machine`,
`address` and `team-root`, signs the CSR into an intermediate, removes the plaintext root
key, runs `proximo trust` and `proximo up`, then `proximo doctor`. Expected:
`peer-intermediate`, `peer-routes` and `peer-dns` pass; `mesh` fails, because nothing routes
the subtree yet.

On macOS with NetBird, `peer-dns` fails with an i/o timeout although the service works: the
machine cannot reach its own mesh address ([#144](https://github.com/filippolmt/proximo/issues/144)).
Query it from B instead, and treat that answer as the gate for the nameserver group:

```sh
dig @<A-ip> -p 5354 proximo-doctor.studio-01.mesh.internal +short   # <A-ip>
```

Then label one app that has a real login with `proximo.share=true`. `proximo status` shows
its two peer names in the `PEER` column.

## 2. Access policy and DNS on the mesh

In the NetBird dashboard, in this order:

1. **Group** `proximo-lab` holding A and B. Check membership per peer afterwards.
2. **Policy** `proximo-lab → proximo-lab`, bidirectional, TCP 443 and TCP+UDP 5354, nothing
   else. A `Default` allow-all policy still admits everything: while it is enabled, the
   policy is not what is being measured. Disabling it affects every other peer on the
   account, so decide that before the lab, not during it.
3. **Nameserver group**, created **only now that `peer-dns` passes**: domain
   `studio-01.mesh.internal`, nameserver `<A-ip>` port **5354**, **primary off**,
   **search domains off**, distributed to `proximo-lab`. Confirm the three values after
   saving: an omitted field can default to port 53 or primary on.

On A, `proximo doctor`: `mesh` passes, and `https://<name>.test` still answers (constraint 6).

## 3. Enrol B

Copy `~/proximo-netbird-lab/team-root.crt` from A to B. On B, install the Linux release
binary of the same version. Then:

```sh
proximo config peer-suffix mesh.internal
proximo config team-root ~/team-root.crt
proximo trust
resolvectl query proximo-doctor.studio-01.mesh.internal   # answers <A-ip>
resolvectl query proximo-doctor.nobody.mesh.internal      # control: no answer
```

Run `trust` **without** `sudo`: it asks for `sudo` itself. Under `sudo`, Ubuntu sets
`HOME=/root`, so proximo reads root's configuration, finds no team root and silently installs
only a local CA of root's. `trust` mints B's own local CA too; it is harmless and needs no
Docker. Chrome's NSS store, `~/.pki/nssdb`, exists only once Chrome has run: start and quit
Chrome once before `trust`, then fully restart it.
A snap Chromium or Firefox keeps its own NSS store: use the deb Chrome.

## 4. The acceptance items

Each item is ticked on the issue with what was seen, including the connection type.

1. **The Qualified peer name in a browser.** On B, open `https://<app>.<project>.studio-01.mesh.internal`:
   no certificate warning, log in, navigate, reload — the session holds. Compare with the
   Bare name `https://<app>.studio-01.mesh.internal`.
2. **A session survives `Login required`.** Enable the account's peer login expiration at its
   minimum, and the per-peer expiration on A only. The API reports A `login_expired`; the
   client itself keeps saying `Connected` and `netbird up` answers `Already connected`. Record what B sees
   meanwhile. Log A in again with `netbird down` then `netbird up`, then reload on B: the same session, no new
   login.
3. **The mesh address under `Login required`.** During item 2, on A:
   `ifconfig | grep <A-ip>`. Repeat after a deliberate `netbird down`. Record both: held or
   not.
4. **On Ubuntu, the address is held by the mesh interface.** On B:
   `proximo config address <B-ip>` prints **no** "no interface holds" warning, and
   `ip -4 addr show wt0` lists `<B-ip>`. Then `proximo config unset address`.
5. **Mesh down at `proximo up`.** On A: `proximo down`, `netbird down`, `proximo up`,
   `netbird up`. Wait for the client to connect, then `proximo doctor`: `peer-dns` passes
   with no further command.
6. **Mesh dropping under a running peer DNS service.** On A with the stack up:
   `netbird down`, `netbird up`, then `proximo doctor`, and on B
   `resolvectl query proximo-doctor.studio-01.mesh.internal`. If `peer-dns` stays failed,
   record it: widening the watcher's restart is a new decision
   ([ADR 0013](../../docs/adr/0013-proximo-answers-the-peer-subtree.md)).
7. **One nameless certificate.** On B, for an unshared name and an invented one:

   ```sh
   for n in <unshared>.studio-01.mesh.internal invented.studio-01.mesh.internal; do
     openssl s_client -connect <A-ip>:443 -servername "$n" </dev/null 2>/dev/null |
       openssl x509 -noout -fingerprint -sha256 -ext subjectAltName
   done
   ```

   The two fingerprints are equal, and neither certificate has a SAN.

## 5. The team root on macOS, by SHA-1

On A, last, because it tears the stack down:

```sh
openssl x509 -in ~/proximo-netbird-lab/team-root.crt -noout -fingerprint -sha1
security find-certificate -a -Z /Library/Keychains/System.keychain | grep -i <sha1-without-colons>
proximo uninstall
security find-certificate -a -Z /Library/Keychains/System.keychain | grep -i <sha1-without-colons>
```

Before `uninstall` the SHA-1 is listed; after, it is gone, and the local CA is gone too.
Then `proximo install` to restore the machine.

## Teardown

On A: `proximo config unset` each of `intermediate`, `team-root`, `address`, `machine`,
`peer-suffix`, then `proximo up`. On B, `proximo uninstall` would stop a Docker stack B does
not have, so remove the two anchors by hand:

```sh
sudo rm /usr/local/share/ca-certificates/proximo-team-root.crt \
  /usr/local/share/ca-certificates/proximo-local-ca.crt
sudo update-ca-certificates --fresh
certutil -D -d sql:$HOME/.pki/nssdb -n 'proximo team root'
certutil -D -d sql:$HOME/.pki/nssdb -n 'proximo local CA'
rm -rf ~/.proximo
```

Delete the nameserver group, the policy and the group, and re-enable any
policy disabled for the lab. Remove `~/proximo-netbird-lab`.
