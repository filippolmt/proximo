package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/filippolmt/proximo/internal/checks"
	"github.com/filippolmt/proximo/internal/config"
	"github.com/spf13/cobra"
)

// Marks for the three outcomes. They are the only thing that differs between a
// statement that held and one that did not: every check is named in the
// positive, so the report reads as a list of facts either way.
const (
	markPass = "✔"
	markFail = "✘"
	markSkip = "–"
)

// docBase is where a failure's explanation lives. A repo-relative path rather
// than a URL: it is the file in the tree the anchor test verifies, so it can
// never point at a section that does not exist.
const docBase = "docs/troubleshooting.md"

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Report every check on this host, with a remedy for each failure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			env, err := checks.DefaultEnv(cfg.TLD)
			if err != nil {
				return err
			}
			rep := checks.Run(cmd.Context(), checks.All(env))
			writeReport(cmd.OutOrStdout(), rep)
			if rep.OK() {
				return nil
			}
			// Any failure exits non-zero, route failures included: an exit code
			// that needs a rule to interpret is worse than one that does not.
			return fmt.Errorf("%s", countFailed(len(rep.Failures())))
		},
	}
}

// writeReport renders one complete pass. It prints the checks that passed too:
// those say where not to look, and narrow the search as much as a failure does.
func writeReport(out io.Writer, rep checks.Report) {
	for _, o := range rep.Outcomes {
		writeOutcome(out, o)
	}
}

// writeOutcome renders one check. A pass fits on its line; a failure or a skip
// spends the lines it needs, because those are the ones being read.
func writeOutcome(out io.Writer, o checks.Outcome) {
	switch o.Result.Status {
	case checks.Pass:
		line := markPass + " " + o.Check.Name
		if o.Result.Detail != "" {
			line += " — " + o.Result.Detail
		}
		fmt.Fprintln(out, line)
	case checks.Skip:
		fmt.Fprintf(out, "%s %s — %s\n", markSkip, o.Check.Name, o.Result.Detail)
	default:
		fmt.Fprintf(out, "%s %s\n", markFail, o.Check.Name)
		for _, line := range strings.Split(o.Result.Detail, "\n") {
			fmt.Fprintf(out, "    %s\n", line)
		}
		fmt.Fprintf(out, "    Remedy: %s\n", o.Result.Remedy)
		fmt.Fprintf(out, "    See:    %s#%s\n", docBase, o.Check.Doc)
	}
}

// gate runs the checks that are meaningful before the host has been changed and
// stops the command when one fails, printing only the failures: a healthy `up`
// stays quiet, and a broken one says what to do before it touches anything.
func gate(ctx context.Context, out io.Writer, tld string) error {
	env, err := checks.DefaultEnv(tld)
	if err != nil {
		return err
	}
	rep := checks.Run(ctx, checks.Preflight(env))
	failures := rep.Failures()
	if len(failures) == 0 {
		return nil
	}
	for _, o := range failures {
		writeOutcome(out, o)
	}
	return fmt.Errorf("%s; `proximo doctor` reports the whole environment", countFailed(len(failures)))
}

func countFailed(n int) string {
	if n == 1 {
		return "1 check failed"
	}
	return fmt.Sprintf("%d checks failed", n)
}
