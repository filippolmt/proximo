package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/dns"
	"github.com/filippolmt/proximo/internal/tls"
)

// The peer Checks (docs/specs/peer-sharing.md). On a machine that has not opted
// in every one of them is Skipped, and no other Check reads a peer value.
const (
	IDPeerIntermediate = "peer-intermediate"
	IDPeerRoutes       = "peer-routes"
	IDPeerDNS          = "peer-dns"
	IDMesh             = "mesh"
)

// intermediateMargin is how long before expiry peer-intermediate fails, so
// doctor exits non-zero a month ahead: proximo would otherwise go on issuing
// valid leaves under an expired intermediate that only a colleague's browser
// would see.
const intermediateMargin = 30 * 24 * time.Hour

func peerChecks(env Env) []Check {
	p := env.Peer
	subtree := tls.MachineSubtree(p.Machine, p.PeerSuffix)
	sentinel := dns.Sentinel(subtree)
	return []Check{
		{
			ID:   IDPeerIntermediate,
			Name: "The machine's intermediate is valid for at least 30 more days",
			Doc:  "the-machines-intermediate-is-about-to-expire",
			Run: func(context.Context) Result {
				if p.Machine == "" || p.PeerSuffix == "" {
					return Skipped("machine or peer-suffix is not configured")
				}
				c, err := env.Intermediate()
				switch {
				case err != nil:
					return Failed("proximo config csr", "the installed intermediate could not be read: %v", err)
				case c == nil:
					return Skipped("no intermediate is installed")
				}
				if got := tls.IntermediateSubtree(c); got != subtree {
					return Failed("proximo config csr", "the installed intermediate is constrained to %s, not %s", got, subtree)
				}
				day := c.NotAfter.Format("2006-01-02")
				switch left := time.Until(c.NotAfter); {
				case left <= 0:
					return Failed("proximo config csr", "it expired on %s", day)
				case left < intermediateMargin:
					return Failed("proximo config csr", "it expires on %s, in under 30 days", day)
				}
				return Passed("valid until %s", day)
			},
		},
		{
			ID:    IDPeerRoutes,
			Name:  "Every shared route is served on its peer names",
			Doc:   "a-shared-route-is-not-served-on-its-peer-names",
			Needs: []string{IDStack},
			Run: func(ctx context.Context) Result {
				if p.Machine == "" || p.PeerSuffix == "" {
					return Skipped("machine or peer-suffix is not configured")
				}
				routes, err := env.Routes(ctx)
				if err != nil {
					return Failed("docker ps --filter label=proximo.share", "could not list routes: %v", err)
				}
				var faults, containers []string
				shared := 0
				for _, r := range routes {
					switch {
					case r.ShareTCP:
						faults = append(faults, fmt.Sprintf("%s is a TCP route, which is never shared", r.Container))
					case r.ShareOutside:
						faults = append(faults, fmt.Sprintf("%s: %s is outside .%s, so it has no peer name", r.Container, r.Host, env.TLD))
					case r.Share:
						shared++
						continue
					default:
						continue
					}
					if !containsString(containers, r.Container) {
						containers = append(containers, r.Container)
					}
				}
				if len(faults) > 0 {
					return Failed("docker inspect "+strings.Join(containers, " "), "%s", strings.Join(faults, "; "))
				}
				if shared == 0 {
					return Passed("no route is shared")
				}
				return Passed("%d shared route(s)", shared)
			},
		},
		{
			ID:    IDPeerDNS,
			Name:  "The proximo DNS server answers the Peer suffix",
			Doc:   "proximo-does-not-answer-on-the-mesh-address",
			Needs: []string{IDStack},
			Run: func(ctx context.Context) Result {
				if p.Machine == "" || p.PeerSuffix == "" || p.Address == "" {
					return Skipped("machine, peer-suffix or address is not configured")
				}
				server := fmt.Sprintf("%s:%d", p.Address, config.PeerDNSPort)
				addr, err := env.QueryAt(ctx, server, sentinel)
				if err == nil && addr == p.Address {
					return Passed("%s answers %s on %s", sentinel, p.Address, server)
				}
				// The one cause of a failed bind proximo can see.
				held := "is held by an interface of this machine"
				if addrs, aerr := env.InterfaceAddrs(); aerr == nil && !config.AddressHeld(p.Address, addrs) {
					held = "is held by no interface of this machine"
				}
				if err != nil {
					return Failed("proximo up", "%s did not answer %s: %v; %s %s", server, sentinel, err, p.Address, held)
				}
				return Failed("proximo up", "%s answered %q for %s, want %s; %s %s", server, addr, sentinel, p.Address, p.Address, held)
			},
		},
		{
			ID:    IDMesh,
			Name:  "This machine's peer names resolve through the system resolver",
			Doc:   "this-machines-peer-names-do-not-resolve",
			Needs: []string{IDPeerDNS},
			Run: func(ctx context.Context) Result {
				// Unset, the platform command shows whether a resolver exists for
				// the suffix and where it points: only a declared cure is missing.
				remedy := p.MeshRemedy
				if remedy == "" {
					remedy = env.ResolverRemedy
				}
				addr, err := env.SystemResolve(ctx, sentinel)
				switch {
				case err != nil:
					return Failed(remedy, "the system resolver could not be asked for %s: %v", sentinel, err)
				case addr != p.Address:
					return Failed(remedy, "%s resolves to %q through the system resolver, not %s", sentinel, addr, p.Address)
				}
				return Passed("%s resolves to %s", sentinel, p.Address)
			},
		},
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
