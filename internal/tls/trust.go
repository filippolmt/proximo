package tls

import (
	"crypto/sha1"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"github.com/filippolmt/proximo/internal/platform"
)

const linuxTrustPath = "/usr/local/share/ca-certificates/proximo-local-ca.crt"
const macSystemKeychain = "/Library/Keychains/System.keychain"

// The team root is a second anchor beside the local CA, installed and removed
// under names of its own, so removing one anchor can never remove the other
// (ADR 0010): its own Linux trust file, its own NSS nickname, and on macOS a
// deletion by SHA-1 rather than by a common name the team chose.
const (
	linuxTeamRootPath = "/usr/local/share/ca-certificates/proximo-team-root.crt"
	teamRootNickname  = "proximo team root"
)

// InstallSystemTrust adds the local CA to the OS system trust store using
// built-in OS tooling. Privileged operations go through the injected Runner so
// the install step is testable.
func InstallSystemTrust(r platform.Runner) error {
	caPath, err := CACertPath()
	if err != nil {
		return err
	}
	return installSystemAnchor(r, caPath, linuxTrustPath)
}

// RemoveSystemTrust removes the local CA from the OS system trust store.
func RemoveSystemTrust(r platform.Runner) error {
	return removeSystemAnchor(r, []string{"-c", caCommonName}, linuxTrustPath)
}

// InstallTeamRootTrust adds the team root at path to the system and NSS
// stores, beside the local CA.
func InstallTeamRootTrust(r platform.Runner, path string) error {
	if err := installTeamRootSystemTrust(r, path); err != nil {
		return err
	}
	return installNSSAnchor(r, path, teamRootNickname, "team root")
}

// RemoveTeamRootTrust removes the team root that was installed with the given
// fingerprint from the system and NSS stores. It needs no file: the one
// configured now may have been replaced or deleted since it was trusted.
func RemoveTeamRootTrust(r platform.Runner, fingerprint string) error {
	return errors.Join(removeTeamRootSystemTrust(r, fingerprint), removeNSSAnchor(r, teamRootNickname))
}

// TeamRootFingerprint is the SHA-1 of the team root's DER, the selector
// `security delete-certificate -Z` takes: it names exactly one certificate,
// where -c matches a substring of a common name the team chose.
func TeamRootFingerprint(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("the team root is not a PEM certificate")
	}
	return fmt.Sprintf("%X", sha1.Sum(block.Bytes)), nil
}

func installTeamRootSystemTrust(r platform.Runner, path string) error {
	return installSystemAnchor(r, path, linuxTeamRootPath)
}

func removeTeamRootSystemTrust(r platform.Runner, fingerprint string) error {
	return removeSystemAnchor(r, []string{"-Z", fingerprint}, linuxTeamRootPath)
}

func installSystemAnchor(r platform.Runner, path, linuxPath string) error {
	return platform.Dispatch(
		func() error {
			return r.Sudo("security", "add-trusted-cert", "-d",
				"-r", "trustRoot", "-k", macSystemKeychain, path)
		},
		func() error {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := r.WriteFilePrivileged(linuxPath, data, 0o644); err != nil {
				return err
			}
			return r.Sudo("update-ca-certificates")
		},
	)
}

func removeSystemAnchor(r platform.Runner, macSelector []string, linuxPath string) error {
	return platform.Dispatch(
		// Best-effort: the certificate may already be gone.
		func() error {
			return r.Sudo(append(append([]string{"security", "delete-certificate"}, macSelector...), macSystemKeychain)...)
		},
		func() error {
			if err := r.RemoveFilePrivileged(linuxPath); err != nil {
				return err
			}
			return r.Sudo("update-ca-certificates", "--fresh")
		},
	)
}
