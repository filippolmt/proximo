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
	covered, outside := permittedSubtrees(c.PermittedDNSDomains, suffix)
	if !covered {
		missing = append(missing, fmt.Sprintf("its permitted DNS subtree does not cover %s", suffix))
	}
	if len(outside) > 0 {
		missing = append(missing, fmt.Sprintf("it permits %s, outside %s", strings.Join(outside, ", "), suffix))
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

// permittedSubtrees reports whether one permitted DNS subtree is the suffix
// itself, and lists every one that is not at or beneath it. Covering the suffix
// is not enough: an ancestor, or a second unrelated subtree, would let the root
// sign outside it (constraint 7 of docs/specs/peer-sharing.md). Go writes a subdomains-only subtree with a
// leading dot.
func permittedSubtrees(permitted []string, suffix string) (covered bool, outside []string) {
	for _, raw := range permitted {
		p := strings.ToLower(strings.TrimPrefix(raw, "."))
		switch {
		case p == suffix:
			covered = true
		case !strings.HasSuffix(p, "."+suffix):
			outside = append(outside, raw)
		}
	}
	return covered, outside
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
