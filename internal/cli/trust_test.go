package cli

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/platform"
)

// TestApplyTrustOrder asserts the trust command writes the system store before
// the NSS store, passing the privileged runner through to both.
func TestApplyTrustOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	origSystem, origNSS := installSystemTrust, installNSSTrust
	t.Cleanup(func() { installSystemTrust, installNSSTrust = origSystem, origNSS })

	var order []string
	var systemRunner, nssRunner platform.Runner
	installSystemTrust = func(r platform.Runner) error {
		order = append(order, "system")
		systemRunner = r
		return nil
	}
	installNSSTrust = func(r platform.Runner) error {
		order = append(order, "nss")
		nssRunner = r
		return nil
	}

	if err := applyTrust(io.Discard, defaultRunner); err != nil {
		t.Fatalf("applyTrust: %v", err)
	}

	if len(order) != 2 || order[0] != "system" || order[1] != "nss" {
		t.Errorf("call order = %v, want [system nss]", order)
	}
	if systemRunner != defaultRunner || nssRunner != defaultRunner {
		t.Error("trust writes did not receive the default privileged runner")
	}
}

// TestApplyTrustSystemErrorStops ensures a system-store failure short-circuits
// before the NSS store is touched.
func TestApplyTrustSystemErrorStops(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	origSystem, origNSS := installSystemTrust, installNSSTrust
	t.Cleanup(func() { installSystemTrust, installNSSTrust = origSystem, origNSS })

	installSystemTrust = func(platform.Runner) error { return io.ErrClosedPipe }
	nssRan := false
	installNSSTrust = func(platform.Runner) error { nssRan = true; return nil }

	if err := applyTrust(io.Discard, defaultRunner); err == nil {
		t.Fatal("expected system-store error to propagate")
	}
	if nssRan {
		t.Error("NSS trust ran despite a system-store failure")
	}
}

// stubTrust replaces every trust-store write with a recorder.
func stubTrust(t *testing.T) *[]string {
	t.Helper()
	origSystem, origNSS, origRoot, origRemove := installSystemTrust, installNSSTrust, installTeamRootTrust, removeTeamRootTrust
	t.Cleanup(func() {
		installSystemTrust, installNSSTrust, installTeamRootTrust, removeTeamRootTrust = origSystem, origNSS, origRoot, origRemove
	})
	var calls []string
	installSystemTrust = func(platform.Runner) error { calls = append(calls, "system"); return nil }
	installNSSTrust = func(platform.Runner) error { calls = append(calls, "nss"); return nil }
	installTeamRootTrust = func(_ platform.Runner, path string) error { calls = append(calls, "team-root "+path); return nil }
	removeTeamRootTrust = func(_ platform.Runner, path string) error {
		calls = append(calls, "remove team-root "+path)
		return nil
	}
	return &calls
}

// configureTeamRoot saves a Peer suffix and a team root that covers it.
func configureTeamRoot(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg := config.Default()
	cfg.PeerSuffix = "mesh.internal"
	cfg.TeamRoot = writeTeamRoot(t)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestApplyTrustInstallsTheTeamRootAfterTheLocalCA(t *testing.T) {
	calls := stubTrust(t)
	cfg := configureTeamRoot(t)
	var out bytes.Buffer
	if err := applyTrust(&out, defaultRunner); err != nil {
		t.Fatal(err)
	}
	if want := []string{"system", "nss", "team-root " + cfg.TeamRoot}; !slices.Equal(*calls, want) {
		t.Errorf("calls = %v, want %v", *calls, want)
	}
	if !strings.Contains(out.String(), "team root") {
		t.Errorf("output = %q", out.String())
	}
}

// The file is judged again before it reaches a trust store: it may have
// changed, or the suffix may have, since config team-root accepted it.
func TestApplyTrustRefusesATeamRootThatNoLongerValidates(t *testing.T) {
	calls := stubTrust(t)
	cfg := configureTeamRoot(t)
	cfg.PeerSuffix = "other.internal"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if err := applyTrust(io.Discard, defaultRunner); err == nil || !strings.Contains(err.Error(), "does not cover other.internal") {
		t.Fatalf("err = %v, want the team root refused", err)
	}
	if want := []string{"system", "nss"}; !slices.Equal(*calls, want) {
		t.Errorf("calls = %v, want the local CA only: %v", *calls, want)
	}
}

func TestHostStepsTeamRoot(t *testing.T) {
	calls := stubTrust(t)
	cfg := configureTeamRoot(t)
	steps := hostSteps(defaultRunner, cfg)
	if len(steps) != 5 {
		t.Fatalf("hostSteps len = %d, want 5", len(steps))
	}
	last := steps[4]
	if last.applyMsg == "" || last.revertMsg == "" {
		t.Errorf("team root step banners = %q / %q", last.applyMsg, last.revertMsg)
	}
	if err := last.apply(); err != nil {
		t.Fatal(err)
	}
	if err := last.revert(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"team-root " + cfg.TeamRoot, "remove team-root " + cfg.TeamRoot}; !slices.Equal(*calls, want) {
		t.Errorf("calls = %v, want %v", *calls, want)
	}

	// A peer-side failure never fails the command that serves .test: install
	// warns and goes on, and uninstall still reaches the local CA.
	installTeamRootTrust = func(platform.Runner, string) error { return errors.New("boom") }
	removeTeamRootTrust = func(platform.Runner, string) error { return errors.New("boom") }
	if err := last.apply(); err != nil {
		t.Errorf("a team root failure failed install: %v", err)
	}
	if err := last.revert(); err != nil {
		t.Errorf("a team root failure failed uninstall: %v", err)
	}
}
