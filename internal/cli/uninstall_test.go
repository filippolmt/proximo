package cli

import (
	"io"
	"testing"
)

// TestStopAndCleanStackWiring asserts uninstall stops the stack (which also
// removes the profiled observability containers) and then purges the
// observability secret + env files, in that order, passing the materialized
// stack dir to the cleanup.
func TestStopAndCleanStackWiring(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var order []string
	var purgedDir string

	origTeardown, origPurge := teardownStack, purgeObservability
	t.Cleanup(func() { teardownStack, purgeObservability = origTeardown, origPurge })

	teardownStack = func() error {
		order = append(order, "teardown")
		return nil
	}
	purgeObservability = func(stackDir string) error {
		order = append(order, "purge")
		purgedDir = stackDir
		return nil
	}

	if err := stopAndCleanStack(io.Discard); err != nil {
		t.Fatalf("stopAndCleanStack: %v", err)
	}

	if len(order) != 2 || order[0] != "teardown" || order[1] != "purge" {
		t.Errorf("call order = %v, want [teardown purge]", order)
	}
	if purgedDir == "" {
		t.Error("purge was not given the stack dir")
	}
}

// TestStopAndCleanStackTeardownErrorStops ensures a teardown failure short-
// circuits before the secret is purged.
func TestStopAndCleanStackTeardownErrorStops(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	origTeardown, origPurge := teardownStack, purgeObservability
	t.Cleanup(func() { teardownStack, purgeObservability = origTeardown, origPurge })

	teardownStack = func() error { return io.ErrClosedPipe }
	purged := false
	purgeObservability = func(string) error { purged = true; return nil }

	if err := stopAndCleanStack(io.Discard); err == nil {
		t.Fatal("expected teardown error to propagate")
	}
	if purged {
		t.Error("purge ran despite teardown failure")
	}
}
