package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/observability"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/spf13/cobra"
)

// imageFlag registers the --image escape hatch on a command that converges the
// stack. It takes a ref verbatim — a tag, a digest, or a locally built image —
// and replaces the whole stack, never one component. `install` deliberately
// does not carry it: first-run host setup installs the canonical thing.
func imageFlag(cmd *cobra.Command, image *string) {
	cmd.Flags().StringVar(image, "image", "",
		"Stack image ref to run instead of the version-pinned one "+
			"(sticky: cleared by the next up/update without it)")
}

// reportImage prints the effective stack image whenever this run changes what
// the stack runs — most importantly the reversal when a sticky --image override
// is dropped, which would otherwise silently swap the image back. It resolves
// the ref through the same options the converge will use, so it can never
// announce one image while the stack starts another. It must be called before
// Converge, which rewrites the .env it reads.
func reportImage(out io.Writer, opts docker.ConvergeOpts) {
	image := opts.EffectiveImage()
	prev, err := docker.EnvImage()
	if err != nil || prev == "" || prev == image {
		return
	}
	fmt.Fprintf(out, "Stack image: %s (was %s).\n", image, prev)
}

func newUpCmd() *cobra.Command {
	var (
		withObs bool
		image   string
	)
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
			opts := docker.ConvergeOpts{Image: image}
			reportImage(cmd.OutOrStdout(), opts)
			if withObs {
				return upObservability(cmd, cfg.TLD, certDir, opts)
			}
			if err := docker.Converge(cfg.TLD, certDir, opts); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Proximo stack started.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&withObs, "observability", false,
		"Also start the opt-in logs (Dozzle) and metrics (Beszel) dashboards")
	imageFlag(cmd, &image)
	return cmd
}

// upObservability brings the core stack up together with the opt-in
// observability services and runs the Beszel hub↔agent bootstrap, then prints
// the dashboard URLs.
func upObservability(cmd *cobra.Command, tld, certDir string, opts docker.ConvergeOpts) error {
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
	if err := docker.ConvergeObservability(tld, certDir, opts, bootstrap); err != nil {
		return err
	}

	fmt.Fprintln(out, "Proximo stack started with observability.")
	fmt.Fprintf(out, "  Logs:    https://logs.%s\n", tld)
	fmt.Fprintf(out, "  Metrics: https://metrics.%s\n", tld)
	return nil
}

func newDownCmd() *cobra.Command {
	var obsOnly bool
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Stop the proximo stack (no host-config changes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if obsOnly {
				if err := docker.DownObservability(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Observability dashboards stopped (core stack left running).")
				return nil
			}
			if err := docker.Down(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Proximo stack stopped.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&obsOnly, "observability", false,
		"Stop only the observability dashboards, leaving the core stack running")
	return cmd
}
