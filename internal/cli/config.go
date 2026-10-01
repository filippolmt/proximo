package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/filippolmt/proximo/internal/checks"
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
	cmd.AddCommand(newConfigMachineCmd())
	cmd.AddCommand(newConfigPeerSuffixCmd())
	cmd.AddCommand(newConfigAddressCmd())
	cmd.AddCommand(newConfigTeamRootCmd())
	cmd.AddCommand(newConfigMeshRemedyCmd())
	return cmd
}

// The peer values are hidden until the capability that reads them is built:
// documenting them now would advertise a feature the binary does not have
// (docs/specs/peer-sharing.md). Unhide them in the commit that moves that
// specification's cli.md sections into docs/cli.md.
//
// The peer values are set one per subcommand, and validated here — at the
// moment a person types them, never at reconcile and never as a refusal to
// start. What a machine can decide is refused; what it cannot is reported.

func newConfigMachineCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "machine <label>",
		Short:  "Set this machine's label in its peer names",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := config.NormalizeMachine(args[0])
			if err != nil {
				return err
			}
			return savePeerValue(cmd, "machine", m, func(c *config.Config) { c.Machine = m }, config.MachineRule)
		},
	}
}

func newConfigPeerSuffixCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "peer-suffix <suffix>",
		Short:  "Set the suffix every peer name lives under",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := config.NormalizePeerSuffix(args[0])
			if err != nil {
				return err
			}
			if w := config.PeerSuffixWarning(s); w != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", warnPrefix, w)
			}
			return savePeerValue(cmd, "peer-suffix", s, func(c *config.Config) { c.PeerSuffix = s }, "")
		},
	}
}

// interfaceAddrs is swapped in tests.
var interfaceAddrs = net.InterfaceAddrs

func newConfigAddressCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "address <ip>",
		Short:  "Set the address proximo answers this machine's peer names with",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := config.ParseAddress(args[0])
			if err != nil {
				return err
			}
			// A report, never a refusal: a correct address is held by nothing
			// whenever the mesh client is down.
			if addrs, err := interfaceAddrs(); err == nil && !config.AddressHeld(a, addrs) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s%s is not held by any interface of this machine right now.\n", warnPrefix, a)
			}
			return savePeerValue(cmd, "address", a, func(c *config.Config) { c.Address = a }, config.AddressRule)
		},
	}
}

func newConfigTeamRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "team-root <path>",
		Short:  "Point proximo at the team root certificate",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.PeerSuffix == "" {
				return fmt.Errorf("the peer suffix is not set, so the team root's coverage cannot be judged\n    Remedy: proximo config peer-suffix <suffix>")
			}
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := tls.ValidateTeamRoot(data, cfg.PeerSuffix); err != nil {
				return fmt.Errorf("team root %s: %w", path, err)
			}
			return savePeerValue(cmd, "team-root", path, func(c *config.Config) { c.TeamRoot = path }, "")
		},
	}
}

func newConfigMeshRemedyCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "mesh-remedy <command>",
		Short:  "Set the command the mesh Check offers as its Remedy",
		Hidden: true,
		Long: "Store, verbatim, the command the mesh Check offers when it fails —\n" +
			"typically the transport's own status command. proximo cannot judge a\n" +
			"transport's command, so nothing is validated.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := args[0]
			return savePeerValue(cmd, "mesh-remedy", r, func(c *config.Config) { c.MeshRemedy = r }, "")
		},
	}
}

// savePeerValue persists one peer value and says what it stored, followed by
// the rule a person has to apply when there is one. Nothing reads these values
// at runtime yet, so there is nothing to converge.
func savePeerValue(cmd *cobra.Command, name, value string, set func(*config.Config), rule string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	set(&cfg)
	if err := cfg.Save(); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Saved %s: %s\n", name, value)
	if rule != "" {
		fmt.Fprintln(out, rule)
	}
	return nil
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
	// A TLD nobody reserved is served all the same — the choice is the user's —
	// but it is never served silently.
	if w := config.TLDWarning(newTLD); w != "" {
		fmt.Fprintf(out, "%s%s\n", warnPrefix, w)
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

	if err := checks.DockerReachable(cmd.Context()); err != nil {
		return err
	}
	platform.SudoPrime("update the host resolver for the new TLD")

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
	// This converge is a side effect of changing the TLD, which makes it the
	// easiest place for the stack's image to change unannounced — including a
	// sticky --image override, which any converge without the flag clears.
	opts := docker.ConvergeOpts{}
	reportImage(out, opts)
	if err := docker.Converge(newTLD, certDir, opts); err != nil {
		return err
	}

	fmt.Fprintf(out, "Done. Route containers under .%s via their Traefik labels.\n", newTLD)
	return nil
}
