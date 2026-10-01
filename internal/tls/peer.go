package tls

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
)

// ValidateTeamRoot refuses a team root that could sign outside the Peer
// suffix. It is the one peer value whose rule is checked completely, and the
// one with the largest consequence: proximo installs this certificate into a
// trust store that serves a colleague's real browsing (ADR 0010).
//
// Constraining dNSName alone is not enough — under RFC 5280 a name type absent
// from the permitted subtrees is unrestricted — so every IP range, every email
// address and every URI must be excluded too.
func ValidateTeamRoot(pemBytes []byte, suffix string) error {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("not a PEM certificate")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse certificate: %w", err)
	}
	var missing []string
	if !c.IsCA {
		missing = append(missing, "it is not a CA")
	}
	if !c.PermittedDNSDomainsCritical {
		missing = append(missing, "its name constraints are not marked critical")
	}
	if !permitsSuffix(c.PermittedDNSDomains, suffix) {
		missing = append(missing, fmt.Sprintf("its permitted DNS subtree does not cover %s", suffix))
	}
	if !excludesAll(c.ExcludedIPRanges, "0.0.0.0/0") || !excludesAll(c.ExcludedIPRanges, "::/0") {
		missing = append(missing, "it does not exclude every IP address (0.0.0.0/0 and ::/0)")
	}
	if !slices.Contains(c.ExcludedEmailAddresses, "") {
		missing = append(missing, "it does not exclude every email address")
	}
	if !slices.Contains(c.ExcludedURIDomains, "") {
		missing = append(missing, "it does not exclude every URI")
	}
	if len(missing) > 0 {
		return fmt.Errorf("refused: %s", strings.Join(missing, "; "))
	}
	return nil
}

// permitsSuffix reports whether one permitted DNS subtree covers suffix: the
// suffix itself, or an ancestor of it. Go writes a subdomains-only subtree
// with a leading dot.
func permitsSuffix(permitted []string, suffix string) bool {
	for _, p := range permitted {
		p = strings.TrimPrefix(p, ".")
		if p != "" && (suffix == p || strings.HasSuffix(suffix, "."+p)) {
			return true
		}
	}
	return false
}

func excludesAll(ranges []*net.IPNet, cidr string) bool {
	_, want, _ := net.ParseCIDR(cidr)
	for _, r := range ranges {
		if r.String() == want.String() {
			return true
		}
	}
	return false
}
