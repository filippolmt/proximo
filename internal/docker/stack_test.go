package docker

import (
	"errors"
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

	dir, err := Materialize("test", "", "ghcr.io/filippolmt/proximo:v0.1.0")
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
		image string
		force bool
	}{
		{"release tag pulls only when missing", "ghcr.io/filippolmt/proximo:v0.1.0", false},
		{"mobile ref pulls always", "ghcr.io/filippolmt/proximo:main", false},
		{"force on a tag pulls always", "ghcr.io/filippolmt/proximo:v0.1.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			c := &recordComposer{}
			if err := convergeWith(c, "test", "", ConvergeOpts{Force: tc.force, Image: tc.image}); err != nil {
				t.Fatalf("convergeWith: %v", err)
			}
			want := composeConvergeCmds(tc.image, tc.force)
			if !equalCmds(c.cmds, want) {
				t.Errorf("convergeWith ran %v, want %v", c.cmds, want)
			}
		})
	}
}

// TestConvergeWritesImageEnv asserts the ref a converge runs is the one written
// into the materialized .env — the sticky store that survives a boot-time
// container restart — and that an empty ConvergeOpts.Image falls back to the
// version-pinned canonical ref rather than to nothing.
func TestConvergeWritesImageEnv(t *testing.T) {
	for _, tc := range []struct{ name, give, src, want string }{
		{"override is written verbatim", "ghcr.io/other/proximo:v9", "", "ghcr.io/other/proximo:v9"},
		{"no override pins the CLI version", "", "", CanonicalImage()},
		// A PROXIMO_SRC checkout is built, not pulled — and must be declared as
		// what it is, or the stack runs one image and labels another.
		{"PROXIMO_SRC declares the local image", "", "/checkout", devImage},
		// An explicit --image wins over PROXIMO_SRC.
		{"--image wins over PROXIMO_SRC", "ghcr.io/other/proximo:v9", "/checkout", "ghcr.io/other/proximo:v9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("PROXIMO_SRC", tc.src)
			if err := convergeWith(&recordComposer{}, "test", "", ConvergeOpts{Image: tc.give}); err != nil {
				t.Fatalf("convergeWith: %v", err)
			}
			got, err := EnvImage()
			if err != nil {
				t.Fatalf("EnvImage: %v", err)
			}
			if got != tc.want {
				t.Errorf("EnvImage = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStickyImage asserts an override is carried forward by a side-effect
// converge while a published ref is not — so `config tld` never freezes the
// stack on a superseded version tag, and never silently drops an --image.
func TestStickyImage(t *testing.T) {
	for _, tc := range []struct{ name, env, want string }{
		{"override is sticky", "ghcr.io/other/proximo:v9", "ghcr.io/other/proximo:v9"},
		{"digest override is sticky", "ghcr.io/other/proximo@sha256:abc", "ghcr.io/other/proximo@sha256:abc"},
		// A ref this CLI computes for itself is recomputed, so a side-effect
		// converge never freezes the stack on a superseded version.
		{"release tag is recomputed", imageRepo + ":v0.1.0", ""},
		{"branch tag is recomputed", imageRepo + ":main", ""},
		// But a ref in the same repo that imageRef() never produces was named by
		// the developer, and dropping it would silently undo their flag.
		{"sha build in the published repo is sticky", imageRepo + ":sha-1a2b3c4", imageRepo + ":sha-1a2b3c4"},
		{"latest is sticky", imageRepo + ":latest", imageRepo + ":latest"},
		// Derived from PROXIMO_SRC, not from a flag: never carried forward.
		{"local source image is not sticky", devImage, ""},
		{"nothing materialized", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("PROXIMO_SRC", "")
			if tc.env != "" {
				if _, err := Materialize("test", "", tc.env); err != nil {
					t.Fatalf("Materialize: %v", err)
				}
			}
			got, err := StickyImage()
			if err != nil {
				t.Fatalf("StickyImage: %v", err)
			}
			if got != tc.want {
				t.Errorf("StickyImage = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDevOverridePointsAtLocalSource asserts PROXIMO_SRC redirects all three Go
// services to one locally built image (never a ghcr ref, which could be pushed
// or pulled by accident), and that clearing PROXIMO_SRC removes the override so
// the published image is used again.
func TestDevOverridePointsAtLocalSource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	src := t.TempDir()
	t.Setenv("PROXIMO_SRC", src)

	dir, err := Materialize("test", "", ConvergeOpts{}.EffectiveImage())
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	overridePath := filepath.Join(dir, "docker-compose.override.yml")
	data, err := os.ReadFile(overridePath)
	if err != nil {
		t.Fatalf("read override: %v", err)
	}
	override := string(data)
	for _, want := range []string{"  dns:", "  watcher:", "  inspector:",
		"context: " + src, "dockerfile: " + dockerfile} {
		if !strings.Contains(override, want) {
			t.Errorf("dev override missing %q:\n%s", want, override)
		}
	}
	if strings.Contains(override, imageRepo) {
		t.Errorf("dev override must not name the published repo:\n%s", override)
	}
	if n := strings.Count(override, "build:"); n != 3 {
		t.Errorf("dev override builds %d services, want 3:\n%s", n, override)
	}
	// Every build arg is quoted: an unquoted RFC 3339 date is a YAML timestamp,
	// and compose hands it back re-serialized with spaces, which splits the -X
	// linker flag it is interpolated into.
	for _, arg := range []string{"VERSION", "COMMIT", "DATE"} {
		if !strings.Contains(override, arg+`: "`) {
			t.Errorf("dev override does not quote the %s build arg:\n%s", arg, override)
		}
	}
	// The label the services stamp must name what they actually run.
	if got, err := EnvImage(); err != nil || got != devImage {
		t.Errorf("EnvImage = %q (err %v), want %q", got, err, devImage)
	}

	// An explicit --image wins over PROXIMO_SRC: nothing may rebuild over the
	// ref the developer named.
	if _, err := Materialize("test", "", "ghcr.io/other/proximo:v9"); err != nil {
		t.Fatalf("Materialize with --image: %v", err)
	}
	if _, err := os.Stat(overridePath); !os.IsNotExist(err) {
		t.Errorf("--image did not disable the PROXIMO_SRC build: %v", err)
	}

	t.Setenv("PROXIMO_SRC", "")
	if _, err := Materialize("test", "", CanonicalImage()); err != nil {
		t.Fatalf("Materialize without PROXIMO_SRC: %v", err)
	}
	if _, err := os.Stat(overridePath); !os.IsNotExist(err) {
		t.Errorf("stale dev override left behind: %v", err)
	}
}

func TestImageRef(t *testing.T) {
	cases := map[string]string{
		// Released binaries carry a bare semver (GoReleaser drops the "v"); the
		// published tag has it, so it is restored here.
		"0.1.0":     imageRepo + ":v0.1.0",
		"1.2.3":     imageRepo + ":v1.2.3",
		"0.1.0-rc1": imageRepo + ":v0.1.0-rc1",
		// Already-canonical refs are left untouched.
		"v0.1.0": imageRepo + ":v0.1.0",
		// Local/dev builds fall back to the branch tag the main pipeline pushes.
		"dev": imageRepo + ":main",
		"":    imageRepo + ":main",
	}
	for in, want := range cases {
		if got := imageRef(in); got != want {
			t.Errorf("imageRef(%q) = %q, want %q", in, got, want)
		}
	}
	// :latest is published for a human browsing the package page; proximo never
	// pins itself to it, because a floating tag is exactly the skew this design
	// exists to prevent.
	for _, v := range []string{"", "dev", "0.1.0", "v1.2.3"} {
		if got := imageRef(v); strings.HasSuffix(got, ":latest") {
			t.Errorf("imageRef(%q) = %q, must never pin :latest", v, got)
		}
	}
}

func TestIsMobileRef(t *testing.T) {
	mobile := []string{
		imageRepo + ":main",
		imageRepo + ":sha-abc1234",
		imageRepo + ":latest",
		imageRepo,     // untagged: docker resolves it to :latest
		"proximo:src", // a locally built image is rebuilt under a fixed name
		"",
	}
	for _, ref := range mobile {
		if !isMobileRef(ref) {
			t.Errorf("isMobileRef(%q) = false, want true", ref)
		}
	}
	immutable := []string{
		imageRepo + ":v0.1.0",
		imageRepo + ":v1.2.3",
		imageRepo + ":v0.1.0-rc1",
		imageRepo + "@sha256:0123456789abcdef",
		"registry.local:5000/proximo:v1.2.3",
	}
	for _, ref := range immutable {
		if isMobileRef(ref) {
			t.Errorf("isMobileRef(%q) = true, want false", ref)
		}
	}
	// A registry port must not be mistaken for a tag.
	if !isMobileRef("registry.local:5000/proximo") {
		t.Error("isMobileRef on an untagged ref with a registry port = false, want true")
	}
}

func TestComposeConvergeCmds(t *testing.T) {
	upPullPolicy := func(cmds [][]string) string {
		up := cmds[len(cmds)-1]
		if up[0] != "up" {
			t.Fatalf("last command is %v, want the bring-up", up)
		}
		i := slices.Index(up, "--pull")
		if i < 0 || i+1 >= len(up) {
			t.Fatalf("bring-up has no --pull policy: %v", up)
		}
		return up[i+1]
	}

	tests := []struct {
		name     string
		image    string
		force    bool
		wantPull string
	}{
		// A release tag is published once and never moved, so a cached copy is
		// necessarily current.
		{"release tag pulls only when missing", imageRepo + ":v0.1.0", false, "missing"},
		{"mobile ref pulls always", imageRepo + ":main", false, "always"},
		{"empty ref pulls always", "", false, "always"},
		{"force on a tag pulls always", imageRepo + ":v0.1.0", true, "always"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds := composeConvergeCmds(tt.image, tt.force)
			if got := upPullPolicy(cmds); got != tt.wantPull {
				t.Errorf("composeConvergeCmds(%q, %v) pull policy = %q, want %q",
					tt.image, tt.force, got, tt.wantPull)
			}
			// Traefik is pinned to a tag its upstream moves for patches, so it is
			// refreshed on every converge however the stack image is pinned — and
			// best-effort, so an offline host still converges from cache.
			pull := cmds[0]
			for _, want := range []string{"pull", "--ignore-pull-failures", "traefik"} {
				if !slices.Contains(pull, want) {
					t.Errorf("first command %v missing %q", pull, want)
				}
			}
		})
	}
}

// TestComposeRunsThePublishedImage asserts the embedded compose no longer
// compiles Go on the host: each Go service names the image and picks its binary
// with an entrypoint, and the compose-level default matches the ref the CLI
// computes, so a missing .env cannot silently resolve to a different repo.
func TestComposeRunsThePublishedImage(t *testing.T) {
	raw, err := assets.ReadFile("assets/docker-compose.yml")
	if err != nil {
		t.Fatalf("read embedded compose: %v", err)
	}
	compose := string(raw)

	if strings.Contains(compose, "build:") {
		t.Errorf("compose still builds a service on the host:\n%s", compose)
	}
	if strings.Contains(compose, "PROXIMO_REF") {
		t.Error("compose still references the module ref PROXIMO_REF")
	}
	for _, bin := range []string{"dnsserver", "watcher", "inspector"} {
		if want := `entrypoint: ["/usr/local/bin/` + bin + `"]`; !strings.Contains(compose, want) {
			t.Errorf("compose missing %q", want)
		}
	}
	// One image, three services — plus the label that records it, so a running
	// stack can never declare a ref it is not running.
	if n := strings.Count(compose, "image: ${"+imageEnvKey); n != 3 {
		t.Errorf("compose points %d services at ${%s}, want 3", n, imageEnvKey)
	}
	if n := strings.Count(compose, "proximo.image=${"+imageEnvKey); n != 3 {
		t.Errorf("compose stamps proximo.image on %d services, want 3", n)
	}
	if want := "${" + imageEnvKey + ":-" + imageRef("dev") + "}"; !strings.Contains(compose, want) {
		t.Errorf("compose default ref is not %q — it must match imageRef()", want)
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
		// The hop's read API is loopback-only; its proxy side is never published.
		fmt.Sprintf("127.0.0.1:%d:9001", config.InspectAPIPort),
		"proximo.role=inspector",
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
	for _, sentinel := range []string{tldSentinel, dnsPortSentinel, obsEmailSentinel, obsHubPortSentinel, inspectPortSentinel} {
		if strings.Contains(out, sentinel) {
			t.Errorf("sentinel %q left unsubstituted", sentinel)
		}
	}
	// The observability services must not carry a proximo.role label, or the
	// watcher would exclude them from routing.
	if strings.Contains(out, "proximo.role=dozzle") || strings.Contains(out, "proximo.role=beszel") {
		t.Error("observability service must not have a proximo.role label")
	}
	// The hop must never publish its proxy port: only Traefik reaches it.
	if strings.Contains(out, ":9000\"") {
		t.Error("the hop's proxy port must not be published to the host")
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
	if err := convergeObservabilityWith(c, "test", "", ConvergeOpts{}, bootstrap); err != nil {
		t.Fatalf("convergeObservabilityWith: %v", err)
	}

	if len(c.cmds) != 4 {
		t.Fatalf("ran %d commands, want 4 (traefik pull, core up, hub up, agent up): %v", len(c.cmds), c.cmds)
	}
	core, hub, agent := c.cmds[1], c.cmds[2], c.cmds[3]

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
	if bootstrapAt != 3 {
		t.Errorf("bootstrap ran at index %d, want 3 (after core+hub, before agent)", bootstrapAt)
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
	if _, err := Materialize("test", "", "ghcr.io/filippolmt/proximo:v0.1.0"); err != nil {
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
	if _, err := Materialize("test", "", "ghcr.io/filippolmt/proximo:v0.1.0"); err != nil {
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

// TestPurgeRemovesImages asserts uninstall's teardown also removes proximo's
// own images, and that it does so without asking compose to delete the
// third-party ones a plain `down` leaves cached for the next `up`.
func TestPurgeRemovesImages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Materialize("test", "", CanonicalImage()); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	called := false
	orig := purgeImages
	purgeImages = func() { called = true }
	t.Cleanup(func() { purgeImages = orig })

	if err := purgeWith(&recordComposer{}); err != nil {
		t.Fatalf("purgeWith: %v", err)
	}
	if !called {
		t.Error("Purge did not remove the stack images")
	}

	// Third-party images (traefik, the dashboards) must survive: compose must
	// never be asked to remove every image the project references.
	c := &recordComposer{}
	if err := downWith(c); err != nil {
		t.Fatalf("downWith: %v", err)
	}
	if slices.Contains(c.cmds[0], "--rmi") {
		t.Errorf("teardown must not remove third-party images: %v", c.cmds[0])
	}
}

// errComposer is a Composer whose every command fails, so the converge error
// path can be exercised without Docker.
type errComposer struct{}

func (errComposer) Compose(string, ...string) error { return errors.New("pull access denied") }

// TestConvergeRemedyOnFailedPull asserts a converge that leaves the stack image
// absent reports the command that fixes it — and that it never invents a pull
// remedy for an image the host was supposed to build, or for a failure that has
// nothing to do with the image.
func TestConvergeRemedyOnFailedPull(t *testing.T) {
	const ref = "ghcr.io/filippolmt/proximo:v9.9.9"
	cases := []struct {
		name       string
		image      string
		present    bool
		wantRemedy bool
	}{
		{"image never landed", ref, false, true},
		{"image is there, the failure is something else", ref, true, false},
		{"a local build failure is not a pull failure", devImage, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("PROXIMO_SRC", "")
			orig := imagePresent
			imagePresent = func(string) bool { return tc.present }
			t.Cleanup(func() { imagePresent = orig })

			err := convergeWith(errComposer{}, "test", "", ConvergeOpts{Image: tc.image})
			if err == nil {
				t.Fatal("convergeWith on a failing composer returned nil")
			}
			if got := strings.Contains(err.Error(), "Remedy: docker pull "+tc.image); got != tc.wantRemedy {
				t.Errorf("remedy present = %v, want %v: %v", got, tc.wantRemedy, err)
			}
			if !strings.Contains(err.Error(), "pull access denied") {
				t.Errorf("remedy dropped the underlying error: %v", err)
			}
		})
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
