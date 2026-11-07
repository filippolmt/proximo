package docker

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/moby/moby/api/types/container"
)

// savePeerDNSConfig configures exactly what the peer DNS service needs.
func savePeerDNSConfig(t *testing.T) {
	t.Helper()
	cfg := config.Default()
	cfg.Machine, cfg.PeerSuffix, cfg.Address = "studio-01", "mesh.internal", "100.89.88.2"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func materializedCompose(t *testing.T) string {
	t.Helper()
	dir, err := Materialize("test", "", "ghcr.io/filippolmt/proximo:v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// With no peer value set, the materialised Compose file is identical to
// today's (Acceptance 8): the embedded asset, sentinels substituted.
func TestComposeIsTodaysWithoutPeerValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got := materializedCompose(t)
	raw, err := fs.ReadFile(assets, "assets/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	dataDir, err := config.DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := string(replaceSentinels(raw, "test", config.DNSPort, dataDir)); got != want {
		t.Error("the Compose file changed on a machine with no peer value")
	}
	// machine and suffix alone are not enough: the address is what it answers.
	cfg := config.Default()
	cfg.Machine, cfg.PeerSuffix = "studio-01", "mesh.internal"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(materializedCompose(t), "peer-dns") {
		t.Error("a peer DNS service without an address")
	}
}

func TestComposeGainsThePeerDNSService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	today := materializedCompose(t)
	savePeerDNSConfig(t)
	got := materializedCompose(t)
	// Everything already there is untouched — the loopback dns service
	// included — and the peer DNS service is appended after it.
	if !strings.HasPrefix(got, today) {
		t.Error("the peer DNS service changed the services already in the Compose file")
	}
	for _, want := range []string{
		"\n  peer-dns:\n",
		"profiles: [" + peerProfile + "]",
		`PROXIMO_PEER_SUBTREE: "studio-01.mesh.internal"`,
		`PROXIMO_PEER_ADDRESS: "100.89.88.2"`,
		`"100.89.88.2:5354:5354/udp"`,
		`"100.89.88.2:5354:5354/tcp"`,
		`"proximo.role=` + peerDNSRole + `"`,
		"\n  peer-relay:\n",
		`entrypoint: ["/usr/local/bin/peerrelay"]`,
		`"100.89.88.2:443:443/tcp"`,
		`"proximo.role=` + peerRelayRole + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Compose file lacks %q", want)
		}
	}
}

// A peer-side failure never fails the command that serves .test: the core
// converge runs as today, the peer DNS service starts after it, and its
// failure is a warning.
func TestConvergeStartsPeerDNSApart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	savePeerDNSConfig(t)
	c := &failingComposer{failOn: "peer-dns"}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: "ghcr.io/filippolmt/proximo:v0.1.0"}); err != nil {
		t.Fatalf("a peer DNS failure failed the converge: %v", err)
	}
	want := append(composeConvergeCmds("ghcr.io/filippolmt/proximo:v0.1.0", false), peerUpCmd)
	if !slices.EqualFunc(c.cmds, want, slices.Equal[[]string]) {
		t.Errorf("ran %v, want %v", c.cmds, want)
	}
}

func TestConvergeWithoutPeerDNSIsToday(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := &recordComposer{}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: "ghcr.io/filippolmt/proximo:v0.1.0"}); err != nil {
		t.Fatal(err)
	}
	if want := composeConvergeCmds("ghcr.io/filippolmt/proximo:v0.1.0", false); !slices.EqualFunc(c.cmds, want, slices.Equal[[]string]) {
		t.Errorf("ran %v, want %v", c.cmds, want)
	}
}

type failingComposer struct {
	recordComposer
	failOn string
}

func (f *failingComposer) Compose(dir string, args ...string) error {
	_ = f.recordComposer.Compose(dir, args...)
	if slices.Contains(args, f.failOn) {
		return errors.New("bind: cannot assign requested address")
	}
	return nil
}

// The watcher starts a created or exited peer service, and nothing else.
func TestWatcherRestartsPeerDNS(t *testing.T) {
	f := &fakeDocker{containers: []container.Summary{
		{ID: "peer", State: container.StateCreated, Labels: map[string]string{RoleLabel: peerDNSRole}},
		{ID: "relay", State: container.StateExited, Labels: map[string]string{RoleLabel: peerRelayRole}},
		{ID: "dns", State: container.StateExited, Labels: map[string]string{RoleLabel: "dns"}},
		{ID: "app", State: container.StateExited, Labels: map[string]string{}},
	}}
	w := plainWatcher(t, f)
	w.restartPeerServices(context.Background())
	if !slices.Equal(f.started, []string{"peer", "relay"}) {
		t.Errorf("started %v, want [peer relay]", f.started)
	}
	f.started = nil
	f.containers[0].State = container.StateRunning
	f.containers[1].State = container.StateRunning
	w.restartPeerServices(context.Background())
	if len(f.started) != 0 {
		t.Errorf("started a running peer DNS service: %v", f.started)
	}
}

// An address the peer DNS service could not answer — an IPv6 value saved
// before config address refused one, or a hand-edited file — produces no
// service rather than one that crash-loops.
func TestComposeSkipsAnUnservableAddress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Machine, cfg.PeerSuffix, cfg.Address = "studio-01", "mesh.internal", "fd7a:115c:a1e0::1"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(materializedCompose(t), "peer-dns") {
		t.Error("a peer DNS service for an IPv6 address")
	}
}

// Unsetting a value removes the peer DNS service the previous Compose file
// still describes, before that file is rewritten without it.
func TestConvergeRemovesAStalePeerDNS(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	savePeerDNSConfig(t)
	img := "ghcr.io/filippolmt/proximo:v0.1.0"
	if err := convergeWith(&recordComposer{}, "test", "", ConvergeOpts{Image: img}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	cfg.Address = ""
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	c := &recordComposer{}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: img}); err != nil {
		t.Fatal(err)
	}
	want := append([][]string{peerRemoveCmd(peerRoles)}, composeConvergeCmds(img, false)...)
	if !slices.EqualFunc(c.cmds, want, slices.Equal[[]string]) {
		t.Errorf("ran %v, want %v", c.cmds, want)
	}
	// Once gone, nothing more runs.
	c = &recordComposer{}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: img}); err != nil {
		t.Fatal(err)
	}
	if want := composeConvergeCmds(img, false); !slices.EqualFunc(c.cmds, want, slices.Equal[[]string]) {
		t.Errorf("ran %v, want %v", c.cmds, want)
	}
}

// The peer DNS service is not a core service: a stack without it is whole, so
// stopping it leaves `stack` passing (Acceptance 13).
func TestPeerDNSIsNotACoreRole(t *testing.T) {
	if missing := (StackInfo{Running: true, Roles: []string{"traefik", "dns", "watcher"}}).MissingRoles(); len(missing) != 0 {
		t.Errorf("a stack without %s is missing %v", peerDNSRole, missing)
	}
}

// Traefik is published on the loopback only, like every other stack service:
// every interface would put each routed container on the LAN (#122).
func TestTraefikListensOnTheLoopbackOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got := materializedCompose(t)
	for _, want := range []string{`"127.0.0.1:80:80"`, `"127.0.0.1:443:443"`} {
		if !strings.Contains(got, want) {
			t.Errorf("Compose file lacks %s", want)
		}
	}
	for _, bad := range []string{`- "80:80"`, `- "443:443"`} {
		if strings.Contains(got, bad) {
			t.Errorf("Compose file publishes %s on every interface", bad)
		}
	}
}

// The converge that moves a stack off the wildcard says so once; one already
// on the loopback says nothing.
func TestConvergeSaysWhenItLeavesTheWildcard(t *testing.T) {
	for _, was := range []bool{true, false} {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		orig := onEveryInterface
		onEveryInterface = func() bool { return was }
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stderr := os.Stderr
		os.Stderr = w
		err = convergeWith(&recordComposer{}, "test", "", ConvergeOpts{Image: "ghcr.io/filippolmt/proximo:v0.1.0"})
		os.Stderr = stderr
		onEveryInterface = orig
		w.Close()
		out, _ := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(out), "now listening on 127.0.0.1 only"); got != was {
			t.Errorf("stack on every interface = %v: notice printed = %v (%q)", was, got, out)
		}
	}
}

// A Compose file written before the peer relay describes peer-dns alone, and
// Compose refuses an rm naming a service the file lacks — which would leave
// peer-dns running. Only the roles the file defines are removed.
func TestConvergeRemovesOnlyThePeerServicesTheFileDefines(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	savePeerDNSConfig(t)
	img := "ghcr.io/filippolmt/proximo:v0.1.0"
	if err := convergeWith(&recordComposer{}, "test", "", ConvergeOpts{Image: img}); err != nil {
		t.Fatal(err)
	}
	stack, err := StackDir()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(stack, "docker-compose.yml")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	old := string(data)[:strings.Index(string(data), "\n  "+peerRelayRole+":\n")+1]
	if err := os.WriteFile(file, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load()
	cfg.Address = ""
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	c := &recordComposer{}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: img}); err != nil {
		t.Fatal(err)
	}
	if len(c.cmds) == 0 || !slices.Equal(c.cmds[0], peerRemoveCmd([]string{peerDNSRole})) {
		t.Errorf("first command %v, want %v", c.cmds, peerRemoveCmd([]string{peerDNSRole}))
	}
}
