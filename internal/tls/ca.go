package tls

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

// EnsureCA generates a local CA on first use and reuses an existing one on
// subsequent runs. It returns the CA certificate and key file paths.
func EnsureCA() (certPath, keyPath string, err error) {
	certPath, err = CACertPath()
	if err != nil {
		return "", "", err
	}
	keyPath, err = CAKeyPath()
	if err != nil {
		return "", "", err
	}
	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		Subject: pkix.Name{
			CommonName:   caCommonName,
			Organization: []string{"proximo local development"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	if err := issue(tmpl, nil, nil, certPath, keyPath); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}
