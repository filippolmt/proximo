package cli

import (
	"fmt"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/spf13/cobra"
)

func newUpCmd() *cobra.Command {
	return &cobra.Command{
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
			if err := docker.Up(cfg.TLD, certDir); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Proximo stack started.")
			return nil
		},
	}
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
