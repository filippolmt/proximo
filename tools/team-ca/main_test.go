package main

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/filippolmt/proximo/internal/tls"
)

// The whole ceremony through the tool: what it signs, proximo installs.
func TestRootThenSign(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	if err := run([]string{"root", "mesh.internal"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"root", "mesh.internal"}, io.Discard); err == nil {
		t.Fatal("a second root overwrote the first")
	}
	csr, err := tls.MachineCSR("studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("machine.csr", csr, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"sign", "mesh.internal", "studio-01", "team-root.crt", "team-root.key", "machine.csr"}, &out); err != nil {
		t.Fatal(err)
	}
	root, err := os.ReadFile("team-root.crt")
	if err != nil {
		t.Fatal(err)
	}
	if err := tls.ValidateTeamRoot(root, "mesh.internal"); err != nil {
		t.Errorf("config team-root would refuse the root: %v", err)
	}
	if err := tls.ValidateIntermediate(out.Bytes(), root, "studio-01", "mesh.internal"); err != nil {
		t.Errorf("config intermediate would refuse the intermediate: %v", err)
	}
}
