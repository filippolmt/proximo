package cli

import (
	"fmt"
	"io"

	"github.com/filippolmt/proximo/internal/platform"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/spf13/cobra"
)

// Seams for the trust-store writes so the wiring is unit-testable without
// touching the host (mirrors uninstall.go's teardownStack/purgeObservability).
var (
	installSystemTrust = tls.InstallSystemTrust
	installNSSTrust    = tls.InstallNSSTrust
)

func newTrustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trust",
		Short: "(Re)install the local CA into the system and NSS trust stores",
		Long: "trust re-adds proximo's local CA to the OS system trust store and, when\n" +
			"present, the NSS store (Firefox / Chromium). Unlike install it touches\n" +
			"neither DNS nor the Docker stack, so it is safe to run while proximo is\n" +
			"up — use it when a browser stops trusting https://<name>.<tld>, e.g. after\n" +
			"the CA was regenerated or never made it into the store. The trust writes\n" +
			"are idempotent. Fully restart the browser afterwards to pick up the CA.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTrust(cmd)
		},
	}
}

func runTrust(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	// EnsureCA reuses the existing CA and only mints one on a fresh host, so
	// re-trusting never rotates the CA out from under already-issued leaf certs.
	if _, _, err := tls.EnsureCA(); err != nil {
		return err
	}
	if err := platform.SudoPrime("trust the local CA"); err != nil {
		return err
	}
	if err := applyTrust(out, defaultRunner); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nCA trusted. Fully restart your browser to pick it up.")
	return nil
}

// applyTrust installs the CA into the system store then the NSS store, in that
// order, under a single banner — matching the "system + NSS" grouping install
// uses. Unlike install it skips the DNS port check, so it runs with the stack up.
func applyTrust(out io.Writer, r platform.Runner) error {
	fmt.Fprintln(out, "==> Installing CA trust (system + NSS)")
	if err := installSystemTrust(r); err != nil {
		return err
	}
	return installNSSTrust(r)
}
