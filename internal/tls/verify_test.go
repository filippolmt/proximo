package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// selfSigned issues a throwaway certificate and returns its DER and PEM.
func selfSigned(t *testing.T, cn string) (der, pemBytes []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	return der, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// The Linux trust answer is "is the CA in the bundle", not "is the source file
// on disk": comparing the decoded certificate keeps it immune to how
// update-ca-certificates re-wrapped the PEM.
func TestBundleHasFindsTheCAAmongOthers(t *testing.T) {
	caDER, caPEM := selfSigned(t, "proximo local CA")
	_, otherPEM := selfSigned(t, "some other CA")

	bundle := append(append([]byte{}, otherPEM...), caPEM...)
	if !bundleHas(bundle, caDER) {
		t.Error("the CA is in the bundle but was not found")
	}
	if bundleHas(otherPEM, caDER) {
		t.Error("a bundle without the CA reported it as trusted")
	}
	if bundleHas(nil, caDER) {
		t.Error("an empty bundle reported the CA as trusted")
	}
}
