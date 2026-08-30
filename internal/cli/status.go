package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/filippolmt/proximo/internal/checks"
	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/spf13/cobra"
)

// warnPrefix marks a warning line in `proximo status` output — the per-route
// and inspection notes. Nothing else: a diagnosis with a Remedy belongs to
// `proximo doctor`.
const warnPrefix = "⚠ "

// writeInspectionNotes reports, under the route table, which routes are under
// Inspection and anything proximo had to do to their responses to get there.
// Relaxing a page's Content-Security-Policy is the one that must never be
// invisible, and it belongs here rather than only in `proximo errors`: it is a
// property of the route for as long as it carries the label, not of one request
// that may already have been evicted from the hop's buffer.
func writeInspectionNotes(out io.Writer, routes []docker.Route) {
	var inspected []docker.Route
	for _, r := range routes {
		if r.Inspect || r.InspectNote != "" {
			inspected = append(inspected, r)
		}
	}
	if len(inspected) == 0 {
		return
	}

	// Best-effort: the hop holds the warnings, and a stack without it (or one
	// still starting) simply has none to report.
	warnings := inspectRouteWarnings()

	fmt.Fprintln(out)
	for _, r := range inspected {
		if r.InspectNote != "" {
			fmt.Fprintf(out, "%s%s: %s\n", warnPrefix, r.Container, r.InspectNote)
			continue
		}
		fmt.Fprintf(out, "%s under inspection — `proximo errors --host %s`\n", r.Host, r.Host)
		for _, note := range warnings[r.Host] {
			fmt.Fprintf(out, "  %s%s\n", warnPrefix, note)
		}
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List routed containers and their URLs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if err := checks.DockerReachable(ctx); err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// Version skew and an --image override are diagnoses, not
			// inventory: they belong to `proximo doctor`, which is the only
			// command that prints a Remedy. status keeps its per-route notes,
			// which are facts about what is reachable right now.
			routes, err := docker.Routes(ctx, cfg.TLD)
			if err != nil {
				return err
			}
			if len(routes) == 0 {
				fmt.Fprintln(out, "No routed containers.")
				return nil
			}
			// The MIDDLEWARES column appears only when at least one route carries
			// proximo middlewares, so the common (no-middleware) listing stays a
			// two-column table.
			anyMW := false
			for _, r := range routes {
				if len(r.Middlewares) > 0 {
					anyMW = true
					break
				}
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			if anyMW {
				fmt.Fprintln(w, "CONTAINER\tURL\tMIDDLEWARES")
			} else {
				fmt.Fprintln(w, "CONTAINER\tURL")
			}
			for _, r := range routes {
				val := r.Display()
				if r.Note != "" {
					val = warnPrefix + r.Note
				}
				if anyMW {
					fmt.Fprintf(w, "%s\t%s\t%s\n", r.Container, val, strings.Join(r.Middlewares, ", "))
				} else {
					fmt.Fprintf(w, "%s\t%s\n", r.Container, val)
				}
			}
			if err := w.Flush(); err != nil {
				return err
			}
			writeInspectionNotes(out, routes)
			return nil
		},
	}
}
