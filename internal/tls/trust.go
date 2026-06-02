package tls

import (
	"os"

	"github.com/filippolmt/proximo/internal/platform"
)

const linuxTrustPath = "/usr/local/share/ca-certificates/proximo-local-ca.crt"
const macSystemKeychain = "/Library/Keychains/System.keychain"

// InstallSystemTrust adds the local CA to the OS system trust store using
// built-in OS tooling. Privileged operations go through the injected Runner so
// the install step is testable.
func InstallSystemTrust(r platform.Runner) error {
	caPath, err := CACertPath()
	if err != nil {
		return err
	}
	return platform.Dispatch(
		func() error {
			return r.Sudo("security", "add-trusted-cert", "-d",
				"-r", "trustRoot", "-k", macSystemKeychain, caPath)
		},
		func() error {
			data, err := os.ReadFile(caPath)
			if err != nil {
				return err
			}
			if err := r.WriteFilePrivileged(linuxTrustPath, data, 0o644); err != nil {
				return err
			}
			return r.Sudo("update-ca-certificates")
		},
	)
}

// RemoveSystemTrust removes the local CA from the OS system trust store.
func RemoveSystemTrust(r platform.Runner) error {
	return platform.Dispatch(
		// Best-effort: the certificate may already be gone.
		func() error {
			return r.Sudo("security", "delete-certificate", "-c", caCommonName, macSystemKeychain)
		},
		func() error {
			if err := r.RemoveFilePrivileged(linuxTrustPath); err != nil {
				return err
			}
			return r.Sudo("update-ca-certificates", "--fresh")
		},
	)
}
