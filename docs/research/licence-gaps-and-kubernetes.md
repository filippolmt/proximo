# The licence gaps, and self-hosting on Kubernetes

> **Addendum — this research was overtaken by the decision it fed into.**
> It is a snapshot of what was known when it was written, kept for its evidence and its
> reasoning, and it is **not current advice**. Two of its conclusions were reversed:
>
> - It argues that the **vendor-managed free tier** is the option to adopt. The team turned out
>   to be six people rather than five, which converts that tier to a paid plan and puts it
>   outside constraint 2 on
>   [#75](https://github.com/filippolmt/proximo/issues/75).
> - It argues that **self-hosting is impractical**. That was established against one deployment
>   shape — the team's Kubernetes cluster, whose nodes hold no external addresses — and not
>   against self-hosting as such. The team self-hosts on a VM, where the relay's STUN endpoint is
>   directly reachable on UDP/3478.
>
> It also records, as its own evidence gap, that "the Self-Hosted EULA does not exclude the
> open-source Community Edition from its scope". That gap is now closed by the repository's own
> `LICENSE`, which puts `management/`, `signal/` and `relay/` under **AGPLv3** — a licence that
> affirms unlimited permission to run the unmodified program and forbids further restrictions on
> that right. The EULA's thirty-day Proof of Concept clause governs the commercially licensed
> distribution, not the Community Edition.
>
> The decision this research informed, with the full reasoning:
> [The transport we adopt](https://github.com/filippolmt/proximo/issues/84) — **self-hosted
> NetBird**.

Research for [issue #86](https://github.com/filippolmt/proximo/issues/86), under the
constraints charted in [issue #75](https://github.com/filippolmt/proximo/issues/75) and
following the findings of [issue #76](https://github.com/filippolmt/proximo/issues/76).
Three questions were left open. The first decides whether the cheapest candidate is
usable at all; the second decides whether any self-hosted candidate can run on the
cluster the team already has; the third closes an evidence gap that could make a free
option quietly not free.

Sources here are vendor terms of service, end-user licence agreements, pricing pages,
official product documentation, project source files, and official Kubernetes and cloud
provider reference documentation. Where a question turns on a form of words, the words
are quoted rather than paraphrased. Where no primary source answers, the absence is
stated as the finding rather than filled in.

No real domain, address, cluster name or project name appears anywhere below, per
constraint 7 on #75. The cluster is described by capability only, and placeholders stand
in for anything the operator supplies.

## 1. Whether NetBird's free tier permits business use

### The complete legal corpus, established rather than assumed

The first thing to establish is what the governing documents actually are, because
"nothing addresses it" is only a finding if the search was exhaustive. NetBird's site
map enumerates every published legal page, and there are six:
[`/terms`](https://netbird.io/terms), [`/self-hosted-EULA`](https://netbird.io/self-hosted-EULA),
[`/privacy`](https://netbird.io/privacy), `/candidate-privacy`, [`/cla`](https://netbird.io/cla)
and [`/imprint`](https://netbird.io/imprint)
([sitemap-0.xml](https://netbird.io/sitemap-0.xml)). The footer of the
[pricing page](https://netbird.io/pricing) lists the same set under the heading "Legal",
minus the recruitment privacy notice.

There is **no acceptable use policy**. NetBird publishes none, at any URL, under any
name. This matters because the acceptable use policy is conventionally the document that
enumerates prohibited uses, and it is where Tailscale's counterpart question was looked
for and also came back empty (#76, gap 1). For NetBird the document does not exist at
all, so there is nothing to read either way.

### What the Terms of Service say, and to whom they apply

The [Terms of Service](https://netbird.io/terms) settle the question in the opposite
direction from the one the ticket anticipated. They do not permit business use as an
exception; they are scoped to business use as their subject. § 1, "Scope", reads:

> These terms of service by NetBird GmbH for the use of the NetBird tool (as applicable
> from time to time "ToS") apply for contracts between NetBird GmbH ("NetBird") and its
> customers as business persons (section 14 German Civil Code, Bürgerliches Gesetzbuch
> (BGB)) ("Customer") regarding the use of the 'NetBird' IT tool (software-as-a-service)
> ("Software" or "Tool") as described in detail on https://netbird.io ("Website") and and
> related services (the Software and related services jointly the "Service(s)"). The
> provided scope of the Services depends on the package the Customer subscribes for.

Section 14 BGB is the German Civil Code's definition of an *Unternehmer* — an entrepreneur
or business, as opposed to a *Verbraucher*, a consumer, defined at section 13. A contract
governed by these Terms is by its own scope clause a contract with a business. The last
sentence then makes the *scope of the Services* — not the character of the user — the thing
that varies by package.

The Terms contemplate a free package within that business-only scope explicitly. § 10,
"Liability of NetBird", opens:

> For services by NetBird free of charge the following shall apply: To the extent that
> NetBird provides services to you free of charge, NetBird shall only be liable in
> accordance with the statutory provisions, provided that claims for damages are based on
> willful intent (Vorsatz) or gross negligence (grobe Fahrlässigkeit).

So the document whose scope clause restricts it to business persons also legislates for
the case where NetBird charges nothing. A free-of-charge service under these Terms is
therefore, by construction, a service supplied to a business person. That is a materially
stronger position than the bare absence #76 was able to report.

§ 8, "Prohibited Use", is the nearest thing NetBird has to an acceptable use policy, and
it is worth confirming what it does *not* say. It prohibits unlawful and abusive content,
interference with the service, reverse engineering, harvesting personal data, and:

> use the Services for any other purpose than provided for in these ToS, in particular,
> not offer the Services to unauthorized third parties or sell, sub-license, lease,
> transfer or otherwise commercially exploit the Software;

The prohibited commercial act is *exploiting the Software commercially* — reselling it,
sub-licensing it, offering it on to third parties. Using it to run one's own company's
network is not among the prohibitions, and no clause anywhere in § 8 distinguishes a paid
package from a free one.

### What the pricing page says

The [pricing page](https://netbird.io/pricing) describes the Free plan as:

> Free — For individuals or small teams looking for an easy-to-use and secure connectivity.
> €0 user / month — up to 5 users — 100 machines

"For individuals or small teams" is a description of who the plan suits, not a term of
use, and — unlike Tailscale's "only suitable for non-commercial use" and "not intended for
commercial use" (#76) — it draws no line at commerce. It draws a line at *size*. A
five-person company is a small team.

The pricing page incorporates nothing by reference. It carries no "by subscribing you
agree" notice and no link into the Terms other than the site-wide footer.

### The answer to part 1's first question

**Two of the three questions the ticket poses about the free tier are answered, and the
third is answered in the negative.**

- Does any governing document *address* commercial or business use of the free tier?
  Yes — the Terms of Service, whose § 1 scope clause confines the entire contract to
  customers "as business persons" and whose § 10 provides for services rendered free of
  charge under that same contract.
- Does any governing document *forbid* business use of the free tier? No. Nothing in the
  Terms, the pricing page, the self-hosted EULA or the documentation restricts the free
  plan to non-commercial use, and no acceptable use policy exists that might.
- Does any document *expressly grant* business use of the free tier in so many words —
  a sentence reading "the Free plan may be used for business purposes"? **No.** The
  conclusion is reached by reading the scope clause together with the free-of-charge
  clause, not by quoting a permission. That inference is recorded as a gap below rather
  than presented as a quotation.

This is a genuine advance on #76, which could report only silence. The silence turns out
to be silence *inside a business-to-business contract*, which is a different thing from
silence.

### What a sixth user costs

The free plan's ceiling is five users, and the team is five people, so the cost of the
sixth is a live number rather than a hypothetical. The
[pricing page](https://netbird.io/pricing) gives the Team plan as "€6 user / month —
unlimited users — 100 machines + 10 per user".

The decisive question is whether the first five users stay free once the sixth arrives.
They do not. The [plans and billing
documentation](https://docs.netbird.io/manage/settings/plans-and-billing) works the
arithmetic:

> **Example**: Adding 20 extra machines to a Team plan with 10 users:
> Base plan cost: (10 users × €6/user) = €60

Ten users are billed at ten times the per-user rate; there is no deduction for a free
allowance. The pricing calculator on the pricing page does the same, computing
"100 Users x €6 / month = €600". The free tier is a *plan*, not a discount carried into
the paid plans.

**So the sixth user costs €36 per month, not €6.** Adding one person converts the whole
account from Free to Team and bills every active user on it. The step is from €0 to €36
per month, a discontinuity of the full amount rather than a marginal seat charge.

Two details soften the edge without changing it. Billing is by *active* user —

> A user or machine counts as "active" only if it connects or logs in (including the
> admin dashboard) at least once during the billing period.

([plans and billing](https://docs.netbird.io/manage/settings/plans-and-billing)) — so a
colleague who is enrolled but dormant for a month is not billed for that month. And the
plan may be downgraded again, subject to a documented cooling-off: "After changing your
plan, you need to wait 48 hours from the last update before you can change it again."

### A separate licence problem for NetBird *self-hosted*

This is not what part 1 asked, but it was found while reading the same corpus and it
bears directly on part 2, so it is recorded here rather than discovered later.

The [Self-Hosted EULA](https://netbird.io/self-hosted-EULA) is a distinct contract from
the Terms, and it binds by conduct: "BY INSTALLING, DEPLOYING, OR USING THE SOFTWARE,
CUSTOMER EXPLICITLY AGREES TO BE BOUND BY THIS AGREEMENT." Its § 3.2 reads:

> If the Software is installed without an active paid subscription License Key, or under
> an authorized evaluation configuration, it shall be deemed a Proof of Concept (PoC)
> package. NetBird grants Customer a non-exclusive, non-transferable, revocable license to
> use the Software solely for Customer's internal testing and evaluation. Any commercial,
> productive, or external use, including resale, sublicensing, or offering the Software as
> part of a service, is strictly prohibited during the PoC phase.

and § 14.2 puts a clock on it: "A PoC evaluation installation runs for a default period of
thirty (30) days from installation unless otherwise extended by NetBird."

Read literally, that would make an unlicensed self-hosted NetBird control plane a
thirty-day evaluation that may not be used productively — which is precisely what this
effort would be doing with it. The documentation says the opposite. The [Enterprise
Commercial License page](https://docs.netbird.io/selfhosted/enterprise) states:

> You can start evaluating today for free: the open source Community Edition is free to
> self-host for as long as you like, with no license and no time limit.

The two can be reconciled: the EULA defines "Software" as "The object-code version or
binaries of NetBird's on-premise software, including updates, patches, and versions
delivered by NetBird", which reads as the commercial distribution rather than the
AGPLv3-licensed Community Edition built from source, and § 9.2 keeps the two apart —
"Open-source components are governed by their respective open-source licenses; those
licenses do not expand Customer's rights in NetBird proprietary software." But the EULA
contains no clause excluding the Community Edition from its scope, and the reconciliation
is inference. It is named as a gap.

## 2. Whether any of this can run on the cluster

### The cluster, restated as capabilities

Constraint 8 on #75 binds anything the team hosts to Kubernetes. The cluster in question
is a regional managed Kubernetes service on a major cloud provider, with **private nodes
that hold no external addresses**: egress leaves through a managed NAT, and ingress
arrives only through the provider's own L4 passthrough load balancer. Three
`LoadBalancer` Services exist there and every one is TCP; a Service with `protocol: UDP`
has never been created, and apart from cluster DNS there is no UDP workload anywhere.
Exposure is by Gateway API with Envoy Gateway; the experimental Gateway API channel is
being adopted for an unrelated service, so its CRDs are treated below as already present
and already paid for. Applications arrive through a two-level ArgoCD app-of-apps, Helm
only, no Kustomize. Persistent volumes are `ReadWriteOnce` only. cert-manager is
installed with a working DNS-01 issuer whose token is scoped per zone. No WireGuard,
Tailscale, Headscale, NetBird or `cloudflared` workload has ever run there.

The decisive unknown is the UDP one, because a WireGuard mesh needs a publicly reachable
UDP endpoint and this cluster has never had one.

### The decisive question: does the provider's L4 passthrough load balancer do UDP?

**It does.** The cluster runs on Google Kubernetes Engine, and GKE's own overview names
the protocol in the sentence that describes what a `LoadBalancer` Service produces:

> When you create a Service of type LoadBalancer, GKE automatically creates a Layer 4
> (TCP/UDP) Passthrough Network Load Balancer based on the parameters of your Service
> manifest.

([about load balancing](https://cloud.google.com/kubernetes-engine/docs/concepts/about-load-balancing))

and the LoadBalancer Service reference fixes which load balancer an external Service gets:

> External LoadBalancer Services are implemented by using regional external passthrough
> Network Load Balancers. Clients located outside your VPC network and Google Cloud VMs
> with internet access can access an external LoadBalancer Service.

([LoadBalancer Services](https://cloud.google.com/kubernetes-engine/docs/concepts/service-load-balancer))

At the product level, the passthrough load balancer carries UDP in both of its variants.
The backend-service variant: "Backend service-based regional external passthrough Network
Load Balancers can load-balance TCP, UDP, ESP, GRE, ICMP, and ICMPv6 traffic", and
"Regional external passthrough Network Load Balancers support the following protocol
options for each forwarding rule: TCP, UDP, and L3_DEFAULT"
([backend service-based](https://cloud.google.com/load-balancing/docs/network/networklb-backend-service)).
The target-pool variant, which is what an unannotated Service still creates: "Target
pool-based regional external passthrough Network Load Balancers support only IPv4 traffic,
and only support the TCP and UDP protocols"
([target pools](https://cloud.google.com/load-balancing/docs/network/networklb-target-pools)).
The one Google load balancer that could not carry UDP is the proxy Network Load Balancer —
"implemented on the open source Envoy proxy software stack. It can handle only TCP
traffic" ([proxy NLB](https://cloud.google.com/load-balancing/docs/tcp)) — and that is not
what a `LoadBalancer` Service creates.

So the fear the ticket names — that a `Service` of `type: LoadBalancer` might quietly not
cover UDP — is unfounded on this provider. Three details qualify it.

**Mixing TCP and UDP in one Service is possible but version-gated.** Upstream Kubernetes
settled this some time ago: "Load balancers with mixed protocol types — This is a stable
feature in Kubernetes, and has been since the 1.26 release. You can no longer toggle this
feature (the associated feature gate has been removed)"
([Service](https://kubernetes.io/docs/concepts/services-networking/service/)), with the
standing caveat that "The set of protocols that can be used for load balanced Services is
defined by your cloud provider; they may impose restrictions beyond what the Kubernetes API
enforces." GKE's own restriction is a minimum version and an explicit class:

> Mixed-protocol load balancing is generally available from GKE version
> 1.36.2-gke.1498000 and later.

> For new external LoadBalancer Services, set the `spec.loadBalancerClass` field to
> `networking.gke.io/l4-regional-external` in the Service manifest.

([mixed-protocol LoadBalancer Services](https://cloud.google.com/kubernetes-engine/docs/how-to/mixed-protocol-lb))

That matters because both self-hosted candidates want TCP 443 and UDP 3478 on the same
public name. Two Services with two addresses avoids the question entirely, at the cost of
a second address and a second DNS record.

**UDP health checks do not exist in Google Cloud, and GKE covers for it.** The product
documentation is blunt — "Because all supported health check protocols rely on TCP (UDP
health checks are not supported), when you use a regional external passthrough Network Load
Balancer to balance connections and traffic for other protocols, backend VMs must run a
TCP-based server to answer health check probers"
([backend service-based](https://cloud.google.com/load-balancing/docs/network/networklb-backend-service)).
On GKE that requirement is already met by the platform: "Load balancer health check packets
are answered by either the kube-proxy (legacy dataplane) or cilium-agent (GKE Dataplane V2)
software running on each node", and under the default `externalTrafficPolicy: Cluster`
"The load balancer health check port must be TCP port 10256"
([LoadBalancer Services](https://cloud.google.com/kubernetes-engine/docs/concepts/service-load-balancer)).
A UDP-only workload therefore needs no TCP listener of its own.

**Session affinity needs changing from the GKE default for UDP.** Connection tracking
behaves differently: "UDP, ESP, and GRE connections are connection trackable for all
session affinity options except for NONE"
([traffic distribution](https://cloud.google.com/load-balancing/docs/network/ext-netlb-traffic-distribution)),
while "By default, internal and external LoadBalancer Services create passthrough Network
Load Balancers with session affinity set to NONE". For a WireGuard endpoint, where every
packet of a session should reach the same backend, that default is wrong and
`sessionAffinity: ClientIP` is the correction.

**What is not settled.** Two things, and they are named rather than smoothed over. First,
no GKE page documents any UDP-specific restriction for LoadBalancer Services at all — the
word barely appears in the GKE LoadBalancer documentation — so "no restriction is
documented" must not be read as "documented to work" with `externalTrafficPolicy: Local`,
with subsetting, or with weighted load balancing. Second, **no official page states in so
many words that a cluster with private nodes supports *external* LoadBalancer Services.**
The mechanism strongly implies it — "Regional external passthrough Network Load Balancers
use special routes outside of your VPC network to direct incoming requests and health check
probes to each backend VM"
([backend service-based](https://cloud.google.com/load-balancing/docs/network/networklb-backend-service)),
which is delivery by route rather than by node address, and GKE's networking best practices
say that in a cluster with private nodes only "You can control these directional flows by
exposing services by using load balancing and Cloud NAT"
([networking best practices](https://cloud.google.com/kubernetes-engine/docs/best-practices/networking)).
But that sentence does not distinguish internal from external load balancers, and the
sentence that would settle it does not exist. The cluster already runs three external TCP
`LoadBalancer` Services on private nodes, which is a stronger practical answer than the
documentation gives, but it is an observation about the cluster rather than a citation.

### Through the Gateway, or a plain Service?

The team's exposure policy points at Gateway API with Envoy Gateway, so the question is
whether a WireGuard endpoint belongs behind a Gateway at all. It probably does not, and
the reason is specific rather than a preference for the older mechanism.

First, a premise correction. **`UDPRoute` is no longer experimental.** It graduated with
Gateway API v1.6.0:

> The `UDPRoute` resource is GA and has been part of the Standard Channel since `v1.6.0`.

([UDPRoute](https://gateway-api.sigs.k8s.io/reference/api-types/udproute/))

so the experimental channel, which this cluster is adopting anyway for an unrelated
service, is not what would be paying for UDP here.

Envoy Gateway does implement it, and describes it in one paragraph
([Gateway API support](https://gateway.envoyproxy.io/docs/tasks/traffic/gatewayapi-support/)):

> A UDPRoute configures routing of raw UDP traffic through one or more Gateways. Traffic
> can be forwarded to the desired BackendRefs based on a UDP port number.
> __Note:__ Similar to TCPRoutes, UDPRoutes only support proxying in non-transparent mode
> i.e. the backend will see the source IP and port of the Envoy Proxy instance instead of
> the client.

That note is the decisive fact, and Envoy Gateway's design document confirms it is a
deliberate choice rather than an omission: "The implementation will only support proxying
in non-transparent mode i.e. the backend will see the source IP and port of the deployed
Envoy instance instead of the client"
([TCP/UDP design](https://gateway.envoyproxy.io/community/design/tcp-udp-design/)). **A
WireGuard endpoint behind Envoy Gateway would see every peer arriving from the same
address.** WireGuard identifies peers cryptographically rather than by address, so this is
not fatal to authentication — but endpoint roaming and NAT keepalive both work from the
observed source address, and both a Headscale DERP region and a NetBird STUN server exist
precisely to tell a peer what its own external address looks like. A STUN server behind a
proxy that rewrites the source address is answering the wrong question by construction.

The maturity signal is mixed, and worth stating exactly because "the CRD is installed" is
not the same claim. Envoy Gateway's UDPRoute conformance tests pass and are reported
upstream — `UDPRoute`, `UDPRouteWeightedRouting`, `UDPRouteReferenceGrant` and six others
appear under `succeededProvisionalTests` in its
[v1.9.0 conformance report](https://github.com/kubernetes-sigs/gateway-api/blob/main/conformance/reports/v1.6/envoy-gateway/experimental-v1.9.0-default-report.yaml) —
but the certified profiles in that same report are `GATEWAY-TLS`, `GATEWAY-HTTP` and
`GATEWAY-GRPC` only. A `GATEWAY-UDP` profile exists upstream; Envoy Gateway does not claim
it. No Envoy Gateway page states a maturity or support level for UDPRoute at all.

The API itself is deliberately thin, which suits a raw endpoint: "UDPRoute is intentionally
minimal. UDP carries no application-layer metadata that the Gateway can match on, so
traffic is classified only by the listener's `protocol:port`. As a result, UDPRoute has no
hostnames, matches, or filters, and a rule consists only of the backends to forward traffic
to" ([UDPRoute](https://gateway-api.sigs.k8s.io/reference/api-types/udproute/)). TLS may
not be set on a UDP listener; `SecurityPolicy` cannot target a UDPRoute at all (its
documented targets are `Gateway`, `ListenerSet`, `HTTPRoute`, `GRPCRoute` and `TCPRoute`);
and only the oldest attached route receives traffic. So the Gateway contributes no
routing, no policy and no TLS to a UDP endpoint — it contributes a hop that hides the
client address.

The alternative is unremarkable, which is the point. A `Service` of `type: LoadBalancer`
with `protocol: UDP` is ordinary Kubernetes: "Kubernetes supports the following protocols
with Services: SCTP, TCP (the default), UDP", and "You can use UDP for most Services. For
type: LoadBalancer Services, UDP support depends on the cloud provider offering this
facility" ([service protocols](https://kubernetes.io/docs/reference/networking/service-protocols/)) —
a dependency the previous section discharged for this provider. It is a passthrough, so
`externalTrafficPolicy` and the cloud load balancer decide what the backend sees about the
client, rather than an Envoy hop deciding it in advance.

**So the finding is that this workload does not belong behind a Gateway.** Not because
Gateway API cannot express it — it can, at GA, in the standard channel — but because the
one thing a Gateway adds to a raw UDP flow is the loss of the client address, which is the
one thing a NAT-traversal endpoint needs to keep.

### The candidate that needs nothing from the cluster: `cloudflared`

Taking the easy case first, because it sets the bar the others have to clear.

A Cloudflare Tunnel connector requires **no inbound port, no public address, no
`LoadBalancer` Service and no UDP listener at all**. Cloudflare states the model
directly:

> Cloudflare Tunnel uses an outbound-only connection model to enable bidirectional
> communication. When you install and run `cloudflared`, `cloudflared` initiates an
> outbound connection through your firewall from the origin to the Cloudflare global
> network.

and, more sharply, in the course of explaining why a different feature has no effect:

> Because Cloudflare Tunnel does not use an inbound listener on your origin,
> Authenticated Origin Pulls has no effect on hostnames routed through Cloudflare Tunnel.

([Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/))

What it needs is egress to a single port:

> `cloudflared` connects to Cloudflare's global network on port `7844`. To use Cloudflare
> Tunnel, your firewall must allow outbound connections to the following destinations on
> port `7844` (via UDP if using the `quic` protocol or TCP if using the `http2` protocol).

([Tunnel with firewall](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/))

Either protocol suffices, which matters for a cluster whose egress leaves through a
managed NAT that may or may not pass UDP. Cloudflare's connectivity pre-checks page
addresses exactly that case:

> **TCP succeeds, UDP fails** — Outbound TCP is allowed, but UDP on port `7844` is
> blocked. `cloudflared` will only be able to connect using `http2`. If you force `quic`
> while UDP is blocked, the tunnel will fail.

([Connectivity pre-checks](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/troubleshoot-tunnels/connectivity-prechecks/))

The `--protocol` default is `auto`, which "will automatically configure the `quic`
protocol. If `cloudflared` is unable to establish UDP connections, it will fallback to
using the `http2` protocol"
([run parameters](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/run-parameters/)).
The one documented loss on `http2` is post-quantum key agreement, which is "not supported
when using `http2` protocol".

**Is there an official chart?** Yes, and it is in poor health. `cloudflare/helm-charts`
is a live repository in Cloudflare's own organisation —

> A convenient location to publish Cloudflare helm charts

([README](https://raw.githubusercontent.com/cloudflare/helm-charts/master/README.md)) —
publishing `cloudflare-tunnel` and `cloudflare-tunnel-remote`. But the
[index](https://cloudflare.github.io/helm-charts/index.yaml) shows the last release as
4 September 2024, `cloudflare-tunnel` at chart version `0.3.2` pinning
`appVersion: "2024.8.3"`, the chart has no README, the repository has no description, and
**no page on `developers.cloudflare.com` references it**. What Cloudflare documents and
maintains instead is a raw `Deployment` manifest applied with `kubectl create -f`, with
`replicas: 2`, the tunnel token in a Kubernetes `Secret`, and a `/ready` liveness probe on
port 2000
([Kubernetes deployment guide](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/deployment-guides/kubernetes/)).
The one operator Cloudflare ever shipped,
[`cloudflare/cloudflare-ingress-controller`](https://github.com/cloudflare/cloudflare-ingress-controller),
is archived and was last pushed in March 2023.

For a Helm-only ArgoCD pipeline that is an awkward position: an official chart exists, so
the Helm constraint is technically satisfiable, but adopting it means owning a two-year
stale artifact that Cloudflare's own documentation ignores. Wrapping the documented
manifest in a small in-house chart is the more honest option, and costs a `Deployment`
and a `Secret`.

**Replicas and state.** Cloudflare documents multiple replicas of one tunnel as the
supported high-availability arrangement — "You can run up to 25 replicas (100 connections)
per tunnel"
([configuration](https://developers.cloudflare.com/tunnel/configuration/#replicas-and-high-availability))
— and warns against autoscaling, since "downscaling (removing replicas) will break
existing user connections to that replica" and "`cloudflared` does not load balance across
replicas; replicas are strictly for high availability". Every Cloudflare-authored
artifact is a `Deployment` with no PersistentVolumeClaim, so the `ReadWriteOnce`-only
storage is irrelevant here.

**Is Kubernetes supported, or merely exemplified?** Exemplified. The deployment guide
frames itself as "this example" and the system-requirements page reasons in terms of
dedicated hosts, openly conceding the limit of its own model: "`cloudflared` should be
deployed on a dedicated host machine. This model is typically appropriate, but there may
be serverless or clustered workflows where a dedicated host is not possible"
([system requirements](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-availability/system-requirements/)).
No Cloudflare page designates Kubernetes a supported platform, and none translates the
host guidance — 4 GB of RAM, 4 CPU cores and 50,000 allocated ports per host — into pod
resource requests.

### NetBird self-hosted

**Ports and protocols.** The self-hosting guide gives the definitive table
([port requirements](https://docs.netbird.io/selfhosted/selfhosted-guide#port-requirements)).
Since version 0.29 the control plane consolidates onto two TCP ports behind a reverse
proxy:

> | Port | Protocol | Description |
> |------|----------|-------------|
> | 80   | TCP      | HTTP (Let's Encrypt certificate validation, redirects to HTTPS) |
> | 443  | TCP      | HTTPS (Dashboard, Management API/gRPC, Signal gRPC, Relay WebSocket) |
> | 3478 | UDP      | Coturn STUN/TURN server |

Without a reverse proxy the TCP ports fan back out to 80, 443, 33073 (Management gRPC),
10000 (Signal gRPC) and 33080 (Relay), and UDP 3478 stays.

Two things about that table matter more than its shape. The first is good news: **the
relay is no longer UDP**. Coturn has been removed, its TURN relay port range with it —

> **Embedded STUN Server**: The relay service now includes a built-in STUN server,
> eliminating the need for a separate Coturn container […] If you were using Coturn purely
> for TURN relay functionality, note that NetBird Relay has replaced TURN for relayed
> connections.

([Coturn to STUN migration](https://docs.netbird.io/selfhosted/migration/coturn-to-stun-migration))

so the wide UDP 49152–65535 range is legacy, needed only for clients below v0.29. The
relay itself listens on TCP 443 and speaks WebSocket
([external relays](https://docs.netbird.io/selfhosted/maintenance/scaling/set-up-external-relays)).

The second is the finding that decides the question, and NetBird states it without
qualification. From the high-availability guide's section on STUN handling:

> STUN is served by the **Relay instances themselves**. Each relay container with
> `NB_ENABLE_STUN=true` runs an embedded STUN server on UDP/3478 alongside the WebSocket
> relay listener. You do **not** need a separate STUN/TURN service.
>
> Tell peers where to find STUN through `server.stuns` in the Management configuration.
> **UDP/3478 cannot pass through an HTTP or TCP load balancer, so peers reach each Relay
> instance's STUN endpoint directly.** Configure one STUN URL and DNS A record for each
> Relay instance.

([high availability](https://docs.netbird.io/selfhosted/maintenance/scaling/high-availability#stun-handling))

The compose example in the same guide carries the same warning inline against its port
mapping — `3478:3478/udp` is annotated "STUN is direct and not load-balanced. See below."
NetBird's documented arrangement is therefore *one publicly resolvable A record per relay host, pointing at that host's own
public address*. On a cluster whose nodes hold no external addresses and whose only
ingress is a load balancer, there is no such address to point an A record at. This is not
an inference about UDP load balancing in general; it is NetBird documenting that its own
STUN endpoint is not to be load-balanced.

The prerequisite pages say the same thing from the other direction. The quickstart
requires "The VM must be publicly accessible on **TCP ports 80 and 443**, and **UDP port
3478**" together with "A **public domain** name that resolves to the VM's public IP
address"
([quickstart](https://docs.netbird.io/selfhosted/selfhosted-quickstart)); the external
relay and external signal guides each list "Public IP address" under server requirements;
and the enterprise getting-started page states that a real DNS-resolvable FQDN with an A
record pointing at the host is required and that "**Bare IP addresses are not
supported.**"

**Is there an official Helm chart?** There are two, and neither is the one this would
need. NetBird publishes a Helm repository at `https://netbirdio.github.io/helms`, backed
by [`netbirdio/helms`](https://github.com/netbirdio/helms), whose
[index](https://netbirdio.github.io/helms/index.yaml) lists `kubernetes-operator`,
`netbird` and `netbird-operator-config`.

`kubernetes-operator` is the one NetBird documents and promotes, and it deploys the
*client*, not the control plane:

> The NetBird Kubernetes Operator automates the provisioning of NetBird network access for
> services running in your cluster. It extends the Kubernetes API with CRDs, letting you
> manage NetBird peers, routes, and groups declaratively […] Works with any NetBird
> deployment — compatible with NetBird Cloud and self-hosted instances.

([README](https://raw.githubusercontent.com/netbirdio/kubernetes-operator/main/README.md))

Its CRDs are `SetupKey`, `Group`, `NetworkRouter`, `NetworkResource`, `NetworkEgress`,
`SidecarProfile` and `ClusterProxy` — all peer-side. Its documentation home is the
[Kubernetes integration page](https://docs.netbird.io/manage/integrations/kubernetes), and
its companion guide is titled "Deploy routing peers to a Kubernetes cluster". It makes a
cluster a member of a NetBird network; it does not host one.

`charts/netbird` is the control-plane chart, and its own README opens by disclaiming its
provenance:

> # netbird
>
> Forked from [TOT MICRO's Helm Repository](https://github.com/totmicro/helms).

([README](https://raw.githubusercontent.com/netbirdio/helms/main/charts/netbird/README.md))

It is a third-party chart adopted into the organisation rather than one the project
authored. Its `Chart.yaml` pins `appVersion: "0.46.0"` against a current NetBird release
in the 0.77 series; it was last touched in August 2025 across four commits total; it
renders `management.json`, the pre-consolidation multi-container layout the documentation
now calls legacy; it ships **no STUN or TURN template at all**, and its own example values
require an externally supplied `stunServer` and `turnServer`; and **no page of
`docs.netbird.io` references it**. The `SELF-HOST NETBIRD` navigation lists Docker
Compose quickstart, automated setup, commercial licence, Ansible under "Infrastructure as
Code", cloud marketplaces, maintenance, observability, authentication and migration — and
no Kubernetes or Helm entry.

`netbirdio/netbird` itself carries no `helm/` or `charts/` directory; `infrastructure_files/`
holds Compose templates and `getting-started` shell scripts.

**Persistence.** Management defaults to SQLite in a `/var/lib/netbird` volume — "File-based
database stored in the `netbird_data` Docker volume […] does not support concurrent writes
or running multiple management instances"
([configuration files](https://docs.netbird.io/selfhosted/maintenance/configuration-files))
— with PostgreSQL or MySQL as the production option. `ReadWriteOnce` is sufficient for a
single Management replica, so storage is not the obstacle here. High availability is,
separately, gated: "The upstream community Signal image (`netbirdio/signal`) runs in
single-node mode only and cannot be used in HA."

**Is Kubernetes documented as unsupported?** No — and it is not documented as supported
either. No self-hosting page says Kubernetes is unsupported or discouraged, and none
describes deploying the control plane on it. The
[self-hosted support matrix](https://docs.netbird.io/help/support-matrix/self-hosted) is
still a stub in which every row reads "TBD". The only Kubernetes support matrix NetBird
publishes is for the operator — that is, for Kubernetes as a *client*.

### Headscale

**Ports and protocols, control plane separated from relay.** Headscale's requirements page
lists four ports and marks each for public exposure
([ports in use](https://headscale.net/stable/setup/requirements/#ports-in-use)):

> - tcp/80 — Expose publicly: yes — HTTP, used by Let's Encrypt to verify ownership via
>   the HTTP-01 challenge. Only required if the built-in Let's Encrypt client with the
>   HTTP-01 challenge is used.
> - tcp/443 — Expose publicly: yes — HTTPS, required to make Headscale available to
>   Tailscale clients. Required if the embedded DERP server is enabled
> - udp/3478 — Expose publicly: yes — STUN, required if the embedded DERP server is enabled
> - tcp/9090 — Expose publicly: no — Metrics and debug endpoint

**Stated plainly: the Headscale control plane needs no UDP.** With the embedded DERP left
at its shipped default of disabled, a Headscale control server is a single TCP/443 HTTPS
service — tcp/80 is avoidable entirely by using DNS-01 rather than the built-in HTTP-01
client, which suits a cluster that already runs cert-manager with a DNS-01 issuer. The
metrics port defaults to `127.0.0.1:9090` and is documented as not for public exposure.
No gRPC port appears in the current `config-example.yaml` or requirements page; remote
control is documented over the HTTP REST API with a bearer token.

**Stated equally plainly: the relay does need UDP, and it must bypass the proxy.** udp/3478
carries STUN, and the reverse-proxy page is explicit that this traffic cannot be
proxied — "STUN (used along with the embedded DERP server) requires udp/3478 to be served
publicly" and features of that kind "are expected to be exposed directly or to be only
available on localhost"
([reverse proxy limitations](https://headscale.net/stable/ref/integration/reverse-proxy/#limitations)).

The control plane and the relay can therefore be separated: the control plane is an
ordinary HTTPS workload the cluster could host today, and only the relay wants a public
UDP address. Whether the relay is needed at all is the subject of part 3.

**Is there an official Helm chart?** No. The `juanfont/headscale` repository contains no
chart, no manifests and no operator, and the documentation mentions Kubernetes exactly
once, on the community tools page, under a banner reading "This page contains community
contributions. The projects listed here are not maintained by the headscale authors and
are written by community members"
([tools](https://headscale.net/stable/ref/integration/tools/)). A maintainer has closed
the request explicitly. On [issue #2717](https://github.com/juanfont/headscale/issues/2717),
asking for a chart in the main repository, kradalby
[replied](https://github.com/juanfont/headscale/issues/2717#issuecomment-3153988297):

> Hi, I think the challenge would be to keep it maintained over time. I dont really have
> the knowledge to review Helm charts, and since they are particularly hard to validate and
> test, I would not know how to make sure we do not break it over time. So having it as
> part of the main repo is problematic as it would likely bit rot and people will, no
> matter how much we write that it isnt officially supported, expect it to be. And then we
> will get the support load.

Two earlier requests, [#1153](https://github.com/juanfont/headscale/issues/1153) and
[#170](https://github.com/juanfont/headscale/issues/170), were closed too. For a cluster
that onboards Helm only, this means writing and owning the chart.

**How far the "unsupported" statement reaches.** #76 recorded that the maintainers neither
support nor encourage reverse proxies and containers. The ticket asks how far that
statement reaches, and the answer is that there are four separate statements of differing
strength, and **none of them names Kubernetes**.

The broadest is in the README, under "Running headscale"
([README](https://raw.githubusercontent.com/juanfont/headscale/main/README.md)):

> **Please note that we do not support nor encourage the use of reverse proxies
> and container to run Headscale.**

That reaches containers and reverse proxies as categories. A Kubernetes deployment is
both, so it is covered by implication — but only by implication. The FAQ's version is
narrower and weaker, naming one runtime: "please be aware that we don't officially support
deploying headscale using Docker"
([FAQ](https://headscale.net/stable/about/faq/)). The container installation page and the
reverse-proxy page each carry a third statement, which is about documentation provenance
rather than support — "This page is not actively maintained by the headscale authors and
is written by community members. It is _not_ verified by headscale developers." And the
FAQ supplies the fourth, which is the most candid: "We don't know. We don't use reverse
proxies with headscale ourselves, so we don't have any experience with them."

The supported methods are named positively in the same FAQ: "We currently support
deploying headscale using our binaries and the DEB packages", with community packages and
container images as unsupported conveniences. The requirements page states the assumed
shape: "Headscale is running as system service via a dedicated local user `headscale`."

Two further constraints bear on any Kubernetes ingress. The control protocol is not
ordinary HTTPS —

> The reverse proxy **must** be configured to support WebSockets in order to communicate
> with Tailscale clients and it needs to handle two peculiarities of the Tailscale Control
> Protocol:
>
> - The POST method is used to upgrade the WebSocket connection.
> - The value for the `Upgrade` header is `tailscale-control-protocol`.

([reverse proxy](https://headscale.net/stable/ref/integration/reverse-proxy/)) — and one
proxy is documented as outright broken: "Running Headscale behind a Cloudflare Proxy or
Cloudflare Tunnel is not supported and will not work as Cloudflare does not support
WebSocket POSTs as required by the Tailscale protocol" ([FAQ](https://headscale.net/stable/about/faq/)).
That page does carry Envoy and Istio examples, including an `EnvoyFilter` adding
`upgrade_type: tailscale-control-protocol`, which is the closest thing to Kubernetes
ingress guidance the project has — still under the community-documentation banner.

**Persistence.** The data directory holds more than a database
([requirements](https://headscale.net/stable/setup/requirements/#assumptions)): "The data
directory for headscale (used for private keys, policy, SQLite database, …) is located in
`/var/lib/headscale`." The Noise private key and, when embedded DERP is on, the DERP
private key live there, and `config-example.yaml` notes for each that "A missing key will
be automatically generated" — so a pod with no persistent volume mints a new control-plane
identity on every restart and invalidates every node's session. SQLite with write-ahead
logging is the default and the recommendation; PostgreSQL, which would have let a
Kubernetes deployment avoid a volume for the database, is discouraged in the shipped
configuration:

```yaml
database:
  # Database type. Available options: sqlite, postgres
  # Please note that using Postgres is highly discouraged as it is only supported for
  # legacy reasons.
  # All new development, testing and optimisations are done with SQLite in mind.
  type: sqlite
```

([config-example.yaml](https://raw.githubusercontent.com/juanfont/headscale/main/config-example.yaml))

and the FAQ agrees: "PostgreSQL is still supported, but is considered to be in
'maintenance mode'." The key files need a volume in either case. A single replica on
`ReadWriteOnce` satisfies this; it also means there is no horizontal scaling story, which
for five colleagues is irrelevant.

**Scope.** Worth carrying into any decision, from the project's own README: "Headscale's
goal is to provide self-hosters and hobbyists with an open-source server they can use for
their projects and labs. It implements a narrow scope, a _single_ Tailscale network
(tailnet), suitable for a personal use, or a small open-source organisation."

## 3. Headscale, DERP, and Tailscale's relays

### The shipped default does relay through Tailscale

Confirmed from the source, not from documentation about the source.
[`config-example.yaml`](https://raw.githubusercontent.com/juanfont/headscale/main/config-example.yaml)
contains, in the `derp` block:

> ```yaml
>   # List of externally available DERP maps encoded in JSON
>   urls:
>     - https://controlplane.tailscale.com/derpmap/default
> ```

The entry is uncommented and active, and it is refreshed on a schedule: the same block
sets `auto_update_enabled: true` and `update_frequency: 3h`. A stock Headscale therefore
fetches Tailscale Inc.'s DERP map every three hours and hands it to its nodes. The
embedded alternative is off:

> ```yaml
> derp:
>   server:
>     # If enabled, runs the embedded DERP server and merges it into the rest of the DERP config
>     # The Headscale server_url defined above MUST be using https, DERP requires TLS to be in place
>     enabled: false
> ```

The documentation says the same in prose — "The embedded DERP server is disabled by
default and needs to be enabled" — and frames removing Tailscale's relays as the opt-out
rather than the default:

> Once enabled, Headscale's embedded DERP is added to the list of free-to-use DERP servers
> offered by Tailscale Inc. To only use Headscale's embedded DERP server, disable the
> loading of the default DERP map

([DERP](https://headscale.net/stable/ref/derp/)), and it warns against doing so: "Removing
Tailscale's DERP servers means that there is now just a single DERP server available for
clients. This is a single point of failure and could hamper connectivity."

DERP is a fallback, not the ordinary path. The comment immediately above the `derp:` key
reads "DERP is a relay system that Tailscale uses when a direct connection cannot be
established", and Tailscale's own reference describes it as "a fallback option when a
direct connection isn't possible"
([DERP servers](https://tailscale.com/kb/1232/derp-servers)). So the exposure is to
whatever fraction of five colleagues' traffic cannot go direct — not to all of it. That
fraction is not zero, and behind carrier-grade NAT it is not small.

### Whether any Tailscale term addresses a non-customer using those relays

**No document does, and the absence was established by searching rather than assumed.**

The [Terms of Service](https://tailscale.com/terms) contain no occurrence of "DERP",
"relay" or "Headscale". The relay fleet is not named even in §1.17's definition of the
Tailscale Solution, which enumerates "an Internet-accessible hosted software service,
including an admin console and coordination server", the client software, PAM and
Aperture. More to the point, the Terms define who is bound by the act of holding an
account:

> Please read these Terms carefully as they affect your legal rights. By creating or
> administering a Tailscale account and accessing or using our Services, you agree to be
> bound by these Terms

and the scope clause reaches "all customers that purchase the Services … and all customers
that use the Services under a free trial or free Plan". Someone running Headscale and
pulling `controlplane.tailscale.com/derpmap/default` creates no account, is neither a
Self-Serve nor a Free customer, and so falls outside the stated scope. Nothing extends the
Terms to accountless users of Tailscale infrastructure.

The [Acceptable Use Policy](https://tailscale.com/tailscale-aup) likewise contains no
occurrence of "DERP", "relay" or "Headscale". Its scope is drawn more widely than the
Terms' — "This Acceptable Use Policy ('AUP') is part of the Documentation applicable to
all who use or access the Services" — which is the one place official text arguably
reaches a non-customer. But the nearest applicable prohibition is a general anti-abuse
clause that names nothing relevant:

> Interfering with, disrupting, or creating an undue burden on the Tailscale Solution or
> the networks, servers, systems or services connected to the Tailscale Solution;

Neither the [DERP servers reference](https://tailscale.com/kb/1232/derp-servers) nor the
custom-DERP page addresses eligibility, cost, quotas or third-party control servers. And
the one official page that discusses Headscale by name,
[Tailscale's open source page](https://tailscale.com/opensource), endorses the project
warmly — "Kristoffer Dalby works at Tailscale, such that Tailscale can support his efforts
to develop Headscale", "Tailscale also works with the independent Headscale project to
help maintain compatibility with Tailscale clients" — while saying nothing whatever about
whether Headscale deployments may point clients at Tailscale's relays. The
[custom control server how-to](https://tailscale.com/docs/how-to/set-up-custom-control-server)
tells users precisely how to point a client at a Headscale instance and is silent on the
same question.

**The only party that characterises those relays as free to use is Headscale**, in the
sentence quoted above. That is Headscale's description of someone else's infrastructure,
not a commitment by its owner. Neither "this is permitted" nor "this violates the terms"
can be supported from a primary source. #76 named this as gap 4; it is now named more
precisely, and it has not moved.

### What enabling the embedded DERP requires

This is the documented way out, and it is cheap in configuration and expensive in
infrastructure.

| Requirement | Source |
| --- | --- |
| TLS on the control server | "The Headscale server_url defined above MUST be using https, DERP requires TLS to be in place" (`config-example.yaml`) |
| A STUN listener on UDP, mandatory | "Listens over UDP at the configured address for STUN connections … When the embedded DERP server is enabled stun_listen_addr MUST be defined." Default `stun_listen_addr: "0.0.0.0:3478"` |
| udp/3478 publicly reachable, unproxied | "udp/3478 — Expose publicly: yes — STUN, required if the embedded DERP server is enabled" ([ports in use](https://headscale.net/stable/setup/requirements/#ports-in-use)); "expected to be exposed directly or to be only available on localhost" ([reverse proxy](https://headscale.net/stable/ref/integration/reverse-proxy/#limitations)) |
| tcp/443 publicly reachable | same ports page |
| A DERP private key on persistent storage | `private_key_path: /var/lib/headscale/derp_server_private.key`; "A missing key will be automatically generated" |
| A stable public address, recommended | "you should configure the public IPv4 and public IPv6 address of your Headscale server for improved connection stability" ([DERP](https://headscale.net/stable/ref/derp/)); config keys `ipv4:` and `ipv6:` |

Client verification is on by default (`verify_clients: true`, "Only allow clients
associated with this server access"), so the relay is not opened to the world's traffic,
only to the world's packets. Two documented limitations are worth noting: the embedded
server "can't be used for Tailscale's captive portal checks as it doesn't support the
`/generate_204` endpoint via HTTP on port tcp/80", and "There are no speed or throughput
optimisations, the main purpose is to assist in node connectivity."

The infrastructure cost is the whole of the problem. Enabling the embedded DERP is what
converts Headscale from a pure TCP/443 workload — which this cluster could host
today — into one that needs a public UDP endpoint and, for the recommended stability, a
stable public address of its own.

## What the evidence favours

**The vendor-managed NetBird free tier, and it is not close.**

The single strongest fact behind that is a scope clause rather than a feature. NetBird's
Terms of Service confine the entire contract to "customers as business persons (section 14
German Civil Code, Bürgerliches Gesetzbuch (BGB))", and the same document legislates at
§ 10 for the case where "NetBird provides services to you free of charge". The free tier
is not a consumer product that a business might be tolerated on; it is a free package
inside a business-to-business agreement. That is the opposite of Tailscale's position,
where the Pricing page incorporated by §1.14 says the Personal plan "is only suitable for
non-commercial use" (#76). It is also the only candidate that requires the cluster to do
nothing at all, so constraint 8 does not engage.

The cost of the ceiling is now a number rather than a worry: **€36 per month** the moment a
sixth colleague joins, because the free tier is a plan and not an allowance carried into
Team pricing. At five people it is €0, and the team is five people.

**Self-hosting on this cluster is impractical, and the reason differs by candidate.**

The provider was not the obstacle. GKE's external `LoadBalancer` Service is a passthrough
Network Load Balancer that carries UDP, the platform answers the mandatory TCP health check
on the workload's behalf, and the only correction a WireGuard endpoint needs is
`sessionAffinity: ClientIP`. The cluster could expose a public UDP endpoint. The
obstacles are in the projects.

- **NetBird self-hosted is documented as incompatible with this shape of cluster.** Not
  "undocumented" — documented. "UDP/3478 cannot pass through an HTTP or TCP load balancer,
  so peers reach each Relay instance's STUN endpoint directly", with one public A record per
  relay host. Nodes here have no external addresses to point an A record at. On top of that
  the only control-plane chart is a third-party fork adopted into the organisation, pinned
  to `appVersion: "0.46.0"` against a current 0.77 series, built on the deprecated
  `management.json` layout, shipping no STUN component, and referenced by no page of the
  documentation. Every self-hosting page describes Docker Compose.
- **Headscale is *possible* and *unsupported at every step*.** Its control plane is the
  best-behaved workload of the three — a single TCP/443 HTTPS service with a small
  `ReadWriteOnce` volume, no UDP, and DNS-01 certificates the cluster already knows how to
  issue. But there is no official chart, and the maintainer declined to add one on the
  grounds that "it would likely bit rot and people will, no matter how much we write that it
  isnt officially supported, expect it to be"; the README says the project does "not support
  nor encourage the use of reverse proxies and container to run Headscale"; the control
  protocol needs a proxy that will pass a WebSocket upgrade over `POST` with
  `Upgrade: tailscale-control-protocol`; and the project describes its own audience as
  "self-hosters and hobbyists" running "a _single_ Tailscale network (tailnet), suitable for
  a personal use, or a small open-source organisation". Turning on the embedded DERP adds a
  public UDP endpoint that GKE can supply, so the relay is the *easy* part; the rest is a
  chart the team writes, owns and keeps current against a project that has said it will not
  help.
- **`cloudflared` is the only self-hosted component that fits the cluster comfortably**, and
  it barely counts as self-hosting: no inbound port, no address, no UDP, no volume, and one
  egress port that works over TCP when the managed NAT blocks UDP. Its official chart is
  two years stale and undocumented, so the honest cost is a small in-house chart around a
  `Deployment` and a `Secret`. But #76 already settled that Cloudflare relays every flow
  through the nearest data centre, which is the reason it was not the preferred transport,
  and nothing here changes that.

Read together: the two candidates that would give the team a peer-to-peer mesh are the two
that this cluster cannot host well, and the candidate the cluster hosts easily is the one
that is not a mesh. Constraint 8 does not so much fail as reveal that hosting was never the
cheap part.

**The DERP question is settled, and it is a reason to prefer the managed tier rather than a
blocker on its own.** Headscale as shipped does relay through Tailscale's servers, and no
Tailscale document — Terms, Acceptable Use Policy, DERP reference, or the page that
endorses Headscale by name — grants, prices, limits or forbids that use. The way out is
documented and cheap in configuration, and it costs a public UDP endpoint plus a persistent
key. Anyone deploying Headscale should turn it on deliberately rather than inherit the
default; nobody should represent the default as either permitted or prohibited.

### Where the evidence is thin

Each of these is a point no primary source settles. They are named rather than resolved by
inference.

1. **No NetBird document grants business use of the free tier in so many words.** The
   conclusion rests on reading § 1's business-persons scope together with § 10's
   free-of-charge clause. That is a strong inference and a much better position than #76
   could reach, but it is still an inference. A written confirmation from NetBird remains
   cheap to obtain and would close the question outright.

2. **The Self-Hosted EULA does not exclude the open-source Community Edition from its
   scope.** § 3.2 makes any installation without a paid licence key a thirty-day proof of
   concept in which "Any commercial, productive, or external use … is strictly prohibited",
   while the documentation says "the open source Community Edition is free to self-host for
   as long as you like, with no license and no time limit". The two are reconcilable through
   the EULA's definition of "Software" as NetBird's delivered binaries, but no clause says
   so. This matters only if self-hosting is revisited.

3. **No Google page states that a cluster with private nodes supports *external*
   `LoadBalancer` Services.** The delivery mechanism — "special routes outside of your VPC
   network" rather than node addresses — implies it, and the cluster already runs three such
   Services, but the sentence that would settle it does not exist.

4. **No GKE page documents any UDP-specific restriction for `LoadBalancer` Services at
   all.** The word scarcely appears in that documentation. "No restriction is documented"
   is therefore not evidence that UDP works with `externalTrafficPolicy: Local`, with
   subsetting, or with weighted load balancing. It is evidence of silence.

5. **Envoy Gateway states no maturity or support level for `UDPRoute`.** Its conformance
   tests pass and are reported upstream, but as *provisional* tests rather than a claimed
   `GATEWAY-UDP` profile, and no page assigns the resource a level. Separately, no Envoy
   Gateway page documents UDP-specific data-plane behaviour — health checking, load
   balancing, session handling, datagram limits — and no page documents mixing TCP and UDP
   listeners on one Gateway, which is settled only by reading the resource-rendering code.
   Independently of all that, no UDP workload has ever run on this cluster apart from
   cluster DNS, so the first UDP endpoint would also be the first test of that path.

6. **Headscale documents no certificate requirement for the embedded DERP distinct from the
   control server's own TLS.** The only statement is "DERP requires TLS to be in place",
   tied to `server_url` being HTTPS. Tailscale's own `derper` requires a dedicated hostname,
   but that page is about a different program and does not settle Headscale's requirement.

7. **No official Headscale document names Kubernetes at all** — not to permit it, not to
   forbid it. The README's statement reaches Kubernetes only by the categories "container"
   and "reverse proxy". Anyone reporting that the maintainers call Kubernetes unsupported is
   extrapolating.

8. **Neither NetBird nor Headscale documents a deployment behind nodes with no external
   addresses.** NetBird documents the arrangement that rules it out for STUN; Headscale
   documents nothing either way. Neither states it as unsupported.

9. **NetBird documents no way to run the self-hosted control plane with no STUN at all.**
   The high-availability guide leaves a hook — "External STUN: per-instance hostnames
   (option 1) or external STUN/TURN (option 2)" — and never spells out option 2. Whether an
   external STUN service could substitute, and what would then need to be public, is
   unanswered.

10. **Cloudflare publishes no statement that Kubernetes is a supported platform**, only a
    guide framed as "this example", and its sizing guidance reasons in dedicated hosts
    while conceding "there may be serverless or clustered workflows where a dedicated host is
    not possible". No page translates 4 GB, 4 CPU cores and 50,000 ports per host into pod
    resource requests.

11. **No Cloudflare source says whether the official Helm charts are maintained or
    deprecated.** Their last release was September 2024, they carry no README, and no
    documentation page references them. Their official status rests on organisation
    ownership alone.

12. **The NetBird `charts/netbird` chart's support status is unstated.** It lives in
    NetBird's own Helm repository, which is what makes it nominally official, and its README
    opens by naming the third party it was forked from. No NetBird source says whether it is
    supported, maintained or recommended.

