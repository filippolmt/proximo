---
status: proposed
---

# Peer certificates come from a name-constrained team root

> **Proposed.** Part of the [peer-sharing specification](../specs/peer-sharing.md),
> which describes a capability the binary does not have yet. It becomes
> `accepted` in the commit that builds it.

A colleague's browser has to trust a shared route's certificate, and proximo's
local CA cannot be the answer as it stands: a CA in a trust store can sign for
*any* name, so a colleague who trusted another developer's local CA would hand
that developer's laptop the power to intercept their whole browsing — their bank
included, not just the shared app. The workload being internal does not shrink
that: the store serves the colleague's real browsing.

**Decision.** A **team root, kept offline and name-constrained to the Peer
suffix**, signs **one intermediate per machine**, constrained to
`<machine>.<suffix>`. proximo signs that machine's peer leaves with its
intermediate, locally, exactly as the local CA signs `.test` leaves: exact SANs,
397 days, no wildcard. A colleague installs **one** certificate — the team root —
and proximo installs and removes it as a second anchor beside the local CA.

Name constraints change the *kind* of risk, not its quantity, which is what the
objection to a private CA demanded:

- Both certificates carry `PermittedDNSDomains`, **marked critical**.
- **Constraining `dNSName` alone is not enough.** Under RFC 5280 a name type
  absent from the permitted subtrees is unrestricted, so a leaf carrying an IP SAN
  would satisfy a DNS-only constraint. Both certificates also exclude every IP
  range (`0.0.0.0/0`, `::/0`), every email address and every URI.
- The intermediate has `MaxPathLen` 0, so a machine cannot mint a further CA.
- `config team-root` and `config intermediate` refuse a certificate that misses
  any of this: it is the one rule proximo can check completely, and the one with
  the largest consequence.

Enforcement on user-added anchors was demonstrated rather than cited: no verifier
read — macOS system trust, Chrome on macOS and Ubuntu, Firefox, `curl` with
OpenSSL — accepted an out-of-subtree leaf, for a foreign DNS name or an IP SAN.
Chrome enforces constraints on local anchors from 112 and lost the policy that
could disable it in 126, so Chrome 126 is the documented floor.

What a browser needs from the simulation is a *secure context* — real HTTPS,
`Secure` cookies, no mixed-content downgrade, service workers — and it grants
that on whether it trusts the chain, never on who signed it. So a privately
signed chain loses no fidelity.

## Considered options

**A publicly trusted certificate by ACME DNS-01, run by Traefik, one wildcard per
machine.** The first resolution
([#83](https://github.com/filippolmt/proximo/issues/83#issuecomment-5499422879)),
and the reasoning that showed the certificate is independent of the transport.
Rejected: public trust bought exactly one thing — the colleague installs nothing —
and cost a registrar, a public zone, a DNS-editing token on every laptop, a
ninety-day renewal gated on a laptop being awake, and every machine label in
Certificate Transparency. It also required a wildcard, which the Peer suffix
cannot carry safely
([ADR 0011](0011-a-shared-route-answers-on-two-derived-peer-names.md)), and
Baseline Requirements forbid public issuance under a private-use suffix at all.

**An unconstrained team CA.** Divides the interception risk among fewer keys
without changing its kind. Rejected for the reason above.

**Every colleague installs every other colleague's local CA.** Every machine
trusting every other machine's unconstrained anchor. Rejected on the same ground,
multiplied, and because reinstalling one machine would touch every other.

**An internal ACME CA on the team's cluster.** Rejected: a service in continuous
operation for a development convenience, which would also need the cluster to be
a mesh peer for challenges to reach a laptop. A one-time signing ceremony gets the
same properties with nothing running.

**Where the root key lives** was weighed separately
([#97](https://github.com/filippolmt/proximo/issues/97)). A hardware token is
disproportionate, and a physical object between a machine and its enrolment is a
ceremony that does not get performed. A password manager, a cluster secret and the
mesh control plane's host each make it a service, the last putting the signing key
and mesh access behind one door. Destroying the key after one ceremony fails on
the common event, a reinstalled laptop.

## Consequences

- **The root key is an encrypted file on offline media, two copies, two people.**
  Losing both is an accepted recovery path — new root, re-signed intermediates,
  re-installed anchors — so custody needs secrecy, not durability.
- **Validity: root 10 years, intermediate 5, leaf 397 days.** One ceremony per
  machine in its life. `doctor`'s `peer-intermediate` fails 30 days before expiry,
  because proximo would otherwise keep issuing valid leaves under an expired
  intermediate that only a colleague's browser notices.
- **No revocation.** Presenting a peer certificate requires mesh membership, which
  offboarding removes; a revocation list would be a service in continuous
  operation for a development tool.
- **The ceremony is a command**, `proximo config csr`, and a signing script in this
  repository that takes the suffix and the machine as arguments. A CSR and a
  certificate are public, so only the root key needs protecting.
- **Renewal is code that exists.** Leaves are issued by proximo, as `.test` leaves
  are; nothing renews against a remote service.
- **The local CA's name must stop doing two jobs.** One constant is both its NSS
  nickname and its macOS deletion selector, so the team root needs a name of its
  own before either trust path is touched, or removing one anchor could remove the
  other.
- **A platform that ignores name constraints leaves the supported set**; the Peer
  suffix does not change.
