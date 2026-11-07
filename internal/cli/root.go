// Package cli implements the proximo command surface.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
		// Cobra skips this for help, so reading the help under sudo still works.
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return refuseUnderSudo(os.Geteuid(), os.Getenv, os.Args[1:])
		},
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
		newSkillCmd(),
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

// refuseUnderSudo stops a command run through sudo. proximo asks for sudo
// itself for each privileged write; under sudo HOME is root's on Ubuntu, so the
// command reads and writes root's state instead of the developer's. Root
// without sudo has no SUDO_USER and one home, so it is left alone.
func refuseUnderSudo(euid int, getenv func(string) string, args []string) error {
	if euid != 0 || getenv("SUDO_USER") == "" || getenv("SUDO_USER") == "root" {
		return nil
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return errors.New("proximo must not run under sudo: it asks for sudo itself, " +
		"and under sudo it would use root's configuration instead of yours.\n" +
		"Run it as yourself:\n  " + strings.TrimSpace("proximo "+strings.Join(quoted, " ")))
}

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@,+%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
