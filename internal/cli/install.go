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

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Preflight, generate CA/cert, configure the host resolver, install trust, and start the stack",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInstall(cmd)
		},
	}
}

func runInstall(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "==> Preflight checks")
	if err := preflight(); err != nil {
		return err
	}
	if err := dns.CheckPortFree(); err != nil {
		return err
	}

	if err := platform.SudoPrime("configure the host resolver and trust the local CA"); err != nil {
		return err
	}

	fmt.Fprintln(out, "==> Generating local CA")
	if _, _, err := tls.EnsureCA(); err != nil {
		return err
	}

	fmt.Fprintf(out, "==> Configuring host resolver for .%s\n", cfg.TLD)
	if err := dns.ConfigureResolver(cfg.TLD); err != nil {
		return err
	}

	fmt.Fprintln(out, "==> Installing CA trust (system + NSS)")
	if err := tls.InstallSystemTrust(); err != nil {
		return err
	}
	if err := tls.InstallNSSTrust(); err != nil {
		return err
	}

	fmt.Fprintln(out, "==> Starting the proximo stack")
	certDir, err := tls.Dir()
	if err != nil {
		return err
	}
	if err := docker.Up(cfg.TLD, certDir); err != nil {
		return err
	}

	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nInstalled. Containers are reachable at https://<name>.%s\n", cfg.TLD)
	return nil
}
