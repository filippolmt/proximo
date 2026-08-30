package cli

import (
	"fmt"

	"github.com/filippolmt/proximo/internal/checks"
	"github.com/filippolmt/proximo/internal/config"
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
	if err := runPreflight(cmd.Context(), out, cfg.TLD, checks.PreInstall); err != nil {
		return err
	}

	if err := platform.SudoPrime("configure the host resolver and trust the local CA"); err != nil {
		return err
	}

	if err := applySteps(out, hostSteps(defaultRunner, cfg.TLD)); err != nil {
		return err
	}

	fmt.Fprintln(out, "==> Starting the proximo stack")
	certDir, err := tls.Dir()
	if err != nil {
		return err
	}
	// First-run host setup installs the canonical thing, so it also clears a
	// sticky --image override left by an earlier `up`. Say so rather than swap
	// the image out from under the developer.
	opts := docker.ConvergeOpts{}
	reportImage(out, opts)
	if err := docker.Converge(cfg.TLD, certDir, opts); err != nil {
		return err
	}

	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nInstalled. Containers are reachable at https://<name>.%s\n", cfg.TLD)
	return nil
}
