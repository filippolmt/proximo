package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func teamRootPEM(t *testing.T, edit func(*x509.Certificate)) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, v4, _ := net.ParseCIDR("0.0.0.0/0")
	_, v6, _ := net.ParseCIDR("::/0")
	tmpl := &x509.Certificate{
		SerialNumber:                big.NewInt(1),
		Subject:                     pkix.Name{CommonName: "team root"},
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
	if edit != nil {
		edit(tmpl)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestValidateTeamRootAcceptsAFullyConstrainedRoot(t *testing.T) {
	if err := ValidateTeamRoot(teamRootPEM(t, nil), "mesh.internal"); err != nil {
		t.Fatalf("refused: %v", err)
	}
	// DNS names compare case-insensitively.
	mixed := teamRootPEM(t, func(c *x509.Certificate) { c.PermittedDNSDomains = []string{".Mesh.Internal"} })
	if err := ValidateTeamRoot(mixed, "mesh.internal"); err != nil {
		t.Fatalf("mixed case refused: %v", err)
	}
}

func TestValidateTeamRootRefuses(t *testing.T) {
	cases := map[string]struct {
		edit   func(*x509.Certificate)
		suffix string
		want   string
	}{
		// The case the rule exists for: a DNS-only constraint leaves IP SANs open.
		"dNSName only": {func(c *x509.Certificate) {
			c.ExcludedIPRanges, c.ExcludedEmailAddresses, c.ExcludedURIDomains = nil, nil, nil
		}, "mesh.internal", "every IP address"},
		"not critical":     {func(c *x509.Certificate) { c.PermittedDNSDomainsCritical = false }, "mesh.internal", "critical"},
		"other suffix":     {nil, "other.internal", "does not cover other.internal"},
		"unconstrained CA": {func(c *x509.Certificate) { c.PermittedDNSDomains = nil }, "mesh.internal", "does not cover"},
		"not a CA":         {func(c *x509.Certificate) { c.IsCA = false }, "mesh.internal", "not a CA"},
		"emails open":      {func(c *x509.Certificate) { c.ExcludedEmailAddresses = nil }, "mesh.internal", "every email address"},
		"URIs open":        {func(c *x509.Certificate) { c.ExcludedURIDomains = nil }, "mesh.internal", "every URI"},
		// Covering the suffix is not enough: the root may sign nothing outside it.
		"ancestor subtree": {func(c *x509.Certificate) { c.PermittedDNSDomains = []string{"internal"} }, "mesh.internal", "outside mesh.internal"},
		"extra subtree": {func(c *x509.Certificate) {
			c.PermittedDNSDomains = []string{".mesh.internal", "example.com"}
		}, "mesh.internal", "outside mesh.internal"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateTeamRoot(teamRootPEM(t, tc.edit), tc.suffix)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if ValidateTeamRoot([]byte("nope"), "mesh.internal") == nil {
		t.Error("non-PEM accepted")
	}
}

// The local CA is removed from the macOS keychain by common name, which
// matches a substring: a team root whose name contains it could go with it.
func TestValidateTeamRootRefusesTheLocalCAName(t *testing.T) {
	root := teamRootPEM(t, func(c *x509.Certificate) { c.Subject.CommonName = "acme " + caCommonName })
	if err := ValidateTeamRoot(root, "mesh.internal"); err == nil || !strings.Contains(err.Error(), caCommonName) {
		t.Fatalf("err = %v, want a refusal naming %q", err, caCommonName)
	}
}

// The team root is removed from the macOS keychain by its SHA-1, which selects
// exactly one certificate, never by a name another anchor could share.
func TestTeamRootFingerprintIsItsSHA1(t *testing.T) {
	root := teamRootPEM(t, nil)
	path := filepath.Join(t.TempDir(), "root.crt")
	if err := os.WriteFile(path, root, 0o644); err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(root)
	got, err := TeamRootFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%X", sha1.Sum(block.Bytes)); got != want {
		t.Fatalf("fingerprint = %s, want %s", got, want)
	}
}

type recordRunner struct{ calls []string }

func (r *recordRunner) Run(name string, args ...string) error {
	r.calls = append(r.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return nil
}
func (r *recordRunner) Sudo(args ...string) error {
	r.calls = append(r.calls, "sudo "+strings.Join(args, " "))
	return nil
}
func (r *recordRunner) WriteFilePrivileged(path string, _ []byte, _ os.FileMode) error {
	r.calls = append(r.calls, "write "+path)
	return nil
}
func (r *recordRunner) RemoveFilePrivileged(path string) error {
	r.calls = append(r.calls, "remove "+path)
	return nil
}

// On Linux the team root has a trust file of its own, so neither anchor's
// install or removal touches the other's.
func TestTeamRootSystemTrustOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Linux trust path")
	}
	path := filepath.Join(t.TempDir(), "root.crt")
	if err := os.WriteFile(path, teamRootPEM(t, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recordRunner{}
	if err := installTeamRootSystemTrust(r, path); err != nil {
		t.Fatal(err)
	}
	if err := removeTeamRootSystemTrust(r, "ABCD"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"write " + linuxTeamRootPath, "sudo update-ca-certificates",
		"remove " + linuxTeamRootPath, "sudo update-ca-certificates --fresh",
	}
	if !slices.Equal(r.calls, want) || linuxTeamRootPath == linuxTrustPath {
		t.Fatalf("calls = %v, want %v", r.calls, want)
	}
}
