package cli

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/platform"
	"github.com/filippolmt/proximo/internal/tls"
)

// TestApplyTrustOrder asserts the trust command writes the system store before
// the NSS store, passing the privileged runner through to both.
func TestApplyTrustOrder(t *testing.T) {
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
	removeTeamRootTrust = func(_ platform.Runner, fp string) error { calls = append(calls, "remove team-root "+fp); return nil }
	return &calls
}

// configureTeamRoot saves a Peer suffix and a team root that covers it, and
// returns the config with the root's fingerprint.
func configureTeamRoot(t *testing.T) (config.Config, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg := config.Default()
	cfg.PeerSuffix = "mesh.internal"
	cfg.TeamRoot = writeTeamRoot(t)
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	fp, err := tls.TeamRootFingerprint(cfg.TeamRoot)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, fp
}

// trust installs the team root after the local CA, and records what it
// installed, so uninstall removes it without needing the file.
func TestTrustConfiguredTeamRoot(t *testing.T) {
	calls := stubTrust(t)
	cfg, fp := configureTeamRoot(t)
	var out bytes.Buffer
	if err := trustConfiguredTeamRoot(&out, defaultRunner); err != nil {
		t.Fatal(err)
	}
	if want := []string{"team-root " + cfg.TeamRoot}; !slices.Equal(*calls, want) {
		t.Errorf("calls = %v, want %v", *calls, want)
	}
	if !strings.Contains(out.String(), teamRootApplyMsg) {
		t.Errorf("output = %q", out.String())
	}
	saved, err := config.Load()
	if err != nil || saved.TeamRootTrusted != fp {
		t.Errorf("TeamRootTrusted = %q, %v; want %s", saved.TeamRootTrusted, err, fp)
	}
}

// Without a team root, trust does exactly what it did before.
func TestTrustConfiguredTeamRootUnconfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	calls := stubTrust(t)
	var out bytes.Buffer
	if err := trustConfiguredTeamRoot(&out, defaultRunner); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || out.Len() != 0 {
		t.Errorf("calls = %v, output = %q; want nothing", *calls, out.String())
	}
}

// A replaced team root leaves the trust stores: the macOS keychain selects by
// fingerprint, so nothing else would ever remove the old one.
func TestTrustTeamRootReplacesTheOldAnchor(t *testing.T) {
	calls := stubTrust(t)
	cfg, fp := configureTeamRoot(t)
	cfg.TeamRootTrusted = "OLD"
	if err := trustTeamRoot(defaultRunner, &cfg); err != nil {
		t.Fatal(err)
	}
	if want := []string{"remove team-root OLD", "team-root " + cfg.TeamRoot}; !slices.Equal(*calls, want) {
		t.Errorf("calls = %v, want %v", *calls, want)
	}
	if cfg.TeamRootTrusted != fp {
		t.Errorf("TeamRootTrusted = %q, want %s", cfg.TeamRootTrusted, fp)
	}
}

// The file is judged again before it reaches a trust store: it may have
// changed, or the suffix may have, since config team-root accepted it.
func TestTrustTeamRootRefusesARootThatNoLongerValidates(t *testing.T) {
	calls := stubTrust(t)
	cfg, _ := configureTeamRoot(t)
	cfg.PeerSuffix = "other.internal"
	if err := trustTeamRoot(defaultRunner, &cfg); err == nil || !strings.Contains(err.Error(), "does not cover other.internal") {
		t.Fatalf("err = %v, want the team root refused", err)
	}
	if len(*calls) != 0 || cfg.TeamRootTrusted != "" {
		t.Errorf("calls = %v, TeamRootTrusted = %q; want nothing installed", *calls, cfg.TeamRootTrusted)
	}
}
