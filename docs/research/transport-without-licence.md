# Which transport, without a new licence

Research for [issue #76](https://github.com/filippolmt/proximo/issues/76), under the
constraints charted in [issue #75](https://github.com/filippolmt/proximo/issues/75).
Five colleagues of one company need to reach each other's machines. The team already
pays for Cloudflare Zero Trust, so that option costs nothing more; every other candidate
must be free **for business use**, and a plan licensed for personal use only does not
qualify, however generous its limits.

Four candidates are settled here on the same terms: Cloudflare Zero Trust with
Cloudflare Tunnel, Tailscale, Headscale, and NetBird. `tailscale/tailcat` was ruled out
while charting and is not revisited.

Every claim is followed to the page that owns it. Where a question turns on a form of
words, the words are quoted rather than summarised, because the summary is where the
ambiguity gets lost. Sources are official vendor documentation, official pricing and
terms pages, project source and LICENSE files, and — for Headscale's certificate story,
which no documentation page answers — the project's own routing table and issue tracker.

## The licence question, first

It is the decisive one, and it separates the four candidates cleanly.

**Headscale and NetBird are settled by reading their LICENSE files**, and neither imposes
a field-of-use restriction. Headscale is BSD 3-Clause
([LICENSE](https://raw.githubusercontent.com/juanfont/headscale/main/LICENSE),
"Copyright (c) 2020, Juan Font"): "Redistribution and use in source and binary forms,
with or without modification, are permitted provided that the following conditions are
met" — three attribution and no-endorsement conditions, none of which is triggered by
running the binary internally, and nothing at all about who the user is or what they use
it for. The Tailscale client one would run against it is BSD 3-Clause too
([LICENSE](https://raw.githubusercontent.com/tailscale/tailscale/main/LICENSE)), so the
whole self-hosted stack is permissively licensed. The project's own FAQ closes the
question from the other side, without hedging:

> It depends. As often stated, Headscale is not enterprise software and our focus is
> homelabbers and self-hosters. Of course, we do not prevent people from using it in a
> commercial/professional setting and often get questions about scaling.

([Scaling FAQ](https://headscale.net/stable/about/faq/#scaling-how-many-clients-does-headscale-support))

NetBird is not one licence but two, and the distinction matters for anyone who would
self-host. The root
[LICENSE](https://raw.githubusercontent.com/netbirdio/netbird/main/LICENSE) opens with a
scope clause above the BSD text:

> This BSD‑3‑Clause license applies to all parts of the repository except for the
> directories management/, signal/, relay/ and combined/. Those directories are licensed
> under the GNU Affero General Public License version 3.0 (AGPLv3). See the respective
> LICENSE files inside each directory.

`management/LICENSE` and `relay/LICENSE` both begin "GNU AFFERO GENERAL PUBLIC LICENSE
Version 3", as does the separate
[dashboard repository](https://raw.githubusercontent.com/netbirdio/dashboard/main/LICENSE).
So the client on the five laptops is BSD and the entire control plane one would host is
AGPLv3. Neither restricts business use; the AGPL's obligations attach to distribution and
to network interaction, which for an unmodified internal deployment amounts to a
source-offer to one's own colleagues rather than a bar to use.

**Tailscale is where the answer is genuinely contested**, and it is worth setting out in
full rather than compressing.

The governing document is the [Terms of Service](https://tailscale.com/terms), last
updated 25 August 2026, which state their own scope explicitly: they are "the standard
Terms of Service … applicable to all customers that purchase the Services through the
Hosted Software … and all customers that use the Services under a free trial or free
Plan". A free-plan user is bound by them. The grant, at §2.1, reads:

> In accordance with the terms and conditions of the Agreement, Tailscale shall grant you
> and your Permitted Users access to and use of the Services as detailed in Documentation
> solely for your own personal use or internal business purposes (as applicable depending
> on your Plan).

The operative words are the parenthesis. The Terms do not say which plan carries which
grant; they delegate that, and §1.14 defines a Plan as "a subscription package for the
Services. We offer several Plans for both personal and business use at different price
points, including free Plans … For more information on our available Plans, please visit
our Pricing page." The [Pricing page](https://tailscale.com/pricing) is thereby
incorporated by reference, and it is where the restriction actually lives:

> Our Personal plan is for individuals who want to use Tailscale at home. This is a free
> plan and is only suitable for non-commercial use of Tailscale. It's perfect for things
> like building a homelab or home VPN, playing games with friends, or securely connecting
> to anything from a DigitalOcean droplet to a Raspberry Pi, home security camera, or even
> a Steam Deck.

and, in the adjacent answer:

> Please note, however, that the Personal plan is not intended for commercial use. If you
> sign up for Tailscale with your work email or other custom domains (e.g., @acme.com),
> then the Tailscale account is owned by the company or organization that owns and
> controls that email domain, regardless of which plan you are on.

The same page describes how Tailscale routes people into a plan by construction: "If you
create a tailnet with a public domain, such as Gmail, Apple, or a personal GitHub account,
it's treated as personal use. These tailnets are automatically enrolled in the free
Personal plan. If you create a tailnet with a custom domain, it's considered business use,
and you'll be automatically enrolled in a free trial."

The conservative reading — and the one this note adopts — is that the free Personal plan
does not permit a five-person company to run its work mesh on it. That reading is not
airtight, and the gap is named in the closing section rather than smoothed over here.

**Cloudflare needs no licence analysis for the entitlement the team already holds.** The
[Zero Trust Service-Specific Terms](https://www.cloudflare.com/service-specific-terms-zero-trust-services/)
describe the product as "a suite of cloud-based security solutions made available by
Cloudflare to its Customers for use by their authorized End Users", licensed "on a Seat
licensing basis", and the
[Self-Serve Subscription Agreement](https://www.cloudflare.com/terms/) contemplates
entities directly. The prohibitions bite on resale — §2.2 of the Zero Trust terms: "You
shall not resell Cloudflare Zero Trust to any third parties" — not on a company securing
its own staff. `cloudflared` is Apache License 2.0
([LICENSE](https://raw.githubusercontent.com/cloudflare/cloudflared/master/LICENSE)); the
client is not open source, and is governed by a EULA under which you agree "not to modify,
decompile, reverse engineer, or create derivative works of the Cloudflare One Application"
([Cloudflare One Agent terms](https://www.cloudflare.com/cloudflareone/application/terms/)).

## Cloudflare Zero Trust and Cloudflare Tunnel

**Cost and limits.** Nothing more, and the free tier would have sufficed anyway. The plans
page renders client-side, but the payload it renders from gives the Free plan as "$0
forever", "Best for teams under 50 users or enterprise proof-of-concept tests", with a
usage row reading "50 user limit"; Pay-as-you-go — the tier the documentation calls Zero
Trust Standard — at "$7/user/month" with "No user limit"; and the Contract plan at "Annual
custom price per user"
([Zero Trust plans](https://www.cloudflare.com/plans/zero-trust-services/)). Both the
tunnel daemon and the device client are marked available on all three. A seat is consumed
per person, not per device: "A user consumes a seat when they perform an authentication
event"
([Seat management](https://developers.cloudflare.com/cloudflare-one/team-and-resources/users/seat-management/)).
Five people on the free tier would give up the SLA, drop to 24-hour log retention and lose
ticket support; at five users the paid tier is about $35 per month.

**Traffic path, and what it costs two colleagues in one office.** This is the finding that
matters most, and Cloudflare states it in as many words. The feature has been renamed —
"Cloudflare Mesh was previously known as WARP Connector and peer-to-peer connectivity" —
and despite the former name nothing about it is peer-to-peer in the network sense. From
the section of the overview page that maps Tailscale and WireGuard concepts onto Mesh:

> Traffic routes through the nearest Cloudflare data center, not directly between devices.

and, earlier on the same page:

> All traffic passes through Cloudflare, so Gateway network policies, device posture
> checks, and access rules apply to every connection.

([Cloudflare Mesh](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-mesh/))

The page's own topology diagram draws every participant connected to a single "Cloudflare
network" vertex and to nothing else. Elsewhere the docs say two laptops "can reach each
other directly by Mesh IP", which means without a Mesh node in between, not without
Cloudflare; the overview sentence governs. Every participant gets an address from
`100.96.0.0/12`, and both ends must be enrolled: "Both devices must be enrolled in your
Cloudflare account for the connection to work"
([Device to device](https://developers.cloudflare.com/cloudflare-one/setup/replace-vpn/device-to-device/)).

Cloudflare frames the edge hop as the point of the design, and for remote access that is a
defensible trade. For two colleagues sitting in the same office it is a straight cost: a
request that could cross a switch instead leaves the building, reaches the nearest
Cloudflare data centre, and comes back. Cloudflare publishes no latency figure for that
path, so the size of the penalty depends on where the office is relative to an edge — but
its existence is documented rather than inferred, and the sentence above exists precisely
to distinguish Cloudflare from products that attempt a direct path.

Two operational traps are documented and worth carrying into any spec: the default Split
Tunnel excludes the CGNAT range, so Mesh silently does not work until it is removed, and
"Windows Firewall blocks inbound traffic from `100.96.0.0/12`"
([Client devices](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-mesh/client-devices/)).

**Stable name per machine.** No. Mesh gives a stable per-device address, not a name, and
the docs say so by omission in the same concept-mapping table that produced the quote
above: Tailscale's "MagicDNS / custom DNS" maps to "Local Domain Fallback + Gateway
resolver policies" — that is, bring your own naming. The team domain
`<team-name>.cloudflareaccess.com` is not a per-machine name either; it is "a unique
subdomain assigned to your Cloudflare account … where your users will find the apps you
have secured"
([Getting started FAQ](https://developers.cloudflare.com/cloudflare-one/faq/getting-started-faq/)).

A name therefore means a tunnel hostname on a domain the team owns: "When you create a
tunnel, Cloudflare generates a subdomain at `<UUID>.cfargotunnel.com`. You point a CNAME
record at this subdomain to route traffic from your hostname to the tunnel", and "The
`cfargotunnel.com` subdomain only proxies traffic for DNS records in the same Cloudflare
account"
([Routing to a tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/routing-to-tunnel/dns/)).
Protecting it with Access requires "An active domain on Cloudflare". The one hostname that
needs no domain is a Quick Tunnel, and it is disqualified twice over: "TryCloudflare will
launch a process that generates a **random** subdomain on `trycloudflare.com`" — so it is
not stable — and "Quick Tunnels are intended for testing and development only".

The upside of owning the domain is that the team controls every label, which fits proximo's
peer-qualified host grammar better than any fixed vendor namespace.

**Publicly trusted certificate.** Free, with a depth caveat that has an escape hatch. "By
default, Cloudflare issues — and renews — free, unshared, publicly trusted SSL certificates
to all domains added to and activated on Cloudflare", on every plan
([Universal SSL](https://developers.cloudflare.com/ssl/edge-certificates/universal-ssl/)).
On a full setup the coverage stops one level down:

> When you rely only on Universal SSL in a full setup zone, coverage is limited to the root
> domain (for example, `example.com`) and first-level subdomains (for example,
> `www.example.com` or `blog.example.com`). Deeper subdomains — such as
> `dev.www.example.com` or `app3.dev.www.example.com` — are not covered and will not serve
> a valid certificate.

([Universal SSL limitations](https://developers.cloudflare.com/ssl/edge-certificates/universal-ssl/limitations/))

A peer-qualified host carrying both a service label and a peer label is three levels deep,
so on a full setup it is uncovered, and the remedy — Total TLS — "is available for domains
that have purchased Advanced Certificate Manager", itself listed as a "Paid add-on" at
every plan tier including Enterprise. But the same limitations page records an exemption
that changes the answer:

> On a CNAME setup zone, each subdomain (regardless of level) has its own Universal SSL
> certificate and does not require additional features or purchases. As long as the
> subdomains are proxied to Cloudflare, a universal certificate will be provisioned.

So a publicly trusted certificate at arbitrary depth is achievable at no extra cost, on a
partial (CNAME) setup zone. That is a real finding rather than a workaround, and it is the
one respect in which Cloudflare's already-owned entitlement stretches further than it first
appears.

## Tailscale

**Licence.** Settled above: the free Personal plan is documented as non-commercial, so it
does not qualify for a five-person company's work mesh.

**Limits on the free tier.** Ample, which is exactly why the licence is the only obstacle.
The pricing comparison gives Personal as "$0 for up to 6 users" with "Unlimited" user
devices and "Up to 3 ACL groups". Five colleagues fit inside six with room to spare. The
plan is disqualified by its terms, not by its ceiling.

**Cost at five users.** Standard is "$8 per user, per month" — $40 per month; Premium is
"$18 per user, per month" — $90. Tagged resources are metered separately, "50 tagged
resources included; add more for $1/month each", which five developers will not exceed.
There is a runway before committing: "Business customers can enjoy a 14-day free trial of
the product with no user limit."

**Traffic path.** Direct where it can be, relayed where it cannot, with the sequence
documented explicitly:

> All connections start as relayed through a DERP server, and Tailscale then tries to
> upgrade them to a direct connection. If that fails, Tailscale tries to connect them using
> a peer relay. If that fails, the connection remains relayed through the DERP server.

([Connection types](https://tailscale.com/kb/1257/connection-types))

A direct connection "is a peer-to-peer connection between two tailnet devices where they
can send packets directly to each other over UDP … it offers higher throughput and lower
latency", and "In most environments, Tailscale successfully establishes direct connections,
but some network configurations (hard NAT) can prevent this". A developer can check which
they got: `tailscale status` prints `direct 140.82.13.138:41641` or `relay "tor"` per peer.
Relayed traffic is not exposed to the relay — "it's impossible for a DERP server to decrypt
your traffic"
([DERP servers](https://tailscale.com/docs/reference/derp-servers)).

For two colleagues in the same office the expected outcome is a direct UDP connection with
no intermediary, and therefore LAN-order latency. The qualifier belongs in the closing
section: Tailscale documents that a direct connection has no intermediary, but no reference
page states that endpoint discovery gathers local addresses, so "it stays on the LAN" is a
reasonable inference rather than a quoted fact.

**Stable name per machine.** Yes. MagicDNS "automatically registers DNS names for devices in
your network, using their machine name", and each tailnet has "a tailnet DNS name like
`tailNNNN.ts.net` or `tailnet-NNNN.ts.net`", with the option to "generate and select a
randomized tailnet DNS name generated by Tailscale, like `yak-bebop.ts.net`". A machine is
reachable at `machine-name.tailNNNN.ts.net` over HTTPS and at the bare `machine-name`
without it
([Set up HTTPS certificates](https://tailscale.com/kb/1153/enabling-https)).

**Publicly trusted certificate.** Yes, free, automatic — and this is Tailscale's decisive
advantage. Enabling HTTPS in the admin console and running `tailscale cert` causes Tailscale
to "automatically request a certificate for this machine on this domain, using Let's
Encrypt", where "Tailscale creates a `*.ts.net` DNS TXT record for your nodes to complete
their DNS-01 challenges". Keys stay local: "Your certificate's private key and your Let's
Encrypt (ACME) account's private key are generated and stored locally on your machine and
Tailscale never sees them." Three caveats are documented. Machine names enter the public
Certificate Transparency ledger, so "Do not enable the HTTPS feature if any of your machine
names contain sensitive information". Bare names get nothing: "You cannot obtain an HTTPS
URL to go to a bare hostname, such as `https://machine-name`." And file-delivered
certificates are the operator's to renew, whereas those obtained through the local client
API "will automatically be renewed without the user doing anything".

Of the four candidates this is the only one that hands a machine a publicly trusted
certificate for a name it already has, at no extra cost and with no domain to own.

## Headscale

**Licence.** BSD 3-Clause, quoted above; business use is unrestricted and Headscale imposes
no ceiling of its own. There is no cap in the configuration and no licensing code in the
server — the question is treated purely as a scaling one, and the FAQ's answer is that
"under certain conditions, Headscale can likely handle 100s of devices (maybe more)". What
the project does do is scope its ambitions down, in the README:

> Headscale's goal is to provide self-hosters and hobbyists with an open-source server they
> can use for their projects and labs. It implements a narrow scope, a _single_ Tailscale
> network (tailnet), suitable for a personal use, or a small open-source organisation.

That is a statement of intent, not a term, and five colleagues sit comfortably inside it.

**What hosting actually requires.** More than the easy installation suggests. From the
[requirements](https://headscale.net/stable/setup/requirements/):

> - A server with a public IP address for headscale. A dual-stack setup with a public IPv4
>   and a public IPv6 address is recommended.
> - Headscale is served via HTTPS on port 443 and may use additional ports.
> - A reasonably modern Linux or BSD based operating system.
> - A dedicated local user account to run headscale.

The footnote explains why 443 is not really optional: "The Tailscale client assumes HTTPS on
port 443 in certain situations. Serving headscale either via HTTP or via HTTPS on a port
other than 443 is possible but sticking with HTTPS on port 443 is strongly recommended for
production setups." Ports 80 and 443 must be publicly exposed, plus udp/3478 if the embedded
DERP server is enabled.

Two domains are needed, not one: MagicDNS requires `base_domain`, and the server refuses a
configuration where it overlaps `server_url` — the config comment reads "This domain _must_
be different from the server_url domain", enforced in `hscontrol/types/config.go` by
`errServerURLSame` and `errServerURLSuffix`.

Relaying is where the hosting story gets interesting. The embedded DERP server is off by
default and the shipped configuration points at somebody else's relays instead:

```yaml
derp:
  server:
    enabled: false
  urls:
    - https://controlplane.tailscale.com/derpmap/default
```

Headscale's [DERP reference](https://headscale.net/stable/ref/derp/) characterises those as
"the list of free-to-use DERP servers offered by Tailscale Inc.", and warns that removing
them leaves "a single point of failure". So a default Headscale deployment relays company
traffic through infrastructure belonging to a vendor the company has no account with —
which is a licence-adjacent question no primary source settles, and it is recorded below.

Two more constraints will surprise anyone planning a container deployment. The README states:
"Please note that we do not support nor encourage the use of reverse proxies and container to
run Headscale." And there is no administrative interface: "Headscale doesn't provide a
built-in web interface but users may pick one from the available options"
([Web UI](https://headscale.net/stable/ref/integration/web-ui/)), the options being
third-party projects carried under a "not maintained by the headscale authors" banner.

**Traffic path.** Identical to Tailscale's, because the client is Tailscale's, unmodified:
direct UDP where NAT traversal succeeds, a peer relay next, DERP last. The control server is
never in the data path — the README describes it as "an exchange point of Wireguard public
keys for the nodes in the Tailscale network".

**Stable name per machine.** Yes, and the shape is configurable rather than vendor-fixed.
`config-example.yaml`: "Defines the base domain to create the hostnames for MagicDNS … The
FQDN of the hosts will be `hostname.base_domain` (e.g., _myhost.example.com_)." The source
agrees — `Node.GetFQDN` composes exactly two segments, the node's given name and the base
domain, with no user component. That last part is recent and deliberate; the CHANGELOG
records the removal of `dns.use_username_in_magic_dns` with the note "Having usernames in
magic DNS is no longer possible", bringing "Headscales behaviour in line with Tailscale".
Collisions resolve to a numeric suffix (`laptop`, `laptop-1`, `laptop-2`), which is worth
noting against proximo's own Collision semantics.

**Publicly trusted certificate.** No — and this is what decides Headscale's fate for this
use. The [feature support page](https://headscale.net/stable/about/features/) lists MagicDNS,
Taildrop, Taildrive, tags, subnet routers, exit nodes, dual stack, ephemeral nodes, the
embedded DERP server, peer relays, ACLs and Tailscale SSH as supported; OIDC as partial
("OIDC groups cannot be used in ACLs"); and Funnel, Serve and network flow logs as not
supported. HTTPS certificates appear in neither column.

The answer is in the server's own routing table. Tailscale's certificate flow depends on the
control server writing an ACME TXT record on the node's behalf, and Headscale registers that
endpoint as unimplemented — `hscontrol/noise.go`:

```go
// client sends a [tailcfg.SetDNSRequest] to this endpoints and expect
// the server to create or update this DNS record "somewhere".
// It is typically a TXT record for an ACME challenge.
r.Post("/set-dns", ns.NotImplementedHandler)
```

The maintainers' tracking issue,
[#2527 "tailscale cert + serve tracking"](https://github.com/juanfont/headscale/issues/2527),
open since April 2025 and milestoned v0.34, says the same thing in prose: "there are no ETA
for this as we have a bunch of other things to do first", and "`tailscale cert` requires
headscale to implement `/machine/set-dns`, which in terms requires headscale to automatically
create TXT ACME records 'somewhere' for the given domain." All four items on its checklist are
unticked, and the proof-of-concept
[PR #2696](https://github.com/juanfont/headscale/pull/2696) and the libdns challenge
[PR #3321](https://github.com/juanfont/headscale/pull/3321) are open and unmerged.

The same missing endpoint blocks `tailscale serve` over HTTPS and Funnel. The same file
declines several other control endpoints — `/audit-log`, `/id-token`, `/set-device-attr`,
`/c2n` — so there is no audit-log ingestion and no control-plane-initiated client operations
either. Headscale removes Tailscale's licence problem, keeps its naming, and loses precisely
the feature that made Tailscale worth wanting here.

**Official clients.** Less degraded than one might fear. Tailscale itself documents pointing
its clients at a third-party control plane
([Set up a custom control server](https://tailscale.com/docs/how-to/set-up-custom-control-server)):
"The Tailscale clients let you specify a custom control server URL instead of the default
`https://controlplane.tailscale.com` server. If you are using a self-managed deployment of
Headscale as your control plane, use your Headscale instance's URL." The App Store macOS and
iOS builds both expose it in the UI, as do Android and tvOS. The single gap is Windows: "The
Tailscale user interface for Windows doesn't support adding a custom control server URL yet.
However, you can use the Tailscale CLI or edit registry settings." Headscale for its part aims
"to support the last 10 releases of the Tailscale client".

## NetBird

**Licence.** Split as described above: BSD 3-Clause client, AGPLv3 control plane
(`management/`, `signal/`, `relay/`, `combined/`, and in fact `proxy/`), AGPLv3 dashboard.
Neither licence restricts business use.

**Free tier and its limits.** [netbird.io/pricing](https://netbird.io/pricing) gives the Free
plan as "€ 0 user / month", "up to 5 users", "100 machines", described as "For individuals or
small teams looking for an easy-to-use and secure connectivity", including "P2P connections &
encryption", "Private DNS", "Access controls" and "Network Routes". Five colleagues fit it
exactly, with no headroom for a sixth.

On business use the honest answer is that nothing forbids it and nothing expressly permits it.
Neither the pricing page nor [netbird.io/terms](https://netbird.io/terms) contains a
non-commercial or personal-use clause; the terms distinguish free service only on liability,
limiting it to claims "based on willful intent (Vorsatz) or gross negligence (grobe
Fahrlässigkeit)". The words "or small teams" in the plan description are the closest thing to
an affirmative statement. This is an argument from absence and is recorded as such below — but
absence of a restriction is a materially stronger position than Tailscale's, where an explicit
if softly worded statement points the other way.

**Cost at five users if paid.** Team is "€ 6 user / month" with "unlimited users" and "100
machines + 10 per user" — €30 per month for five. Business is "€ 12 user / month" — €60.
Additional machines are "€ 0.50 / month" each, and NetBird bills only "for active users and
machines that connected or logged in at least once during the billing period".

**Traffic path.** Direct first, relayed as fallback, and NetBird's docs are the most explicit
of the four about the same-office case.
[How NetBird works](https://docs.netbird.io/about-netbird/how-netbird-works) describes machines
that "form a mesh network connecting to each other directly via an encrypted point-to-point
WireGuard tunnel", a Signal service that "does not store any data and no traffic passes through
it", and a Relay whose "purpose … is to gracefully implement a 'Plan B' by relaying traffic
between peers when a direct point-to-point connection is not possible".
[Understanding NAT and connectivity](https://docs.netbird.io/about-netbird/understanding-nat-and-connectivity)
gives the candidate ordering:

> ICE performs these checks in priority order, preferring direct connections over relayed ones:
> Priority 1: Host ↔ Host (direct LAN connection) / Priority 2: Host ↔ Server Reflexive (direct
> through NAT)

That first line is the only place among these four vendors where the same-LAN direct path is
named outright. Relaying is reserved for the cases the same page enumerates — both peers behind
symmetric NAT, UDP blocked, deep packet inspection, "Extremely restrictive corporate networks" —
and relayed traffic stays end-to-end encrypted: "The relay server cannot decrypt or inspect your
traffic - it only forwards encrypted packets between peers."

**Stable name per machine.** Yes. [DNS](https://docs.netbird.io/manage/dns) states that "NetBird
automatically assigns a domain name to each peer in a private `netbird.cloud` space. You can
access machines using names like `my-server.netbird.cloud` instead of IP addresses", available
"on both NetBird Cloud and self-hosted versions". The suffix is not the same in both, though the
docs never say so: the source sets `defaultSingleAccModeDomain = "netbird.selfhosted"`
([`management/cmd/defaults.go`](https://raw.githubusercontent.com/netbirdio/netbird/main/management/cmd/defaults.go)),
matched by `NETBIRD_MGMT_DNS_DOMAIN=netbird.selfhosted` in
[`setup.env.example`](https://raw.githubusercontent.com/netbirdio/netbird/main/infrastructure_files/setup.env.example),
and it is overridable via `--dns-domain`. One caveat to carry into any spec: on macOS and Windows
those names resolve only when nameservers are configured — "Without nameservers configured,
NetBird doesn't modify DNS. Peer domain names like `my-server.netbird.cloud` won't resolve." On
Linux they always do.

**Publicly trusted certificate.** No primary source says either way. The only TLS page in the
documentation,
[Certificates](https://docs.netbird.io/selfhosted/troubleshooting/certificates), deals
exclusively with the Let's Encrypt certificate for the deployment's own domain — the dashboard
and Management endpoint — and nothing addresses per-peer names. Structurally a public CA could
not validate them, the namespace being private and resolved by an in-client resolver over the
`100.64.0.0/10` overlay. But that is inference, and it is listed below as a gap rather than
reported as a finding.

**What self-hosting requires.** Substantially less than it used to, and the older guides mislead
on this point. The [quickstart](https://docs.netbird.io/selfhosted/selfhosted-quickstart) asks
for "A Linux VM with at least **1CPU** and **2GB** of memory", a VM "publicly accessible on
**TCP ports 80 and 443**, and **UDP port 3478**", and "A **public domain** name that resolves to
the VM's public IP address". The bundled Traefik "handles TLS certificates automatically via
Let's Encrypt". The identity-provider requirement is gone:
[Local users](https://docs.netbird.io/selfhosted/identity-providers/local) records that "Starting
with version 0.62, NetBird **no longer requires an external identity provider** … so you can get
started without setting up Zitadel, Keycloak, or any other IdP", powered by "an embedded Dex
server running within the NetBird Management service". Relaying, though, becomes the operator's
problem: "When using the self-hosted version, you need to set up your own relay servers. This a
complex task and requires additional maintenance effort"
([Self-hosted vs cloud](https://docs.netbird.io/about-netbird/self-hosted-vs-cloud)).

**What is degraded self-hosted.** The same page enumerates what the cloud has and the self-hosted
build does not: "Users and groups provisioning from your identity provider (IdP)", "Traffic events
logging", "Event streaming to 3rd party platforms and SIEM systems (also available in self-hosted
deployments with an enterprise license)", "Integrations with EDR like CrowdStrike and others",
"Peer approval to join the network", "User invites", and "MSP functionality". Peer approval is
independently confirmed cloud-only:
[Approve peers](https://docs.netbird.io/manage/peers/approve-peers) states "This feature is only
available in the NetBird cloud version." Four of those — high availability, SCIM, EDR and MDM
integrations, traffic-flow logging — are unlocked by a commercial licence key set as
`NB_LICENSE_KEY` ([Enterprise](https://docs.netbird.io/selfhosted/enterprise)), which also confirms
that "the **open source Community Edition is free to self-host for as long as you like**, with no
license and no time limit". Peer approval and user invites appear on the cloud-only list but not on
the licence table, which suggests they are unavailable self-hosted at any price; the two pages are
not reconciled.

## What the evidence favours, and where it is thin

No candidate satisfies every constraint. The choice is which constraint to relax, and the evidence
at least makes that trade legible.

**Cloudflare is stronger than the ticket assumed, and weaker where it matters most.** Its
already-owned entitlement does after all reach a publicly trusted certificate at arbitrary
subdomain depth, free, provided the zone uses a partial (CNAME) setup — the exemption quoted above
is explicit and unconditional. What it cannot do is get out of the way. Every packet between two
colleagues is documented as routing "through the nearest Cloudflare data center, not directly
between devices", so a mesh whose whole purpose is interactive use between people who are often in
the same room pays an edge round-trip on every request, permanently, for policy enforcement the
team may not want between its own five laptops. It also provides no per-machine name; naming is
entirely the team's to build.

**Tailscale is the only candidate that solves naming and TLS together, and it is not free.**
`tailscale cert` issues a Let's Encrypt certificate for `machine-name.tailNNNN.ts.net` with the
private key never leaving the machine, at no charge and with no domain to own, on top of a direct
peer-to-peer data path. Headscale cannot do this and has no ETA; NetBird's per-peer namespace is
private and no certificate story is documented; Cloudflare can, but only for names the team invents
and hosts itself, and only over a relayed path. Tailscale Standard for five people is $40 per month
— which means the constraint being relaxed is "no new licence", and the parent map should be told
so explicitly rather than discovering it during implementation.

**If proximo is willing to carry TLS itself, NetBird's free tier fits the licence constraint best.**
Its single strongest supporting fact: the Free plan is "up to 5 users" at "€ 0 user / month",
described as "For individuals or small teams", with no non-commercial clause anywhere in its pricing
page or its terms — the only candidate that is both free and unrestricted at exactly this headcount.
It also has the best-documented direct path, being the only one whose docs name "Host ↔ Host (direct
LAN connection)" as the first-priority ICE candidate, and it gives a stable per-machine name out of
the box. The cost is that certificates become proximo's problem — and proximo already runs a CA
([docs/architecture.md — TLS and trust](../architecture.md#tls-and-trust)), so this is a question the
spec can answer rather than a dead end. It is, notably, the question the parent map already lists as
unspecified under "Interaction with the local CA". The exact-fit ceiling is the risk: a sixth
colleague costs €30 a month, not €6.

**Headscale is the near-miss.** It removes the licence problem entirely, keeps Tailscale's client and
its naming, and then fails on the one feature that made Tailscale attractive. It also brings a public
host, two domains, no admin interface, and a maintainer position against both containers and reverse
proxies.

### Where the evidence is thin

Each of these is a point where no primary source settles the question. They are named rather than
resolved by inference, because a named gap is more useful than a confident guess.

1. **Tailscale's non-commercial restriction is hortatory, not prohibitive.** The words are "only
   suitable for non-commercial use" and "not intended for commercial use". Nowhere — not in the Terms,
   not in the [Acceptable Use Policy](https://tailscale.com/tailscale-aup), which is the document that
   enumerates what a customer may not do and which says nothing about commercial use at all — is there
   a clause reading "you may not use the Personal plan for commercial purposes". §2.1 makes the grant
   plan-dependent and the Pricing page supplies the plan's character, which is enough to reach the
   conservative reading adopted here, and Tailscale enforces it operationally by email domain. But the
   text does not close the question, and anyone taking the opposite view would not be contradicting a
   quoted prohibition.

2. **Whether Tailscale's HTTPS certificate feature is available on the free Personal plan.** The
   pricing comparison table has no row for it and the KB names no plan. Moot if Personal is
   disqualified, but unsettled on its own terms.

3. **Whether two Tailscale peers on the same LAN keep their traffic on the LAN.** The connection-types
   KB says a direct connection sends packets "directly to each other over UDP" with no intermediary,
   which strongly implies it, but no Tailscale reference page documents that endpoint discovery gathers
   local addresses. NetBird documents the equivalent outright; Tailscale's counterpart could not be
   found.

4. **Whether Headscale's default reliance on Tailscale's public DERP relays is licensed.** The shipped
   configuration points `derp.urls` at `https://controlplane.tailscale.com/derpmap/default`, so an
   out-of-the-box deployment relays company traffic through Tailscale's infrastructure without a
   Tailscale account. Headscale calls them "free-to-use", but that is Headscale's characterisation;
   neither Tailscale's Terms nor its AUP addresses a non-customer's use of them. This bears directly on
   whether "Headscale is free for business use" is true of the deployment as shipped rather than only
   of the code. It is cheaply avoided — enable the embedded DERP server and drop the dependency — but
   it should be decided, not inherited.

5. **Whether NetBird's free plan permits business use is settled only by absence.** No clause grants it;
   no clause forbids it. "For individuals or small teams" is a plan description rather than a term. A
   written confirmation from NetBird would be cheap to obtain and worth having before the plan is
   depended on.

6. **Whether NetBird issues a publicly trusted certificate for a per-peer name.** No NetBird page
   addresses it in either direction. The conclusion that it does not is drawn from the namespace being
   private and from the total absence of peer-certificate documentation, not from a NetBird statement.

7. **NetBird's repository disagrees with itself about which directories are AGPL.** The root LICENSE
   names four (`management/`, `signal/`, `relay/`, `combined/`);
   [`LICENSES/REUSE.toml`](https://raw.githubusercontent.com/netbirdio/netbird/main/LICENSES/REUSE.toml)
   and the README name three, omitting `combined/`; and `proxy/LICENSE` is AGPLv3 on disk while being
   named by none of the three. This changes nothing for a five-person internal deployment, but it makes
   any statement of the form "NetBird is BSD licensed" wrong, and the discrepancy is worth raising
   upstream rather than working around.

8. **Which Cloudflare plan Cloudflare Mesh requires, and whether it is Beta.** No page states a plan
   requirement. The documentation navigation carries a Beta badge against Cloudflare Mesh, but no Beta
   notice appears in any page body, so its status cannot be confirmed — and if it is Beta, §5 of the
   Self-Serve Subscription Agreement applies, under which Cloudflare "may discontinue, suspend, or
   remove Beta Services … at any time". For a transport a team would build a workflow on, that is worth
   settling with Cloudflare directly.

9. **How much latency the Cloudflare edge hop actually costs.** Cloudflare publishes no figure and no
   guarantee for the Mesh path. The hop is documented; its size is not.

10. **The scope of §2.2.1(j) of Cloudflare's Self-Serve Subscription Agreement**, which prohibits using
    the Services to "provide a virtual private network or other similar proxy services". The natural
    reading is that it forbids reselling connectivity, not replacing your own VPN — which is the
    product's advertised purpose, with an entire documentation section titled "Replace your VPN". But
    the clause is broadly worded and Cloudflare publishes no clarifying note.

11. **Whether Headscale supports app connectors or tailnet lock.** Neither appears anywhere in its
    documentation, changelog or feature list, and no key-authority implementation exists in the server.
    They are unsupported by omission, which is not the same as an authoritative statement.

12. **Advanced Certificate Manager's price.** The documentation says only "Paid add-on" at every plan
    tier and gives no figure on any readable page. This matters only if the team uses a full setup zone
    rather than the CNAME setup that avoids the charge entirely.

13. **NetBird's self-hosted platform support window.** The official support matrix at
    `docs.netbird.io/help/support-matrix/self-hosted` is a stub in which every operating-system and
    dependency row reads "TBD".
