// Package tls generates the local CA and wildcard certificate (via
// crypto/x509) and installs/removes trust in the system and NSS stores.
package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"

	"github.com/filippolmt/proximo/internal/config"
)

// caCommonName is the subject/nickname used for the local CA across all stores.
const caCommonName = "proximo local CA"

// Dir returns the directory holding the CA and certificate material.
func Dir() (string, error) {
	return config.SubDir("tls")
}

func pathIn(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// CACertPath is the path to the local CA certificate (PEM).
func CACertPath() (string, error) { return pathIn("ca.pem") }

// CAKeyPath is the path to the local CA private key (PEM).
func CAKeyPath() (string, error) { return pathIn("ca-key.pem") }

// CertPath is the path to the wildcard leaf certificate (PEM).
func CertPath() (string, error) { return pathIn("cert.pem") }

// KeyPath is the path to the wildcard leaf private key (PEM).
func KeyPath() (string, error) { return pathIn("key.pem") }

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Purge deletes the local CA and certificate material from disk.
func Purge() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	for _, name := range []string{"ca.pem", "ca-key.pem", "cert.pem", "key.pem"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	return os.WriteFile(path, data, mode)
}

func writeKeyPEM(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(path, "PRIVATE KEY", der, 0o600)
}

// issue generates a fresh P-256 key, stamps the template with a random serial,
// creates the certificate, and writes both PEM files. A nil parent/signer means
// the certificate is self-signed with the generated key (used for the CA).
func issue(tmpl, parent *x509.Certificate, signer *ecdsa.PrivateKey, certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	tmpl.SerialNumber = serial
	if parent == nil {
		parent = tmpl
	}
	if signer == nil {
		signer = key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signer)
	if err != nil {
		return err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	return writeKeyPEM(keyPath, key)
}

func loadCertPEM(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	return x509.ParseCertificate(block.Bytes)
}

func loadKeyPEM(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("unexpected key type")
	}
	return ec, nil
}
