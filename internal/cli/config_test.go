package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestConfigCAPathPrintsWithoutSideEffects asserts `config ca-path` prints the
// CA path under the state home and — being a query for external tools — never
// creates the state home on a host without proximo installed.
func TestConfigCAPathPrintsWithoutSideEffects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out bytes.Buffer
	cmd := newConfigCAPathCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config ca-path: %v", err)
	}

	want := filepath.Join(home, ".proximo", "tls", "ca.pem") + "\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}

	if _, err := os.Stat(filepath.Join(home, ".proximo")); !os.IsNotExist(err) {
		t.Errorf("state home was created by a query-only command (stat err = %v)", err)
	}
}
