package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"time"
)

// LoadCA loads the CA certificate and private key from PEM files.
func LoadCA(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cert, err := loadCertPEM(certPath)
	if err != nil {
		return nil, nil, err
	}
	key, err := loadKeyPEM(keyPath)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// IssueHostCert generates a leaf certificate covering the exact hosts, signed by
// the CA, and returns it PEM-encoded. Validity stays under 398 days to satisfy
// the Apple/Chrome TLS policy (which rejects longer-lived leaf certs, and also
// rejects TLD-level wildcards like *.test — hence exact SANs, no wildcard).
func IssueHostCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, hosts []string) (certPEM, keyPEM []byte, err error) {
	if len(hosts) == 0 {
		return nil, nil, errors.New("no hosts given")
	}
	return issueLeaf(caCert, caKey, hosts[0], hosts)
}

// IssueDefaultCert generates the certificate Traefik serves when no leaf
// matches the SNI: signed by the CA, with no SAN at all. Naming nothing, it
// cannot tell an unserved name from an invented one, and it names none of the
// machine's routes. The CommonName is not a hostname, so no client that still
// falls back to it can match it either.
func IssueDefaultCert(caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certPEM, keyPEM []byte, err error) {
	return issueLeaf(caCert, caKey, "proximo default certificate", nil)
}

func issueLeaf(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, hosts []string) (certPEM, keyPEM []byte, err error) {
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
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     hosts,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(0, 0, 397),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}
