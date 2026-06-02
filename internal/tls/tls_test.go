package tls

import (
	"crypto/x509"
	"encoding/pem"
	"slices"
	"testing"
	"time"
)

func TestEnsureCAReused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	certPath, _, err := EnsureCA()
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	before, _ := loadCertPEM(certPath)

	if _, _, err := EnsureCA(); err != nil {
		t.Fatalf("EnsureCA (2nd): %v", err)
	}
	after, _ := loadCertPEM(certPath)
	if before.SerialNumber.Cmp(after.SerialNumber) != 0 {
		t.Fatal("CA was regenerated on the second run")
	}
}

func TestIssueHostCertChainsAndMatches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	certPath, keyPath, err := EnsureCA()
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	caCert, caKey, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}

	hosts := []string{"whoami.test", "api.test"}
	certPEM, _, err := IssueHostCert(caCert, caKey, hosts)
	if err != nil {
		t.Fatalf("IssueHostCert: %v", err)
	}

	leaf := parseLeaf(t, certPEM)
	if !slices.Equal(leaf.DNSNames, hosts) {
		t.Fatalf("SANs = %v, want %v", leaf.DNSNames, hosts)
	}

	// Apple/Chrome reject leaf certs valid for more than 398 days.
	if got := leaf.NotAfter.Sub(leaf.NotBefore); got > 398*24*time.Hour {
		t.Fatalf("validity %s exceeds 398 days", got)
	}

	// The leaf must verify against the CA for the exact host.
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "whoami.test", Roots: roots}); err != nil {
		t.Fatalf("leaf does not verify for whoami.test: %v", err)
	}
}

func parseLeaf(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("no PEM block in leaf")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return cert
}
