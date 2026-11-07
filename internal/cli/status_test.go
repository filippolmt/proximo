package cli

import (
	"net/netip"
	"testing"

	"github.com/filippolmt/proximo/internal/docker"
)

func TestPeerCell(t *testing.T) {
	served := docker.Route{Share: true, Peer: "api.studio-01.mesh.internal", PeerQualified: "api.shop.studio-01.mesh.internal"}
	cases := []struct {
		name    string
		r       docker.Route
		missing []string
		want    string
	}{
		{"served", served, nil, "https://api.studio-01.mesh.internal  + api.shop.studio-01.mesh.internal"},
		{"not shared", docker.Route{}, nil, ""},
		{"one missing", served, []string{"machine"}, warnPrefix + "proximo.share is set; not served on its peer names (machine is not configured)"},
		{"several missing", served, []string{"machine", "address", "intermediate"}, warnPrefix + "proximo.share is set; not served on its peer names (machine, address and intermediate are not configured)"},
		{"lost a Collision", docker.Route{Share: true, Collision: true, PeerQualified: "api.blog.studio-01.mesh.internal"}, nil, "https://api.blog.studio-01.mesh.internal"},
		{"TCP route", docker.Route{ShareTCP: true}, nil, warnPrefix + "proximo.share ignored on a TCP route"},
	}
	for _, tc := range cases {
		if got := peerCell(tc.r, tc.missing); got != tc.want {
			t.Errorf("%s: peerCell = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestStackNote(t *testing.T) {
	cases := []struct {
		name string
		info docker.StackInfo
		want string
	}{
		{"whole", docker.StackInfo{Running: true, Roles: []string{"traefik", "dns", "inspector", "watcher"}}, ""},
		{"down", docker.StackInfo{}, warnPrefix + "the proximo stack is not running, so none of these URLs answers — `proximo doctor`"},
		{"degraded", docker.StackInfo{Running: true, Roles: []string{"traefik", "dns"}}, warnPrefix + "the proximo stack is degraded (watcher is not running), so these URLs may not answer — `proximo doctor`"},
		{"degraded twice", docker.StackInfo{Running: true, Roles: []string{"traefik"}}, warnPrefix + "the proximo stack is degraded (dns and watcher are not running), so these URLs may not answer — `proximo doctor`"},
	}
	for _, tc := range cases {
		if got := stackNote(tc.info); got != tc.want {
			t.Errorf("%s: stackNote = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestListenNote(t *testing.T) {
	ap := netip.MustParseAddrPort
	cases := []struct {
		name string
		info docker.StackInfo
		want string
	}{
		{"loopback", docker.StackInfo{Running: true, Listens: []netip.AddrPort{ap("127.0.0.1:80"), ap("127.0.0.1:443")}}, "Listening on 127.0.0.1:80, 127.0.0.1:443"},
		{"shared", docker.StackInfo{Running: true, Listens: []netip.AddrPort{ap("127.0.0.1:443"), ap("100.89.88.2:443")}}, "Listening on 127.0.0.1:443, 100.89.88.2:443"},
		{"wildcard", docker.StackInfo{Running: true, Listens: []netip.AddrPort{ap("0.0.0.0:443")}}, warnPrefix + "Listening on 0.0.0.0:443: every route is reachable from other hosts on this network"},
		{"down", docker.StackInfo{}, ""},
	}
	for _, tc := range cases {
		if got := listenNote(tc.info); got != tc.want {
			t.Errorf("%s: listenNote = %q, want %q", tc.name, got, tc.want)
		}
	}
}
