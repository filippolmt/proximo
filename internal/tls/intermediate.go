package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// The machine's peer-sharing material in the TLS state directory: the key that
// never leaves the machine, the CSR printed from it, and the intermediate a
// custodian signed from that CSR.
const (
	machineKeyName   = "machine-key.pem"
	machineCSRName   = "machine.csr"
	intermediateName = "intermediate.pem"
)

// MachineSubtree is the peer subtree one machine answers and its intermediate
// is constrained to: <machine>.<suffix>.
func MachineSubtree(machine, suffix string) string { return machine + "." + suffix }

// MachineCSR returns the certificate signing request for this machine's
// intermediate, for its MachineSubtree. The machine key is created only if
// none exists, and never replaced. The CSR is kept, so a second run prints the
// same bytes; a new label or suffix is a new request for the same key.
func MachineCSR(machine, suffix string) ([]byte, error) {
	keyPath, err := pathIn(machineKeyName)
	if err != nil {
		return nil, err
	}
	csrPath, err := pathIn(machineCSRName)
	if err != nil {
		return nil, err
	}
	if !fileExists(keyPath) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		if err := writeKeyPEM(keyPath, key); err != nil {
			return nil, err
		}
	}
	key, err := loadKeyPEM(keyPath)
	if err != nil {
		return nil, err
	}
	subtree := MachineSubtree(machine, suffix)
	if data, err := os.ReadFile(csrPath); err == nil {
		if block, _ := pem.Decode(data); block != nil {
			if csr, err := x509.ParseCertificateRequest(block.Bytes); err == nil &&
				csr.Subject.CommonName == subtree && key.PublicKey.Equal(csr.PublicKey) {
				return data, nil
			}
		}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: subtree},
	}, key)
	if err != nil {
		return nil, err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	if err := os.WriteFile(csrPath, data, 0o644); err != nil {
		return nil, err
	}
	return data, nil
}

// NewTeamRoot mints a team root for suffix: ten years, name-constrained
// critically to the suffix, excluding every IP, email address and URI — the
// shape ValidateTeamRoot accepts, in the encoding the ADR 0010 proof verified
// on every platform. The key is returned unencrypted for the custodian to
// encrypt; nothing here stores it.
func NewTeamRoot(suffix string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "proximo team root " + suffix},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	constrain(tmpl, suffix)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// SignIntermediate is the custodian's half of the ceremony: it signs the
// machine's CSR with the team root into an intermediate for <machine>.<suffix>
// — five years, MaxPathLen 0, constrained like the root.
func SignIntermediate(rootPEM, rootKeyPEM, csrPEM []byte, machine, suffix string) ([]byte, error) {
	return signIntermediate(rootPEM, rootKeyPEM, csrPEM, machine, suffix, nil)
}

// signIntermediate applies edit to the template before signing, so tests can
// mint the faulty intermediates ValidateIntermediate exists to refuse.
func signIntermediate(rootPEM, rootKeyPEM, csrPEM []byte, machine, suffix string, edit func(*x509.Certificate)) ([]byte, error) {
	root, err := parseCertPEM(rootPEM)
	if err != nil {
		return nil, fmt.Errorf("team root: %w", err)
	}
	block, _ := pem.Decode(rootKeyPEM)
	if block == nil {
		return nil, errors.New("team root key: not PEM")
	}
	rootKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("team root key: %w", err)
	}
	block, _ = pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("CSR: not a PEM certificate request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR: %w", err)
	}
	subtree := MachineSubtree(machine, suffix)
	if csr.Subject.CommonName != subtree {
		return nil, fmt.Errorf("CSR is for %q, not %q", csr.Subject.CommonName, subtree)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: subtree},
		PublicKey:             csr.PublicKey,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	constrain(tmpl, subtree)
	if edit != nil {
		edit(tmpl)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, root, tmpl.PublicKey, rootKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// constrain permits the subdomains of subtree, critically, and excludes every
// other name type: under RFC 5280 a type absent from the permitted subtrees is
// unrestricted.
func constrain(c *x509.Certificate, subtree string) {
	_, v4, _ := net.ParseCIDR("0.0.0.0/0")
	_, v6, _ := net.ParseCIDR("::/0")
	c.PermittedDNSDomains = []string{"." + subtree}
	c.PermittedDNSDomainsCritical = true
	c.ExcludedIPRanges = []*net.IPNet{v4, v6}
	c.ExcludedEmailAddresses = []string{""}
	c.ExcludedURIDomains = []string{""}
}

// ValidateIntermediate refuses an intermediate this machine must not sign
// with: one that does not chain to the team root, whose permitted subtree is
// not exactly <machine>.<suffix>, whose MaxPathLen is not 0, whose name
// constraints leave a name type open, or that is not for the machine key.
func ValidateIntermediate(intPEM, rootPEM []byte, machine, suffix string) error {
	c, err := parseCertPEM(intPEM)
	if err != nil {
		return err
	}
	root, err := parseCertPEM(rootPEM)
	if err != nil {
		return fmt.Errorf("team root: %w", err)
	}
	subtree := MachineSubtree(machine, suffix)
	var faults []string
	// The team root signs every intermediate directly, so the chain is one
	// signature; checking it apart from the validity window keeps "expired"
	// from reading as "not ours".
	if err := c.CheckSignatureFrom(root); err != nil {
		faults = append(faults, "it is not signed by the configured team root")
	}
	if now := time.Now(); now.After(c.NotAfter) {
		faults = append(faults, fmt.Sprintf("it expired on %s", c.NotAfter.Format("2006-01-02")))
	} else if now.Before(c.NotBefore) {
		faults = append(faults, fmt.Sprintf("it is not valid before %s", c.NotBefore.Format("2006-01-02")))
	}
	if len(c.IPAddresses)+len(c.EmailAddresses)+len(c.URIs) > 0 {
		faults = append(faults, "it carries an IP address, email or URI name, outside every peer name")
	}
	if !c.IsCA {
		faults = append(faults, "it is not a CA")
	}
	if len(c.PermittedDNSDomains) != 1 || strings.ToLower(strings.TrimPrefix(c.PermittedDNSDomains[0], ".")) != subtree {
		faults = append(faults, fmt.Sprintf("its permitted DNS subtree is %v, not exactly %s", c.PermittedDNSDomains, subtree))
	}
	if !c.BasicConstraintsValid || c.MaxPathLen != 0 || !c.MaxPathLenZero {
		faults = append(faults, "its MaxPathLen is not 0, so this machine could mint a further CA")
	}
	faults = append(faults, nameTypeFaults(c)...)
	keyPath, err := pathIn(machineKeyName)
	if err != nil {
		return err
	}
	key, err := loadKeyPEM(keyPath)
	if err != nil {
		faults = append(faults, "this machine has no machine key: run proximo config csr first")
	} else if !key.PublicKey.Equal(c.PublicKey) {
		faults = append(faults, "it is not for this machine key")
	}
	if len(faults) > 0 {
		return fmt.Errorf("refused: %s", strings.Join(faults, "; "))
	}
	return nil
}

// InstallIntermediate stores a validated intermediate beside the machine key.
func InstallIntermediate(intPEM []byte) error {
	path, err := pathIn(intermediateName)
	if err != nil {
		return err
	}
	return os.WriteFile(path, intPEM, 0o644)
}

// IntermediateFor reports the subtree the installed intermediate is
// constrained to, and whether that is this machine's MachineSubtree. With no
// intermediate installed it returns "", false.
func IntermediateFor(machine, suffix string) (subtree string, ok bool) {
	c, err := Intermediate()
	if err != nil || c == nil || len(c.PermittedDNSDomains) != 1 {
		return "", false
	}
	subtree = strings.ToLower(strings.TrimPrefix(c.PermittedDNSDomains[0], "."))
	return subtree, subtree == MachineSubtree(machine, suffix)
}

// Intermediate returns the installed intermediate, or nil when there is none.
func Intermediate() (*x509.Certificate, error) {
	path, err := locate(intermediateName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseCertPEM(data)
}
