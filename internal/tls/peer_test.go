package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
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
