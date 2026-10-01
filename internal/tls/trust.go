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
	return removeSystemAnchor(r, func() ([]string, error) { return []string{"-c", caCommonName}, nil }, linuxTrustPath)
}

// InstallTeamRootTrust adds the team root at path to the system and NSS
// stores, beside the local CA.
func InstallTeamRootTrust(r platform.Runner, path string) error {
	if err := installTeamRootSystemTrust(r, path); err != nil {
		return err
	}
	return installNSSAnchor(r, path, teamRootNickname)
}

// RemoveTeamRootTrust removes the team root at path from the system and NSS
// stores. The file is read on macOS only, to select the certificate by hash.
func RemoveTeamRootTrust(r platform.Runner, path string) error {
	if err := removeTeamRootSystemTrust(r, path); err != nil {
		return err
	}
	return removeNSSAnchor(r, teamRootNickname)
}

func installTeamRootSystemTrust(r platform.Runner, path string) error {
	return installSystemAnchor(r, path, linuxTeamRootPath)
}

func removeTeamRootSystemTrust(r platform.Runner, path string) error {
	return removeSystemAnchor(r, func() ([]string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("select the team root to remove: %w", err)
		}
		return teamRootMacSelector(data)
	}, linuxTeamRootPath)
}

// teamRootMacSelector selects the team root in the keychain by SHA-1 of its
// DER: `security delete-certificate -c` matches a substring of the common name.
func teamRootMacSelector(pemBytes []byte) ([]string, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("the team root is not a PEM certificate")
	}
	return []string{"-Z", fmt.Sprintf("%X", sha1.Sum(block.Bytes))}, nil
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

func removeSystemAnchor(r platform.Runner, macSelector func() ([]string, error), linuxPath string) error {
	return platform.Dispatch(
		// Best-effort: the certificate may already be gone.
		func() error {
			sel, err := macSelector()
			if err != nil {
				return err
			}
			return r.Sudo(append(append([]string{"security", "delete-certificate"}, sel...), macSystemKeychain)...)
		},
		func() error {
			if err := r.RemoveFilePrivileged(linuxPath); err != nil {
				return err
			}
			return r.Sudo("update-ca-certificates", "--fresh")
		},
	)
}
