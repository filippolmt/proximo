package tls

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/filippolmt/proximo/internal/platform"
)

// InstallNSSTrust adds the local CA to the NSS trust databases used by Firefox
// and Chrome-on-Linux. When certutil is missing it installs nss-tools first.
// certutil invocations go through the injected Runner; certutil discovery and
// bootstrap stay on the package helpers.
func InstallNSSTrust(r platform.Runner) error {
	if err := ensureCertutil(); err != nil {
		return err
	}
	caPath, err := CACertPath()
	if err != nil {
		return err
	}
	dbs := nssDatabases()
	if len(dbs) == 0 {
		fmt.Fprintln(os.Stderr, "proximo: no NSS databases found (Firefox/Chrome); skipping NSS trust")
		return nil
	}
	for _, db := range dbs {
		// Remove any stale entry first so re-runs stay idempotent.
		_ = r.Run("certutil", "-D", "-d", "sql:"+db, "-n", caCommonName)
		if err := r.Run("certutil", "-A", "-d", "sql:"+db,
			"-t", "C,,", "-n", caCommonName, "-i", caPath); err != nil {
			fmt.Fprintf(os.Stderr, "proximo: warning: could not add CA to NSS db %s: %v\n", db, err)
		}
	}
	return nil
}

// RemoveNSSTrust removes the local CA from all discovered NSS databases.
func RemoveNSSTrust(r platform.Runner) error {
	if !platform.Has("certutil") {
		return nil
	}
	for _, db := range nssDatabases() {
		_ = r.Run("certutil", "-D", "-d", "sql:"+db, "-n", caCommonName)
	}
	return nil
}

func ensureCertutil() error {
	if platform.Has("certutil") {
		return nil
	}
	pm, err := platform.DetectPackageManager()
	if err != nil {
		return err
	}
	switch pm {
	case platform.Brew:
		if err := platform.Run("brew", "install", "nss"); err != nil {
			return err
		}
	case platform.Apt:
		if err := platform.InstallPackage("libnss3-tools"); err != nil {
			return err
		}
	}
	if !platform.Has("certutil") {
		return fmt.Errorf("certutil still not available after installing nss-tools")
	}
	return nil
}

// nssDatabases discovers NSS databases for the current user's browsers.
func nssDatabases() []string {
	var dbs []string
	home, err := os.UserHomeDir()
	if err != nil {
		return dbs
	}
	osType, err := platform.Current()
	if err != nil {
		return dbs
	}

	// Chromium/Chrome shared NSS DB (Linux). Create it if missing so trust can
	// be installed before the browser first runs.
	if osType == platform.Linux {
		pki := filepath.Join(home, ".pki", "nssdb")
		if !hasNSSDB(pki) && platform.Has("certutil") {
			if err := os.MkdirAll(pki, 0o755); err == nil {
				_ = platform.Run("certutil", "-N", "-d", "sql:"+pki, "--empty-password")
			}
		}
		if hasNSSDB(pki) {
			dbs = append(dbs, pki)
		}
	}

	var globs []string
	switch osType {
	case platform.MacOS:
		globs = []string{filepath.Join(home, "Library", "Application Support", "Firefox", "Profiles", "*")}
	case platform.Linux:
		globs = []string{
			filepath.Join(home, ".mozilla", "firefox", "*"),
			filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox", "*"),
		}
	}
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			if hasNSSDB(m) {
				dbs = append(dbs, m)
			}
		}
	}
	return dbs
}

func hasNSSDB(dir string) bool {
	for _, f := range []string{"cert9.db", "cert8.db"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}
