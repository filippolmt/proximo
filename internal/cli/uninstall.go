package cli

import (
	"fmt"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/platform"
	"github.com/spf13/cobra"
)

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Reverse all host changes (resolver + CA trust) and tear down the stack",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd)
		},
	}
}

func runUninstall(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if err := platform.SudoPrime("remove the host resolver and untrust the local CA"); err != nil {
		return err
	}

	fmt.Fprintln(out, "==> Stopping the proximo stack")
	if err := docker.Down(); err != nil {
		return err
	}

	if err := revertSteps(out, hostSteps(defaultRunner, cfg.TLD)); err != nil {
		return err
	}

	// Remove the state home last: the trust reversal above reads the CA from it,
	// and the stack is already down so the data bind mounts are released.
	fmt.Fprintln(out, "==> Removing the proximo state home")
	if err := config.RemoveHome(); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nUninstalled. The host has been restored to its prior state.")
	return nil
}
