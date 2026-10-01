package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"strings"
	"testing"
)

// ceremony runs the whole enrolment: a team root, this machine's CSR, and the
// intermediate a custodian signs from it, with edit applied to its template.
func ceremony(t *testing.T, edit func(*x509.Certificate)) (rootPEM, intPEM []byte) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	rootPEM, rootKeyPEM, err := NewTeamRoot("mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, err := MachineCSR("studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	intPEM, err = signIntermediate(rootPEM, rootKeyPEM, csrPEM, "studio-01", "mesh.internal", edit)
	if err != nil {
		t.Fatal(err)
	}
	return rootPEM, intPEM
}

func TestNewTeamRootPassesItsOwnValidation(t *testing.T) {
	root, _, err := NewTeamRoot("mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTeamRoot(root, "mesh.internal"); err != nil {
		t.Fatalf("a root the tool made is refused: %v", err)
	}
}

func TestMachineCSRIsStable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first, err := MachineCSR("studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	again, err := MachineCSR("studio-01", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(again) {
		t.Error("a second run printed a different CSR")
	}
	// A new label is a new request, for the same key.
	renamed, err := MachineCSR("studio-02", "mesh.internal")
	if err != nil {
		t.Fatal(err)
	}
	a, b := parseCSR(t, first), parseCSR(t, renamed)
	if b.Subject.CommonName != "studio-02.mesh.internal" || !a.PublicKey.(*ecdsa.PublicKey).Equal(b.PublicKey) {
		t.Errorf("renamed CSR = %q; the key must not change", b.Subject.CommonName)
	}
}

func parseCSR(t *testing.T, data []byte) *x509.CertificateRequest {
	t.Helper()
	block, _ := pem.Decode(data)
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return csr
}

func TestValidateIntermediateAcceptsTheCeremony(t *testing.T) {
	root, intPEM := ceremony(t, nil)
	if err := ValidateIntermediate(intPEM, root, "studio-01", "mesh.internal"); err != nil {
		t.Fatalf("refused: %v", err)
	}
	// What the constraint is for: a leaf with an IP SAN under it fails.
	ic := parsePEMCert(t, intPEM)
	key, _ := loadKeyPEM(mustMachineKeyPath(t))
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: ic.SerialNumber, NotBefore: ic.NotBefore, NotAfter: ic.NotAfter,
		DNSNames: []string{"app.studio-01.mesh.internal"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ic, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	roots, inter := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(parsePEMCert(t, root))
	inter.AddCert(ic)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter}); err == nil {
		t.Error("a leaf with an IP SAN verified under the intermediate")
	}
}

func TestValidateIntermediateRefuses(t *testing.T) {
	cases := map[string]struct {
		edit func(*x509.Certificate)
		want string
	}{
		// The case the rule exists for: an intermediate that would sign an IP SAN.
		"IP SANs open":    {func(c *x509.Certificate) { c.ExcludedIPRanges = nil }, "every IP address"},
		"wider subtree":   {func(c *x509.Certificate) { c.PermittedDNSDomains = []string{".mesh.internal"} }, "exactly studio-01.mesh.internal"},
		"another machine": {func(c *x509.Certificate) { c.PermittedDNSDomains = []string{".studio-02.mesh.internal"} }, "exactly studio-01.mesh.internal"},
		"path length":     {func(c *x509.Certificate) { c.MaxPathLen, c.MaxPathLenZero = 1, false }, "MaxPathLen"},
		"not critical":    {func(c *x509.Certificate) { c.PermittedDNSDomainsCritical = false }, "critical"},
		"another machine's key": {func(c *x509.Certificate) {
			k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			c.PublicKey = &k.PublicKey
		}, "machine key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root, intPEM := ceremony(t, tc.edit)
			err := ValidateIntermediate(intPEM, root, "studio-01", "mesh.internal")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	t.Run("another root", func(t *testing.T) {
		_, intPEM := ceremony(t, nil)
		other, _, _ := NewTeamRoot("mesh.internal")
		if err := ValidateIntermediate(intPEM, other, "studio-01", "mesh.internal"); err == nil || !strings.Contains(err.Error(), "team root") {
			t.Fatalf("err = %v, want a refusal naming the team root", err)
		}
	})
}

func TestInstallIntermediate(t *testing.T) {
	root, intPEM := ceremony(t, nil)
	if err := ValidateIntermediate(intPEM, root, "studio-01", "mesh.internal"); err != nil {
		t.Fatal(err)
	}
	if err := InstallIntermediate(intPEM); err != nil {
		t.Fatal(err)
	}
	got, err := Intermediate()
	if err != nil || got == nil || !got.Equal(parsePEMCert(t, intPEM)) {
		t.Fatalf("Intermediate() = %v, %v", got, err)
	}
}

func parsePEMCert(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(data)
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustMachineKeyPath(t *testing.T) string {
	t.Helper()
	p, err := pathIn(machineKeyName)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
