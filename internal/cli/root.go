// Package cli implements the proximo command surface.
package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
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
	root := newRootCmd()
	// Before cobra parses anything, so a bad flag or argument under sudo is
	// still answered with the refusal.
	err := refuseUnderSudo(os.Geteuid(), os.Getenv, os.Args[1:], asksForHelp(root, os.Args[1:]))
	if err == nil {
		err = root.Execute()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// refuseUnderSudo stops a command run through sudo. proximo asks for sudo
// itself for each privileged write; under sudo HOME is root's on Ubuntu, so the
// command reads and writes root's state instead of the developer's. Root
// without sudo has no SUDO_USER and one home, so it is left alone, and so is
// reading the help, which writes nothing.
func refuseUnderSudo(euid int, getenv func(string) string, args []string, help bool) error {
	user := getenv("SUDO_USER")
	if euid != 0 || user == "" || user == "root" || help {
		return nil
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return errors.New("proximo must not run under sudo: it asks for sudo itself, " +
		"and under sudo it would use root's configuration instead of yours\n" +
		"    Remedy: " + strings.TrimSpace("proximo "+strings.Join(quoted, " ")))
}

// asksForHelp reports whether args only ask cobra for help: the help command,
// or -h/--help on any command.
func asksForHelp(root *cobra.Command, args []string) bool {
	root.InitDefaultHelpCmd() // cobra adds `help` only inside Execute
	if cmd, _, err := root.Find(args); err == nil && cmd.Name() == "help" {
		return true
	}
	return slices.ContainsFunc(args, func(a string) bool { return a == "-h" || a == "--help" })
}

// shellSafe holds the characters a shell word can carry unquoted.
const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@,+%"

func shellQuote(s string) string {
	if s != "" && strings.Trim(s, shellSafe) == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
