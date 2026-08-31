package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/filippolmt/proximo/internal/transcript"
	"github.com/moby/moby/client"
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
		since       string
		limit       int
		asJSON, all bool
	)

	cmd := &cobra.Command{
		Use:   "errors",
		Short: "Show what went wrong on inspected routes",
		Long: "Lists recent Exchanges: what the stack served, what the container that " +
			"served it wrote while the request was live, and — on routes labelled " +
			"proximo.inspect — what the browser reported.\n\n" +
			"By default it shows only what went wrong — a client report, a warning, or a " +
			"failing status — because the alternative buries the one broken page under every " +
			"request that worked. --all shows the rest.\n\n" +
			"The output is meant to be read by a person or an agent without further " +
			"processing. Use `proximo errors transcript <id>` for a container's whole " +
			"output, and `proximo errors dom <id>` for the page's DOM at the time.\n\n" +
			"A transcript is the application's own output, quoted with no redaction: " +
			"it may carry credentials or personal data.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cutoff, err := parseSince(since, time.Now())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			r, closeDocker, err := newTranscriptReader(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDocker()

			logged, err := r.Access(cmd.Context(), cutoff)
			if err != nil {
				return err
			}
			// The window is every Exchange the sources hold; the listing is the
			// subset asked for. Overlaps are counted against the window, because
			// a request that interleaved its lines with this one did so whether or
			// not the filter kept it.
			window := r.Merge(logged, hopExchanges(cutoff))
			exchanges := inspect.Select(window, host, cutoff, limit, !all)
			quoted := r.Join(cmd.Context(), quotable(exchanges), window, transcript.DefaultLimit)

			// Both notices apply whether the listing is empty or not, and whether
			// it is read by a person or parsed: an agent asking with --json is the
			// reader that will never run `proximo doctor` on its own.
			notes := listingNotes(cmd.Context(), host, len(exchanges) == 0)

			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(struct {
					Exchanges   []inspect.Exchange               `json:"exchanges"`
					Transcripts map[string]transcript.Transcript `json:"transcripts"`
					Notes       []string                         `json:"notes,omitempty"`
				}{exchanges, quoted, notes})
			}
			for _, n := range notes {
				fmt.Fprintln(out, n)
			}
			if len(exchanges) == 0 {
				writeNothingFound(out, all, host)
				return nil
			}
			show := warnAndAbove
			if all {
				show = everything
			}
			writeListing(out, exchanges, quoted, show)
			return nil
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "only this host (e.g. web.test)")
	cmd.Flags().StringVar(&since, "since", "15m", "a duration back from now (15m, 2h) or an absolute RFC 3339 instant")
	cmd.Flags().IntVar(&limit, "limit", 20, "most recent N Exchanges")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the raw Exchanges as JSON")
	cmd.Flags().BoolVar(&all, "all", false, "hold nothing back: Exchanges with nothing wrong, and breadcrumbs below warning level")

	cmd.AddCommand(newErrorsDOMCmd(), newErrorsTranscriptCmd())
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
			resp, err := http.Get(inspectAPI("/dom?" + url.Values{"x": {id}}.Encode()))
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
			// 0600 for the same reason a transcript gets it: a captured DOM holds
			// whatever the page held, which in development is routinely real data.
			if err := os.WriteFile(out, body, 0o600); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "write to this path (default: a file under the temp dir)")
	return cmd
}

// getJSON reads one endpoint of the hop's loopback API into v.
func getJSON(path string, v any) error {
	resp, err := http.Get(inspectAPI(path))
	if err != nil {
		return hopUnreachable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inspection hop returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("inspection hop returned something unreadable: %w", err)
	}
	return nil
}

// inspectRouteWarnings asks the hop what it had to do to the routes it serves.
// Best-effort by design: `proximo status` must still list routes when the hop is
// not up, so an unreachable hop simply yields nothing.
func inspectRouteWarnings() map[string][]string {
	var out map[string][]string
	if err := getJSON("/warnings", &out); err != nil {
		return nil
	}
	return out
}

// writeNothingFound explains an empty listing. The two reasons it can be empty
// are opposite — nothing broke, or the buffer was just emptied — and a restart is
// easy to cause by accident: bringing the stack up to pick up a change discards
// every Exchange recorded before it.
func writeNothingFound(out io.Writer, all bool, host string) {
	if host != "" {
		fmt.Fprintf(out, "No Exchange for %s in this window. Either nothing called it, or the name does not resolve here at all — `proximo doctor` tells those apart.\n", host)
		fmt.Fprintln(out, "Widen the window with --since, or provoke a request: `curl -sS https://"+host+"/`.")
		return
	}
	if uptime := hopUptime(); uptime > 0 && uptime < 10*time.Minute {
		fmt.Fprintf(out, "No Exchanges. The inspection hop restarted %s ago, and the Client reports it held were in memory only.\n",
			uptime.Round(time.Second))
		fmt.Fprintln(out, "Reload the page and reproduce the problem — anything the browser reported before the restart is gone.")
		return
	}
	if all {
		fmt.Fprintln(out, "No Exchanges recorded. Provoke a request — `curl -sS https://<host>.test/` — or load a page in the browser.")
		return
	}
	fmt.Fprintln(out, "Nothing went wrong in this window. Widen it with --since, or use --all to see the clean Exchanges too.")
}

// hopUptime returns how long the hop has been running, or zero when it cannot be
// asked. Best-effort: this only ever adds a hint to a message.
func hopUptime() time.Duration {
	var info struct {
		Started time.Time `json:"started"`
	}
	if err := getJSON("/info", &info); err != nil || info.Started.IsZero() {
		return 0
	}
	return time.Since(info.Started)
}

func hopUnreachable(err error) error {
	return fmt.Errorf("cannot reach the inspection hop on 127.0.0.1:%d — is the stack up? (`proximo up`): %w",
		config.InspectAPIPort, err)
}

// detail says how much of a Client report's trail to render. Nothing is ever
// dropped at capture time — in development the console is dominated by framework
// warnings and dev-server chatter, and burying the report in them is the fastest
// way to make the output useless — so the filtering happens here and only here.
type detail int

const (
	warnAndAbove detail = iota // the default: what a developer is looking for
	everything                 // --all
)

var noisyBreadcrumb = map[string]bool{"debug": true, "info": true, "log": true, "": true}

// writeExchange renders one Exchange as a fixed-order block. The shape is stable
// on purpose: it is read as often by an agent as by a person.
func writeExchange(w io.Writer, e inspect.Exchange, tr transcript.Transcript, show detail) {
	fmt.Fprintf(w, "%s  %s  %s %s  →  %s  %s\n",
		e.At.Local().Format("15:04:05"), e.ID, e.Method, e.Path, formatStatus(e.Status), formatDuration(e.Duration))

	for _, warn := range e.Warnings {
		fmt.Fprintf(w, "  %s%s\n", warnPrefix, warn)
	}

	if len(e.Reports) == 0 {
		if e.Status >= 400 {
			fmt.Fprintln(w, "  (no client report — the failure is the backend's)")
		}
		writeTranscript(w, e, tr)
		fmt.Fprintln(w)
		return
	}

	for _, r := range e.Reports {
		fmt.Fprintf(w, "  ✗ %s\n", r.Message)
		for _, f := range r.Frames {
			fmt.Fprintf(w, "      at %s (%s)\n", orPlaceholder(f.Func), frameLocation(f))
		}
		// Nothing recognisable in the stack — an engine that formats it
		// differently, or a cross-origin script. Print what the browser wrote
		// rather than nothing: it is still the most useful thing on the page.
		if len(r.Frames) == 0 && r.Stack != "" {
			for _, line := range strings.Split(strings.TrimSpace(r.Stack), "\n") {
				fmt.Fprintf(w, "      %s\n", strings.TrimSpace(line))
			}
		}
		for _, b := range visibleBreadcrumbs(r.Breadcrumbs, show) {
			fmt.Fprintf(w, "      · %-8s %-9s %s\n", b.Level, b.Category, b.Message)
		}
	}
	if e.Suppressed > 0 {
		fmt.Fprintf(w, "  … and %d more report(s), not kept: this page kept throwing\n", e.Suppressed)
	}
	if e.HasSnapshot {
		fmt.Fprintf(w, "  DOM captured — `proximo errors dom %s`\n", e.ID)
	}
	writeTranscript(w, e, tr)
	fmt.Fprintln(w)
}

// quotable is the subset of a listing whose Transcripts will actually be shown.
// Reading a container's output back is a round trip to Docker, and doing it for
// a clean request nobody will look at buys nothing.
func quotable(exchanges []inspect.Exchange) []inspect.Exchange {
	out := make([]inspect.Exchange, 0, len(exchanges))
	for _, e := range exchanges {
		if hasSomethingToSay(e) {
			out = append(out, e)
		}
	}
	return out
}

// hasSomethingToSay gates the Transcript. Quoting a healthy container's chatter
// under every clean request buries the one page that broke, which is the failure
// the default listing already refuses.
//
// It is deliberately narrower than Exchange.Interesting(), which also counts
// Warnings and decides what the listing *shows*. A warning is about the route —
// proximo relaxed a policy — so the container's output has nothing to add to it,
// and quoting one there would be output nobody asked for. Widen this only if a
// warning ever describes something the container itself did.
func hasSomethingToSay(e inspect.Exchange) bool {
	return e.Status >= 400 || len(e.Reports) > 0
}

// writeTranscript quotes what the serving container wrote, inline and tightly
// capped. Both ends survive and the elision between them is declared: a panic's
// message is at the head and its most recent output at the tail, and a
// truncation nobody is told about is the one after which a reader stops looking.
func writeTranscript(w io.Writer, e inspect.Exchange, tr transcript.Transcript) {
	if !hasSomethingToSay(e) {
		return
	}
	if tr.Container == "" && tr.Silence == "" {
		return
	}

	fmt.Fprintf(w, "  %s\n", transcriptHeading(tr))
	if tr.Silence != "" {
		fmt.Fprintf(w, "      (%s)\n", tr.Silence)
		return
	}
	writeQuotedLines(w, tr, "      ", "… %d line(s) elided …")
	if tr.Overlap > 0 {
		fmt.Fprintf(w, "      %s\n", overlapNote(tr))
	}
	fmt.Fprintf(w, "      whole transcript — `proximo errors transcript %s`\n", e.ID)
}

// writeQuotedLines renders the container's own output: the head, the declared
// elision, then the tail. Both renderings of a Transcript go through here, so
// the elision can never be declared in one and dropped from the other.
func writeQuotedLines(w io.Writer, tr transcript.Transcript, indent, elision string) {
	for _, line := range tr.Head {
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
	if tr.Dropped > 0 {
		fmt.Fprintf(w, "%s"+elision+"\n", indent, tr.Dropped)
	}
	for _, line := range tr.Tail {
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

// overlapNote is what proximo says instead of attributing a line.
func overlapNote(tr transcript.Transcript) string {
	return fmt.Sprintf("%d other request(s) overlapped this one on %s — these lines are the window, not this request",
		tr.Overlap, tr.Container)
}

// transcriptHeading names the container quoted and, when the service has more
// than one, how many replicas it has. Without the count an agent reads "happens
// only sometimes" as a race condition when the cause is one replica running
// stale config.
func transcriptHeading(tr transcript.Transcript) string {
	if tr.Container == "" {
		return "transcript:"
	}
	if tr.Replicas > 1 {
		return fmt.Sprintf("transcript of %s (1 of %d replicas):", tr.Container, tr.Replicas)
	}
	return "transcript of " + tr.Container + ":"
}

// credentialNotice is what proximo owes in place of redacting: redacting is
// interpreting, and a redactor covering most patterns produces false confidence
// exactly where an unrecognised format slips through.
const credentialNotice = "A transcript is the application's own output, quoted with no redaction: it may carry credentials or personal data. Check before pasting it anywhere."

// writeListing renders every Exchange, and states once — never per Exchange —
// that a Transcript is raw application output. proximo redacts nothing, and
// saying so is what it owes instead: a redactor covering most patterns produces
// false confidence exactly where an unrecognised format slips through.
func writeListing(w io.Writer, exchanges []inspect.Exchange, quoted map[string]transcript.Transcript, show detail) {
	anyQuoted := false
	for _, e := range exchanges {
		tr := quoted[e.ID]
		writeExchange(w, e, tr, show)
		if hasSomethingToSay(e) && !tr.Empty() {
			anyQuoted = true
		}
	}
	if anyQuoted {
		fmt.Fprintln(w, credentialNotice)
	}
}

func visibleBreadcrumbs(crumbs []inspect.Breadcrumb, show detail) []inspect.Breadcrumb {
	if show == everything {
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

func frameLocation(f inspect.Frame) string {
	loc := orPlaceholder(f.File) + ":" + strconv.Itoa(f.Line)
	if f.Col > 0 {
		loc += ":" + strconv.Itoa(f.Col)
	}
	return loc
}

func orPlaceholder(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func formatStatus(code int) string {
	if code == 0 {
		return "---"
	}
	return strconv.Itoa(code)
}

func formatDuration(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}

// parseSince turns --since into the instant a window starts at. It takes a
// duration for a person asking "the last quarter hour", and an absolute RFC 3339
// instant for an agent that knows exactly when it saved the file it is asking
// about. There is deliberately no cursor and no persisted state: the agent knows
// when it looked, proximo does not.
func parseSince(v string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(v); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("cannot read --since %q: give a duration back from now (15m, 2h) or an absolute RFC 3339 instant (2026-08-31T10:30:00Z)", v)
}

// newTranscriptReader opens the Docker connection a join needs. The socket is
// deliberately not mounted into the hop — it is the one stack service the
// browser can reach — so the join happens here, where Docker is already at hand.
func newTranscriptReader(ctx context.Context) (*transcript.Reader, func(), error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot reach Docker, which is where a transcript is read back from: %w", err)
	}
	r, err := transcript.NewReader(ctx, cli)
	if err != nil {
		cli.Close()
		return nil, nil, err
	}
	return r, func() { cli.Close() }, nil
}

// hopExchanges asks the hop for what it recorded. Best-effort: every route now
// produces an Access record from Traefik's log, so a hop that is not up costs
// the Client reports of inspected routes, not the listing.
func hopExchanges(since time.Time) []inspect.Exchange {
	// The hop windows by duration. A --since in the future would invert into a
	// negative one, which the hop reads as "no bound" — ask for everything and
	// let selectExchanges apply the real window, which it does by activity anyway.
	window := time.Since(since)
	if window < 0 {
		window = 0
	}
	q := url.Values{"since": {window.String()}, "all": {"1"}}
	var out []inspect.Exchange
	if err := getJSON("/exchanges?"+q.Encode(), &out); err != nil {
		return nil
	}
	return out
}

// listingNotes are the things a listing has to say about itself, whatever it
// contains and however it is read. Both are causes a listing cannot show on its
// own, and both matter most when it is empty — which is exactly where they used
// to be skipped.
//
// Neither carries a Remedy. Only a Report does: `proximo doctor` runs the Checks
// and prints the command, and `proximo errors` is an inventory. Naming the Check
// is what an agent needs, since a Remedy that changes the host is the
// developer's to run in any case.
func listingNotes(ctx context.Context, host string, empty bool) []string {
	var notes []string
	if warning := contestedHostWarning(ctx, host); warning != "" {
		notes = append(notes, warning)
	}
	// Asked only when there is nothing to show: it is a Docker round trip, and
	// version skew is the one cause of an empty listing that has nothing to do
	// with the developer's code.
	if empty {
		if on, err := docker.StackRecordsAccessLog(ctx); err == nil && !on {
			notes = append(notes, warnPrefix+"This stack records no access log, so no route produces an Exchange. That is why this is empty — version skew, not an absence of errors. `proximo doctor` reports it as a failed Check, with the Remedy.")
		}
	}
	return notes
}

// contestedHostWarning says when the bare host asked about is one several
// containers claim. The listing would otherwise look like the route is flapping,
// when the cause is that the name is not the one container's to keep.
func contestedHostWarning(ctx context.Context, host string) string {
	if host == "" {
		return ""
	}
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	routes, err := docker.Routes(ctx, cfg.TLD)
	if err != nil {
		return ""
	}
	for _, rt := range routes {
		if rt.Host == host && rt.Collision {
			return fmt.Sprintf("%s%s is claimed by more than one container. Ask on the qualified host instead — it is the name a collision cannot move — and see `proximo status`.",
				warnPrefix, host)
		}
	}
	return ""
}

// newErrorsTranscriptCmd prints the whole of one container's output for one
// Exchange. Unlike `dom`, it goes to stdout: a transcript is text to read and
// pipe, not hundreds of kilobytes to grep.
func newErrorsTranscriptCmd() *cobra.Command {
	var (
		out   string
		since string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "transcript <exchange-id>",
		Short: "Print what the container wrote while one Exchange was live",
		Long: "Quotes the serving container's own output for one Exchange, verbatim and " +
			"uncapped by default.\n\nIt is raw application output: it may carry " +
			"credentials or personal data, and proximo redacts nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cutoff, err := parseSince(since, time.Now())
			if err != nil {
				return err
			}
			r, closeDocker, err := newTranscriptReader(cmd.Context())
			if err != nil {
				return err
			}
			defer closeDocker()

			logged, err := r.Access(cmd.Context(), cutoff)
			if err != nil {
				return err
			}
			all := r.Merge(logged, hopExchanges(cutoff))
			var found *inspect.Exchange
			for i, e := range all {
				if e.ID == args[0] {
					found = &all[i]
					break
				}
			}
			if found == nil {
				return fmt.Errorf("no Exchange %s in the window --since %s covers — widen it, or list them again with `proximo errors` (identities are derived, so they are stable across invocations)",
					args[0], since)
			}

			tr := r.JoinOne(cmd.Context(), *found, all, limit)
			var b strings.Builder
			writeWholeTranscript(&b, *found, tr)
			if out != "" {
				// 0600: this is the project's own output, and the command's own
				// help says it may carry credentials.
				return os.WriteFile(out, []byte(b.String()), 0o600)
			}
			_, err = io.WriteString(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "write to this path instead of stdout")
	cmd.Flags().StringVar(&since, "since", "15m", "the window the Exchange was found in (see `proximo errors --since`)")
	cmd.Flags().IntVar(&limit, "limit", 1<<20, "cap the transcript at this many bytes")
	return cmd
}

// writeWholeTranscript renders one Transcript on its own, with the Exchange that
// scoped it named above it so the quote is never read out of context.
func writeWholeTranscript(w io.Writer, e inspect.Exchange, tr transcript.Transcript) {
	fmt.Fprintf(w, "# %s  %s %s  →  %s  %s  (%s)\n",
		e.At.Local().Format("15:04:05"), e.Method, e.Path, formatStatus(e.Status), formatDuration(e.Duration), e.Host)
	fmt.Fprintf(w, "# %s\n", transcriptHeading(tr))
	if tr.Overlap > 0 {
		fmt.Fprintf(w, "# %s\n", overlapNote(tr))
	}
	fmt.Fprintln(w, "# "+credentialNotice)
	if tr.Silence != "" {
		fmt.Fprintf(w, "\n(%s)\n", tr.Silence)
		return
	}
	fmt.Fprintln(w)
	writeQuotedLines(w, tr, "", "… %d line(s) elided — raise --limit to see them …")
}
