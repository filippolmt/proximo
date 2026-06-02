package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/observability"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	var withObs bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the proximo stack (no host-config changes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := checkDocker(); err != nil {
				return err
			}
			certDir, err := tls.Dir()
			if err != nil {
				return err
			}
			if withObs {
				return upObservability(cmd, cfg.TLD, certDir)
			}
			if err := docker.Up(cfg.TLD, certDir); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Proximo stack started.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&withObs, "observability", false,
		"Also start the opt-in logs (Dozzle) and metrics (Beszel) dashboards")
	return cmd
}

// upObservability brings the core stack up together with the opt-in
// observability services and runs the Beszel hub↔agent bootstrap, then prints
// the dashboard URLs.
func upObservability(cmd *cobra.Command, tld, certDir string) error {
	out := cmd.OutOrStdout()

	password, err := observability.EnsureSecret()
	if err != nil {
		return err
	}
	stackDir, err := docker.StackDir()
	if err != nil {
		return err
	}
	if err := observability.WriteHubEnv(stackDir, password); err != nil {
		return err
	}

	email := observability.Email(tld)
	bootstrap := func(dir string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()
		hub := observability.NewHubClient(fmt.Sprintf("http://127.0.0.1:%d", config.ObsHubPort))
		return observability.Bootstrap(ctx, hub, dir, observability.HubURL(), email, password)
	}
	if err := docker.ConvergeObservability(tld, certDir, docker.ConvergeOpts{}, bootstrap); err != nil {
		return err
	}

	fmt.Fprintln(out, "Proximo stack started with observability.")
	fmt.Fprintf(out, "  Logs:    https://logs.%s\n", tld)
	fmt.Fprintf(out, "  Metrics: https://metrics.%s\n", tld)
	return nil
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the proximo stack (no host-config changes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := docker.Down(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Proximo stack stopped.")
			return nil
		},
	}
}
