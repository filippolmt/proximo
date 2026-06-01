package docker

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/filippolmt/proximo/internal/tls"
)

func TestProximoHosts(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"single", "web.test", []string{"web.test"}},
		{"multi with whitespace", "app.test, api.test", []string{"app.test", "api.test"}},
		{"empty and whitespace entries", "app.test,,  ,api.test", []string{"app.test", "api.test"}},
		{"absent", "", nil},
		{"invalid charset dropped", "ok.test, bad host!, also_bad.test", []string{"ok.test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proximoHosts(map[string]string{proximoHostsLabel: tt.raw})
			if !slices.Equal(got, tt.want) {
				t.Fatalf("proximoHosts(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestIsProximoEnabled(t *testing.T) {
	enabled := []string{"", "true", "1", "yes", "anything"}
	for _, v := range enabled {
		if !isProximoEnabled(map[string]string{proximoEnableLabel: v}) {
			t.Errorf("proximo.enable=%q should be enabled", v)
		}
	}
	disabled := []string{"false", "FALSE", "0", "no", " No "}
	for _, v := range disabled {
		if isProximoEnabled(map[string]string{proximoEnableLabel: v}) {
			t.Errorf("proximo.enable=%q should be disabled", v)
		}
	}
}

func TestIsRoutedProximo(t *testing.T) {
	if !isRouted(makeSummary(map[string]string{proximoHostsLabel: "web.test"})) {
		t.Error("proximo.hosts alone should route")
	}
	if isRouted(makeSummary(map[string]string{proximoHostsLabel: "web.test", proximoEnableLabel: "false"})) {
		t.Error("proximo.enable=false should park the container")
	}
	if isRouted(makeSummary(map[string]string{roleLabel: "traefik", proximoHostsLabel: "web.test"})) {
		t.Error("stack container must never route even with proximo.hosts")
	}
	if isRouted(makeSummary(map[string]string{proximoEnableLabel: "true"})) {
		t.Error("enable without hosts must not route")
	}
}

func TestResolveBackendPortExplicit(t *testing.T) {
	w := &Watcher{}
	c := makeSummary(map[string]string{proximoHostsLabel: "app.test", proximoPortLabel: "8080"})
	port, ok := w.resolveBackendPort(context.Background(), c)
	if !ok || port != 8080 {
		t.Fatalf("explicit port = (%d,%v), want (8080,true)", port, ok)
	}

	bad := makeSummary(map[string]string{proximoPortLabel: "nope"})
	if _, ok := w.resolveBackendPort(context.Background(), bad); ok {
		t.Error("invalid explicit port should be rejected")
	}
}

func TestPortFromExposed(t *testing.T) {
	single := nat.PortSet{"3000/tcp": struct{}{}}
	if p, ok := portFromExposed(single); !ok || p != 3000 {
		t.Fatalf("single = (%d,%v), want (3000,true)", p, ok)
	}
	// One TCP plus a UDP port still resolves to the single TCP port.
	tcpAndUDP := nat.PortSet{"3000/tcp": struct{}{}, "53/udp": struct{}{}}
	if p, ok := portFromExposed(tcpAndUDP); !ok || p != 3000 {
		t.Fatalf("tcp+udp = (%d,%v), want (3000,true)", p, ok)
	}
	none := nat.PortSet{}
	if _, ok := portFromExposed(none); ok {
		t.Error("zero ports should be ambiguous")
	}
	many := nat.PortSet{"80/tcp": struct{}{}, "8080/tcp": struct{}{}}
	if _, ok := portFromExposed(many); ok {
		t.Error("multiple TCP ports should be ambiguous")
	}
}

func TestSanitizeName(t *testing.T) {
	tests := map[string]string{
		"whoami":       "whoami",
		"my/app":       "my-app",
		"Foo_Bar.test": "Foo_Bar.test",
		"--x--":        "x",
		"a b c":        "a-b-c",
		"":             "container",
		"!!!":          "container",
	}
	for in, want := range tests {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAssignSafeNamesDisambiguates(t *testing.T) {
	rcs := []routedContainer{
		{name: "web", id: "aaaaaaaaaaaa1111"},
		{name: "web", id: "bbbbbbbbbbbb2222"},
		{name: "api", id: "cccccccccccc3333"},
	}
	assignSafeNames(rcs)
	if rcs[0].safe == rcs[1].safe {
		t.Errorf("colliding names not disambiguated: %q == %q", rcs[0].safe, rcs[1].safe)
	}
	if rcs[2].safe != "api" {
		t.Errorf("unique name should keep base: got %q", rcs[2].safe)
	}
}

func TestRenderRouter(t *testing.T) {
	rc := routedContainer{name: "whoami", safe: "whoami", hosts: []string{"app.test", "api.test"}, port: 80, proximo: true}
	out := string(renderRouter(rc))
	wants := []string{
		"proximo-whoami:",
		"rule: \"Host(`app.test`) || Host(`api.test`)\"",
		"url: \"http://whoami:80\"",
		"- websecure",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("renderRouter output missing %q\n---\n%s", w, out)
		}
	}
}

// testWatcher builds a Watcher backed by a freshly generated local CA and a
// temp dynamic dir, for exercising certificate sync without Docker.
func testWatcher(t *testing.T) *Watcher {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	certPath, keyPath, err := tls.EnsureCA()
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	caCert, caKey, err := tls.LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	return &Watcher{
		caCert:     caCert,
		caKey:      caKey,
		dynamicDir: t.TempDir(),
		lastHosts:  map[string]string{},
	}
}

func TestSyncCertsPerContainer(t *testing.T) {
	w := testWatcher(t)
	certsDir := filepath.Join(w.dynamicDir, "certs")

	app := routedContainer{name: "app", safe: "app", hosts: []string{"app.test"}, proximo: true}
	db := routedContainer{name: "db", safe: "db", hosts: []string{"db.test"}, proximo: true}

	// Two containers → two certs and a TLS config listing both.
	w.syncCerts([]routedContainer{app, db})
	appCrt := filepath.Join(certsDir, "app.crt")
	dbCrt := filepath.Join(certsDir, "db.crt")
	if !fileExists(appCrt) || !fileExists(dbCrt) {
		t.Fatal("expected one cert per container")
	}
	tlsYAML, err := os.ReadFile(filepath.Join(w.dynamicDir, "proximo-tls.yml"))
	if err != nil {
		t.Fatalf("read tls config: %v", err)
	}
	if !strings.Contains(string(tlsYAML), "app.crt") || !strings.Contains(string(tlsYAML), "db.crt") {
		t.Error("tls config should list both certificates")
	}

	// Same host set → not reissued (bytes unchanged).
	before, _ := os.ReadFile(appCrt)
	w.syncCerts([]routedContainer{app, db})
	after, _ := os.ReadFile(appCrt)
	if !slices.Equal(before, after) {
		t.Error("unchanged host set should not reissue the certificate")
	}

	// Host change → that container's cert is reissued.
	appPlus := routedContainer{name: "app", safe: "app", hosts: []string{"app.test", "extra.test"}, proximo: true}
	w.syncCerts([]routedContainer{appPlus, db})
	reissued, _ := os.ReadFile(appCrt)
	if slices.Equal(before, reissued) {
		t.Error("host change should reissue the certificate")
	}

	// Dropping a container removes its cert files.
	w.syncCerts([]routedContainer{db})
	if fileExists(appCrt) || fileExists(filepath.Join(certsDir, "app.key")) {
		t.Error("cert files should be removed when a container stops being routed")
	}
	if !fileExists(dbCrt) {
		t.Error("remaining container's cert should survive")
	}

	// No routed containers → TLS config removed.
	w.syncCerts(nil)
	if fileExists(filepath.Join(w.dynamicDir, "proximo-tls.yml")) {
		t.Error("tls config should be removed when nothing is routed")
	}
}

func TestSyncDynamicWritesAndCleans(t *testing.T) {
	w := &Watcher{dynamicDir: t.TempDir(), lastHosts: map[string]string{}}
	rc := routedContainer{name: "whoami", safe: "whoami", hosts: []string{"whoami.test"}, port: 80, proximo: true}

	w.syncDynamic([]routedContainer{rc})
	routerFile := filepath.Join(w.dynamicDir, routerFilePrefix+"whoami.yml")
	if !fileExists(routerFile) {
		t.Fatal("expected router config file to be written")
	}

	// No longer routed → file removed.
	w.syncDynamic(nil)
	if fileExists(routerFile) {
		t.Error("stale router config should be removed")
	}
}
