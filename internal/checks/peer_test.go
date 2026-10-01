package checks

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
)

// sharingEnv is a healthy machine that shares: every peer value set, an
// intermediate valid for a year, and the mesh routing its subtree.
func sharingEnv() Env {
	env := healthyEnv()
	env.Peer.Machine, env.Peer.PeerSuffix, env.Peer.Address = "studio-01", "mesh.internal", "100.89.88.2"
	env.Intermediate = func() (*x509.Certificate, error) {
		return &x509.Certificate{NotAfter: time.Now().AddDate(1, 0, 0), PermittedDNSDomains: []string{".studio-01.mesh.internal"}}, nil
	}
	env.QueryAt = func(_ context.Context, server, _ string) (string, error) {
		if server != "100.89.88.2:5354" {
			return "", errors.New("wrong server " + server)
		}
		return "100.89.88.2", nil
	}
	env.SystemResolve = func(_ context.Context, name string) (string, error) {
		if strings.HasSuffix(name, ".mesh.internal") {
			return "100.89.88.2", nil
		}
		return "127.0.0.1", nil
	}
	env.InterfaceAddrs = func() ([]net.Addr, error) { return nil, nil }
	return env
}

func outcomes(env Env) map[string]Result {
	got := map[string]Result{}
	for _, o := range Run(context.Background(), All(env)).Outcomes {
		got[o.Check.ID] = o.Result
	}
	return got
}

// On a machine that has not opted in, every peer Check is Skipped, and the
// local DNS Checks return exactly what they do configured (constraint 9).
func TestPeerChecksSkippedUnconfigured(t *testing.T) {
	unset, set := outcomes(healthyEnv()), outcomes(sharingEnv())
	for _, id := range []string{IDPeerIntermediate, IDPeerRoutes, IDPeerDNS, IDMesh} {
		if unset[id].Status != Skip {
			t.Errorf("%s = %s unconfigured, want skip", id, unset[id].Status)
		}
		if set[id].Status != Pass {
			t.Errorf("%s = %s %q configured, want pass", id, set[id].Status, set[id].Detail)
		}
	}
	for _, id := range []string{IDDNSServer, IDDNSResolver} {
		if unset[id] != set[id] {
			t.Errorf("%s = %+v unconfigured, %+v configured", id, unset[id], set[id])
		}
	}
}

func TestPeerIntermediate(t *testing.T) {
	env := sharingEnv()
	env.Intermediate = func() (*x509.Certificate, error) {
		return &x509.Certificate{NotAfter: time.Now().AddDate(0, 0, 20), PermittedDNSDomains: []string{".studio-01.mesh.internal"}}, nil
	}
	if r := outcomes(env)[IDPeerIntermediate]; r.Status != Fail || r.Remedy != "proximo config csr" || !strings.Contains(r.Detail, "expires") {
		t.Errorf("20 days left = %+v, want a failure a month ahead", r)
	}
	env.Intermediate = func() (*x509.Certificate, error) {
		return &x509.Certificate{NotAfter: time.Now().AddDate(0, 0, -1), PermittedDNSDomains: []string{".studio-01.mesh.internal"}}, nil
	}
	if r := outcomes(env)[IDPeerIntermediate]; r.Status != Fail || !strings.Contains(r.Detail, "expired") {
		t.Errorf("expired = %+v", r)
	}
	env.Intermediate = func() (*x509.Certificate, error) { return nil, nil }
	if r := outcomes(env)[IDPeerIntermediate]; r.Status != Skip {
		t.Errorf("no intermediate = %+v, want skip", r)
	}
}

func TestPeerRoutes(t *testing.T) {
	env := sharingEnv()
	env.Routes = func(context.Context) ([]docker.Route, error) {
		return []docker.Route{
			{Container: "web", Host: "web.test", Share: true},
			{Container: "db", Host: "db.test", ShareTCP: true},
			{Container: "api", Host: "api.example.com", Share: true, ShareOutside: true},
		}, nil
	}
	r := outcomes(env)[IDPeerRoutes]
	if r.Status != Fail || r.Remedy != "docker inspect db api" || !strings.Contains(r.Detail, "TCP") || !strings.Contains(r.Detail, "api.example.com") {
		t.Errorf("peer-routes = %+v", r)
	}
}

func TestPeerDNSSaysWhetherTheAddressIsHeld(t *testing.T) {
	env := sharingEnv()
	env.QueryAt = func(context.Context, string, string) (string, error) { return "", errors.New("connection refused") }
	r := outcomes(env)
	if r[IDPeerDNS].Status != Fail || r[IDPeerDNS].Remedy != "proximo up" || !strings.Contains(r[IDPeerDNS].Detail, "held by no interface") {
		t.Errorf("peer-dns = %+v", r[IDPeerDNS])
	}
	// One answer, as dns-server and dns-resolver are: mesh waits on it.
	if r[IDMesh].Status != Skip {
		t.Errorf("mesh = %+v, want skip behind peer-dns", r[IDMesh])
	}
	// Stopping the peer DNS service never fails stack (Acceptance 13).
	if r[IDStack].Status != Pass {
		t.Errorf("stack = %+v", r[IDStack])
	}
}

func TestMeshRemedy(t *testing.T) {
	env := sharingEnv()
	env.SystemResolve = func(context.Context, string) (string, error) { return "", nil }
	if r := outcomes(env)[IDMesh]; r.Status != Fail || r.Remedy != env.ResolverRemedy {
		t.Errorf("mesh without mesh-remedy = %+v, want the platform command", r)
	}
	env.Peer.MeshRemedy = "meshctl status"
	if r := outcomes(env)[IDMesh]; r.Remedy != "meshctl status" {
		t.Errorf("mesh remedy = %q, want the configured one", r.Remedy)
	}
}
