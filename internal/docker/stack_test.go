package docker

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/config"
)

// TestReplaceSentinels covers the pure substitution lifted out of Materialize:
// __TLD__, __DNSPORT__, and __DATADIR__ are replaced, and content with no
// sentinel passes through unchanged.
func TestReplaceSentinels(t *testing.T) {
	got := string(replaceSentinels([]byte("host: app.__TLD__\nport: __DNSPORT__\ndata: __DATADIR__/traefik\n"), "test", 5354, "/home/u/.proximo/data"))
	want := "host: app.test\nport: 5354\ndata: /home/u/.proximo/data/traefik\n"
	if got != want {
		t.Errorf("replaceSentinels = %q, want %q", got, want)
	}

	// Both sentinels may appear more than once.
	multi := string(replaceSentinels([]byte("__TLD__ __TLD__ __DNSPORT__"), "dev", 99, "/d"))
	if multi != "dev dev 99" {
		t.Errorf("repeated sentinels = %q, want %q", multi, "dev dev 99")
	}

	// No sentinel: passthrough, byte-identical.
	plain := []byte("nothing to replace here")
	if out := replaceSentinels(plain, "test", 5354, "/d"); string(out) != string(plain) {
		t.Errorf("passthrough = %q, want %q", out, plain)
	}
}

// TestMaterializeBindMounts asserts the materialized compose carries the
// absolute host data-dir bind mount (no unreplaced __DATADIR__ sentinel) and no
// longer declares a top-level named volumes block, and that Materialize creates
// the data/traefik bind-mount source.
func TestMaterializeBindMounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	dir, err := Materialize("test", "")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	composeBytes, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	yaml := string(composeBytes)

	dataDir := filepath.Join(home, ".proximo", "data")
	wantMount := filepath.Join(dataDir, "traefik") + ":/etc/traefik/dynamic"
	if !strings.Contains(yaml, wantMount) {
		t.Errorf("compose missing bind mount %q:\n%s", wantMount, yaml)
	}
	if strings.Contains(yaml, dataDirSentinel) {
		t.Errorf("compose still contains unreplaced %s:\n%s", dataDirSentinel, yaml)
	}
	// A top-level (column-0) "volumes:" block must no longer be declared; the
	// service-level volumes: keys are indented and so don't match.
	for line := range strings.SplitSeq(yaml, "\n") {
		if line == "volumes:" {
			t.Errorf("compose still declares a top-level named volumes block:\n%s", yaml)
		}
	}
	if fi, err := os.Stat(filepath.Join(dataDir, "traefik")); err != nil || !fi.IsDir() {
		t.Errorf("Materialize did not create data/traefik: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dataDir, "beszel")); err != nil || !fi.IsDir() {
		t.Errorf("Materialize did not create data/beszel: %v", err)
	}
}

// recordComposer is a fake Composer that records the compose commands it is
// asked to run, so Converge's sequencing is verifiable without Docker.
type recordComposer struct{ cmds [][]string }

func (r *recordComposer) Compose(_ string, args ...string) error {
	r.cmds = append(r.cmds, append([]string(nil), args...))
	return nil
}

// TestConvergeRunsCommandSequence asserts convergeWith issues exactly the
// command sequence composeConvergeCmds produces — for an immutable tag (cache
// reused), a mobile ref (cache busted), and Force on a tag — so execution
// faithfully follows the pure, tested decision.
func TestConvergeRunsCommandSequence(t *testing.T) {
	equalCmds := func(a, b [][]string) bool {
		return slices.EqualFunc(a, b, slices.Equal[[]string])
	}
	cases := []struct {
		name  string
		ref   string
		force bool
	}{
		{"immutable tag reuses cache", "v0.1.0", false},
		{"mobile ref busts cache", "main", false},
		{"force on a tag busts cache", "v0.1.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			c := &recordComposer{}
			if err := convergeWith(c, tc.ref, "test", "", ConvergeOpts{Force: tc.force}); err != nil {
				t.Fatalf("convergeWith: %v", err)
			}
			want := composeConvergeCmds(tc.ref, tc.force)
			if !equalCmds(c.cmds, want) {
				t.Errorf("convergeWith ran %v, want %v", c.cmds, want)
			}
		})
	}
}

func TestModuleRef(t *testing.T) {
	cases := map[string]string{
		// Released binaries carry a bare semver (GoReleaser drops the "v").
		"0.1.0":     "v0.1.0",
		"1.2.3":     "v1.2.3",
		"0.1.0-rc1": "v0.1.0-rc1",
		// Already-canonical refs are left untouched.
		"v0.1.0": "v0.1.0",
		// Local/dev builds fall back to the default branch.
		"dev": "main",
		"":    "main",
	}
	for in, want := range cases {
		if got := moduleRef(in); got != want {
			t.Errorf("moduleRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsMobileRef(t *testing.T) {
	mobile := []string{"", "main", "dev"}
	for _, ref := range mobile {
		if !isMobileRef(ref) {
			t.Errorf("isMobileRef(%q) = false, want true", ref)
		}
	}
	immutable := []string{"v0.1.0", "v1.2.3", "v0.1.0-rc1"}
	for _, ref := range immutable {
		if isMobileRef(ref) {
			t.Errorf("isMobileRef(%q) = true, want false", ref)
		}
	}
}

func TestComposeConvergeCmds(t *testing.T) {
	hasNoCacheBuild := func(cmds [][]string) bool {
		for _, c := range cmds {
			if len(c) > 0 && c[0] == "build" && slices.Contains(c, "--no-cache") {
				return true
			}
		}
		return false
	}
	upPulls := func(cmds [][]string) bool {
		up := cmds[len(cmds)-1]
		return up[0] == "up" && slices.Contains(up, "--build") &&
			slices.Contains(up, "--pull") && slices.Contains(up, "always")
	}

	tests := []struct {
		name        string
		ref         string
		force       bool
		wantNoCache bool
	}{
		{"immutable tag reuses cache", "v0.1.0", false, false},
		{"mobile ref busts cache", "main", false, true},
		{"empty ref busts cache", "", false, true},
		{"force on a tag busts cache", "v0.1.0", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds := composeConvergeCmds(tt.ref, tt.force)
			if got := hasNoCacheBuild(cmds); got != tt.wantNoCache {
				t.Errorf("composeConvergeCmds(%q, %v) no-cache build = %v, want %v",
					tt.ref, tt.force, got, tt.wantNoCache)
			}
			// The bring-up always re-pulls so the pinned Traefik tag is refreshed.
			if !upPulls(cmds) {
				t.Errorf("composeConvergeCmds(%q, %v) up cmd does not --pull always: %v",
					tt.ref, tt.force, cmds)
			}
		})
	}
}

// TestObservabilitySentinelsInCompose substitutes the embedded compose asset for
// a given TLD and asserts the observability services come out with the right
// routing hosts, seeded email/auto-login, and loopback bootstrap port — and that
// no sentinel is left behind.
func TestObservabilitySentinelsInCompose(t *testing.T) {
	raw, err := assets.ReadFile("assets/docker-compose.yml")
	if err != nil {
		t.Fatalf("read embedded compose: %v", err)
	}
	out := string(replaceSentinels(raw, "example", config.DNSPort, "/home/u/.proximo/data"))

	wantContains := []string{
		"proximo.hosts=logs.example",
		"proximo.hosts=metrics.example",
		"proximo.port=8080",
		"proximo.port=8090",
		"USER_EMAIL: proximo@proximo.example",
		"AUTO_LOGIN: proximo@proximo.example",
		fmt.Sprintf("127.0.0.1:%d:8090", config.ObsHubPort),
	}
	for _, want := range wantContains {
		if !strings.Contains(out, want) {
			t.Errorf("substituted compose missing %q", want)
		}
	}
	// Both dashboards opt into the HTTP->HTTPS redirect so http://logs|metrics
	// always lands on the CA-trusted https host.
	if n := strings.Count(out, "proximo.redirect=true"); n != 2 {
		t.Errorf("want proximo.redirect=true on both observability dashboards (x2), got %d", n)
	}
	for _, sentinel := range []string{tldSentinel, dnsPortSentinel, obsEmailSentinel, obsHubPortSentinel} {
		if strings.Contains(out, sentinel) {
			t.Errorf("sentinel %q left unsubstituted", sentinel)
		}
	}
	// The observability services must not carry a proximo.role label, or the
	// watcher would exclude them from routing.
	if strings.Contains(out, "proximo.role=dozzle") || strings.Contains(out, "proximo.role=beszel") {
		t.Error("observability service must not have a proximo.role label")
	}
}

// TestObservabilityConvergeSequence asserts the staged bring-up: the core `up`
// carries no profile flag, the hub and agent bring-ups activate the observability
// profile, and the bootstrap runs between the hub and the agent so the agent
// never starts without its hub credentials.
func TestObservabilityConvergeSequence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c := &recordComposer{}
	bootstrapAt := -1
	bootstrap := func(_ string) error {
		bootstrapAt = len(c.cmds)
		return nil
	}
	if err := convergeObservabilityWith(c, "v0.1.0", "test", "", ConvergeOpts{}, bootstrap); err != nil {
		t.Fatalf("convergeObservabilityWith: %v", err)
	}

	if len(c.cmds) != 3 {
		t.Fatalf("ran %d commands, want 3 (core up, hub up, agent up): %v", len(c.cmds), c.cmds)
	}
	core, hub, agent := c.cmds[0], c.cmds[1], c.cmds[2]

	if slices.Contains(core, "--profile") {
		t.Errorf("core up activated a profile: %v", core)
	}
	if core[0] != "up" {
		t.Errorf("first command is %v, want core up", core)
	}
	for _, want := range []string{"--profile", observabilityProfile, svcDozzle, svcBeszel} {
		if !slices.Contains(hub, want) {
			t.Errorf("hub up missing %q: %v", want, hub)
		}
	}
	if slices.Contains(hub, svcBeszelAgent) {
		t.Errorf("hub up must not start the agent before bootstrap: %v", hub)
	}
	for _, want := range []string{"--profile", observabilityProfile, svcBeszelAgent} {
		if !slices.Contains(agent, want) {
			t.Errorf("agent up missing %q: %v", want, agent)
		}
	}
	if bootstrapAt != 2 {
		t.Errorf("bootstrap ran at index %d, want 2 (after core+hub, before agent)", bootstrapAt)
	}
}

// TestStackLogCaps asserts every stack container gets a small, rotated
// json-file log cap via the shared x-logging anchor, so container stdout/stderr
// the daemon retains cannot grow unbounded on the dev host.
func TestStackLogCaps(t *testing.T) {
	raw, err := assets.ReadFile("assets/docker-compose.yml")
	if err != nil {
		t.Fatalf("read embedded compose: %v", err)
	}
	compose := string(raw)
	for _, want := range []string{
		"x-logging: &proximo-logging",
		"driver: json-file",
		`max-size: "5m"`,
		`max-file: "3"`,
		"logging: *proximo-logging",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose missing log-cap config %q", want)
		}
	}
}

// TestDownTearsDownEverything asserts a plain `down` enables the observability
// profile and removes orphans, so profile-gated dashboards are torn down with
// the core stack (a bare `docker compose down` leaves them running).
func TestDownTearsDownEverything(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Materialize("test", ""); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	c := &recordComposer{}
	if err := downWith(c); err != nil {
		t.Fatalf("downWith: %v", err)
	}
	if len(c.cmds) != 1 {
		t.Fatalf("ran %d commands, want 1: %v", len(c.cmds), c.cmds)
	}
	for _, want := range []string{"--profile", observabilityProfile, "down", "--remove-orphans"} {
		if !slices.Contains(c.cmds[0], want) {
			t.Errorf("down cmd missing %q: %v", want, c.cmds[0])
		}
	}
}

// TestDownObservabilityScoped asserts `down --observability` removes only the
// observability services (stop + force-remove) and never runs a project-wide
// down that would stop the core stack.
func TestDownObservabilityScoped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Materialize("test", ""); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	c := &recordComposer{}
	if err := downObservabilityWith(c); err != nil {
		t.Fatalf("downObservabilityWith: %v", err)
	}
	if len(c.cmds) != 1 {
		t.Fatalf("ran %d commands, want 1: %v", len(c.cmds), c.cmds)
	}
	got := c.cmds[0]
	for _, want := range []string{"--profile", observabilityProfile, "rm", svcDozzle, svcBeszel, svcBeszelAgent} {
		if !slices.Contains(got, want) {
			t.Errorf("down --observability cmd missing %q: %v", want, got)
		}
	}
	if slices.Contains(got, "down") {
		t.Errorf("scoped observability teardown must not run a project-wide down: %v", got)
	}
}

func TestVersionSkew(t *testing.T) {
	if w := VersionSkew("v0.1.0", true, "v0.1.0"); w != "" {
		t.Errorf("aligned versions = %q, want empty", w)
	}
	if w := VersionSkew("", false, "v0.1.0"); w != "" {
		t.Errorf("stack down = %q, want empty", w)
	}
	if w := VersionSkew("v0.1.0", true, "v0.2.0"); w == "" {
		t.Error("mismatch = empty, want a warning")
	}
	// A running stack without a version label predates stamping (pre-0.4.0):
	// it must warn, not be mistaken for a stack that is down.
	if w := VersionSkew("", true, "v0.1.0"); !strings.Contains(w, "pre-0.4.0") {
		t.Errorf("legacy unlabeled stack = %q, want a pre-0.4.0 warning", w)
	}
}
