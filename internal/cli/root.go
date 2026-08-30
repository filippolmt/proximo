// Package cli implements the proximo command surface.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "proximo",
		Short: "Local dev reverse proxy with automatic DNS and trusted HTTPS",
		Long: "proximo makes any running Docker container reachable at " +
			"https://<name>.<tld> with zero per-container port publishing and " +
			"zero hosts-file edits, on macOS and Linux.",
		SilenceUsage: true,
		// Execute prints the error itself, with a "error:" prefix; without this
		// cobra prints it too, which doubles every multi-line error (a converge
		// failure carries a Remedy on its own lines).
		SilenceErrors: true,
	}
	root.AddCommand(
		newInstallCmd(),
		newUpCmd(),
		newDownCmd(),
		newUpdateCmd(),
		newTrustCmd(),
		newStatusCmd(),
		newDoctorCmd(),
		newErrorsCmd(),
		newConfigCmd(),
		newUninstallCmd(),
		newVersionCmd(),
	)
	return root
}

// Execute runs the root command, exiting non-zero on error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
