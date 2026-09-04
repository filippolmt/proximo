# The name-constraint proof

The amendment on [Whose certificate a peer trusts](https://github.com/filippolmt/proximo/issues/83)
rests on one claim: **X.509 name constraints are enforced for a trust anchor the user
installed themselves.** If a platform ignores them, the team root is an unconstrained CA on
every colleague's machine and the amendment reverts.

This directory mints the fixtures that claim needs, serves them over TLS so a real browser can
be pointed at them, and takes the readings that need no browser. It belongs to
[Two machines, proved by hand](https://github.com/filippolmt/proximo/issues/81) and lives on a
throwaway branch: it is a measurement harness, not part of proximo.

The certificates mirror the shape decided on
[Where the team root lives, and who signs an intermediate](https://github.com/filippolmt/proximo/issues/97) —
a name-constrained team root, one intermediate per machine, leaves signed locally — so what a
browser does to these fixtures is what it would do to the real thing.

## The three cases

| Case | URL | Expected | Why |
| --- | --- | --- | --- |
| `in-subtree` | `https://app.machine-a.mesh.internal:8443/` | **accepted** | The control. |
| `dns-out` | `https://out-of-subtree.example.com:8444/` | **rejected** | A DNS name outside the root's `PermittedDNSDomains`. |
| `ip-san` | `https://127.0.0.1:8445/` | **rejected** | A SAN that is an IP address, excluded by `ExcludedIPRanges`. |

`in-subtree` is not optional. Without it, a rejection of `dns-out` is equally well explained by
the root not being trusted at all, which is the ordinary first-run state.

`ip-san` exists because, under RFC 5280, a name type absent from the permitted subtrees is
*unrestricted* for that type: a DNS-only constraint still admits a leaf whose SAN is an IP
address. A platform that rejects `dns-out` and silently accepts `ip-san` has **not**
demonstrated the amendment.

## Mint, and take the readings that need no browser

Go runs in Docker, as everywhere else in this repository:

```sh
docker run --rm -v "$PWD":/src -w /src -e CGO_ENABLED=0 golang:1.27-alpine \
  go run ./hack/nameconstraints -mint -verify -out /src/proof-out
```

`-suffix` and `-machine` default to `mesh.internal` and `machine-a`. No real name appears here,
per constraint 7 on the map.

The fixtures land in `proof-out/`, which is **not** committed: `root-key.pem` is a CA key.

## Running the proof on macOS

`run.sh` does the manual half — the fixtures, the two `/etc/hosts` lines, the root going into the
keychain and into every Firefox profile, and taking all of it back out again. Reversal is the part
worth automating: the root is a real CA in a real trust store until it is removed.

```sh
./hack/nameconstraints/run.sh setup      # mint the fixtures, make the names resolve
./hack/nameconstraints/run.sh serve      # serve the three leaves in the background
./hack/nameconstraints/run.sh trust      # install the root into the keychain (and Firefox)
./hack/nameconstraints/run.sh logs       # what each client did at the TLS layer
./hack/nameconstraints/run.sh stop       # stop serving
./hack/nameconstraints/run.sh teardown   # stop, remove the root, delete the fixtures
```

Ubuntu is not covered by the script; its four trust stores are below.

### If you are an agent on the macOS host

The whole proof except the Chrome reading can be taken without a browser, because macOS exposes
its own trust evaluator. Work in this order and do not skip the *before* round — it is what makes
the *after* round mean anything.

1. `./hack/nameconstraints/run.sh teardown` first, unconditionally. It costs nothing on a clean
   machine and it clears the one trap this proof has: a server still running from an earlier round
   serves fixtures a later `setup` has already replaced, so the leaves no longer chain to the
   installed root and *every* case reads as untrusted.
2. `./hack/nameconstraints/run.sh setup`, then `serve`. `setup` mints fresh keys and serials, so a
   trust exception left over from an earlier round cannot apply to these leaves.
3. **Before installing the root**, record the untrusted baseline — the ordinary first-run state a
   colleague sees, which the documented limits have to describe:

   ```sh
   security verify-cert -c proof-out/in-subtree.pem -c proof-out/int.pem -p ssl -s app.machine-a.mesh.internal
   ```

   Expect a failure naming an untrusted root. If this *succeeds*, stop: a stale anchor or a trust
   exception is still in the keychain, and nothing measured afterwards is a reading.
4. `./hack/nameconstraints/run.sh trust`, then evaluate all three cases against the system trust
   store. `security verify-cert` calls the same evaluator Safari does:

   ```sh
   security verify-cert -c proof-out/in-subtree.pem -c proof-out/int.pem -p ssl -s app.machine-a.mesh.internal
   security verify-cert -c proof-out/dns-out.pem    -c proof-out/int.pem -p ssl -s out-of-subtree.example.com
   security verify-cert -c proof-out/ip-san.pem     -c proof-out/int.pem -p ssl -s 127.0.0.1
   ```

   The amendment predicts the first succeeds and the other two fail. Record the **exact** output of
   each, success and failure alike. A platform that rejects `dns-out` and silently accepts `ip-san`
   has not demonstrated the amendment: it is the IP case that proves the explicit exclusions work.

   These commands were written from the `security(1)` interface and have not been run — this
   repository's Go work happens in a Linux container, which has no Apple evaluator. Treat an
   unexpected *invocation* error as a command to fix, and an unexpected *verdict* as the finding.
5. Chrome needs Chrome, and its verdict is not Apple's: since M105 Chrome on macOS verifies with
   its own code, querying the platform store only for user-added anchors. Check
   `chrome://version` (must be ≥ 126, since the `EnforceLocalAnchorConstraintsEnabled` policy
   could disable enforcement until it was removed in 126) and `chrome://policy` (that policy must
   be unset), then open the three URLs `serve` printed.

   **Do not click through a warning**, and do not ask anyone else to: Safari stores a click-through
   as a trust exception for that certificate, and Chrome remembers a bypass per host — either one
   turns every later round into a false accept.
6. `./hack/nameconstraints/run.sh logs` is the reading that does not depend on interpreting a
   browser's UI. A case the browser refused appears as
   `http: TLS handshake error from …: remote error: tls: unknown certificate` (or `bad
   certificate`) — the browser aborted the handshake. A case the browser accepted produces no
   error line at all, because the handshake completed and the request was served. So
   `in-subtree` absent from the log while `dns-out` and `ip-san` are present **is** enforcement.

   A served page body is not by itself evidence: it says the handshake completed, not why.
7. `./hack/nameconstraints/run.sh teardown`. Then confirm the keychain is clean —
   `security dump-trust-settings` lists user-domain exceptions — and report.

What to report: for each of the three cases, the `security verify-cert` output before and after
`trust`, the Chrome verdict with its version, and the `logs` excerpt. Exact text, not a summary:
the error strings are what the documented limits quote.

## Serve them to the browsers

The names must resolve, and the URL host must match the fixture's SAN — otherwise the reading is
a name mismatch rather than a constraint decision. Two lines in `/etc/hosts`:

```
127.0.0.1 app.machine-a.mesh.internal
127.0.0.1 out-of-subtree.example.com
```

Then serve, published to the host's loopback only:

```sh
docker run --rm -v "$PWD":/src -w /src -e CGO_ENABLED=0 \
  -p 127.0.0.1:8443:8443 -p 127.0.0.1:8444:8444 -p 127.0.0.1:8445:8445 \
  golang:1.27-alpine go run ./hack/nameconstraints -serve -out /src/proof-out
```

`proximo` is not involved and no mesh is needed: the proof is about one machine's trust stores.

## Install the root, exactly the way proximo installs its own

The commands mirror `internal/tls/trust.go` and `internal/tls/nss.go`, so a passing reading is a
statement about the paths proximo actually writes.

**macOS system trust** — covers Safari and Chrome, which read the keychain and hold no NSS
database of their own:

```sh
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain proof-out/root.pem
```

**Firefox on macOS** (`nss.go` globs `~/Library/Application Support/Firefox/Profiles/*`):

```sh
certutil -A -d sql:"$HOME/Library/Application Support/Firefox/Profiles/<profile>" \
  -t C,, -n "proof team root (name-constrained)" -i proof-out/root.pem
```

**Ubuntu** is four distinct places, and they are not redundant. Firefox on Linux gets a root
solely through its own NSS database; Chrome on Linux reads user-added anchors from the NSS shared
database, not from `/etc/ssl/certs`; the system bundle serves OpenSSL, Go and `curl` and neither
browser:

```sh
certutil -A -d sql:"$HOME/.pki/nssdb" -t C,, -n "proof team root (name-constrained)" -i proof-out/root.pem
certutil -A -d sql:"$HOME/.mozilla/firefox/<profile>" -t C,, -n "proof team root (name-constrained)" -i proof-out/root.pem
certutil -A -d sql:"$HOME/snap/firefox/common/.mozilla/firefox/<profile>" -t C,, -n "proof team root (name-constrained)" -i proof-out/root.pem
sudo cp proof-out/root.pem /usr/local/share/ca-certificates/proof-team-root.crt && sudo update-ca-certificates
```

### Before reading Chrome

Enforcement of constraints on locally installed anchors starts at Chrome 112 and was disableable
by the `EnforceLocalAnchorConstraintsEnabled` policy until that policy was removed in **126**. A
reading on an older Chrome, or on one with that policy set, proves nothing.

- `chrome://version` — record the version; it must be ≥ 126.
- `chrome://policy` — confirm `EnforceLocalAnchorConstraintsEnabled` is not set.

Note also which NSS database the installed Chrome reads: Chrome is reported to relocate it to
`$HOME/.local/share/pki/nssdb` from M146, while `nss.go:91` hard-codes `~/.pki/nssdb`. That
concerns the existing `.test` CA rather than the peer names, but the machine is already in the room.

## Remove the root afterwards

The root is a real CA in a real trust store, and it stays there until it is removed by hand.

```sh
sudo security delete-certificate -c "proof team root (name-constrained)" /Library/Keychains/System.keychain
certutil -D -d sql:"<profile or nssdb>" -n "proof team root (name-constrained)"
sudo rm -f /usr/local/share/ca-certificates/proof-team-root.crt && sudo update-ca-certificates --fresh
```

Then take the two `/etc/hosts` lines out again.

## Record this, per platform

Both cases, per platform, with the **exact** error text — the error text is what
[The documented limits of a shared session](https://github.com/filippolmt/proximo/issues/82) has
to describe, and "it failed" is not a reading.

| Platform | Version | `in-subtree` | `dns-out` (error text) | `ip-san` (error text) |
| --- | --- | --- | --- | --- |
| Safari (macOS system trust) | | | | |
| Chrome on macOS | | | | |
| Firefox on macOS (NSS) | | | | |
| Chrome on Ubuntu (`~/.pki/nssdb`) | | | | |
| Firefox on Ubuntu (NSS profile) | | | | |
| `curl` on Ubuntu (system bundle) | | | | |

Also record what a browser shows with the root **never installed** — the ordinary first-run
state, which #82 has to document.

## Readings already taken

Verifiers that need no trust store, no browser and no second machine, so they were taken in the
container. All three cases behave as the amendment predicts:

**Go `crypto/x509`** (`-verify`):

```
in-subtree  expect=accepted got=accepted ok
dns-out     expect=rejected got=rejected ok
             x509: a root or intermediate certificate is not authorized to sign for this name: DNS name "out-of-subtree.example.com" is not permitted by any constraint
ip-san      expect=rejected got=rejected ok
             x509: a root or intermediate certificate is not authorized to sign for this name: IP address "127.0.0.1" is excluded by constraint "0.0.0.0/0"
```

**OpenSSL 3.5.8** (`openssl verify -CAfile root.pem -untrusted int.pem`):

```
in-subtree  OK
dns-out     error 47 at 0 depth lookup: permitted subtree violation
ip-san      error 48 at 0 depth lookup: excluded subtree violation
```

**curl 8.22.0 / OpenSSL 3.5.8**, root as the only anchor:

```
in-subtree  exit=0  code=200
dns-out     exit=60 SSL certificate OpenSSL verify result: permitted subtree violation (47)
ip-san      exit=60 SSL certificate OpenSSL verify result: excluded subtree violation (48)
```

These establish that the fixtures are correct and that the OpenSSL and Go verifiers enforce both
limbs. They say nothing about the platforms that decide the design — **macOS system trust,
Chrome and Firefox** — which is what the table above is for.
