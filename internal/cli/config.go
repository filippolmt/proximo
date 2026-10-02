package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

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
	cmd.AddCommand(newConfigInventoryDirCmd())
	cmd.AddCommand(newConfigMachineCmd())
	cmd.AddCommand(newConfigPeerSuffixCmd())
	cmd.AddCommand(newConfigAddressCmd())
	cmd.AddCommand(newConfigTeamRootCmd())
	cmd.AddCommand(newConfigMeshRemedyCmd())
	cmd.AddCommand(newConfigCSRCmd())
	cmd.AddCommand(newConfigIntermediateCmd())
	cmd.AddCommand(newConfigUnsetCmd())
	return cmd
}

// The peer values are set one per subcommand, and validated here — at the
// moment a person types them, never at reconcile and never as a refusal to
// start. What a machine can decide is refused; what it cannot is reported.

func newConfigMachineCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "machine <label>",
		Short: "Set this machine's label in its peer names",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := config.NormalizeMachine(args[0])
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			warnIntermediate(cmd, m, cfg.PeerSuffix)
			return savePeerValue(cmd, "machine", m, func(c *config.Config) { c.Machine = m }, config.MachineRule)
		},
	}
}

func newConfigPeerSuffixCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "peer-suffix <suffix>",
		Short: "Set the suffix every peer name lives under",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := config.NormalizePeerSuffix(args[0])
			if err != nil {
				return err
			}
			if w := config.PeerSuffixWarning(s); w != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", warnPrefix, w)
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			// proximo's own DNS answers every name under the TLD with
			// 127.0.0.1, ahead of any nameserver the mesh configures.
			if strings.HasSuffix("."+s, "."+cfg.TLD) {
				return fmt.Errorf("invalid peer suffix %q: proximo answers every name under .%s on this machine, so no peer name under it would reach a colleague", s, cfg.TLD)
			}
			// The team root was judged against the old suffix. A report, not a
			// refusal: there is no other order in which to change both.
			if cfg.TeamRoot != "" {
				if err := checkTeamRoot(cfg.TeamRoot, s); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "%s%v\n    Remedy: proximo config team-root <path>\n", warnPrefix, err)
				}
			}
			warnIntermediate(cmd, cfg.Machine, s)
			return savePeerValue(cmd, "peer-suffix", s, func(c *config.Config) { c.PeerSuffix = s }, "")
		},
	}
}

// interfaceAddrs is swapped in tests.
var interfaceAddrs = net.InterfaceAddrs

func newConfigAddressCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "address <ip>",
		Short: "Set the address proximo answers this machine's peer names with",
		Args:  cobra.ExactArgs(1),
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
		Use:   "team-root <path>",
		Short: "Point proximo at the team root certificate",
		Args:  cobra.ExactArgs(1),
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
			if err := checkTeamRoot(path, cfg.PeerSuffix); err != nil {
				return err
			}
			return savePeerValue(cmd, "team-root", path, func(c *config.Config) { c.TeamRoot = path }, "")
		},
	}
}

func checkTeamRoot(path, suffix string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := tls.ValidateTeamRoot(data, suffix); err != nil {
		return fmt.Errorf("team root %s: %w", path, err)
	}
	return nil
}

func newConfigMeshRemedyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mesh-remedy <command>",
		Short: "Set the command the mesh Check offers as its Remedy",
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

func newConfigCSRCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "csr",
		Short: "Print the certificate signing request for this machine's intermediate",
		Long: "Print, on stdout and nothing else, the CSR a custodian signs into this\n" +
			"machine's intermediate. The machine key is created only if none exists\n" +
			"and never leaves the machine; run again, the same CSR is printed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := requirePeerValues(cfg, "machine", "peer-suffix"); err != nil {
				return err
			}
			csr, err := tls.MachineCSR(cfg.Machine, cfg.PeerSuffix)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(csr)
			return err
		},
	}
}

func newConfigIntermediateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "intermediate <file>",
		Short: "Install the intermediate a custodian signed from this machine's CSR",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := requirePeerValues(cfg, "machine", "peer-suffix", "team-root"); err != nil {
				return err
			}
			// The root is judged again: the file may have changed since config
			// team-root accepted it, and the intermediate inherits its reach.
			if err := checkTeamRoot(cfg.TeamRoot, cfg.PeerSuffix); err != nil {
				return err
			}
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			root, err := os.ReadFile(cfg.TeamRoot)
			if err != nil {
				return err
			}
			if err := tls.ValidateIntermediate(data, root, cfg.Machine, cfg.PeerSuffix); err != nil {
				return fmt.Errorf("intermediate %s: %w", args[0], err)
			}
			if err := tls.InstallIntermediate(data); err != nil {
				return err
			}
			if err := docker.SyncPeer(cfg); err != nil {
				return err
			}
			c, err := tls.Intermediate()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved intermediate for %s, valid until %s\n",
				tls.MachineSubtree(cfg.Machine, cfg.PeerSuffix), c.NotAfter.Format("2006-01-02"))
			return nil
		},
	}
}

// unsetters return each peer value to its unconfigured state. The team root's
// trust anchor is not removed here — that needs sudo, and `uninstall` removes
// what was trusted — and the machine key is never removed: it is the one thing
// a new intermediate must match.
var unsetters = map[string]func(*config.Config){
	"machine":     func(c *config.Config) { c.Machine = "" },
	"peer-suffix": func(c *config.Config) { c.PeerSuffix = "" },
	"address":     func(c *config.Config) { c.Address = "" },
	"team-root":   func(c *config.Config) { c.TeamRoot = "" },
	"mesh-remedy": func(c *config.Config) { c.MeshRemedy = "" },
}

func newConfigUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <machine|peer-suffix|address|team-root|intermediate|mesh-remedy>",
		Short: "Return a peer-sharing value to its unconfigured state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			switch unset, ok := unsetters[name]; {
			case name == "intermediate":
				if err := tls.RemoveIntermediate(); err != nil {
					return err
				}
			case ok:
				unset(&cfg)
				if err := cfg.Save(); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%q is not a peer-sharing value: machine, peer-suffix, address, team-root, intermediate or mesh-remedy", name)
			}
			if err := docker.SyncPeer(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Unset %s.\n", name)
			return nil
		},
	}
}

// requirePeerValues refuses, with a Remedy, when one of the named values is
// unset — the first missing one, in the order they are set.
func requirePeerValues(cfg config.Config, names ...string) error {
	set := map[string]string{"machine": cfg.Machine, "peer-suffix": cfg.PeerSuffix, "team-root": cfg.TeamRoot}
	arg := map[string]string{"machine": "<label>", "peer-suffix": "<suffix>", "team-root": "<path>"}
	for _, n := range names {
		if set[n] == "" {
			return fmt.Errorf("%s is not configured\n    Remedy: proximo config %s %s", n, n, arg[n])
		}
	}
	return nil
}

// warnIntermediate reports an installed intermediate that a new machine label
// or Peer suffix leaves behind: it is constrained to the old one.
func warnIntermediate(cmd *cobra.Command, machine, suffix string) {
	if old, ok := tls.IntermediateFor(machine, suffix); old != "" && !ok {
		fmt.Fprintf(cmd.OutOrStdout(), "%sthe installed intermediate is constrained to %s, so it no longer signs this machine's peer names.\n    Remedy: proximo config csr\n", warnPrefix, old)
	}
}

// savePeerValue persists one peer value and says what it stored, followed by
// the rule a person has to apply when there is one. A materialized stack gets
// the change at once: its watcher re-reads the peer material every reconcile.
func savePeerValue(cmd *cobra.Command, name, value string, set func(*config.Config), rule string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	set(&cfg)
	if err := cfg.Save(); err != nil {
		return err
	}
	if err := docker.SyncPeer(cfg); err != nil {
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

func newConfigInventoryDirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inventory-dir",
		Short: "Print the directory holding the effective-route inventory",
		Long: "Print the absolute path of the directory the watcher keeps routes.json in:\n" +
			"the `proximo status --json` document, kept current while the stack runs.\n" +
			"Mount the directory, not the file — the file is replaced by rename. Like\n" +
			"ca-path, the path is printed even when it does not exist yet, and the\n" +
			"command never creates directories on the host.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.InventoryDir()
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
