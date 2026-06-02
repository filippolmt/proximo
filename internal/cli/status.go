package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List routed containers and their URLs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkDocker(); err != nil {
				return err
			}
			routes, err := docker.Routes(context.Background())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(routes) == 0 {
				fmt.Fprintln(out, "No routed containers.")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CONTAINER\tURL")
			for _, r := range routes {
				fmt.Fprintf(w, "%s\t%s\n", r.Container, r.URL)
			}
			return w.Flush()
		},
	}
}
