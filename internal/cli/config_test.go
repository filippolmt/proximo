package cli

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
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

func runConfig(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newConfigCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestConfigMachinePrintsTheRuleOnEverySet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for i := 0; i < 2; i++ {
		out, err := runConfig(t, "machine", "Studio-01")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Saved machine: studio-01") || !strings.Contains(out, "not a person") {
			t.Errorf("run %d output = %q", i, out)
		}
	}
}

func TestConfigTeamRootNeedsThePeerSuffix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := runConfig(t, "team-root", "/nonexistent.pem")
	if err == nil || !strings.Contains(err.Error(), "proximo config peer-suffix") {
		t.Fatalf("err = %v, want a Remedy naming config peer-suffix", err)
	}
}

func TestConfigAddressWarnsWhenNoInterfaceHoldsIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	interfaceAddrs = func() ([]net.Addr, error) { return nil, nil }
	t.Cleanup(func() { interfaceAddrs = net.InterfaceAddrs })
	out, err := runConfig(t, "address", "100.89.88.2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not held by any interface") || !strings.Contains(out, "Saved address: 100.89.88.2") {
		t.Errorf("output = %q", out)
	}
}
