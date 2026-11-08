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
	origSubnet, origObs := actualSubnet, observabilityRunning
	actualSubnet = func() (string, error) { return subnet, nil }
	observabilityRunning = func() bool { return obs }
	t.Cleanup(func() { actualSubnet, observabilityRunning = origSubnet, origObs })
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
	ups := 0
	for _, a := range c.cmds {
		if slices.Contains(a, "up") {
			ups++
		}
	}
	if ups != 2 {
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
