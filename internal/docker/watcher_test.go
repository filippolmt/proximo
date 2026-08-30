package docker

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// failInspect returns an inspector that fails the test if called — for cases
// where port resolution must not need ContainerInspect.
func failInspect(t *testing.T) inspector {
	t.Helper()
	return func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		t.Fatal("inspect must not be called")
		return client.ContainerInspectResult{}, nil
	}
}

// exposeInspect returns an inspector reporting the given exposed ports.
func exposeInspect(ports network.PortSet) inspector {
	return func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{Container: container.InspectResponse{Config: &container.Config{ExposedPorts: ports}}}, nil
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

// TestIsProximoRedirect: the redirect label is off by default and on only for
// the truthy set (true/1/yes, case-insensitive); falsy and garbage stay off —
// the inverted default of proximo.enable (4.1).
func TestIsProximoRedirect(t *testing.T) {
	on := []string{"true", "TRUE", "1", "yes", " Yes "}
	for _, v := range on {
		if !isProximoRedirect(map[string]string{proximoRedirectLabel: v}) {
			t.Errorf("proximo.redirect=%q should enable the redirect", v)
		}
	}
	off := []string{"", "false", "FALSE", "0", "no", "anything", "garbage"}
	for _, v := range off {
		if isProximoRedirect(map[string]string{proximoRedirectLabel: v}) {
			t.Errorf("proximo.redirect=%q should not enable the redirect", v)
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
	rc, ok, _ := classify(context.Background(), failInspect(t), c, "test")
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

	rc, ok, _ := classify(context.Background(), exposeInspect(network.PortSet{network.MustParsePort("3000/tcp"): {}}), c, "test")
	if !ok || rc.port != 3000 {
		t.Fatalf("single exposed port = (port=%d, ok=%v), want (3000,true)", rc.port, ok)
	}

	for name, ports := range map[string]network.PortSet{
		"zero":     {},
		"multiple": {network.MustParsePort("80/tcp"): {}, network.MustParsePort("8080/tcp"): {}},
	} {
		rc, ok, info := classify(context.Background(), exposeInspect(ports), c, "test")
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
	rc, ok, _ := classify(context.Background(), failInspect(t), c, "test")
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
	rc, ok, _ := classify(context.Background(), failInspect(t), c, "test")
	if !ok || rc.proximo || !slices.Equal(rc.hosts, []string{"live.test"}) {
		t.Fatalf("shared decision = (hosts=%v, proximo=%v, ok=%v), want ([live.test],false,true)", rc.hosts, rc.proximo, ok)
	}
}

func TestPortFromExposed(t *testing.T) {
	single := network.PortSet{network.MustParsePort("3000/tcp"): {}}
	if p, n, ok := portFromExposed(single); !ok || p != 3000 || n != 1 {
		t.Fatalf("single = (%d,%d,%v), want (3000,1,true)", p, n, ok)
	}
	// One TCP plus a UDP port still resolves to the single TCP port.
	tcpAndUDP := network.PortSet{network.MustParsePort("3000/tcp"): {}, network.MustParsePort("53/udp"): {}}
	if p, n, ok := portFromExposed(tcpAndUDP); !ok || p != 3000 || n != 1 {
		t.Fatalf("tcp+udp = (%d,%d,%v), want (3000,1,true)", p, n, ok)
	}
	if _, n, ok := portFromExposed(network.PortSet{}); ok || n != 0 {
		t.Errorf("zero ports = (n=%d,ok=%v), want (0,false)", n, ok)
	}
	many := network.PortSet{network.MustParsePort("80/tcp"): {}, network.MustParsePort("8080/tcp"): {}}
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

// TestRenderRouterRedirect: with redirect off, renderRouter emits only the
// websecure router (no web entrypoint, no middleware); with redirect on it adds
// a web router + redirectScheme middleware sharing the same Host rule (4.2).
func TestRenderRouterRedirect(t *testing.T) {
	base := routedContainer{name: "whoami", safe: "whoami", hosts: []string{"app.test"}, port: 80, proximo: true}

	off := string(renderRouter(base))
	for _, unwanted := range []string{"- web\n", "middlewares:", "redirectScheme"} {
		if strings.Contains(off, unwanted) {
			t.Errorf("redirect off should not emit %q\n---\n%s", unwanted, off)
		}
	}

	on := base
	on.redirect = true
	out := string(renderRouter(on))
	wants := []string{
		"proximo-whoami-redirect:",
		"        - web\n",
		"redirectScheme:",
		"scheme: https",
		"permanent: false",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("redirect on missing %q\n---\n%s", w, out)
		}
	}
	// The web router reuses the websecure router's Host rule (one per router).
	if n := strings.Count(out, "Host(`app.test`)"); n != 2 {
		t.Errorf("redirect router should share the websecure Host rule, got %d occurrences\n%s", n, out)
	}
}

// TestTraefikNoGlobalRedirect: the embedded (materialized) traefik.yml keeps the
// web entrypoint on :80 but configures no global http.redirections (4.3).
func TestTraefikNoGlobalRedirect(t *testing.T) {
	data, err := assets.ReadFile("assets/traefik/traefik.yml")
	if err != nil {
		t.Fatalf("read embedded traefik.yml: %v", err)
	}
	yaml := string(data)
	if strings.Contains(yaml, "redirections") {
		t.Errorf("traefik.yml must not configure a global http.redirections:\n%s", yaml)
	}
	if !strings.Contains(yaml, "web:") || !strings.Contains(yaml, ":80") {
		t.Errorf("traefik.yml should keep the web entrypoint on :80:\n%s", yaml)
	}
}

// testWatcher builds a Watcher backed by a freshly generated local CA and a
// temp dynamic dir, for exercising certificate sync without Docker.
func testWatcher(t *testing.T) *Watcher {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
		tld:        config.DefaultTLD,
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

	// Dropping a container removes its cert files and, crucially, drops the
	// reference from proximo-tls.yml. syncCerts writes the TLS config before
	// unlinking stale certs precisely so this end state is never "cert removed
	// but still referenced" — the window that made Traefik log "no PEM data".
	w.syncCerts([]routedContainer{db})
	if fileExists(appCrt) || fileExists(filepath.Join(certsDir, "app.key")) {
		t.Error("cert files should be removed when a container stops being routed")
	}
	if !fileExists(dbCrt) {
		t.Error("remaining container's cert should survive")
	}
	tlsYAML, err = os.ReadFile(filepath.Join(w.dynamicDir, "proximo-tls.yml"))
	if err != nil {
		t.Fatalf("read tls config: %v", err)
	}
	if strings.Contains(string(tlsYAML), "app.crt") {
		t.Error("tls config must not reference a removed container's certificate")
	}
	if !strings.Contains(string(tlsYAML), "db.crt") {
		t.Error("tls config should still list the surviving certificate")
	}

	// No routed containers → TLS config removed.
	w.syncCerts(nil)
	if fileExists(filepath.Join(w.dynamicDir, "proximo-tls.yml")) {
		t.Error("tls config should be removed when nothing is routed")
	}
}

// fakeDocker implements dockerAPI without a daemon, recording the network
// attach/detach calls reconcile makes and serving event/error channels Run
// consumes.
type fakeDocker struct {
	mu         sync.Mutex
	containers []container.Summary
	inspect    map[string]container.InspectResponse

	connected    []string // network IDs traefik was connected to
	disconnected []string // network IDs traefik was disconnected from
	listN        int      // ContainerList call count
	eventsN      int      // Events subscription count

	events chan events.Message
	errs   chan error
}

func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		inspect: map[string]container.InspectResponse{},
		events:  make(chan events.Message, 1),
		errs:    make(chan error, 1),
	}
}

func (f *fakeDocker) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listN++
	return client.ContainerListResult{Items: f.containers}, nil
}

func (f *fakeDocker) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return client.ContainerInspectResult{Container: f.inspect[id]}, nil
}

func (f *fakeDocker) NetworkConnect(_ context.Context, netID string, _ client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = append(f.connected, netID)
	return client.NetworkConnectResult{}, nil
}

func (f *fakeDocker) NetworkDisconnect(_ context.Context, netID string, _ client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnected = append(f.disconnected, netID)
	return client.NetworkDisconnectResult{}, nil
}

func (f *fakeDocker) Events(context.Context, client.EventsListOptions) client.EventsResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.eventsN++
	return client.EventsResult{Messages: f.events, Err: f.errs}
}

func (f *fakeDocker) listCount() int  { f.mu.Lock(); defer f.mu.Unlock(); return f.listN }
func (f *fakeDocker) eventCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.eventsN }

func netSummary(nets map[string]string) *container.NetworkSettingsSummary {
	out := &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{}}
	for name, id := range nets {
		out.Networks[name] = &network.EndpointSettings{NetworkID: id}
	}
	return out
}

// TestReconcileAttachesAndDetaches drives reconcile through the fake: Traefik
// must join the backend network of a routed container, keep the stack network,
// and be disconnected from a stale (no-longer-desired, non-stack) network.
func TestReconcileAttachesAndDetaches(t *testing.T) {
	f := newFakeDocker()
	f.containers = []container.Summary{
		{
			ID:     "traefikcid",
			Labels: map[string]string{roleLabel: "traefik"},
			NetworkSettings: netSummary(map[string]string{
				"proximo_default": "stacknet", // stack network: must be kept
				"oldnet":          "oldid",    // stale: must be disconnected
			}),
		},
		{
			ID:     "appcid",
			Names:  []string{"/app"},
			Labels: map[string]string{proximoHostsLabel: "app.test", proximoPortLabel: "8080"},
			NetworkSettings: netSummary(map[string]string{
				"appnet": "appid", // desired: traefik must connect
			}),
		},
	}

	w := &Watcher{cli: f, dynamicDir: t.TempDir(), tld: "test", lastHosts: map[string]string{}}
	if err := w.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if !slices.Equal(f.connected, []string{"appid"}) {
		t.Errorf("connected = %v, want [appid]", f.connected)
	}
	if !slices.Equal(f.disconnected, []string{"oldid"}) {
		t.Errorf("disconnected = %v, want [oldid] (stack network must be kept)", f.disconnected)
	}
}

// TestReconcileNoTraefikIsNoOp: with no traefik container running, reconcile
// touches no networks (it cannot attach a backend to a proxy that is not up).
func TestReconcileNoTraefik(t *testing.T) {
	f := newFakeDocker()
	f.containers = []container.Summary{
		{ID: "appcid", Names: []string{"/app"}, Labels: map[string]string{proximoHostsLabel: "app.test", proximoPortLabel: "8080"},
			NetworkSettings: netSummary(map[string]string{"appnet": "appid"})},
	}
	w := &Watcher{cli: f, dynamicDir: t.TempDir(), tld: "test", lastHosts: map[string]string{}}
	if err := w.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(f.connected) != 0 || len(f.disconnected) != 0 {
		t.Errorf("no traefik should touch no networks; connected=%v disconnected=%v", f.connected, f.disconnected)
	}
}

// TestRunReconcilesOnEventAndStops: Run reconciles once on start, again on a
// Docker event, and returns ctx.Err() when the context is cancelled.
func TestRunReconcilesOnEventAndStops(t *testing.T) {
	f := newFakeDocker()
	w := &Watcher{cli: f, dynamicDir: t.TempDir(), tld: "test", lastHosts: map[string]string{}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	f.events <- events.Message{} // trigger a reconcile beyond the initial one
	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if got := f.listCount(); got < 2 {
		t.Errorf("ContainerList called %d times, want >= 2 (initial + event)", got)
	}
}

// TestRunReconnectsAfterEventError: an error on the event stream makes Run
// re-subscribe (after its backoff) rather than exit, then it still stops on
// ctx cancel.
func TestRunReconnectsAfterEventError(t *testing.T) {
	f := newFakeDocker()
	w := &Watcher{cli: f, dynamicDir: t.TempDir(), tld: "test", lastHosts: map[string]string{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	f.errs <- errors.New("stream broke") // Run logs, backs off, re-subscribes

	deadline := time.After(5 * time.Second)
	for f.eventCount() < 2 {
		select {
		case <-deadline:
			t.Fatal("Run did not re-subscribe to Events after a stream error")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
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

// TestTraefikDashboardStaticConfig: the embedded traefik.yml enables the
// read-only dashboard and never the insecure API (4.1).
func TestTraefikDashboardStaticConfig(t *testing.T) {
	data, err := assets.ReadFile("assets/traefik/traefik.yml")
	if err != nil {
		t.Fatalf("read embedded traefik.yml: %v", err)
	}
	yaml := string(data)
	if !strings.Contains(yaml, "dashboard: true") {
		t.Errorf("traefik.yml should enable api.dashboard:\n%s", yaml)
	}
	// Match the YAML key, not the word — the comment explaining the posture
	// legitimately mentions api.insecure.
	if strings.Contains(yaml, "insecure:") {
		t.Errorf("traefik.yml must not enable api.insecure:\n%s", yaml)
	}
}

// TestRenderRouterDashboard: the internal self-route renders a websecure router
// to api@internal with TLS and no backend loadbalancer/port block (4.2). With
// redirect set (as dashboardRoute always does) it additionally emits the web
// router + redirectScheme middleware, both still targeting api@internal.
func TestRenderRouterDashboard(t *testing.T) {
	out := string(renderRouter(routedContainer{
		name: "traefik", safe: dashboardSafe, hosts: []string{"traefik.test"}, proximo: true, internal: true, redirect: true,
	}))
	wants := []string{
		"proximo-dashboard:",
		"rule: \"Host(`traefik.test`)\"",
		"service: api@internal",
		"tls: {}",
		"- websecure",
		"proximo-dashboard-redirect:",
		"- web\n",
		"redirectScheme:",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("dashboard router missing %q\n---\n%s", w, out)
		}
	}
	for _, unwanted := range []string{"loadBalancer", "url:", "services:", "service: proximo-dashboard"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("dashboard router must not emit %q\n---\n%s", unwanted, out)
		}
	}
}

// dashboardCertSANs reads the dashboard leaf cert and returns its DNS SANs.
func dashboardCertSANs(t *testing.T, certsDir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(certsDir, dashboardSafe+".crt"))
	if err != nil {
		t.Fatalf("read dashboard cert: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("dashboard cert is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse dashboard cert: %v", err)
	}
	return cert.DNSNames
}

// TestReconcileDashboardSelfRoute: a reconcile with zero routed user containers
// still produces the dashboard route file and a cert with SAN traefik.<tld>,
// both listed in proximo-tls.yml (4.3).
func TestReconcileDashboardSelfRoute(t *testing.T) {
	f := newFakeDocker()
	f.containers = []container.Summary{
		{ID: "traefikcid", Labels: map[string]string{roleLabel: "traefik"}},
	}
	w := testWatcher(t)
	w.cli = f

	if err := w.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	routeFile := filepath.Join(w.dynamicDir, dashboardFile)
	if !fileExists(routeFile) {
		t.Fatal("expected dashboard route file after a reconcile with no user containers")
	}
	route, _ := os.ReadFile(routeFile)
	if !strings.Contains(string(route), "Host(`traefik.test`)") || !strings.Contains(string(route), "api@internal") {
		t.Errorf("dashboard route should target Host(`traefik.test`) via api@internal:\n%s", route)
	}

	if sans := dashboardCertSANs(t, filepath.Join(w.dynamicDir, "certs")); !slices.Contains(sans, "traefik.test") {
		t.Errorf("dashboard cert SANs = %v, want to include traefik.test", sans)
	}
	tlsYAML, err := os.ReadFile(filepath.Join(w.dynamicDir, "proximo-tls.yml"))
	if err != nil {
		t.Fatalf("read tls config: %v", err)
	}
	if !strings.Contains(string(tlsYAML), dashboardSafe+".crt") {
		t.Errorf("proximo-tls.yml should list the dashboard cert:\n%s", tlsYAML)
	}
}

// TestReconcileDashboardSurvivesStaleCleanup: adding then removing a routed user
// container garbage-collects the user route/cert but never the dashboard's (4.4).
func TestReconcileDashboardSurvivesStaleCleanup(t *testing.T) {
	traefik := container.Summary{ID: "traefikcid", Labels: map[string]string{roleLabel: "traefik"}}
	app := container.Summary{
		ID:     "appcid",
		Names:  []string{"/app"},
		Labels: map[string]string{proximoHostsLabel: "app.test", proximoPortLabel: "8080"},
	}

	f := newFakeDocker()
	f.containers = []container.Summary{traefik, app}
	w := testWatcher(t)
	w.cli = f

	if err := w.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile with app: %v", err)
	}
	appRoute := filepath.Join(w.dynamicDir, routerFilePrefix+"app.yml")
	appCrt := filepath.Join(w.dynamicDir, "certs", "app.crt")
	if !fileExists(appRoute) || !fileExists(appCrt) {
		t.Fatal("expected the user container's route and cert after the first reconcile")
	}

	f.mu.Lock()
	f.containers = []container.Summary{traefik}
	f.mu.Unlock()
	if err := w.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile without app: %v", err)
	}

	if fileExists(appRoute) || fileExists(appCrt) {
		t.Error("user route/cert should be garbage-collected")
	}
	if !fileExists(filepath.Join(w.dynamicDir, dashboardFile)) {
		t.Error("dashboard route must survive stale cleanup")
	}
	if !fileExists(filepath.Join(w.dynamicDir, "certs", dashboardSafe+".crt")) {
		t.Error("dashboard cert must survive stale cleanup")
	}
}

// TestReconcileHealthGating: a health-gated container is not routed while
// starting, gains its route+cert once healthy, and has them withdrawn when it
// turns unhealthy — driven by the Health status the watcher reads each reconcile
// (the same status a `health_status` event delivers).
func TestReconcileHealthGating(t *testing.T) {
	traefik := container.Summary{ID: "traefikcid", Labels: map[string]string{roleLabel: "traefik"}}
	app := container.Summary{
		ID:     "appcid",
		Names:  []string{"/app"},
		Labels: map[string]string{proximoHostsLabel: "app.test", proximoPortLabel: "8080"},
		Health: &container.HealthSummary{Status: container.Starting},
	}

	f := newFakeDocker()
	w := testWatcher(t)
	w.cli = f
	appRoute := filepath.Join(w.dynamicDir, routerFilePrefix+"app.yml")
	appCrt := filepath.Join(w.dynamicDir, "certs", "app.crt")

	setHealth := func(s container.HealthStatus) {
		app.Health = &container.HealthSummary{Status: s}
		f.mu.Lock()
		f.containers = []container.Summary{traefik, app}
		f.mu.Unlock()
		if err := w.reconcile(context.Background()); err != nil {
			t.Fatalf("reconcile (%s): %v", s, err)
		}
	}

	setHealth(container.Starting)
	if fileExists(appRoute) || fileExists(appCrt) {
		t.Error("a starting health-gated container must not be routed")
	}

	setHealth(container.Healthy)
	if !fileExists(appRoute) || !fileExists(appCrt) {
		t.Error("a healthy container must be routed (route + cert present)")
	}

	setHealth(container.Unhealthy)
	if fileExists(appRoute) || fileExists(appCrt) {
		t.Error("an unhealthy container's route + cert must be withdrawn")
	}
}

// TestReconcileHealthGatingExemptions: a container with no healthcheck, and one
// that opts out with proximo.health=false, are both routed immediately on
// running regardless of (missing) health — the change stays additive for the
// common no-healthcheck case.
func TestReconcileHealthGatingExemptions(t *testing.T) {
	traefik := container.Summary{ID: "traefikcid", Labels: map[string]string{roleLabel: "traefik"}}
	noHealth := container.Summary{
		ID:     "nohealthcid",
		Names:  []string{"/nohealth"},
		Labels: map[string]string{proximoHostsLabel: "nohealth.test", proximoPortLabel: "8080"},
	}
	optOut := container.Summary{
		ID:     "optoutcid",
		Names:  []string{"/optout"},
		Labels: map[string]string{proximoHostsLabel: "optout.test", proximoPortLabel: "8080", proximoHealthLabel: "false"},
		Health: &container.HealthSummary{Status: container.Starting},
	}

	f := newFakeDocker()
	f.containers = []container.Summary{traefik, noHealth, optOut}
	w := testWatcher(t)
	w.cli = f
	if err := w.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	for _, safe := range []string{"nohealth", "optout"} {
		if !fileExists(filepath.Join(w.dynamicDir, routerFilePrefix+safe+".yml")) {
			t.Errorf("%s should be routed immediately (no gating)", safe)
		}
	}
}

// TestNewWatcherTLDFromEnv: the watcher reads the TLD from PROXIMO_TLD and
// falls back to the default TLD when unset (4.5).
func TestNewWatcherTLDFromEnv(t *testing.T) {
	t.Setenv("PROXIMO_TLD", "local-dev")
	w, err := NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	if w.tld != "local-dev" {
		t.Errorf("tld = %q, want local-dev", w.tld)
	}
	dr := w.dashboardRoute()
	if !slices.Equal(dr.hosts, []string{"traefik.local-dev"}) {
		t.Errorf("dashboard hosts = %v, want [traefik.local-dev]", dr.hosts)
	}
	if !dr.redirect {
		t.Error("dashboard route must always opt in to the HTTP->HTTPS redirect")
	}

	t.Setenv("PROXIMO_TLD", "")
	w, err = NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	if w.tld != config.DefaultTLD {
		t.Errorf("tld = %q, want default %q", w.tld, config.DefaultTLD)
	}
}

// TestReconcileTraefikRoleNeverContainerRouted: the proximo.role=traefik
// container produces no per-container route/cert via the normal path, even if
// it (mis)declares proximo.hosts — the dashboard is served only through the
// injected self-route (4.6).
func TestReconcileTraefikRoleNeverContainerRouted(t *testing.T) {
	f := newFakeDocker()
	f.containers = []container.Summary{
		{ID: "traefikcid", Names: []string{"/traefik"}, Labels: map[string]string{
			roleLabel:         "traefik",
			proximoHostsLabel: "evil.test",
			proximoPortLabel:  "8080",
		}},
	}
	w := testWatcher(t)
	w.cli = f

	if err := w.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(w.dynamicDir, routerFilePrefix+"*.yml")); len(files) != 0 {
		t.Errorf("role container must produce no per-container routes, got %v", files)
	}
	if fileExists(filepath.Join(w.dynamicDir, "certs", "traefik.crt")) {
		t.Error("role container must get no per-container cert")
	}
	if !fileExists(filepath.Join(w.dynamicDir, dashboardFile)) {
		t.Error("dashboard self-route should still be written")
	}
}

// TestAssignSafeNamesReservesDashboard: a user container named "dashboard" is
// suffixed away from the reserved self-route id (design D2).
func TestAssignSafeNamesReservesDashboard(t *testing.T) {
	rcs := []routedContainer{{name: "dashboard", id: "dddddddddddd4444"}}
	assignSafeNames(rcs)
	if rcs[0].safe == dashboardSafe {
		t.Errorf("user container named dashboard must not claim the reserved safe id, got %q", rcs[0].safe)
	}
}

// TestWriteFileIfChanged: a same-content write is skipped (no touch — Traefik's
// file watcher must not see spurious change events on every resync), a changed
// write and a first write go through.
func TestWriteFileIfChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "route.yml")

	if err := writeFileIfChanged(path, []byte("v1"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Backdate the mtime, rewrite identical content: the file must not be touched.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := writeFileIfChanged(path, []byte("v1"), 0o644); err != nil {
		t.Fatalf("same-content write: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !st.ModTime().Equal(past) {
		t.Error("same-content write should not touch the file")
	}

	// Changed content goes through.
	if err := writeFileIfChanged(path, []byte("v2"), 0o644); err != nil {
		t.Fatalf("changed write: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "v2" {
		t.Errorf("content = %q, want v2", data)
	}
}

// TestAtomicWrite: atomicWrite produces correct content and mode, fully replaces
// an existing file, and leaves no temp file behind.
func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.crt")

	if err := atomicWrite(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "hello" {
		t.Errorf("content = %q, want hello", data)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0o600", st.Mode().Perm())
	}

	// Overwrite atomically: content is fully replaced (no torn/truncated leftover).
	if err := atomicWrite(path, []byte("world!!"), 0o644); err != nil {
		t.Fatalf("atomicWrite overwrite: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "world!!" {
		t.Errorf("content = %q, want world!!", data)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0o644", st.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

// TestAtomicWriteCleansTempOnError: a failed rename (target is a directory)
// must surface the error and remove the temp file, leaving the dir clean.
func TestAtomicWriteCleansTempOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "busy")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("data"), 0o644); err == nil {
		t.Fatal("expected error renaming a file over a directory")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
		t.Errorf("temp file not cleaned on error: %v", leftovers)
	}
}

// TestSyncCertsRetainsRoutedContainer: a still-routed container keeps its cert
// across reconciles (no delete, no rewrite when hosts are unchanged); only a
// container that leaves the routed set has its cert/key removed.
func TestSyncCertsRetainsRoutedContainer(t *testing.T) {
	w := testWatcher(t)
	certsDir := filepath.Join(w.dynamicDir, "certs")
	app := routedContainer{name: "app", safe: "app", hosts: []string{"app.test"}, proximo: true}
	db := routedContainer{name: "db", safe: "db", hosts: []string{"db.test"}, proximo: true}

	w.syncCerts([]routedContainer{app, db})
	appCrt := filepath.Join(certsDir, "app.crt")
	appKey := filepath.Join(certsDir, "app.key")
	first, _ := os.ReadFile(appCrt)

	// Both stay routed → no delete log, cert retained and not rewritten.
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	w.syncCerts([]routedContainer{app, db})
	log.SetOutput(old)

	if !fileExists(appCrt) || !fileExists(appKey) {
		t.Fatal("still-routed container must keep its cert/key")
	}
	if again, _ := os.ReadFile(appCrt); !slices.Equal(first, again) {
		t.Error("unchanged host set must not rewrite the cert")
	}
	if strings.Contains(logs.String(), "removed certificate") {
		t.Errorf("still-routed container must not be deleted; log: %q", logs.String())
	}

	// app leaves the routed set → its cert/key are removed; db survives.
	w.syncCerts([]routedContainer{db})
	if fileExists(appCrt) || fileExists(appKey) {
		t.Error("departed container's cert/key must be removed")
	}
	if !fileExists(filepath.Join(certsDir, "db.crt")) {
		t.Error("still-routed db cert must survive")
	}
}

// TestDynamicWritesLeaveNoTemps: the route file and TLS config are written
// through the atomic helper and leave no stray temp files in either directory.
func TestDynamicWritesLeaveNoTemps(t *testing.T) {
	w := testWatcher(t)
	app := routedContainer{name: "app", safe: "app", hosts: []string{"app.test"}, port: 80, proximo: true}

	w.syncDynamic([]routedContainer{app})
	w.syncCerts([]routedContainer{app})

	if !fileExists(filepath.Join(w.dynamicDir, routerFilePrefix+"app.yml")) {
		t.Fatal("route file not written")
	}
	tlsYAML, err := os.ReadFile(filepath.Join(w.dynamicDir, "proximo-tls.yml"))
	if err != nil || !strings.Contains(string(tlsYAML), "app.crt") {
		t.Fatalf("tls config missing app.crt: %v", err)
	}
	for _, dir := range []string{w.dynamicDir, filepath.Join(w.dynamicDir, "certs")} {
		if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
			t.Errorf("temp files left in %s: %v", dir, leftovers)
		}
	}
}

// TestIsProximoPathStrip: path.strip reuses the redirect truthy helper — on for
// true/1/yes (case-insensitive), off otherwise and by default (1.1).
func TestIsProximoPathStrip(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "1", "yes", " Yes "} {
		if !isProximoPathStrip(map[string]string{proximoPathStripLabel: v}) {
			t.Errorf("proximo.path.strip=%q should strip", v)
		}
	}
	for _, v := range []string{"", "false", "0", "no", "garbage"} {
		if isProximoPathStrip(map[string]string{proximoPathStripLabel: v}) {
			t.Errorf("proximo.path.strip=%q should not strip", v)
		}
	}
}

// TestParseProximoPath: absent label is valid (no prefix); a leading-slash value
// is accepted; a value without a leading slash (or with unsafe characters) is
// rejected so the container is skipped (1.1).
func TestParseProximoPath(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"", "", true},
		{"/api", "/api", true},
		{"/api/v2", "/api/v2", true},
		{" /api ", "/api", true},
		{"api", "api", false},
		{"/bad`inject", "/bad`inject", false},
		{"/bad space", "/bad space", false},
	}
	for _, tt := range tests {
		got, ok := parseProximoPath(map[string]string{proximoPathLabel: tt.raw})
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseProximoPath(%q) = (%q,%v), want (%q,%v)", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

// TestClassifyInvalidPath: a proximo.path without a leading slash makes the
// container not-routed and surfaces the bad value for the watcher to log (1.1).
func TestClassifyInvalidPath(t *testing.T) {
	c := makeSummary(map[string]string{proximoHostsLabel: "app.test", proximoPathLabel: "api"})
	rc, ok, info := classify(context.Background(), failInspect(t), c, "test")
	if ok {
		t.Fatal("invalid path should not route")
	}
	if info.invalidPath != "api" {
		t.Errorf("invalidPath = %q, want %q", info.invalidPath, "api")
	}
	if info.portFailed {
		t.Error("invalid path should short-circuit before port resolution")
	}
	if !slices.Equal(rc.hosts, []string{"app.test"}) {
		t.Errorf("rc should keep its hosts, got %v", rc.hosts)
	}
}

// TestClassifyPathThreaded: a valid proximo.path and proximo.path.strip flow
// into the route model alongside the resolved port (1.2).
func TestClassifyPathThreaded(t *testing.T) {
	c := makeSummary(map[string]string{
		proximoHostsLabel:     "app.test",
		proximoPortLabel:      "8080",
		proximoPathLabel:      "/api",
		proximoPathStripLabel: "true",
	})
	rc, ok, _ := classify(context.Background(), failInspect(t), c, "test")
	if !ok || rc.path != "/api" || !rc.strip || rc.port != 8080 {
		t.Fatalf("classify path = (path=%q, strip=%v, port=%d, ok=%v), want (/api,true,8080,true)",
			rc.path, rc.strip, rc.port, ok)
	}
}

// TestRenderRouterPath: a path prefix produces a Host&&PathPrefix rule with a
// prefix-length priority; multiple hosts are parenthesized before the && (2.1, 2.2).
func TestRenderRouterPath(t *testing.T) {
	one := routedContainer{name: "api", safe: "api", hosts: []string{"app.test"}, port: 80, proximo: true, path: "/api"}
	out := string(renderRouter(one))
	for _, w := range []string{
		"rule: \"Host(`app.test`) && PathPrefix(`/api`)\"",
		"priority: 4\n",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("single-host path router missing %q\n---\n%s", w, out)
		}
	}

	multi := routedContainer{name: "api", safe: "api", hosts: []string{"app.test", "api.test"}, port: 80, proximo: true, path: "/api"}
	out = string(renderRouter(multi))
	if !strings.Contains(out, "rule: \"(Host(`app.test`) || Host(`api.test`)) && PathPrefix(`/api`)\"") {
		t.Errorf("multi-host path rule should parenthesize the host alternation\n---\n%s", out)
	}

	// No path → no PathPrefix and no priority line (bare host keeps the default).
	bare := string(renderRouter(routedContainer{name: "app", safe: "app", hosts: []string{"app.test"}, port: 80, proximo: true}))
	for _, unwanted := range []string{"PathPrefix", "priority:"} {
		if strings.Contains(bare, unwanted) {
			t.Errorf("bare host should not emit %q\n---\n%s", unwanted, bare)
		}
	}
}

// TestRenderRouterStrip: proximo.path.strip emits a StripPrefix middleware
// referenced by the websecure router; without it no strip middleware appears (2.3).
func TestRenderRouterStrip(t *testing.T) {
	strip := routedContainer{name: "api", safe: "api", hosts: []string{"app.test"}, port: 80, proximo: true, path: "/api", strip: true}
	out := string(renderRouter(strip))
	for _, w := range []string{
		"- proximo-api-strip\n",
		"proximo-api-strip:",
		"stripPrefix:",
		"prefixes:",
		"- \"/api\"\n",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("strip router missing %q\n---\n%s", w, out)
		}
	}

	noStrip := string(renderRouter(routedContainer{name: "api", safe: "api", hosts: []string{"app.test"}, port: 80, proximo: true, path: "/api"}))
	for _, unwanted := range []string{"stripPrefix", "-strip"} {
		if strings.Contains(noStrip, unwanted) {
			t.Errorf("path without strip should not emit %q\n---\n%s", unwanted, noStrip)
		}
	}
}

// TestResolveRouteCollisions: two containers on the same host with different
// prefixes are both kept; the same (host, prefix) pair is a collision the
// lexicographically-first name wins; native routes are untouched (2.4, 3.2).
func TestResolveRouteCollisions(t *testing.T) {
	// Different prefixes on one host: both served (the intended split).
	front := routedContainer{name: "front", hosts: []string{"app.test"}, proximo: true}
	api := routedContainer{name: "api", hosts: []string{"app.test"}, proximo: true, path: "/api"}
	res := resolveRoutes([]routedContainer{front, api})
	kept, collisions := res.kept, res.collisions
	if len(kept) != 2 || len(collisions) != 0 {
		t.Fatalf("distinct prefixes: kept=%d collisions=%d, want 2 and 0", len(kept), len(collisions))
	}

	// Same (host, prefix): the lexicographically-first name (api) wins, zzz loses.
	dup := routedContainer{name: "zzz", hosts: []string{"app.test"}, proximo: true, path: "/api"}
	res = resolveRoutes([]routedContainer{dup, api})
	kept, collisions = res.kept, res.collisions
	if len(kept) != 1 || kept[0].name != "api" {
		t.Fatalf("collision winner = %v, want [api]", names(kept))
	}
	if len(collisions) != 1 || collisions[0].name != "zzz" || collisions[0].host != "app.test" || collisions[0].path != "/api" {
		t.Fatalf("collision = %+v, want {zzz app.test /api}", collisions)
	}

	// Native routes never lose a host (Traefik's Docker provider owns them).
	nat1 := routedContainer{name: "n1", hosts: []string{"x.test"}}
	nat2 := routedContainer{name: "n2", hosts: []string{"x.test"}}
	res = resolveRoutes([]routedContainer{nat1, nat2})
	kept, collisions = res.kept, res.collisions
	if len(kept) != 2 || len(collisions) != 0 {
		t.Fatalf("native routes: kept=%d collisions=%d, want 2 and 0", len(kept), len(collisions))
	}
}

func names(rcs []routedContainer) []string {
	out := make([]string, len(rcs))
	for i, rc := range rcs {
		out[i] = rc.name
	}
	return out
}

// TestParseTCPPorts: proximo.tcp.port and proximo.tcp.ports merge into one
// deduplicated, order-preserving port list; invalid entries are collected
// separately and never drop the valid ones.
func TestParseTCPPorts(t *testing.T) {
	tests := []struct {
		name        string
		port, ports string
		want        []int
		wantInvalid []string
	}{
		{"absent", "", "", nil, nil},
		{"single", "5432", "", []int{5432}, nil},
		{"list", "", "5432,6379", []int{5432, 6379}, nil},
		{"port and list merge, dedup", "5432", "5432,6379", []int{5432, 6379}, nil},
		{"invalid skipped, valid kept", "notaport", "6379", []int{6379}, []string{"notaport"}},
		{"out of range", "0,70000", "", nil, []string{"0", "70000"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ports, invalid := parseTCPPorts(map[string]string{proximoTCPPortLabel: tt.port, proximoTCPPortsLabel: tt.ports})
			if !slices.Equal(ports, tt.want) {
				t.Errorf("ports = %v, want %v", ports, tt.want)
			}
			if !slices.Equal(invalid, tt.wantInvalid) {
				t.Errorf("invalid = %v, want %v", invalid, tt.wantInvalid)
			}
		})
	}
}

// TestParseTCPTLSMode: absent/terminate default to terminate; passthrough is
// honored; any other value defaults to terminate and is flagged invalid.
func TestParseTCPTLSMode(t *testing.T) {
	tests := []struct {
		raw         string
		wantMode    string
		wantInvalid string
	}{
		{"", tcpTLSTerminate, ""},
		{"terminate", tcpTLSTerminate, ""},
		{"PASSTHROUGH", tcpTLSPassthrough, ""},
		{" Passthrough ", tcpTLSPassthrough, ""},
		{"garbage", tcpTLSTerminate, "garbage"},
	}
	for _, tt := range tests {
		mode, invalid := parseTCPTLSMode(map[string]string{proximoTCPTLSLabel: tt.raw})
		if mode != tt.wantMode || invalid != tt.wantInvalid {
			t.Errorf("parseTCPTLSMode(%q) = (%q,%q), want (%q,%q)", tt.raw, mode, invalid, tt.wantMode, tt.wantInvalid)
		}
	}
}

// TestClassifyTCP: a proximo container declaring a TCP port is TCP-routed by SNI
// and needs no HTTP backend-port resolution (failInspect must not be called);
// the TLS mode is carried through and multiple ports are collected.
func TestClassifyTCP(t *testing.T) {
	c := makeSummary(map[string]string{proximoHostsLabel: "db.test", proximoTCPPortLabel: "5432"})
	rc, ok, _ := classify(context.Background(), failInspect(t), c, "test")
	if !ok || !rc.isTCP() || !slices.Equal(rc.tcpPorts, []int{5432}) || rc.tcpTLS != tcpTLSTerminate {
		t.Fatalf("classify TCP = (ok=%v, tcpPorts=%v, tls=%q), want (true,[5432],terminate)", ok, rc.tcpPorts, rc.tcpTLS)
	}
	if rc.port != 0 {
		t.Errorf("TCP-routed container should not resolve an HTTP port, got %d", rc.port)
	}

	pass := makeSummary(map[string]string{proximoHostsLabel: "mqtt.test", proximoTCPPortsLabel: "8883,1883", proximoTCPTLSLabel: "passthrough"})
	rc, ok, _ = classify(context.Background(), failInspect(t), pass, "test")
	if !ok || !slices.Equal(rc.tcpPorts, []int{8883, 1883}) || rc.tcpTLS != tcpTLSPassthrough {
		t.Fatalf("classify TCP passthrough = (ok=%v, tcpPorts=%v, tls=%q), want (true,[8883 1883],passthrough)", ok, rc.tcpPorts, rc.tcpTLS)
	}
}

// TestClassifyTCPInvalidPortSkipped: an invalid TCP port is reported for warning;
// when it is the only TCP label the container falls back to HTTP classification.
func TestClassifyTCPInvalidPortSkipped(t *testing.T) {
	c := makeSummary(map[string]string{proximoHostsLabel: "app.test", proximoTCPPortLabel: "notaport", proximoPortLabel: "8080"})
	rc, ok, info := classify(context.Background(), failInspect(t), c, "test")
	if !ok || rc.isTCP() || rc.port != 8080 {
		t.Fatalf("invalid-only TCP should fall back to HTTP = (ok=%v, isTCP=%v, port=%d), want (true,false,8080)", ok, rc.isTCP(), rc.port)
	}
	if !slices.Equal(info.invalidTCPPorts, []string{"notaport"}) {
		t.Errorf("invalidTCPPorts = %v, want [notaport]", info.invalidTCPPorts)
	}
}

// TestResolveRouteCollisionsReplicas: containers with identical bare-host routing
// but different backends are merged into one round-robin route (HTTP and TCP);
// the lexicographically-first name represents the group and carries every backend
// as a server. Divergent config on the same host is not merged — it collides.
func TestResolveRouteCollisionsReplicas(t *testing.T) {
	// Three identical HTTP backends on app.test:8080 -> one balanced route with a
	// server per backend and a reported merge per absorbed replica.
	web1 := routedContainer{name: "web-1", safe: "web-1", hosts: []string{"app.test"}, port: 8080, proximo: true}
	web2 := routedContainer{name: "web-2", safe: "web-2", hosts: []string{"app.test"}, port: 8080, proximo: true}
	web3 := routedContainer{name: "web-3", safe: "web-3", hosts: []string{"app.test"}, port: 8080, proximo: true}
	res := resolveRoutes([]routedContainer{web3, web1, web2})
	kept, merges, collisions := res.kept, res.merges, res.collisions
	if len(kept) != 1 || len(collisions) != 0 || len(merges) != 2 {
		t.Fatalf("HTTP replicas: kept=%d collisions=%d merges=%d, want 1, 0, 2", len(kept), len(collisions), len(merges))
	}
	if kept[0].name != "web-1" || !slices.Equal(kept[0].backends(), []string{"web-1", "web-2", "web-3"}) {
		t.Fatalf("HTTP replicas: rep=%q backends=%v, want web-1 [web-1 web-2 web-3]", kept[0].name, kept[0].backends())
	}
	if merges[0].rep != "web-1" || merges[0].host != "app.test" {
		t.Fatalf("merge event = %+v, want rep web-1 on app.test", merges[0])
	}

	// Two identical TCP backends on db.test:5432 -> one balanced TCP route.
	db1 := routedContainer{name: "db-1", safe: "db-1", hosts: []string{"db.test"}, proximo: true, tcpPorts: []int{5432}, tcpTLS: tcpTLSTerminate}
	db2 := routedContainer{name: "db-2", safe: "db-2", hosts: []string{"db.test"}, proximo: true, tcpPorts: []int{5432}, tcpTLS: tcpTLSTerminate}
	res = resolveRoutes([]routedContainer{db2, db1})
	kept, collisions = res.kept, res.collisions
	if len(kept) != 1 || len(collisions) != 0 || !slices.Equal(kept[0].backends(), []string{"db-1", "db-2"}) {
		t.Fatalf("TCP replicas: kept=%d collisions=%d backends=%v, want 1, 0, [db-1 db-2]", len(kept), len(collisions), kept[0].backends())
	}

	// Same host, different port: not replicas -> collision, not a merge.
	a := routedContainer{name: "a", safe: "a", hosts: []string{"app.test"}, port: 8080, proximo: true}
	b := routedContainer{name: "b", safe: "b", hosts: []string{"app.test"}, port: 9090, proximo: true}
	res = resolveRoutes([]routedContainer{a, b})
	kept, merges, collisions = res.kept, res.merges, res.collisions
	if len(kept) != 1 || kept[0].name != "a" || len(collisions) != 1 || collisions[0].name != "b" || len(merges) != 0 {
		t.Fatalf("divergent port: kept=%v collisions=%v merges=%d, want [a], one {b}, 0", names(kept), collisions, len(merges))
	}

	// Same host + port but divergent redirect -> not replicas, collision (any router
	// difference beyond the backend blocks merging, protecting the replicaKey invariant).
	r1 := routedContainer{name: "r1", safe: "r1", hosts: []string{"app.test"}, port: 8080, proximo: true}
	r2 := routedContainer{name: "r2", safe: "r2", hosts: []string{"app.test"}, port: 8080, proximo: true, redirect: true}
	res = resolveRoutes([]routedContainer{r1, r2})
	kept, merges, collisions = res.kept, res.merges, res.collisions
	if len(kept) != 1 || len(merges) != 0 || len(collisions) != 1 {
		t.Fatalf("divergent redirect: kept=%d merges=%d collisions=%d, want 1, 0, 1", len(kept), len(merges), len(collisions))
	}

	// Same host + TCP port but divergent TLS mode -> not replicas, collision.
	t1 := routedContainer{name: "t1", safe: "t1", hosts: []string{"db.test"}, proximo: true, tcpPorts: []int{5432}, tcpTLS: tcpTLSTerminate}
	t2 := routedContainer{name: "t2", safe: "t2", hosts: []string{"db.test"}, proximo: true, tcpPorts: []int{5432}, tcpTLS: tcpTLSPassthrough}
	res = resolveRoutes([]routedContainer{t1, t2})
	kept, merges, collisions = res.kept, res.merges, res.collisions
	if len(kept) != 1 || len(merges) != 0 || len(collisions) != 1 {
		t.Fatalf("divergent tcpTLS: kept=%d merges=%d collisions=%d, want 1, 0, 1", len(kept), len(merges), len(collisions))
	}

	// An HTTP route and a TCP route on distinct hosts coexist — both served.
	httpC := routedContainer{name: "web", safe: "web", hosts: []string{"web.test"}, port: 80, proximo: true}
	tcpC := routedContainer{name: "cache", safe: "cache", hosts: []string{"cache.test"}, proximo: true, tcpPorts: []int{6379}, tcpTLS: tcpTLSTerminate}
	res = resolveRoutes([]routedContainer{httpC, tcpC})
	kept, collisions = res.kept, res.collisions
	if len(kept) != 2 || len(collisions) != 0 {
		t.Fatalf("HTTP+TCP coexistence: kept=%d collisions=%d, want 2 and 0", len(kept), len(collisions))
	}

	// Host order must not defeat replica detection: same hosts, different order, merge.
	o1 := routedContainer{name: "o1", safe: "o1", hosts: []string{"a.test", "b.test"}, port: 80, proximo: true}
	o2 := routedContainer{name: "o2", safe: "o2", hosts: []string{"b.test", "a.test"}, port: 80, proximo: true}
	res = resolveRoutes([]routedContainer{o1, o2})
	kept, merges, collisions = res.kept, res.merges, res.collisions
	if len(kept) != 1 || len(merges) != 1 || len(collisions) != 0 {
		t.Fatalf("host-order replicas: kept=%d merges=%d collisions=%d, want 1, 1, 0", len(kept), len(merges), len(collisions))
	}

	// A lone container is a one-backend route, identical to pre-replica behavior.
	res = resolveRoutes([]routedContainer{web1})
	kept = res.kept
	if len(kept) != 1 || !slices.Equal(kept[0].backends(), []string{"web-1"}) {
		t.Fatalf("single: backends=%v, want [web-1]", kept[0].backends())
	}
}

// TestRenderRouterReplicas: a merged route renders one server per backend for both
// HTTP (url) and TCP (address), while a single backend renders exactly one server.
func TestRenderRouterReplicas(t *testing.T) {
	http := routedContainer{name: "web-1", safe: "web-1", hosts: []string{"app.test"}, port: 8080, proximo: true, servers: []string{"web-1", "web-2"}}
	out := string(renderRouter(http))
	for _, w := range []string{"url: \"http://web-1:8080\"", "url: \"http://web-2:8080\""} {
		if !strings.Contains(out, w) {
			t.Errorf("HTTP replicas render missing %q\n---\n%s", w, out)
		}
	}

	tcp := routedContainer{name: "db-1", safe: "db-1", hosts: []string{"db.test"}, proximo: true, tcpPorts: []int{5432}, tcpTLS: tcpTLSTerminate, servers: []string{"db-1", "db-2"}}
	out = string(renderRouter(tcp))
	for _, w := range []string{"address: \"db-1:5432\"", "address: \"db-2:5432\""} {
		if !strings.Contains(out, w) {
			t.Errorf("TCP replicas render missing %q\n---\n%s", w, out)
		}
	}

	single := string(renderRouter(routedContainer{name: "solo", safe: "solo", hosts: []string{"solo.test"}, port: 80, proximo: true}))
	if strings.Count(single, "- url:") != 1 {
		t.Errorf("single backend should render exactly one server\n---\n%s", single)
	}
}

// TestRenderRouterInspection: a route under Inspection is served through the hop
// instead of the container, and carries the header the hop reads the real
// backend from. Without the label nothing about the route changes.
func TestRenderRouterInspection(t *testing.T) {
	base := routedContainer{name: "web", safe: "web", hosts: []string{"web.test"}, port: 8080, proximo: true}

	off := string(renderRouter(base))
	for _, unwanted := range []string{"inspector", "X-Proximo-Backend", "proximo-web-inspect"} {
		if strings.Contains(off, unwanted) {
			t.Errorf("Inspection is opt-in, but the route mentions %q\n---\n%s", unwanted, off)
		}
	}

	base.inspect = true
	on := string(renderRouter(base))
	wants := []string{
		"- proximo-web-inspect",
		"proximo-web-inspect:",
		"customRequestHeaders:",
		"X-Proximo-Backend: \"http://web:8080\"",
		"url: \"http://inspector:9000\"",
	}
	for _, w := range wants {
		if !strings.Contains(on, w) {
			t.Errorf("inspected route missing %q\n---\n%s", w, on)
		}
	}
	if strings.Contains(on, "url: \"http://web:8080\"") {
		t.Errorf("Traefik must reach the hop, not the container directly\n---\n%s", on)
	}
}

// TestRenderRouterInspectionWithStrip: the strip middleware still runs ahead of
// the hop, so the backend keeps seeing the path it would have seen.
func TestRenderRouterInspectionWithStrip(t *testing.T) {
	rc := routedContainer{name: "web", safe: "web", hosts: []string{"web.test"}, port: 8080, proximo: true, path: "/app", strip: true, inspect: true}
	out := string(renderRouter(rc))
	i, j := strings.Index(out, "- proximo-web-strip"), strings.Index(out, "- proximo-web-inspect")
	if i < 0 || j < 0 || i > j {
		t.Errorf("strip must precede the hop in the chain\n---\n%s", out)
	}
}

// TestInspectionRefusedOnReplicaSet: a merged replica set balances across several
// backends and the hop is told exactly one, so Inspection is refused for the
// group rather than silently forwarding every request to one replica.
func TestInspectionRefusedOnReplicaSet(t *testing.T) {
	a := routedContainer{name: "web-1", safe: "web-1", hosts: []string{"web.test"}, port: 8080, proximo: true, inspect: true}
	b := routedContainer{name: "web-2", safe: "web-2", hosts: []string{"web.test"}, port: 8080, proximo: true, inspect: true}

	res := resolveRoutes([]routedContainer{a, b})
	kept, merges, dropped := res.kept, res.merges, res.inspectDropped
	if len(merges) != 1 {
		t.Fatalf("expected the two replicas to merge, got %d merges", len(merges))
	}
	if len(kept) != 1 || kept[0].inspect {
		t.Fatalf("Inspection must be refused for a merged group, got %+v", kept)
	}
	if len(dropped) != 1 || dropped[0] != "web-1" {
		t.Fatalf("the refusal must name the route, got %v", dropped)
	}
	if strings.Contains(string(renderRouter(kept[0])), "inspector") {
		t.Error("a refused route must not be rendered through the hop")
	}

	// A single backend is still inspected.
	res = resolveRoutes([]routedContainer{a})
	kept, dropped = res.kept, res.inspectDropped
	if len(dropped) != 0 || !kept[0].inspect {
		t.Fatalf("a single backend must keep Inspection, got kept=%+v dropped=%v", kept, dropped)
	}
}

// TestClassifyInspectLabel: proximo.inspect is opt-in and HTTP-only.
func TestClassifyInspectLabel(t *testing.T) {
	rc, ok, _ := classify(context.Background(), failInspect(t), makeSummary(map[string]string{
		proximoHostsLabel: "web.test",
		proximoPortLabel:  "8080",
	}), "test")
	if !ok || rc.inspect {
		t.Fatalf("Inspection must be off without the label, got ok=%v inspect=%v", ok, rc.inspect)
	}

	rc, ok, _ = classify(context.Background(), failInspect(t), makeSummary(map[string]string{
		proximoHostsLabel:   "web.test",
		proximoPortLabel:    "8080",
		proximoInspectLabel: "true",
	}), "test")
	if !ok || !rc.inspect {
		t.Fatalf("proximo.inspect=true must opt in, got ok=%v inspect=%v", ok, rc.inspect)
	}

	// A TCP route has no HTTP response body to inject into.
	_, ok, info := classify(context.Background(), failInspect(t), makeSummary(map[string]string{
		proximoHostsLabel:   "db.test",
		proximoTCPPortLabel: "5432",
		proximoInspectLabel: "true",
	}), "test")
	if !ok || !slices.Contains(info.tcpIgnoredHTTP, proximoInspectLabel) {
		t.Fatalf("proximo.inspect on a TCP route must be flagged, got %v", info.tcpIgnoredHTTP)
	}
}

// TestInspectRefusalReachesStatus: a refusal that lives only in the watcher's
// log is, from `proximo status`, indistinguishable from Inspection not working.
// Both refusals must therefore reach the route listing.
func TestInspectRefusalReachesStatus(t *testing.T) {
	_, _, info := classify(context.Background(), failInspect(t), makeSummary(map[string]string{
		proximoHostsLabel:   "db.test",
		proximoTCPPortLabel: "5432",
		proximoInspectLabel: "true",
	}), "test")
	if !slices.Contains(info.tcpIgnoredHTTP, proximoInspectLabel) {
		t.Fatalf("classify must flag the TCP refusal for status to surface: %v", info.tcpIgnoredHTTP)
	}

	a := routedContainer{name: "web-1", safe: "web-1", hosts: []string{"web.test"}, port: 8080, proximo: true, inspect: true}
	b := routedContainer{name: "web-2", safe: "web-2", hosts: []string{"web.test"}, port: 8080, proximo: true, inspect: true}
	if dropped := resolveRoutes([]routedContainer{a, b}).inspectDropped; len(dropped) != 1 {
		t.Fatalf("the replica refusal must be reported for status too: %v", dropped)
	}
}

// TestClassifyTCPIgnoresHTTPLabels: a TCP route cannot apply HTTP-layer labels
// (middlewares, proximo.path), so classify flags them for a warning and drops the
// middleware set rather than leaving a user to believe auth guards a TCP service.
func TestClassifyTCPIgnoresHTTPLabels(t *testing.T) {
	c := makeSummary(map[string]string{
		proximoHostsLabel:   "db.test",
		proximoTCPPortLabel: "5432",
		proximoAuthLabel:    "alice:s3cret",
		proximoPathLabel:    "/db",
	})
	rc, ok, info := classify(context.Background(), failInspect(t), c, "test")
	if !ok || !rc.isTCP() {
		t.Fatalf("expected a routed TCP container, got ok=%v isTCP=%v", ok, rc.isTCP())
	}
	if !rc.mw.empty() {
		t.Errorf("middlewares must be dropped on a TCP route, got %+v", rc.mw)
	}
	if len(info.tcpIgnoredHTTP) != 2 {
		t.Errorf("tcpIgnoredHTTP = %v, want two entries (middlewares + proximo.path)", info.tcpIgnoredHTTP)
	}
}

// TestRenderTCPRouter: TCP-routed containers emit a tcp: section with a HostSNI
// rule and an address backend, one router/service per port; the default is TLS
// termination (tls: {}), passthrough opts into tls.passthrough. No HTTP url and
// never a HostSNI(`*`) catch-all.
func TestRenderTCPRouter(t *testing.T) {
	rc := routedContainer{name: "db", safe: "db", hosts: []string{"db.test", "pg.test"}, proximo: true, tcpPorts: []int{5432}, tcpTLS: tcpTLSTerminate}
	out := string(renderRouter(rc))
	wants := []string{
		"tcp:\n",
		"proximo-tcp-db-5432:",
		"rule: \"HostSNI(`db.test`) || HostSNI(`pg.test`)\"",
		"address: \"db:5432\"",
		"tls: {}",
		"- websecure",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("renderTCPRouter missing %q\n---\n%s", w, out)
		}
	}
	for _, unwanted := range []string{"http:", "url:", "HostSNI(`*`)"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("renderTCPRouter should not emit %q\n---\n%s", unwanted, out)
		}
	}

	pass := routedContainer{name: "mq", safe: "mq", hosts: []string{"mqtt.test"}, proximo: true, tcpPorts: []int{8883, 1883}, tcpTLS: tcpTLSPassthrough}
	out = string(renderRouter(pass))
	for _, w := range []string{"proximo-tcp-mq-8883:", "proximo-tcp-mq-1883:", "passthrough: true", "address: \"mq:1883\""} {
		if !strings.Contains(out, w) {
			t.Errorf("renderTCPRouter passthrough missing %q\n---\n%s", w, out)
		}
	}
}

// --- Qualified hosts (ADR 0003) ---

// TestNamespaceOf: the Namespace is the Compose project name sanitized to a DNS
// label; a container outside a Compose project has none.
func TestNamespaceOf(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"shop", "shop"},
		{"My_Shop", "my-shop"},
		{"  shop  ", "shop"},
		{"", ""},
		{"-shop", ""},
		{"shop.two", ""},
		{stackProject, ""}, // the stack's own project is not a Namespace
	}
	for _, tt := range tests {
		if got := namespaceOf(map[string]string{composeProjectLabel: tt.project}); got != tt.want {
			t.Errorf("namespaceOf(%q) = %q, want %q", tt.project, got, tt.want)
		}
	}
	if got := namespaceOf(map[string]string{}); got != "" {
		t.Errorf("no project label = %q, want empty", got)
	}
}

// TestQualifiedHost: the Namespace is inserted before the TLD, and nothing is
// generated when there is nothing to qualify.
func TestQualifiedHost(t *testing.T) {
	tests := []struct{ host, ns, want string }{
		{"api.test", "shop", "api.shop.test"},
		{"a.b.test", "shop", "a.b.shop.test"},
		{"api.test", "", ""},                    // no Namespace, no qualified host
		{"api.example.com", "shop", ""},         // outside the TLD: it would never resolve
		{"api.shop.test", "shop", ""},           // already carries the Namespace
		{"shop.test", "shop", "shop.shop.test"}, // named after the project: still qualified
		{"test", "shop", ""},                    // the bare TLD
	}
	for _, tt := range tests {
		if got := qualifiedHost(tt.host, tt.ns, "test"); got != tt.want {
			t.Errorf("qualifiedHost(%q, %q) = %q, want %q", tt.host, tt.ns, got, tt.want)
		}
	}
}

// TestClassifyQualifiesHosts: a proximo route in a Compose project answers on
// its bare host and on the qualified host derived from it; a native traefik.*
// route is left alone (Traefik's Docker provider owns its rule).
func TestClassifyQualifiesHosts(t *testing.T) {
	c := makeSummary(map[string]string{
		proximoHostsLabel:   "api.test,api.example.com",
		proximoPortLabel:    "80",
		composeProjectLabel: "shop",
		composeServiceLabel: "api",
	})
	rc, ok, _ := classify(context.Background(), failInspect(t), c, "test")
	if !ok {
		t.Fatal("container should be routed")
	}
	if !slices.Contains(rc.hosts, "api.shop.test") {
		t.Errorf("hosts = %v, want the qualified host api.shop.test", rc.hosts)
	}
	if slices.Contains(rc.hosts, "api.example.com.shop.test") {
		t.Errorf("a host outside the TLD must not be qualified: %v", rc.hosts)
	}
	if !rc.generated("api.shop.test") || rc.generated("api.test") {
		t.Errorf("generated() must tell the derived host from the declared one: %v", rc.hosts)
	}
	if got := rc.bareHosts(); !slices.Equal(got, []string{"api.test", "api.example.com"}) {
		t.Errorf("bareHosts() = %v, want the declared hosts only", got)
	}

	nat := makeSummary(map[string]string{
		enableLabel:                   "true",
		"traefik.http.routers.n.rule": "Host(`n.test`)",
		composeProjectLabel:           "shop",
	})
	rc, ok, _ = classify(context.Background(), failInspect(t), nat, "test")
	if !ok || !slices.Equal(rc.hosts, []string{"n.test"}) {
		t.Errorf("native route hosts = %v, want [n.test] unqualified", rc.hosts)
	}
}

// TestResolveRouteCollisionsPerHost: a Collision costs a bare host, not a
// service — the loser keeps every other host it declared, and is reported.
func TestResolveRouteCollisionsPerHost(t *testing.T) {
	a := routedContainer{name: "aaa", hosts: []string{"app.test"}, proximo: true}
	b := routedContainer{name: "bbb", hosts: []string{"app.test", "other.test"}, proximo: true}
	res := resolveRoutes([]routedContainer{a, b})
	if len(res.kept) != 2 {
		t.Fatalf("kept = %v, want both containers", names(res.kept))
	}
	loser := res.kept[1]
	if loser.name != "bbb" || !slices.Equal(loser.hosts, []string{"other.test"}) {
		t.Fatalf("loser = %s %v, want bbb keeping [other.test]", loser.name, loser.hosts)
	}
	if len(res.collisions) != 1 || res.collisions[0].host != "app.test" || res.collisions[0].note == "" {
		t.Fatalf("collisions = %+v, want one reported note for app.test", res.collisions)
	}
}

// TestResolveRouteCollisionsYieldsGeneratedHost: where a qualified host proximo
// generated meets a host a developer declared, proximo withdraws its own —
// regardless of name order.
func TestResolveRouteCollisionsYieldsGeneratedHost(t *testing.T) {
	gen := routedContainer{
		name: "aaa", hosts: []string{"api.test", "api.shop.test"}, proximo: true,
		ns: "shop", qual: map[string]string{"api.test": "api.shop.test"},
	}
	declared := routedContainer{name: "zzz", hosts: []string{"api.shop.test"}, proximo: true}
	res := resolveRoutes([]routedContainer{gen, declared})
	if len(res.kept) != 2 {
		t.Fatalf("kept = %v, want both", names(res.kept))
	}
	if !slices.Equal(res.kept[0].hosts, []string{"api.test"}) {
		t.Errorf("generated host not withdrawn: %v", res.kept[0].hosts)
	}
	if !slices.Equal(res.kept[1].hosts, []string{"api.shop.test"}) {
		t.Errorf("declared host must win: %v", res.kept[1].hosts)
	}
	if len(res.collisions) != 1 || res.collisions[0].name != "aaa" {
		t.Fatalf("collisions = %+v, want the withdrawal reported against aaa", res.collisions)
	}
}

// TestResolveRouteCollisionsYieldsToNative: a host already claimed by a native
// traefik.* rule is left to it — proximo withdraws its router and reports it,
// instead of leaving two routers standing on one host.
func TestResolveRouteCollisionsYieldsToNative(t *testing.T) {
	nat := routedContainer{name: "zzz", hosts: []string{"x.test"}, natives: []string{"x.test"}}
	prox := routedContainer{name: "aaa", hosts: []string{"x.test", "y.test"}, proximo: true}
	res := resolveRoutes([]routedContainer{nat, prox})
	if len(res.kept) != 2 {
		t.Fatalf("kept = %v, want both", names(res.kept))
	}
	for _, rc := range res.kept {
		if rc.proximo && !slices.Equal(rc.hosts, []string{"y.test"}) {
			t.Errorf("proximo route kept %v, want [y.test] after withdrawing x.test", rc.hosts)
		}
	}
	if len(res.collisions) != 1 || res.collisions[0].host != "x.test" {
		t.Fatalf("collisions = %+v, want x.test reported", res.collisions)
	}

	// One container carrying both schemes is the same duplicate-router case, and
	// the one warnDuplicateHosts used to catch: proximo withdraws its own router
	// and says so, instead of standing a second one beside Traefik's.
	both := routedContainer{name: "aaa", hosts: []string{"x.test"}, natives: []string{"x.test"}, proximo: true}
	res = resolveRoutes([]routedContainer{both})
	if len(res.kept) != 0 {
		t.Errorf("kept = %v, want the proximo router withdrawn", names(res.kept))
	}
	if len(res.collisions) != 1 || !strings.Contains(res.collisions[0].note, "this container") {
		t.Fatalf("collisions = %+v, want the same-container withdrawal reported", res.collisions)
	}
}

// TestResolveRouteCollisionsInsideOneProject: two containers of one project
// claiming one host share the qualified host too, so the loser is left with
// nothing — the note must say that instead of pointing at a host it also lost.
func TestResolveRouteCollisionsInsideOneProject(t *testing.T) {
	mk := func(name string, port int) routedContainer {
		return routedContainer{
			name: name, hosts: []string{"api.test", "api.shop.test"}, port: port, proximo: true,
			ns: "shop", qual: map[string]string{"api.test": "api.shop.test"},
		}
	}
	res := resolveRoutes([]routedContainer{mk("aaa", 80), mk("zzz", 81)})
	if len(res.kept) != 1 || res.kept[0].name != "aaa" {
		t.Fatalf("kept = %v, want [aaa]", names(res.kept))
	}
	if len(res.collisions) != 2 {
		t.Fatalf("collisions = %+v, want both hosts reported against zzz", res.collisions)
	}
	for _, c := range res.collisions {
		if c.name != "zzz" {
			t.Errorf("collision reported against %q, want zzz", c.name)
		}
		if strings.Contains(c.note, "answers at") || strings.Contains(c.note, "keeps its other hosts") {
			t.Errorf("note promises a host zzz also lost: %q", c.note)
		}
	}
	if !strings.Contains(res.collisions[0].note, proximoHostsLabel) {
		t.Errorf("note should name the remedy: %q", res.collisions[0].note)
	}
}

// TestReplicaKeySeparatesProjects: replicas live inside one Project; two
// containers of different Projects are never merged into one balanced route.
func TestReplicaKeySeparatesProjects(t *testing.T) {
	a := routedContainer{name: "a", hosts: []string{"api.example.com"}, port: 80, proximo: true, ns: "shop"}
	b := routedContainer{name: "b", hosts: []string{"api.example.com"}, port: 80, proximo: true, ns: "warehouse"}
	if replicaKey(a) == replicaKey(b) {
		t.Fatal("containers of different projects must not merge as replicas")
	}
	b.ns = "shop"
	if replicaKey(a) != replicaKey(b) {
		t.Fatal("containers of one project with identical routing must merge as replicas")
	}
}

// TestAssignSafeNamesFromNamespace: the safe name is the Namespace and the
// Compose service, so a cert file traces back to a container by reading it and
// stays stable as replicas come and go.
func TestAssignSafeNamesFromNamespace(t *testing.T) {
	rcs := []routedContainer{
		{name: "shop-api-1", id: "aaaaaaaaaaaa1111", ns: "shop", service: "api"},
		{name: "loose", id: "bbbbbbbbbbbb2222"},
		{name: "traefik", safe: dashboardSafe},
	}
	assignSafeNames(rcs)
	if rcs[0].safe != "shop-api" {
		t.Errorf("safe = %q, want shop-api", rcs[0].safe)
	}
	if rcs[1].safe != "loose" {
		t.Errorf("container outside a project keeps its name: got %q", rcs[1].safe)
	}
	if rcs[2].safe != dashboardSafe {
		t.Errorf("reserved dashboard id must not be reassigned: got %q", rcs[2].safe)
	}
}

// TestReconcileServesQualifiedHost drives a whole reconcile: a container in a
// Compose project gets one router and one certificate covering both its bare and
// its qualified host, under a file named for the Namespace and the service.
func TestReconcileServesQualifiedHost(t *testing.T) {
	f := newFakeDocker()
	f.containers = []container.Summary{
		{ID: "traefikcid", Labels: map[string]string{roleLabel: "traefik"}},
		{
			ID:    "apicid",
			Names: []string{"/shop-api-1"},
			Labels: map[string]string{
				proximoHostsLabel:   "api.test",
				proximoPortLabel:    "8080",
				composeProjectLabel: "shop",
				composeServiceLabel: "api",
			},
		},
	}
	w := testWatcher(t)
	w.cli = f
	if err := w.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	route, err := os.ReadFile(filepath.Join(w.dynamicDir, routerFilePrefix+"shop-api.yml"))
	if err != nil {
		t.Fatalf("router file should be named for the namespace and service: %v", err)
	}
	for _, want := range []string{"Host(`api.test`)", "Host(`api.shop.test`)"} {
		if !strings.Contains(string(route), want) {
			t.Errorf("router rule missing %s:\n%s", want, route)
		}
	}

	crt, err := os.ReadFile(filepath.Join(w.dynamicDir, "certs", "shop-api.crt"))
	if err != nil {
		t.Fatalf("cert should be named for the namespace and service: %v", err)
	}
	block, _ := pem.Decode(crt)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if !slices.Contains(cert.DNSNames, "api.test") || !slices.Contains(cert.DNSNames, "api.shop.test") {
		t.Errorf("cert SANs = %v, want both the bare and the qualified host", cert.DNSNames)
	}
}
