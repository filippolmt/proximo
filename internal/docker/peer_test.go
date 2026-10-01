package docker

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/tls"
)

var testPeer = PeerNames{Machine: "studio-01", Suffix: "mesh.internal"}

func sharedRoute(hosts []string, ns string) routedContainer {
	rc := routedContainer{name: "api", safe: "api", hosts: hosts, proximo: true, share: true, ns: ns, port: 80}
	rc.qualifyHosts("test")
	return rc
}

func TestPeerHosts(t *testing.T) {
	cases := map[string]struct {
		rc      routedContainer
		want    []string
		outside []string
	}{
		"bare and qualified": {sharedRoute([]string{"api.test"}, "shop"),
			[]string{"api.studio-01.mesh.internal", "api.shop.studio-01.mesh.internal"}, nil},
		"multi-label host keeps its base": {sharedRoute([]string{"api.v2.test"}, "shop"),
			[]string{"api.v2.studio-01.mesh.internal", "api.v2.shop.studio-01.mesh.internal"}, nil},
		"host already carries its Namespace": {sharedRoute([]string{"api.shop.test"}, "shop"),
			[]string{"api.shop.studio-01.mesh.internal"}, nil},
		"outside a Compose project": {sharedRoute([]string{"api.test"}, ""),
			[]string{"api.studio-01.mesh.internal"}, nil},
		"outside the TLD": {sharedRoute([]string{"api.example.com", "api.test"}, ""),
			[]string{"api.studio-01.mesh.internal"}, []string{"api.example.com"}},
		"longer than 253 octets": {sharedRoute([]string{strings.Repeat(strings.Repeat("a", 60)+".", 4) + "test"}, ""),
			nil, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, outside := tc.rc.peerHosts("test", testPeer)
			if !slices.Equal(got, tc.want) || !slices.Equal(outside, tc.outside) {
				t.Errorf("peerHosts = %v, %v; want %v, %v", got, outside, tc.want, tc.outside)
			}
		})
	}
	// A container that lost its Bare host to a Collision keeps only what it
	// still serves.
	rc := sharedRoute([]string{"api.test"}, "shop")
	rc.hosts = []string{"api.shop.test"}
	if got, _ := rc.peerHosts("test", testPeer); !slices.Equal(got, []string{"api.shop.studio-01.mesh.internal"}) {
		t.Errorf("after a lost Collision, peerHosts = %v", got)
	}
}

// The peer router is the local one through other names: same service, same
// middleware chain, same redirect — and Host is never rewritten.
func TestRenderRouterPeer(t *testing.T) {
	rc := sharedRoute([]string{"api.test"}, "shop")
	rc.redirect = true
	rc.mw = middlewareSet{cors: &corsSpec{allowAll: true}}
	local := string(renderRouter(rc))
	rc.peer, _ = rc.peerHosts("test", testPeer)
	out := string(renderRouter(rc))

	if !strings.HasPrefix(out, strings.Split(local, "  middlewares:")[0]) {
		t.Errorf("the local routers changed:\n%s", out)
	}
	peerRule := "\"Host(`api.studio-01.mesh.internal`) || Host(`api.shop.studio-01.mesh.internal`)\""
	for _, want := range []string{
		"    proximo-api-peer:\n      entryPoints:\n        - websecure\n      rule: " + peerRule + "\n      service: proximo-api\n      middlewares:\n        - proximo-api-cors\n      tls: {}\n",
		"    proximo-api-peer-redirect:\n      entryPoints:\n        - web\n      rule: " + peerRule + "\n      service: proximo-api\n      middlewares:\n        - proximo-api-redirect\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing\n%s\nin\n%s", want, out)
		}
	}
	if strings.Contains(out, "Host:") || strings.Contains(out, "customRequestHeaders") {
		t.Errorf("the peer router rewrites a header:\n%s", out)
	}
}

// An unshared route renders exactly as it did before sharing existed.
func TestRenderRouterUnsharedIsUnchanged(t *testing.T) {
	rc := sharedRoute([]string{"api.test"}, "shop")
	rc.share = false
	if out := string(renderRouter(rc)); strings.Contains(out, "-peer") {
		t.Errorf("an unshared route has a peer router:\n%s", out)
	}
}

// peerWatcher is a Watcher holding this machine's peer material, minted by
// the whole ceremony: team root, CSR, intermediate.
func peerWatcher(t *testing.T) (*Watcher, *x509.Certificate) {
	t.Helper()
	w := testWatcher(t)
	rootPEM, rootKey, err := tls.NewTeamRoot("mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	csr, err := tls.MachineCSR("studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	intPEM, err := tls.SignIntermediate(rootPEM, rootKey, csr, "studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	if err := tls.InstallIntermediate(intPEM); err != nil {
		t.Fatal(err)
	}
	certDir, err := tls.Dir()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Machine, cfg.PeerSuffix, cfg.TeamRoot = "studio-01", "mesh.internal", "/team-root.crt"
	caDir := t.TempDir()
	if err := copyPeer(caDir, certDir, cfg); err != nil {
		t.Fatal(err)
	}
	w.peer = loadPeerMaterial(caDir)
	if w.peer == nil {
		t.Fatal("no peer material loaded")
	}
	block, _ := pem.Decode(rootPEM)
	root, _ := x509.ParseCertificate(block.Bytes)
	return w, root
}

func TestSyncCertsPeerLeaf(t *testing.T) {
	w, root := peerWatcher(t)
	rc := sharedRoute([]string{"api.test"}, "shop")
	rc.peer, _ = rc.peerHosts("test", w.peer.names)
	w.syncCerts([]routedContainer{rc})

	certsDir := filepath.Join(w.dynamicDir, "certs")
	leafFile := filepath.Join(certsDir, "api"+peerCertSuffix)
	data, err := os.ReadFile(leafFile)
	if err != nil {
		t.Fatal(err)
	}
	// The file presents the intermediate after the leaf: a colleague's machine
	// holds only the root.
	var chain []*x509.Certificate
	for block, rest := pem.Decode(data); block != nil; block, rest = pem.Decode(rest) {
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, c)
	}
	if len(chain) != 2 {
		t.Fatalf("chain has %d certificates, want leaf + intermediate", len(chain))
	}
	if !slices.Equal(chain[0].DNSNames, rc.peer) {
		t.Errorf("SANs = %v, want exactly %v", chain[0].DNSNames, rc.peer)
	}
	roots, inter := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(root)
	inter.AddCert(chain[1])
	if _, err := chain[0].Verify(x509.VerifyOptions{DNSName: "api.shop.studio-01.mesh.internal", Roots: roots, Intermediates: inter}); err != nil {
		t.Errorf("the peer leaf does not verify against the team root: %v", err)
	}
	tlsYAML, _ := os.ReadFile(filepath.Join(w.dynamicDir, "proximo-tls.yml"))
	if !strings.Contains(string(tlsYAML), "- certFile: "+leafFile) {
		t.Errorf("the peer leaf is not listed:\n%s", tlsYAML)
	}

	// Removing proximo.share withdraws the peer leaf at the next reconcile
	// (Acceptance 15); the local leaf stays.
	rc.share, rc.peer = false, nil
	w.syncCerts([]routedContainer{rc})
	if fileExists(leafFile) {
		t.Error("the peer leaf outlived proximo.share")
	}
	if !fileExists(filepath.Join(certsDir, "api.crt")) {
		t.Error("the local leaf went with it")
	}
	tlsYAML, _ = os.ReadFile(filepath.Join(w.dynamicDir, "proximo-tls.yml"))
	if strings.Contains(string(tlsYAML), peerCertSuffix) {
		t.Errorf("the TLS config still lists the peer leaf:\n%s", tlsYAML)
	}
}

// Nothing peer is copied into the stack until every value the peer router
// needs is configured, so an unconfigured machine's stack is today's.
func TestCopyPeerIsInertUnconfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	certDir, _ := tls.Dir()
	caDir := t.TempDir()
	cfg := config.Default()
	cfg.Machine, cfg.PeerSuffix = "studio-01", "mesh.internal"
	if err := copyPeer(caDir, certDir, cfg); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(caDir); len(entries) != 0 {
		t.Errorf("peer files copied with team-root and intermediate missing: %v", entries)
	}
	if loadPeerMaterial(caDir) != nil {
		t.Error("peer material loaded from an empty directory")
	}
}

func TestPeerMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := PeerMissing(config.Default()); !slices.Equal(got, []string{"machine", "peer-suffix", "address", "team-root", "intermediate"}) {
		t.Errorf("PeerMissing(unset) = %v", got)
	}
	cfg := config.Default()
	cfg.Machine, cfg.PeerSuffix, cfg.TeamRoot = "studio-01", "mesh.internal", "/team-root.crt"
	if got := PeerMissing(cfg); !slices.Equal(got, []string{"address", "intermediate"}) {
		t.Errorf("PeerMissing = %v", got)
	}
}

func TestShareIgnoredOnATCPRoute(t *testing.T) {
	c := makeSummary(map[string]string{
		proximoHostsLabel:   "db.test",
		proximoTCPPortLabel: "5432",
		proximoShareLabel:   "true",
	})
	rc, _, info := classify(t.Context(), failInspect(t), c, "test")
	if rc.share || !slices.Contains(info.tcpIgnoredHTTP, proximoShareLabel) {
		t.Errorf("share = %v, ignored = %v; want the label dropped and reported", rc.share, info.tcpIgnoredHTTP)
	}
}

func TestServedRoutesCarryPeerNames(t *testing.T) {
	shared := sharedRoute([]string{"api.test"}, "shop")
	plain := routedContainer{name: "web", safe: "web", hosts: []string{"web.test"}, proximo: true, port: 80}
	loser := sharedRoute([]string{"api.test"}, "blog")
	loser.name = "blog-api"
	db := routedContainer{name: "db", safe: "db", hosts: []string{"db.test"}, proximo: true, tcpPorts: []int{5432}, tcpTLS: tcpTLSTerminate}
	res := resolveRoutes([]routedContainer{shared, plain, loser, db})
	rows := servedRoutes(res, nil, map[string]bool{"db": true}, "test", testPeer)

	byKey := map[string]Route{}
	for _, r := range rows {
		byKey[r.Container+" "+r.Host] = r
	}
	if r := byKey["api api.test"]; !r.Share || r.Peer != "api.studio-01.mesh.internal" || r.PeerQualified != "api.shop.studio-01.mesh.internal" {
		t.Errorf("shared row = %+v", r)
	}
	if r := byKey["web web.test"]; r.Share || r.Peer != "" {
		t.Errorf("unshared row = %+v", r)
	}
	// The container that lost the Collision keeps only its Qualified peer name.
	if r := byKey["blog-api api.test"]; !r.Collision || r.Peer != "" || r.PeerQualified != "api.blog.studio-01.mesh.internal" {
		t.Errorf("collision row = %+v", r)
	}
	if r := byKey["db db.test"]; !r.ShareTCP {
		t.Errorf("TCP row = %+v, want the ignored label flagged", r)
	}
	// Without peer names (a value missing), the label still shows.
	rows = servedRoutes(res, nil, nil, "test", PeerNames{})
	for _, r := range rows {
		if r.Container == "api" && (!r.Share || r.Peer != "") {
			t.Errorf("unserved shared row = %+v", r)
		}
	}
}
