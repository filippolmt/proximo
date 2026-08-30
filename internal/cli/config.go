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

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and change proximo configuration",
	}
	cmd.AddCommand(newConfigTLDCmd())
	cmd.AddCommand(newConfigCAPathCmd())
	return cmd
}

func newConfigCAPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ca-path",
		Short: "Print the path of the local CA certificate",
		Long: "Print the absolute path of proximo's local CA certificate (PEM).\n" +
			"The path is printed even when the file does not exist yet (proximo not\n" +
			"installed), so external tools can rely on it as a stable contract and\n" +
			"check existence themselves. The command is side-effect free: it never\n" +
			"creates directories on the host.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := tls.CACertLocation()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}

func newConfigTLDCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tld <tld>",
		Short: "Change the configured TLD and update resolver, certificate, and routing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigTLD(cmd, args[0])
		},
	}
}

func runConfigTLD(cmd *cobra.Command, raw string) error {
	out := cmd.OutOrStdout()
	newTLD, err := config.NormalizeTLD(raw)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	oldTLD := cfg.TLD
	if newTLD == oldTLD {
		fmt.Fprintf(out, "TLD is already .%s; nothing to change.\n", newTLD)
		return nil
	}

	if err := checkDocker(); err != nil {
		return err
	}
	if err := platform.SudoPrime("update the host resolver for the new TLD"); err != nil {
		return err
	}

	fmt.Fprintf(out, "==> Switching TLD: .%s -> .%s\n", oldTLD, newTLD)
	if err := dns.RemoveResolver(defaultRunner, oldTLD); err != nil {
		return err
	}

	cfg.TLD = newTLD
	if err := cfg.Save(); err != nil {
		return err
	}

	if err := dns.ConfigureResolver(defaultRunner, newTLD); err != nil {
		return err
	}

	certDir, err := tls.Dir()
	if err != nil {
		return err
	}
	// Carry any sticky --image override forward: only `up` / `update` clear it.
	image, err := docker.StickyImage()
	if err != nil {
		return err
	}
	opts := docker.ConvergeOpts{Image: image}
	// This converge is a side effect of changing the TLD, which makes it the
	// easiest place for the stack's image to change unannounced.
	reportImage(out, opts)
	if err := docker.Converge(newTLD, certDir, opts); err != nil {
		return err
	}

	fmt.Fprintf(out, "Done. Route containers under .%s via their Traefik labels.\n", newTLD)
	return nil
}
