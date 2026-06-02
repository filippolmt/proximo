package cli

import (
	"fmt"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/dns"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/platform"
	"github.com/filippolmt/proximo/internal/tls"
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

	fmt.Fprintf(out, "==> Removing host resolver for .%s\n", cfg.TLD)
	if err := dns.RemoveResolver(cfg.TLD); err != nil {
		return err
	}

	fmt.Fprintln(out, "==> Removing CA trust (system + NSS)")
	if err := tls.RemoveNSSTrust(); err != nil {
		return err
	}
	if err := tls.RemoveSystemTrust(); err != nil {
		return err
	}
	if err := tls.Purge(); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nUninstalled. The host has been restored to its prior state.")
	return nil
}
