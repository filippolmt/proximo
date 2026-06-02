package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/filippolmt/proximo/internal/tls"
)

// failInspect returns an inspector that fails the test if called — for cases
// where port resolution must not need ContainerInspect.
func failInspect(t *testing.T) inspector {
	t.Helper()
	return func(context.Context, string) (container.InspectResponse, error) {
		t.Fatal("inspect must not be called")
		return container.InspectResponse{}, nil
	}
}

// exposeInspect returns an inspector reporting the given exposed ports.
func exposeInspect(ports nat.PortSet) inspector {
	return func(context.Context, string) (container.InspectResponse, error) {
		return container.InspectResponse{Config: &container.Config{ExposedPorts: ports}}, nil
	}
}

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

func TestClassifyHosts(t *testing.T) {
	// proximo path: enabled proximo.hosts wins.
	if hosts, proximo, _ := classifyHosts(map[string]string{proximoHostsLabel: "app.test"}); !proximo || !slices.Equal(hosts, []string{"app.test"}) {
		t.Errorf("proximo path = (%v, proximo=%v), want ([app.test], true)", hosts, proximo)
	}
	// proximo.enable=false falls back to the native rule (not the proximo path).
	hosts, proximo, _ := classifyHosts(map[string]string{
		proximoHostsLabel:             "parked.test",
		proximoEnableLabel:            "false",
		enableLabel:                   "true",
		"traefik.http.routers.x.rule": "Host(`live.test`)",
	})
	if proximo || !slices.Equal(hosts, []string{"live.test"}) {
		t.Errorf("enable=false = (%v, proximo=%v), want ([live.test], false)", hosts, proximo)
	}
	// Native path requires traefik.enable=true.
	if hosts, _, _ := classifyHosts(map[string]string{"traefik.http.routers.w.rule": "Host(`x.test`)"}); len(hosts) != 0 {
		t.Errorf("host rule without traefik.enable should not route, got %v", hosts)
	}
	// Stack containers are never routed.
	if hosts, _, _ := classifyHosts(map[string]string{roleLabel: "traefik", proximoHostsLabel: "x.test"}); len(hosts) != 0 {
		t.Errorf("stack container should not route, got %v", hosts)
	}
	// Invalid proximo hosts are surfaced for logging.
	if _, _, invalid := classifyHosts(map[string]string{proximoHostsLabel: "ok.test, bad host!"}); !slices.Equal(invalid, []string{"bad host!"}) {
		t.Errorf("invalid = %v, want [bad host!]", invalid)
	}
}

func TestResolveBackendPortExplicit(t *testing.T) {
	// An explicit, valid proximo.port resolves without inspecting the container.
	c := makeSummary(map[string]string{proximoHostsLabel: "app.test", proximoPortLabel: "8080"})
	port, ok, _ := resolveBackendPort(context.Background(), failInspect(t), c)
	if !ok || port != 8080 {
		t.Fatalf("explicit port = (%d,%v), want (8080,true)", port, ok)
	}

	bad := makeSummary(map[string]string{proximoPortLabel: "nope"})
	if _, ok, res := resolveBackendPort(context.Background(), failInspect(t), bad); ok || res.badLabel != "nope" {
		t.Errorf("invalid explicit port = (ok=%v, badLabel=%q), want (false, \"nope\")", ok, res.badLabel)
	}
}

// TestPortResultHint: the status hint is cause-specific so `proximo status` and
// the watcher logs agree on why a port could not be resolved.
func TestPortResultHint(t *testing.T) {
	if got := (portResult{badLabel: "nope"}).hint(); !strings.Contains(got, "invalid") || !strings.Contains(got, "nope") {
		t.Errorf("badLabel hint = %q, want it to name the invalid value", got)
	}
	if got := (portResult{inspectErr: errors.New("boom")}).hint(); !strings.Contains(got, "inspect") {
		t.Errorf("inspectErr hint = %q, want it to mention the inspect failure", got)
	}
	if got := (portResult{exposedTCP: 2}).hint(); !strings.Contains(got, proximoPortLabel) || !strings.Contains(got, "2") {
		t.Errorf("ambiguous hint = %q, want it to suggest %s and name the count", got, proximoPortLabel)
	}
}

// TestClassifyExplicitPort: classify resolves the explicit proximo.port (4.1).
func TestClassifyExplicitPort(t *testing.T) {
	c := makeSummary(map[string]string{proximoHostsLabel: "app.test", proximoPortLabel: "8080"})
	rc, ok, _ := classify(context.Background(), failInspect(t), c)
	if !ok || !rc.proximo || rc.port != 8080 || !slices.Equal(rc.hosts, []string{"app.test"}) {
		t.Fatalf("classify explicit = (hosts=%v, proximo=%v, port=%d, ok=%v), want ([app.test],true,8080,true)",
			rc.hosts, rc.proximo, rc.port, ok)
	}
}

// TestClassifyPortResolution: a single exposed port resolves; zero or multiple
// exposed ports (no proximo.port) make the proximo route not-routed but keep its
// hosts for status to flag (4.2).
func TestClassifyPortResolution(t *testing.T) {
	c := makeSummary(map[string]string{proximoHostsLabel: "app.test"})

	rc, ok, _ := classify(context.Background(), exposeInspect(nat.PortSet{"3000/tcp": struct{}{}}), c)
	if !ok || rc.port != 3000 {
		t.Fatalf("single exposed port = (port=%d, ok=%v), want (3000,true)", rc.port, ok)
	}

	for name, ports := range map[string]nat.PortSet{
		"zero":     {},
		"multiple": {"80/tcp": struct{}{}, "8080/tcp": struct{}{}},
	} {
		rc, ok, info := classify(context.Background(), exposeInspect(ports), c)
		if ok {
			t.Errorf("%s ports: expected not routed", name)
		}
		if !info.portFailed {
			t.Errorf("%s ports: expected portFailed=true", name)
		}
		if !rc.proximo || !slices.Equal(rc.hosts, []string{"app.test"}) {
			t.Errorf("%s ports: rc should keep proximo hosts for flagging, got %v", name, rc.hosts)
		}
	}
}

// TestClassifyNativeNeedsNoInspect: the native traefik path (enable + Host rule)
// is classified without inspecting the container (4.3).
func TestClassifyNativeNeedsNoInspect(t *testing.T) {
	c := makeSummary(map[string]string{enableLabel: "true", "traefik.http.routers.w.rule": "Host(`x.test`)"})
	rc, ok, _ := classify(context.Background(), failInspect(t), c)
	if !ok || rc.proximo || !slices.Equal(rc.hosts, []string{"x.test"}) {
		t.Fatalf("native classify = (hosts=%v, proximo=%v, ok=%v), want ([x.test],false,true)", rc.hosts, rc.proximo, ok)
	}
}

// TestWatcherAndStatusShareClassify: both the watcher (buildRouted) and status
// (Routes) delegate to classify, so for the same labels they reach the same
// decision. The proximo.enable=false + native-rule case (the original skew)
// must resolve to the native host for both, never to the parked proximo host (4.4).
func TestWatcherAndStatusShareClassify(t *testing.T) {
	c := makeSummary(map[string]string{
		proximoHostsLabel:             "parked.test",
		proximoEnableLabel:            "false",
		enableLabel:                   "true",
		"traefik.http.routers.x.rule": "Host(`live.test`)",
	})
	rc, ok, _ := classify(context.Background(), failInspect(t), c)
	if !ok || rc.proximo || !slices.Equal(rc.hosts, []string{"live.test"}) {
		t.Fatalf("shared decision = (hosts=%v, proximo=%v, ok=%v), want ([live.test],false,true)", rc.hosts, rc.proximo, ok)
	}
}

func TestPortFromExposed(t *testing.T) {
	single := nat.PortSet{"3000/tcp": struct{}{}}
	if p, n, ok := portFromExposed(single); !ok || p != 3000 || n != 1 {
		t.Fatalf("single = (%d,%d,%v), want (3000,1,true)", p, n, ok)
	}
	// One TCP plus a UDP port still resolves to the single TCP port.
	tcpAndUDP := nat.PortSet{"3000/tcp": struct{}{}, "53/udp": struct{}{}}
	if p, n, ok := portFromExposed(tcpAndUDP); !ok || p != 3000 || n != 1 {
		t.Fatalf("tcp+udp = (%d,%d,%v), want (3000,1,true)", p, n, ok)
	}
	if _, n, ok := portFromExposed(nat.PortSet{}); ok || n != 0 {
		t.Errorf("zero ports = (n=%d,ok=%v), want (0,false)", n, ok)
	}
	many := nat.PortSet{"80/tcp": struct{}{}, "8080/tcp": struct{}{}}
	if _, n, ok := portFromExposed(many); ok || n != 2 {
		t.Errorf("multiple TCP ports = (n=%d,ok=%v), want (2,false)", n, ok)
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
