package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/version"
	"github.com/spf13/cobra"
)

// warnPrefix marks a warning line in `proximo status` output (skew notice and
// per-route notes both use it).
const warnPrefix = "⚠ "

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List routed containers and their URLs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkDocker(); err != nil {
				return err
			}
			ctx := context.Background()
			out := cmd.OutOrStdout()

			// Read-only skew check: warn (never mutate) when the running stack
			// version differs from the installed CLI, pointing to `proximo update`.
			if stackVer, err := docker.StackVersion(ctx); err == nil {
				if w := docker.VersionSkew(stackVer, version.Version); w != "" {
					fmt.Fprintln(out, warnPrefix+w)
				}
			}

			routes, err := docker.Routes(ctx)
			if err != nil {
				return err
			}
			if len(routes) == 0 {
				fmt.Fprintln(out, "No routed containers.")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CONTAINER\tURL")
			for _, r := range routes {
				val := r.URL
				if r.Note != "" {
					val = warnPrefix + r.Note
				}
				fmt.Fprintf(w, "%s\t%s\n", r.Container, val)
			}
			return w.Flush()
		},
	}
}
