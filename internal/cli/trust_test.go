package cli

import (
	"io"
	"testing"

	"github.com/filippolmt/proximo/internal/platform"
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
