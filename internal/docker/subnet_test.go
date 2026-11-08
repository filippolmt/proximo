package docker

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/config"
)

const testImage = "ghcr.io/filippolmt/proximo:v0.1.0"

// stubStack fixes what converge reads about the running stack before it
// rewrites it: the Stack network's actual subnet ("" when it does not exist)
// and whether the observability services are running.
func stubStack(t *testing.T, subnet string, obs bool) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	origReader, origImage := defaultStackReader, imagePresent
	defaultStackReader = fakeStackReader{subnet: subnet, obs: obs}
	imagePresent = func(string) bool { return true }
	t.Cleanup(func() { defaultStackReader, imagePresent = origReader, origImage })
}

type fakeStackReader struct {
	subnet string
	obs    bool
	obsErr error
}

func (f fakeStackReader) Subnet() (string, error)             { return f.subnet, nil }
func (f fakeStackReader) ObservabilityRunning() (bool, error) { return f.obs, f.obsErr }

// countUps counts the `up` commands a converge ran.
func countUps(cmds [][]string) int {
	n := 0
	for _, a := range cmds {
		if slices.Contains(a, "up") {
			n++
		}
	}
	return n
}

// failingDown is a recordComposer whose `down` fails, leaving the stack
// half-removed.
type failingDown struct{ recordComposer }

func (f *failingDown) Compose(dir string, args ...string) error {
	_ = f.recordComposer.Compose(dir, args...)
	if slices.Contains(args, "down") {
		return errors.New("compose down failed")
	}
	return nil
}

// failingUps is a recordComposer whose first n `up` commands fail, the way a
// converge fails when Docker refuses to create the network.
type failingUps struct {
	recordComposer
	n int
}

func (f *failingUps) Compose(dir string, args ...string) error {
	_ = f.recordComposer.Compose(dir, args...)
	if slices.Contains(args, "up") && f.n > 0 {
		f.n--
		return errors.New("Pool overlaps with other one on this address space")
	}
	return nil
}

func envSubnet(t *testing.T) string {
	t.Helper()
	dir, err := StackDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, subnetEnvKey+"="); ok {
			return v
		}
	}
	return ""
}

func hasDown(cmds [][]string) bool {
	return slices.ContainsFunc(cmds, func(c []string) bool { return slices.Contains(c, "down") })
}

func TestComposeDeclaresTheStackSubnet(t *testing.T) {
	raw, err := assets.ReadFile("assets/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	// The asset keeps a working default, so a hand-run `docker compose up` in
	// the stack dir still declares the subnet.
	if !strings.Contains(string(raw), "subnet: ${PROXIMO_SUBNET:-"+config.DefaultSubnet+"}") {
		t.Errorf("compose does not declare the stack subnet from PROXIMO_SUBNET:\n%s", raw)
	}
}

func TestConvergeWritesTheConfiguredSubnet(t *testing.T) {
	stubStack(t, "", false)
	if err := convergeWith(&recordComposer{}, "test", "", ConvergeOpts{Image: testImage}); err != nil {
		t.Fatal(err)
	}
	if got := envSubnet(t); got != config.DefaultSubnet {
		t.Errorf("PROXIMO_SUBNET = %q, want the default %s", got, config.DefaultSubnet)
	}
	// An explicit subnet wins over the configured one, and is not persisted.
	if err := convergeWith(&recordComposer{}, "test", "", ConvergeOpts{Image: testImage, Subnet: "10.9.0.0/24"}); err != nil {
		t.Fatal(err)
	}
	if got := envSubnet(t); got != "10.9.0.0/24" {
		t.Errorf("PROXIMO_SUBNET = %q, want 10.9.0.0/24", got)
	}
	if cfg, _ := config.Load(); cfg.Subnet != config.DefaultSubnet {
		t.Errorf("converge persisted the subnet: %q", cfg.Subnet)
	}
}

func TestConvergeKeepsAMatchingOrAbsentNetwork(t *testing.T) {
	for _, actual := range []string{"", config.DefaultSubnet} {
		stubStack(t, actual, false)
		if _, err := Materialize("test", "", testImage); err != nil {
			t.Fatal(err)
		}
		c := &recordComposer{}
		if err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage}); err != nil {
			t.Fatal(err)
		}
		if hasDown(c.cmds) {
			t.Errorf("actual subnet %q: converge ran a down: %v", actual, c.cmds)
		}
	}
}

func TestConvergeRecreatesAnInheritedNetwork(t *testing.T) {
	stubStack(t, "172.18.0.0/16", false)
	if _, err := Materialize("test", "", testImage); err != nil {
		t.Fatal(err)
	}
	c := &recordComposer{}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage}); err != nil {
		t.Fatal(err)
	}
	// Every profile, or a running profile service keeps Compose from
	// recreating the network.
	if len(c.cmds) == 0 || !slices.Equal(c.cmds[0], []string{"--profile", observabilityProfile, "--profile", peerProfile, "down", "--remove-orphans"}) {
		t.Fatalf("first command = %v, want a down of every profile", c.cmds)
	}
	if want := composeConvergeCmds(testImage, false); !slices.EqualFunc(c.cmds[1:], want, slices.Equal[[]string]) {
		t.Errorf("after the down ran %v, want %v", c.cmds[1:], want)
	}
}

func TestConvergeRestoresObservabilityAfterARecreate(t *testing.T) {
	stubStack(t, "172.18.0.0/16", true)
	if _, err := Materialize("test", "", testImage); err != nil {
		t.Fatal(err)
	}
	c := &recordComposer{}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage}); err != nil {
		t.Fatal(err)
	}
	if last := c.cmds[len(c.cmds)-1]; !slices.Equal(last, []string{"--profile", observabilityProfile, "up", "-d"}) {
		t.Errorf("last command = %v, want the observability profile back up", last)
	}

	// Without a recreate, a running observability profile is left alone.
	stubStack(t, config.DefaultSubnet, true)
	c = &recordComposer{}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage}); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(c.cmds, func(a []string) bool { return slices.Contains(a, observabilityProfile) }) {
		t.Errorf("converge without a recreate touched observability: %v", c.cmds)
	}
}

func TestConvergeRollsBackANetworkDockerRefuses(t *testing.T) {
	stubStack(t, "172.18.0.0/16", false)
	if _, err := Materialize("test", "", testImage); err != nil {
		t.Fatal(err)
	}
	c := &failingUps{n: 1}
	err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage, Subnet: "10.9.0.0/24"})
	if err == nil {
		t.Fatal("converge onto a refused subnet succeeded")
	}
	for _, want := range []string{"Pool overlaps", "10.9.0.0/24", "172.18.0.0/16", "Remedy: proximo config subnet <cidr>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	// The stack is back on the network it had: the rollback converged again
	// with the subnet read before the down.
	if got := envSubnet(t); got != "172.18.0.0/16" {
		t.Errorf("PROXIMO_SUBNET after rollback = %q, want 172.18.0.0/16", got)
	}
	if ups := countUps(c.cmds); ups != 2 {
		t.Errorf("ran %d ups, want the failed one and the rollback: %v", ups, c.cmds)
	}
}

func TestConvergeReportsAFailedRollback(t *testing.T) {
	stubStack(t, "172.18.0.0/16", false)
	if _, err := Materialize("test", "", testImage); err != nil {
		t.Fatal(err)
	}
	err := convergeWith(&failingUps{n: 2}, "test", "", ConvergeOpts{Image: testImage, Subnet: "10.9.0.0/24"})
	if err == nil {
		t.Fatal("converge with a failed rollback succeeded")
	}
	for _, want := range []string{"10.9.0.0/24", "172.18.0.0/16", "the stack is down", "Remedy: proximo config subnet <cidr>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
	if strings.Count(err.Error(), "Pool overlaps") != 2 {
		t.Errorf("error does not carry both failures: %v", err)
	}
}

// The down removed every profile, and the watcher restarts only containers
// that still exist: a rollback that succeeds must bring them back like any
// converge that moved the network.
func TestARolledBackStackGetsObservabilityBack(t *testing.T) {
	stubStack(t, "172.18.0.0/16", true)
	if _, err := Materialize("test", "", testImage); err != nil {
		t.Fatal(err)
	}
	c := &failingUps{n: 1}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage, Subnet: "10.9.0.0/24"}); err == nil {
		t.Fatal("converge onto a refused subnet succeeded")
	}
	if last := c.cmds[len(c.cmds)-1]; !slices.Equal(last, []string{"--profile", observabilityProfile, "up", "-d"}) {
		t.Errorf("last command = %v, want the observability profile back up", last)
	}
}

// A down that fails leaves the stack half-removed: it is converged back onto
// the subnet it had, not left that way.
func TestAFailedDownIsRolledBack(t *testing.T) {
	stubStack(t, "172.18.0.0/16", false)
	if _, err := Materialize("test", "", testImage); err != nil {
		t.Fatal(err)
	}
	err := convergeWith(&failingDown{}, "test", "", ConvergeOpts{Image: testImage})
	if err == nil || !strings.Contains(err.Error(), "it is back on 172.18.0.0/16") {
		t.Fatalf("converge after a failed down = %v, want it back on the old subnet", err)
	}
	if got := envSubnet(t); got != "172.18.0.0/16" {
		t.Errorf("PROXIMO_SUBNET = %q, want 172.18.0.0/16", got)
	}
}

// A missing stack image is no network problem: the rollback would fail the
// same way, and the pull Remedy is the one that cures it.
func TestAMissingImageIsNotRolledBack(t *testing.T) {
	stubStack(t, "172.18.0.0/16", false)
	imagePresent = func(string) bool { return false }
	if _, err := Materialize("test", "", testImage); err != nil {
		t.Fatal(err)
	}
	c := &failingUps{n: 1}
	err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage})
	if err == nil || !strings.Contains(err.Error(), "Remedy: docker pull "+testImage) || strings.Contains(err.Error(), "config subnet") {
		t.Fatalf("converge with a missing image = %v, want only the pull Remedy", err)
	}
	if ups := countUps(c.cmds); ups != 1 {
		t.Errorf("ran %d ups, want only the failed one: %v", ups, c.cmds)
	}
}

// A configured subnet Docker would refuse — written into config.json by hand
// — is refused before the stack is taken down.
func TestAnInvalidConfiguredSubnetIsRefusedBeforeTheDown(t *testing.T) {
	stubStack(t, "172.18.0.0/16", false)
	if err := (config.Config{TLD: "test", Subnet: "8.8.8.0/24"}).Save(); err != nil {
		t.Fatal(err)
	}
	c := &recordComposer{}
	err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage})
	if err == nil || !strings.Contains(err.Error(), "Remedy: proximo config subnet <cidr>") {
		t.Fatalf("converge with an invalid subnet = %v, want the config subnet Remedy", err)
	}
	if hasDown(c.cmds) {
		t.Errorf("took the stack down for an invalid subnet: %v", c.cmds)
	}
}

// An observability state Docker would not report is no reason to keep the
// old network: the move goes ahead.
func TestAnUnreadableObservabilityStateStillMoves(t *testing.T) {
	stubStack(t, "172.18.0.0/16", false)
	defaultStackReader = fakeStackReader{subnet: "172.18.0.0/16", obsErr: errors.New("context deadline exceeded")}
	if _, err := Materialize("test", "", testImage); err != nil {
		t.Fatal(err)
	}
	c := &recordComposer{}
	if err := convergeWith(c, "test", "", ConvergeOpts{Image: testImage}); err != nil {
		t.Fatal(err)
	}
	if !hasDown(c.cmds) {
		t.Errorf("did not move the network: %v", c.cmds)
	}
}
