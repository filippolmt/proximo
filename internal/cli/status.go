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
// and inspection notes. Nothing else: a failure carrying a Remedy belongs to
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
	var asJSON bool
	cmd := &cobra.Command{
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

			// Version skew and an --image override are things to do
			// something about, not inventory: they belong to `proximo doctor`,
			// the only command that prints a Remedy. status keeps its per-route
			// notes, which are facts about what is reachable right now.
			// Peer names are printed only when the route is served on them:
			// with any value missing, the cell names what is missing instead.
			missing := docker.PeerMissing(cfg)
			peer := docker.PeerNames{}
			if len(missing) == 0 {
				peer = docker.PeerNames{Machine: cfg.Machine, Suffix: cfg.PeerSuffix}
			}
			routes, err := docker.Routes(ctx, cfg.TLD, peer)
			if err != nil {
				return err
			}
			if asJSON {
				data, err := docker.NewInventory(routes).MarshalIndent()
				if err != nil {
					return err
				}
				_, err = out.Write(data)
				return err
			}
			if len(routes) == 0 {
				fmt.Fprintln(out, "No routed containers.")
				return nil
			}
			// The MIDDLEWARES and PEER columns appear only when at least one route
			// needs them, so the common listing stays a two-column table.
			anyMW, anyPeer := false, false
			for _, r := range routes {
				anyMW = anyMW || len(r.Middlewares) > 0
				anyPeer = anyPeer || r.Share || r.ShareTCP
			}

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			header := []string{"CONTAINER", "URL"}
			if anyPeer {
				header = append(header, "PEER")
			}
			if anyMW {
				header = append(header, "MIDDLEWARES")
			}
			fmt.Fprintln(w, strings.Join(header, "\t"))
			for _, r := range routes {
				val := r.Display()
				// An observed container's row is a fact about the inventory, not
				// a warning: nothing is wrong with a worker that has no route.
				switch {
				case r.Observed:
					val = r.Note
				case r.Note != "":
					val = warnPrefix + r.Note
				}
				cells := []string{r.Container, val}
				if anyPeer {
					cells = append(cells, peerCell(r, missing))
				}
				if anyMW {
					cells = append(cells, strings.Join(r.Middlewares, ", "))
				}
				fmt.Fprintln(w, strings.Join(cells, "\t"))
			}
			if err := w.Flush(); err != nil {
				return err
			}
			writeInspectionNotes(out, routes)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit every name each route answers on as JSON, for tools that pin them")
	return cmd
}

// peerCell renders a route's PEER cell, mirroring the URL cell: the Bare peer
// name with https://, the Qualified one bare after "  + ". A shared route not
// served on its peer names says which values are missing — a fact, never a
// command. With the mesh down the cell is unchanged: proximo cannot observe it.
func peerCell(r docker.Route, missing []string) string {
	switch {
	case r.ShareTCP:
		return warnPrefix + "proximo.share ignored on a TCP route"
	case !r.Share:
		return ""
	case len(missing) > 0:
		verb := "is"
		if len(missing) > 1 {
			verb = "are"
		}
		return fmt.Sprintf("%sproximo.share is set; not served on its peer names (%s %s not configured)", warnPrefix, joinAnd(missing), verb)
	case r.Peer == "" && r.PeerQualified != "":
		return "https://" + r.PeerQualified
	case r.Peer == "":
		return ""
	case r.PeerQualified != "":
		return "https://" + r.Peer + "  + " + r.PeerQualified
	}
	return "https://" + r.Peer
}

// joinAnd joins words with commas and a final "and".
func joinAnd(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}
