package docker

import (
	"context"
	"errors"
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
	want := append(composeConvergeCmds("ghcr.io/filippolmt/proximo:v0.1.0", false), peerDNSUpCmd)
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

// The watcher starts a created or exited peer DNS service, and nothing else.
func TestWatcherRestartsPeerDNS(t *testing.T) {
	f := &fakeDocker{containers: []container.Summary{
		{ID: "peer", State: container.StateCreated, Labels: map[string]string{RoleLabel: peerDNSRole}},
		{ID: "dns", State: container.StateExited, Labels: map[string]string{RoleLabel: "dns"}},
		{ID: "app", State: container.StateExited, Labels: map[string]string{}},
	}}
	w := plainWatcher(t, f)
	w.restartPeerDNS(context.Background())
	if !slices.Equal(f.started, []string{"peer"}) {
		t.Errorf("started %v, want [peer]", f.started)
	}
	f.started = nil
	f.containers[0].State = container.StateRunning
	w.restartPeerDNS(context.Background())
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
	want := append([][]string{peerDNSRemoveCmd}, composeConvergeCmds(img, false)...)
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
