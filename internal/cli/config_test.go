package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/tls"
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

// TestConfigInventoryDirPrintsWithoutSideEffects: the same contract as ca-path,
// for the directory the watcher keeps the effective-route inventory in.
func TestConfigInventoryDirPrintsWithoutSideEffects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out, err := runConfig(t, "inventory-dir")
	if err != nil {
		t.Fatalf("config inventory-dir: %v", err)
	}
	if want := filepath.Join(home, ".proximo", "data", "inventory") + "\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
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

// writeTeamRoot writes a root permitted .mesh.internal and excluding every IP,
// email address and URI — the shape config team-root accepts — and returns its
// path.
func writeTeamRoot(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, v4, _ := net.ParseCIDR("0.0.0.0/0")
	_, v6, _ := net.ParseCIDR("::/0")
	tmpl := &x509.Certificate{
		SerialNumber:                big.NewInt(1),
		NotBefore:                   time.Now(),
		NotAfter:                    time.Now().AddDate(10, 0, 0),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		KeyUsage:                    x509.KeyUsageCertSign,
		PermittedDNSDomains:         []string{".mesh.internal"},
		PermittedDNSDomainsCritical: true,
		ExcludedIPRanges:            []*net.IPNet{v4, v6},
		ExcludedEmailAddresses:      []string{""},
		ExcludedURIDomains:          []string{""},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "team-root.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigTeamRootStoresAnAbsolutePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := runConfig(t, "peer-suffix", "mesh.internal"); err != nil {
		t.Fatal(err)
	}
	if _, err := runConfig(t, "team-root", writeTeamRoot(t)); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(cfg.TeamRoot) {
		t.Errorf("TeamRoot = %q, want an absolute path", cfg.TeamRoot)
	}
}

func TestConfigPeerSuffixWarnsWhenTheTeamRootNoLongerCoversIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := runConfig(t, "peer-suffix", "mesh.internal"); err != nil {
		t.Fatal(err)
	}
	if _, err := runConfig(t, "team-root", writeTeamRoot(t)); err != nil {
		t.Fatal(err)
	}
	out, err := runConfig(t, "peer-suffix", "other.internal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "does not cover other.internal") || !strings.Contains(out, "Remedy: proximo config team-root") || !strings.Contains(out, "Saved peer-suffix: other.internal") {
		t.Errorf("output = %q", out)
	}
}

// Each refusal stops the command and stores nothing.
func TestConfigRefusals(t *testing.T) {
	for _, args := range [][]string{
		{"machine", "studio.01"},
		{"peer-suffix", "internal"},
		{"peer-suffix", "mesh.local"},
		// proximo's own DNS answers the TLD with 127.0.0.1.
		{"peer-suffix", "mesh.test"},
		{"address", "100.89.88"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if _, err := runConfig(t, args...); err == nil {
				t.Fatal("accepted")
			}
			if cfg, err := config.Load(); err != nil || cfg != config.Default() {
				t.Errorf("config = %+v, %v; want the default", cfg, err)
			}
		})
	}
}

// enrol configures a machine up to its CSR and returns the team root's
// files, so a test can play the custodian.
func enrol(t *testing.T) (rootPEM, rootKeyPEM, csrPEM []byte) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	rootPEM, rootKeyPEM, err := tls.NewTeamRoot("mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "team-root.crt")
	if err := os.WriteFile(root, rootPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"peer-suffix", "mesh.internal"}, {"machine", "studio-01"}, {"team-root", root},
	} {
		if _, err := runConfig(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runConfig(t, "csr")
	if err != nil {
		t.Fatal(err)
	}
	return rootPEM, rootKeyPEM, []byte(out)
}

// config csr prints the CSR and nothing else, since it is redirected to a file.
func TestConfigCSRPrintsOnlyTheRequest(t *testing.T) {
	_, _, csr := enrol(t)
	if !strings.HasPrefix(string(csr), "-----BEGIN CERTIFICATE REQUEST-----") || !strings.HasSuffix(string(csr), "-----END CERTIFICATE REQUEST-----\n") {
		t.Errorf("output = %q", csr)
	}
}

func TestConfigCSRNeedsTheMachineAndSuffix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := runConfig(t, "csr"); err == nil || !strings.Contains(err.Error(), "Remedy: proximo config machine") {
		t.Fatalf("err = %v, want a Remedy naming config machine", err)
	}
	if _, err := runConfig(t, "machine", "studio-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := runConfig(t, "csr"); err == nil || !strings.Contains(err.Error(), "Remedy: proximo config peer-suffix") {
		t.Fatalf("err = %v, want a Remedy naming config peer-suffix", err)
	}
}

func TestConfigIntermediate(t *testing.T) {
	rootPEM, rootKeyPEM, csr := enrol(t)
	intPEM, err := tls.SignIntermediate(rootPEM, rootKeyPEM, csr, "studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "intermediate.crt")
	if err := os.WriteFile(file, intPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runConfig(t, "intermediate", file)
	if err != nil || !strings.Contains(out, "Saved intermediate") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if c, err := tls.Intermediate(); err != nil || c == nil {
		t.Fatalf("Intermediate() = %v, %v", c, err)
	}

	// The intermediate is constrained to the old label: renaming the machine
	// says so, and names the ceremony.
	out, err = runConfig(t, "machine", "studio-02")
	if err != nil || !strings.Contains(out, "Remedy: proximo config csr") {
		t.Errorf("out = %q, err = %v; want a warning naming config csr", out, err)
	}
}

func TestConfigIntermediateRefusesAnotherMachines(t *testing.T) {
	rootPEM, rootKeyPEM, _ := enrol(t)
	// A CSR for the same name, from a key this machine does not hold.
	home := os.Getenv("HOME")
	t.Setenv("HOME", t.TempDir())
	other, err := tls.MachineCSR("studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	intPEM, err := tls.SignIntermediate(rootPEM, rootKeyPEM, other, "studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "intermediate.crt")
	if err := os.WriteFile(file, intPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runConfig(t, "intermediate", file); err == nil || !strings.Contains(err.Error(), "machine key") {
		t.Fatalf("err = %v, want a refusal naming the machine key", err)
	}
	if c, _ := tls.Intermediate(); c != nil {
		t.Error("a refused intermediate was installed")
	}
}

func TestConfigIntermediateNeedsItsValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := runConfig(t, "intermediate", "/nonexistent.crt"); err == nil || !strings.Contains(err.Error(), "Remedy: proximo config machine") {
		t.Fatalf("err = %v, want a Remedy naming config machine", err)
	}
}

// A value set while the stack runs reaches it at once: the watcher re-reads
// the peer material each reconcile, so status and the watcher never disagree.
func TestConfigIntermediateSyncsARunningStack(t *testing.T) {
	rootPEM, rootKeyPEM, csr := enrol(t)
	stack, err := docker.StackDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stack, "ca"), 0o755); err != nil {
		t.Fatal(err)
	}
	intPEM, err := tls.SignIntermediate(rootPEM, rootKeyPEM, csr, "studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "intermediate.crt")
	if err := os.WriteFile(file, intPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runConfig(t, "intermediate", file); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stack, "ca", "peer.json")); err != nil {
		t.Errorf("the stack was not given the peer material: %v", err)
	}
	// A new label withdraws it until a new intermediate arrives.
	if _, err := runConfig(t, "machine", "studio-02"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stack, "ca", "peer.json")); err == nil {
		t.Error("the stack kept peer material for the old label")
	}
}

// unset returns a value to its unconfigured state, and the stack with it.
func TestConfigUnset(t *testing.T) {
	rootPEM, rootKeyPEM, csr := enrol(t)
	intPEM, err := tls.SignIntermediate(rootPEM, rootKeyPEM, csr, "studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "intermediate.crt")
	if err := os.WriteFile(file, intPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runConfig(t, "intermediate", file); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"intermediate", "team-root", "machine", "peer-suffix", "address", "mesh-remedy"} {
		if _, err := runConfig(t, "unset", name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg != config.Default() {
		t.Errorf("config after unsetting everything = %+v", cfg)
	}
	if c, _ := tls.Intermediate(); c != nil {
		t.Error("the intermediate outlived unset")
	}
	if _, err := runConfig(t, "unset", "tld"); err == nil {
		t.Error("unset accepted a value that is not a peer value")
	}
}

// The peer subcommands are visible now that the capability is built.
func TestPeerSubcommandsAreVisible(t *testing.T) {
	for _, c := range newConfigCmd().Commands() {
		if c.Hidden {
			t.Errorf("config %s is hidden", c.Name())
		}
	}
}

// stubSubnetStack fixes whether the stack runs and what a converge does, and
// records the subnet each converge was asked for.
func stubSubnetStack(t *testing.T, running bool, statusErr, convergeErr error) *[]string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var asked []string
	origRunning, origConverge := stackRunning, convergeStack
	stackRunning = func(context.Context) (bool, error) { return running, statusErr }
	convergeStack = func(_, _ string, opts docker.ConvergeOpts) error {
		asked = append(asked, opts.Subnet)
		return convergeErr
	}
	t.Cleanup(func() { stackRunning, convergeStack = origRunning, origConverge })
	return &asked
}

func TestConfigSubnetConvergesThenPersists(t *testing.T) {
	asked := stubSubnetStack(t, true, nil, nil)
	out, err := runConfig(t, "subnet", "10.42.0.7/24")
	if err != nil {
		t.Fatalf("config subnet: %v", err)
	}
	if !slices.Equal(*asked, []string{"10.42.0.0/24"}) {
		t.Errorf("converged onto %v, want the normalised 10.42.0.0/24", *asked)
	}
	// Normalisation is never silent: the saved value is printed.
	if !strings.Contains(out, "Saved subnet: 10.42.0.0/24") {
		t.Errorf("output = %q", out)
	}
	if cfg, _ := config.Load(); cfg.Subnet != "10.42.0.0/24" {
		t.Errorf("persisted %q", cfg.Subnet)
	}
}

func TestConfigSubnetKeepsTheConfigWhenTheConvergeFails(t *testing.T) {
	stubSubnetStack(t, true, nil, errors.New("Pool overlaps with other one on this address space"))
	if _, err := runConfig(t, "subnet", "10.42.0.0/24"); err == nil || !strings.Contains(err.Error(), "Pool overlaps") {
		t.Fatalf("config subnet = %v, want Docker's error", err)
	}
	if cfg, _ := config.Load(); cfg.Subnet != config.DefaultSubnet {
		t.Errorf("a failed converge persisted %q", cfg.Subnet)
	}
}

func TestConfigSubnetWithTheStackDownOnlySaves(t *testing.T) {
	asked := stubSubnetStack(t, false, nil, nil)
	out, err := runConfig(t, "subnet", "10.42.0.0/24")
	if err != nil {
		t.Fatalf("config subnet: %v", err)
	}
	if len(*asked) != 0 {
		t.Errorf("converged a stopped stack: %v", *asked)
	}
	if !strings.Contains(out, "next proximo up") {
		t.Errorf("output = %q, want when it takes effect", out)
	}
	if cfg, _ := config.Load(); cfg.Subnet != "10.42.0.0/24" {
		t.Errorf("persisted %q", cfg.Subnet)
	}
}

// A stack whose state cannot be read may be running: saving would declare a
// subnet the stack is not on.
func TestConfigSubnetRefusesWhenTheStackCannotBeRead(t *testing.T) {
	asked := stubSubnetStack(t, false, errors.New("permission denied"), nil)
	if _, err := runConfig(t, "subnet", "10.42.0.0/24"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("config subnet = %v, want the status error", err)
	}
	if len(*asked) != 0 {
		t.Errorf("converged %v", *asked)
	}
	if cfg, _ := config.Load(); cfg.Subnet != config.DefaultSubnet {
		t.Errorf("persisted %q", cfg.Subnet)
	}
}

func TestConfigSubnetRefusesAnInvalidValue(t *testing.T) {
	asked := stubSubnetStack(t, true, nil, nil)
	if _, err := runConfig(t, "subnet", "172.18.0.0/16"); err == nil {
		t.Fatal("config subnet accepted Docker's default pool")
	}
	if len(*asked) != 0 {
		t.Errorf("converged an invalid subnet: %v", *asked)
	}
}
