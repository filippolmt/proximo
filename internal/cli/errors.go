package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/spf13/cobra"
)

// The hop's read API is published on loopback only, so the CLI is the only thing
// that can reach it — an inspected page cannot.
func inspectAPI(path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", config.InspectAPIPort, path)
}

func newErrorsCmd() *cobra.Command {
	var (
		host        string
		since       time.Duration
		limit       int
		asJSON, all bool
	)

	cmd := &cobra.Command{
		Use:   "errors",
		Short: "Show what went wrong on inspected routes",
		Long: "Lists recent Exchanges from routes labelled proximo.inspect: what the " +
			"stack served, and what the browser reported while that page was live.\n\n" +
			"The output is meant to be read by a person or an agent without further " +
			"processing. Use `proximo errors dom <id>` for the page's DOM at the time.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := fmt.Sprintf("?host=%s&since=%s&limit=%d", host, since, limit)
			exchanges, err := fetchExchanges(inspectAPI("/exchanges" + q))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(exchanges)
			}
			if len(exchanges) == 0 {
				fmt.Fprintln(out, "No Exchanges recorded. Label a container with proximo.inspect=true and load a page.")
				return nil
			}
			for _, e := range exchanges {
				writeExchange(out, e, all)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "only this host (e.g. web.test)")
	cmd.Flags().DurationVar(&since, "since", 15*time.Minute, "only Exchanges newer than this")
	cmd.Flags().IntVar(&limit, "limit", 20, "most recent N Exchanges")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the raw Exchanges as JSON")
	cmd.Flags().BoolVar(&all, "all", false, "include breadcrumbs below warning level")

	cmd.AddCommand(newErrorsDOMCmd())
	return cmd
}

// newErrorsDOMCmd writes a Snapshot to a file rather than to the terminal: a
// page's DOM is hundreds of kilobytes, useful to read and useless to scroll past.
func newErrorsDOMCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "dom <exchange-id>",
		Short: "Write the DOM captured for one Exchange to a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			resp, err := http.Get(inspectAPI("/dom?x=" + id))
			if err != nil {
				return hopUnreachable(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				return fmt.Errorf("no DOM recorded for Exchange %s (it may have been evicted, or no client report carried one)", id)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			if out == "" {
				out = filepath.Join(os.TempDir(), "proximo-dom-"+id+".html")
			}
			if err := os.WriteFile(out, body, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "write to this path (default: a file under the temp dir)")
	return cmd
}

func fetchExchanges(url string) ([]inspect.Exchange, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, hopUnreachable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inspection hop returned %s", resp.Status)
	}
	var out []inspect.Exchange
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("inspection hop returned something unreadable: %w", err)
	}
	return out, nil
}

func hopUnreachable(err error) error {
	return fmt.Errorf("cannot reach the inspection hop on 127.0.0.1:%d — is the stack up? (`proximo up`): %w",
		config.InspectAPIPort, err)
}

// noisyBreadcrumb levels are hidden unless --all: in development the console is
// dominated by framework warnings and dev-server chatter, and burying the report
// in them is the fastest way to make the output useless. Nothing is dropped at
// capture time, only here.
var noisyBreadcrumb = map[string]bool{"debug": true, "info": true, "log": true, "": true}

// writeExchange renders one Exchange as a fixed-order block. The shape is stable
// on purpose: it is read as often by an agent as by a person.
func writeExchange(w io.Writer, e inspect.Exchange, all bool) {
	fmt.Fprintf(w, "%s  %s  %s %s  →  %s  %s\n",
		e.At.Local().Format("15:04:05"), e.ID, e.Method, e.Path, status(e.Status), duration(e.Duration))

	for _, warn := range e.Warnings {
		fmt.Fprintf(w, "  %s%s\n", warnPrefix, warn)
	}

	if len(e.Reports) == 0 {
		if e.Status >= 400 {
			fmt.Fprintln(w, "  (no client report — the failure is the backend's)")
		}
		fmt.Fprintln(w)
		return
	}

	for _, r := range e.Reports {
		fmt.Fprintf(w, "  ✗ %s\n", r.Message)
		for _, f := range r.Frames {
			fmt.Fprintf(w, "      at %s (%s)\n", orUnknown(f.Func), location(f))
		}
		for _, b := range visibleBreadcrumbs(r.Breadcrumbs, all) {
			fmt.Fprintf(w, "      · %-8s %-9s %s\n", b.Level, b.Category, b.Message)
		}
	}
	if e.HasSnapshot {
		fmt.Fprintf(w, "  DOM captured — `proximo errors dom %s`\n", e.ID)
	}
	fmt.Fprintln(w)
}

func visibleBreadcrumbs(crumbs []inspect.Breadcrumb, all bool) []inspect.Breadcrumb {
	if all {
		return crumbs
	}
	out := crumbs[:0:0]
	for _, b := range crumbs {
		if !noisyBreadcrumb[strings.ToLower(b.Level)] {
			out = append(out, b)
		}
	}
	return out
}

func location(f inspect.Frame) string {
	loc := orUnknown(f.File) + ":" + strconv.Itoa(f.Line)
	if f.Col > 0 {
		loc += ":" + strconv.Itoa(f.Col)
	}
	return loc
}

func orUnknown(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func status(code int) string {
	if code == 0 {
		return "---"
	}
	return strconv.Itoa(code)
}

func duration(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}
