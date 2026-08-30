package tls

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"os"

	"github.com/filippolmt/proximo/internal/platform"
)

// linuxBundlePath is the concatenated trust bundle update-ca-certificates
// rebuilds. The CA is trusted on Linux when it is in there, not merely when the
// source file exists under /usr/local/share/ca-certificates.
const linuxBundlePath = "/etc/ssl/certs/ca-certificates.crt"

// ErrNoCertutil reports that the NSS tooling is absent, so the browser trust
// stores cannot be read at all.
var ErrNoCertutil = errors.New("certutil is not installed")

// SystemTrusted reports whether the local CA is in the OS system trust store.
// It only reads — no elevation, no repair — because a diagnosis that asks for a
// password is one nobody runs at the moment they need it most.
func SystemTrusted(ctx context.Context) (bool, error) {
	caPath, err := CACertLocation()
	if err != nil {
		return false, err
	}
	ca, err := loadCertPEM(caPath)
	if err != nil {
		return false, err
	}

	var trusted bool
	err = platform.Dispatch(
		func() error {
			// -a -p prints every matching certificate as PEM, so the answer is
			// "this CA is trusted" rather than "something with this name is".
			// The difference is a regenerated CA whose old namesake is still in
			// the keychain: the browser rejects it, and a name match would call
			// that trusted — the exact failure the check exists to catch.
			// A non-zero exit means no match, which is the answer, not an error.
			out, err := platform.OutputContext(ctx, "security", "find-certificate",
				"-c", caCommonName, "-a", "-p", macSystemKeychain)
			trusted = err == nil && bundleHas([]byte(out), ca.Raw)
			return nil
		},
		func() error {
			bundle, err := os.ReadFile(linuxBundlePath)
			if err != nil {
				return err
			}
			trusted = bundleHas(bundle, ca.Raw)
			return nil
		},
	)
	return trusted, err
}

// bundleHas reports whether a PEM bundle contains a certificate with exactly
// this DER. Comparing the decoded certificate rather than the PEM text keeps
// the answer immune to how update-ca-certificates re-wrapped it.
func bundleHas(bundle, der []byte) bool {
	for block, rest := pem.Decode(bundle); block != nil; block, rest = pem.Decode(rest) {
		if block.Type == "CERTIFICATE" && bytes.Equal(block.Bytes, der) {
			return true
		}
	}
	return false
}

// NSSTrusted reports how many of the NSS databases on this host hold the local
// CA, out of how many were found. Zero databases is not a failure: a machine
// with no Firefox or Chrome profile has nothing to answer with.
func NSSTrusted(ctx context.Context) (found, total int, err error) {
	// Enumerate first: a host with no browser profile has nothing to answer
	// with whether or not the NSS tooling is installed, and the two must give
	// the same answer.
	dbs := nssDatabases(false)
	if len(dbs) == 0 {
		return 0, 0, nil
	}
	if !platform.Has("certutil") {
		return 0, len(dbs), ErrNoCertutil
	}

	caPath, err := CACertLocation()
	if err != nil {
		return 0, len(dbs), err
	}
	ca, err := loadCertPEM(caPath)
	if err != nil {
		return 0, len(dbs), err
	}
	for _, db := range dbs {
		// -a prints the entry as PEM, so a stale certificate stored under the
		// same nickname is not mistaken for this CA.
		out, err := platform.OutputContext(ctx, "certutil", "-L",
			"-d", "sql:"+db, "-n", caCommonName, "-a")
		if err == nil && bundleHas([]byte(out), ca.Raw) {
			found++
		}
	}
	return found, len(dbs), nil
}

// CertutilRemedy is the command that installs the NSS tooling, so a host that
// cannot even be asked about browser trust has a first step.
//
// It answers per OS rather than per detected package manager: the host that
// needs this remedy is precisely the one where no package manager was found,
// and that host must not be told to run Homebrew because it is not a Mac.
func CertutilRemedy() string {
	remedy, _ := platform.Pick("brew install nss", "sudo apt-get install -y libnss3-tools")
	return remedy
}

// CertutilInstallable reports whether browser trust can be installed at all:
// the tooling is already here, or a package manager proximo supports can fetch
// it. Reading it before `install` mutates anything is what keeps a host that
// can never finish the trust step from being changed at all.
func CertutilInstallable() bool {
	if platform.Has("certutil") {
		return true
	}
	_, err := platform.DetectPackageManager()
	return err == nil
}
