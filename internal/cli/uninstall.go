package cli

import (
	"fmt"
	"io"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/observability"
	"github.com/filippolmt/proximo/internal/platform"
	"github.com/spf13/cobra"
)

// Seams for the stack teardown and observability cleanup so the uninstall wiring
// is unit-testable without Docker or touching the host (mirrors defaultRunner).
var (
	teardownStack      = docker.Purge
	purgeObservability = observability.Purge
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

	if err := stopAndCleanStack(out); err != nil {
		return err
	}

	if err := revertSteps(out, hostSteps(defaultRunner, cfg.TLD)); err != nil {
		return err
	}

	removeSkills(out)

	// Remove the state home last: the trust reversal above reads the CA from it,
	// and the stack is already down so the data bind mounts are released.
	fmt.Fprintln(out, "==> Removing the proximo state home")
	if err := config.RemoveHome(); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nUninstalled. The host has been restored to its prior state.")
	return nil
}

// stopAndCleanStack stops the stack (which also removes the profiled
// observability containers) and deletes the generated observability secret + env
// files. The stack's runtime data lives in bind mounts under ~/.proximo, which
// uninstall removes wholesale via config.RemoveHome once the stack is down — so
// there is no named volume left to tear down here.
func stopAndCleanStack(out io.Writer) error {
	fmt.Fprintln(out, "==> Stopping the proximo stack")
	if err := teardownStack(); err != nil {
		return err
	}
	stackDir, err := docker.StackDir()
	if err != nil {
		return err
	}
	return purgeObservability(stackDir)
}
