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

Two steps need `sudo` and an agent shell has no tty, so `sudo` cannot prompt — not from the tool
shell and not from the `!` prefix, which is equally tty-less. Hand `hosts_add` and `trust` (and
`teardown` at the end) to the human, in a real terminal window; the ticket is per-tty and will not
carry over. Never route a password through `sudo -S`: it would land in the transcript. Everything
else — mint, serve, all three `verify-cert` readings, the log, the clean-up checks — needs no
privilege.

1. `./hack/nameconstraints/run.sh teardown` first, unconditionally. It costs nothing on a clean
   machine and it clears the one trap this proof has: a server still running from an earlier round
   serves fixtures a later `setup` has already replaced, so the leaves no longer chain to the
   installed root and *every* case reads as untrusted.

   `teardown` is not enough on its own, and the 2026-09-04 round was caught by exactly this: it
   removes the container named `proximo-nc-proof`, while the raw `docker run` under
   [Serve them to the browsers](#serve-them-to-the-browsers) used to start an **anonymous** one,
   which survives every teardown. Check the ports, not the name:

   ```sh
   docker ps --format '{{.Names}}\t{{.Ports}}' | grep -E '844[345]'
   ```

   Anything listed there must be removed before `setup`, whatever it is called.
2. `./hack/nameconstraints/run.sh setup`, then `serve`. `setup` mints fresh keys and serials, so a
   trust exception left over from an earlier round cannot apply to these leaves.
3. **Before installing the root**, record the untrusted baseline — the ordinary first-run state a
   colleague sees, which the documented limits have to describe:

   ```sh
   security verify-cert -c proof-out/in-subtree.pem -c proof-out/int.pem -p ssl -s app.machine-a.mesh.internal
   security verify-cert -c proof-out/dns-out.pem    -c proof-out/int.pem -p ssl -s out-of-subtree.example.com
   security verify-cert -c proof-out/ip-san.pem     -c proof-out/int.pem -p ssl -s 127.0.0.1
   ```

   Expect the first to fail naming an untrusted root (`CSSMERR_TP_NOT_TRUSTED`). If it *succeeds*,
   stop: a stale anchor or a trust exception is still in the keychain, and nothing measured
   afterwards is a reading. `security dump-trust-settings` should report no user-domain settings
   here.

   Take the other two now as well, even though they are expected to fail either way — their
   baseline is what shows that the one-line verdict does not distinguish "untrusted" from
   "constraint violated". That is the whole reason step 4 needs `-v`.
4. `./hack/nameconstraints/run.sh trust`, then evaluate all three cases against the system trust
   store. `security verify-cert` calls the same evaluator Safari does, and `-v` is not optional —
   see below:

   ```sh
   security verify-cert -v -L -c proof-out/in-subtree.pem -c proof-out/int.pem -p ssl -s app.machine-a.mesh.internal
   security verify-cert -v -L -c proof-out/dns-out.pem    -c proof-out/int.pem -p ssl -s out-of-subtree.example.com
   security verify-cert -v -L -c proof-out/ip-san.pem     -c proof-out/int.pem -p ssl -s 127.0.0.1
   ```

   The amendment predicts the first succeeds and the other two fail. Record the **exact** output of
   each, success and failure alike. A platform that rejects `dns-out` and silently accepts `ip-san`
   has not demonstrated the amendment: it is the IP case that proves the explicit exclusions work.

   **Why `-v`.** Without it, both rejected cases print `Cert Verify Result:
   CSSMERR_TP_INVALID_CERTIFICATE` — and that is the *same* line they print before the root is
   installed, so the one-line form never says the constraint was the reason. All the evidence
   would rest on the `in-subtree` control flipping. `-v` adds the per-certificate result, where
   macOS names it (`Certificate violates name constraints placed on issuing CA
   [NameConstraints]`, `NameConstraints = 0`) and marks it on the **leaf only**, with the chain
   built all the way to the root. That is the text #82 quotes. `-L` keeps the evaluator off the
   network.

   Treat an unexpected *invocation* error as a command to fix, and an unexpected *verdict* as the
   finding.
5. Chrome needs Chrome, and its verdict is not Apple's: since M105 Chrome on macOS verifies with
   its own code, querying the platform store only for user-added anchors. Check
   `chrome://version` (must be ≥ 126, since the `EnforceLocalAnchorConstraintsEnabled` policy
   could disable enforcement until it was removed in 126) and `chrome://policy` (that policy must
   be unset), then open the three URLs `serve` printed.

   **Do not click through a warning**, and do not ask anyone else to: Safari stores a click-through
   as a trust exception for that certificate, and Chrome remembers a bypass per host — either one
   turns every later round into a false accept.
6. `./hack/nameconstraints/run.sh logs` is the reading that does not depend on interpreting a
   browser's UI. A case the client refused appears as `http: TLS handshake error from …: remote
   error: tls: <alert>` — it aborted the handshake. A case it accepted produces no error line at
   all, because the handshake completed and the request was served. So `in-subtree` absent from
   the log while `dns-out` and `ip-san` are present **is** enforcement.

   The alert differs by client, so match on `TLS handshake error`, never on one alert string:
   Chrome sends `unknown certificate`, SecureTransport (Safari, and the `curl` in `/usr/bin`)
   sends `unknown certificate authority`, and `bad certificate` is also permitted by the spec.

   Attribute lines to cases by counting, one URL at a time — a browser opens several connections
   per load, so a rejected case adds more than one line:

   ```sh
   docker logs proximo-nc-proof 2>&1 | grep -c 'TLS handshake error'
   ```

   A served page body is not by itself evidence: it says the handshake completed, not why. The
   converse is stronger than it looks — a client that cannot be clicked through never completed
   the handshake, so there is nothing to bypass at the TLS layer and the log is the whole reading.
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
docker run --rm --name proximo-nc-proof -v "$PWD":/src -w /src -e CGO_ENABLED=0 \
  -p 127.0.0.1:8443:8443 -p 127.0.0.1:8444:8444 -p 127.0.0.1:8445:8445 \
  golang:1.27-alpine go run ./hack/nameconstraints -serve -out /src/proof-out
```

`--name proximo-nc-proof` is load-bearing: it is the one name `run.sh teardown` removes. Without
it Docker assigns a random name, the container outlives every teardown, and it keeps serving
fixtures a later `setup` has replaced — at which point nothing chains to the installed root and
all three cases read as untrusted. Prefer `run.sh serve`, which does this for you.

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
| macOS system trust (`verify-cert`, = Safari) | macOS 26.6.2 (25G83) | `...certificate verification successful.` (`exit=0`) | `Certificate violates name constraints placed on issuing CA [NameConstraints]` | `Certificate violates name constraints placed on issuing CA [NameConstraints]` |
| Chrome on macOS | 152.0.7977.83 (arm64) | served body, no log line | handshake aborted: `remote error: tls: unknown certificate` | handshake aborted: `remote error: tls: unknown certificate` |
| `curl` on macOS (SecureTransport) | curl 8.7.1 / LibreSSL 3.3.6 | `code=200` (`exit=0`) | `curl: (60) SSL certificate problem: unable to get local issuer certificate` | `curl: (60) SSL certificate problem: unable to get local issuer certificate` |
| Firefox on macOS (NSS) | — | *not taken: no NSS profile on the host* | — | — |
| Chrome on Ubuntu (`~/.pki/nssdb`) | | | | |
| Firefox on Ubuntu (NSS profile) | | | | |
| `curl` on Ubuntu (system bundle) | | | | |

The macOS rows were taken on 2026-09-04; the Ubuntu rows still need a second machine. The
readings behind them, and the two rows that could not be filled, are in
[The macOS reading](#the-macos-reading-2026-09-04).

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

## The macOS reading (2026-09-04)

Taken on macOS 26.6.2 (build 25G83), Apple silicon, following
[If you are an agent on the macOS host](#if-you-are-an-agent-on-the-macos-host). **All three
verifiers on this host enforce both limbs**: each accepts the `in-subtree` control and rejects
both `dns-out` and `ip-san`. The IP case is the one that mattered — under RFC 5280 a DNS-only
constraint would have admitted it — and nothing accepted it silently.

Three independent evaluators, because on macOS they are genuinely three: the Apple evaluator
behind the keychain, Chrome's own verifier (its own code since M105, querying the platform store
only for user-added anchors), and SecureTransport as used by `/usr/bin/curl`.

### The stale server was real

The trap step 1 warns about was armed before the round even started: an **anonymous**
`golang:1.27-alpine` container had been serving since 12:01:36 UTC, started by the raw
`docker run` that then carried no `--name`, so `teardown` had left it alone. Its log is the exact
signature of the false negative — every case untrusted, `in-subtree` included:

```
2026/09/04 12:01:48 http: TLS handshake error from 172.17.0.1:61070: remote error: tls: unknown certificate
2026/09/04 12:01:48 http: TLS handshake error from 172.17.0.1:61084: remote error: tls: unknown certificate
2026/09/04 12:01:51 http: TLS handshake error from 172.17.0.1:64920: remote error: tls: unknown certificate
```

It was removed and the server restarted under the fixed name before anything was measured. The
`--name` flag and the port check in step 1 are both there because of this round.

### Before `trust` — the untrusted baseline

The ordinary first-run state a colleague sees. All three `exit=1`:

```
----- BEFORE in-subtree -----
Cert Verify Result: CSSMERR_TP_NOT_TRUSTED
----- BEFORE dns-out -----
Cert Verify Result: CSSMERR_TP_INVALID_CERTIFICATE
----- BEFORE ip-san -----
Cert Verify Result: CSSMERR_TP_INVALID_CERTIFICATE
```

Each followed by the same block:

```
---
No extended validation result found
Certificate Transparency (CT) status: not verified
Unable to find at least 2 signed certificate timestamps (SCTs) from approved logs
```

`in-subtree` failing with `CSSMERR_TP_NOT_TRUSTED` is what makes the round a reading:
`security dump-trust-settings` reported `SecTrustSettingsCopyCertificates: No Trust Settings were
found.`, so no stale anchor and no trust exception could explain anything measured after.

Note that the two rejected cases already read `CSSMERR_TP_INVALID_CERTIFICATE` here, **before**
the root is trusted — the same line they print afterwards. That is why step 4 now passes `-v`.

### After `trust` — the Apple evaluator

```
===== AFTER in-subtree =====
...certificate verification successful.
[exit=0]

===== AFTER dns-out =====
Cert Verify Result: CSSMERR_TP_INVALID_CERTIFICATE
[exit=1]

===== AFTER ip-san =====
Cert Verify Result: CSSMERR_TP_INVALID_CERTIFICATE
[exit=1]
```

With `-v`, macOS names the reason. For `dns-out`:

```
Certificate errors
 0: out-of-subtree.example.com
    Certificate violates name constraints placed on issuing CA [NameConstraints]
 1: proof machine intermediate machine-a
 2: proof team root (name-constrained)
```

```
    TrustResultDetails =     (
                {
            NameConstraints = 0;
            StatusCodes =             (
                "-2147409643"
            );
        },
```

```
Error Domain=NSOSStatusErrorDomain Code=-67689 "“out-of-subtree.example.com” certificate is not standards compliant" UserInfo={NSLocalizedDescription=“out-of-subtree.example.com” certificate is not standards compliant, NSUnderlyingError=0xad0c240c0 {Error Domain=NSOSStatusErrorDomain Code=-67689 "Certificate 0 “out-of-subtree.example.com” has errors: Name constraints violated;" UserInfo={NSLocalizedDescription=Certificate 0 “out-of-subtree.example.com” has errors: Name constraints violated;}}}
```

`ip-san` produces the same text with `127.0.0.1` in place of the DNS name. In both cases the chain
is built complete to the root, the error is marked on the **leaf only** — intermediate and root
carry none — and `TrustResultValue` is `5` against `1` for `in-subtree`.

Two things worth keeping: macOS surfaces a name-constraint violation as *"not standards
compliant"*, which reads like a malformed certificate rather than a policy decision; and the
status code is the same `-2147409643` for the permitted-subtree and the excluded-IP limb, so the
code alone does not say which limb fired.

### Chrome

`chrome://version` reported `152.0.7977.83 (Official Build) (arm64)`, well past the 126 floor, on
`macOS Version 26.6.2 (Build 25G83)`. `EnforceLocalAnchorConstraintsEnabled` was unset:
`/Library/Managed Preferences/com.google.Chrome.plist` does not exist, the user domain reported
`The domain/default pair of (com.google.Chrome, EnforceLocalAnchorConstraintsEnabled) does not
exist`, and `Command Line` showed an empty `--flag-switches-begin --flag-switches-end`.

The verdict was taken from the log, one URL at a time, by counting:

| Case | New log lines | Verdict |
| --- | --- | --- |
| `in-subtree` | +0 | handshake completed, page body served |
| `dns-out` | +6 (three reloads × two connections) | `remote error: tls: unknown certificate` |
| `ip-san` | +2 | `remote error: tls: unknown certificate` |

Neither warning could be clicked through, which is stronger than a bypassable one: the handshake
never completed, so there was nothing to bypass at the TLS layer.

### `curl` on macOS, and the third alert string

`/usr/bin/curl` is `curl 8.7.1 (x86_64-apple-darwin25.0) libcurl/8.7.1 (SecureTransport)
LibreSSL/3.3.6` — a **SecureTransport** build, so it reads the keychain and is the closest client
to Safari that needs no browser:

```
===== curl in-subtree =====
code=200
[exit=0]
===== curl dns-out =====
curl: (60) SSL certificate problem: unable to get local issuer certificate
code=000
[exit=60]
===== curl ip-san =====
curl: (60) SSL certificate problem: unable to get local issuer certificate
code=000
[exit=60]
```

SecureTransport flattens the constraint violation to `unable to get local issuer certificate` —
it never names the constraint, and the text differs from both the Apple evaluator's and the
`permitted subtree violation (47)` that the OpenSSL-backed `curl` in the container reports. Its
handshake abort is a third alert string too, `unknown certificate authority` where Chrome sends
`unknown certificate`, which is why step 6 now says to match on `TLS handshake error` alone.

Line-to-case attribution was verified one request at a time: `in-subtree` added 0 lines,
`dns-out` 1, `ip-san` 1.

### What is still missing, and why

- **Firefox on macOS** — `run.sh trust` reported `no Firefox NSS database — install Firefox, open
  it once, then re-run`. `certutil` is present (`/opt/homebrew/bin/certutil`); the profile
  directory `~/Library/Application Support/Firefox/Profiles` does not exist. Not a negative
  result: the client was absent.
- **Every Ubuntu row** — needs the second machine.
- Neither `~/.pki/nssdb` nor `~/.local/share/pki/nssdb` exists on this host. Chrome 152 on macOS
  reads the keychain and creates no NSS database, so the M146 relocation the section
  [Before reading Chrome](#before-reading-chrome) flags does not bite here. It remains open for
  Chrome on Linux, which is where `nss.go:91` matters.

### Teardown

Confirmed clean afterwards: the root absent from `/Library/Keychains/System.keychain`,
`security dump-trust-settings` back to `SecTrustSettingsCopyCertificates: No Trust Settings were
found.`, both `# proximo-nc-proof` lines gone from `/etc/hosts`, no container on ports 8443–8445
under any name, `proof-out/` deleted.
