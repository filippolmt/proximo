package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/filippolmt/proximo/internal/transcript"
)

// row is one line of the listing: an Exchange, or an Incident. There is one
// listing and one time order, because time order is the only thing tying the
// 14:05:09 checkout to the worker that died seven seconds earlier — and a
// developer's question is never "was it the request or the worker".
//
// Which of the two it holds is asked in exactly one place, render: a listing that
// dispatched on it twice would be two listings wearing one order.
type row struct {
	exchange *inspect.Exchange
	incident *docker.Incident
}

// at is the instant a row sorts by. An Exchange follows its Activity — a page
// served ten minutes ago that threw just now is fresher news — and an Incident
// is the instant the runtime declared it.
func (r row) at() time.Time {
	if r.incident != nil {
		return r.incident.At
	}
	return r.exchange.Activity()
}

// render writes the row, and reports whether it quoted a Transcript — which is
// what the listing owes its credential notice for.
func (r row) render(w io.Writer, quoted map[string]transcript.Transcript, show detail) (didQuote bool) {
	if r.incident != nil {
		tr := quoted[r.incident.ID]
		writeIncident(w, *r.incident, tr)
		return !tr.Empty()
	}
	e := *r.exchange
	tr := quoted[e.ID]
	writeExchange(w, e, tr, show)
	return e.Interesting() && !tr.Empty()
}

// mergeRows folds the two halves of a listing into one, most recent first.
func mergeRows(exchanges []inspect.Exchange, incidents []docker.Incident) []row {
	rows := make([]row, 0, len(exchanges)+len(incidents))
	for i := range exchanges {
		rows = append(rows, row{exchange: &exchanges[i]})
	}
	for i := range incidents {
		rows = append(rows, row{incident: &incidents[i]})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at().After(rows[j].at()) })
	return rows
}

// servicesServing is the set of services that served the Exchanges in a listing.
// It is how a --host reaches the Incidents of the container behind that host:
// the page 502s, the Exchange says the backend was unreachable, and the reason —
// the container was OOM-killed three seconds earlier — is one row above it.
//
// A backend whose container has been *removed* resolves to nothing, and the set
// is then empty: under a --host, proximo would rather show no Incident than one
// it cannot attribute to that host. Drop the --host to see them all.
func servicesServing(r *transcript.Reader, exchanges []inspect.Exchange) map[docker.Service]bool {
	out := map[docker.Service]bool{}
	for _, e := range exchanges {
		if svc := r.ServiceOfBackend(e.Backend); svc != "" {
			out[svc] = true
		}
	}
	return out
}

// onlyService keeps the Exchanges served by one service. A routed service is
// asked about the same way a routeless one is: --service names the service, and
// what it narrows is the whole listing rather than half of it.
func onlyService(r *transcript.Reader, exchanges []inspect.Exchange, service docker.Service) []inspect.Exchange {
	out := make([]inspect.Exchange, 0, len(exchanges))
	for _, e := range exchanges {
		if r.ServiceOfBackend(e.Backend) == service {
			out = append(out, e)
		}
	}
	return out
}

// resolveService turns what a developer typed into --service into the qualified
// Service proximo prints. A bare name is accepted when nothing contests it; a
// contested one has its candidates reported rather than one of them chosen, the
// position proximo already holds on a Collision and on an Overlap.
func resolveService(want string, running []docker.Service, incidents []docker.Incident) (docker.Service, error) {
	known := append([]docker.Service(nil), running...)
	for _, inc := range incidents {
		// An Incident outlives the container that produced it, so a service that
		// exited and was removed is still a name that answers.
		known = append(known, inc.Service)
	}
	match, candidates := docker.MatchService(want, known)
	if match != "" {
		return match, nil
	}
	if len(candidates) > 0 {
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.String()
		}
		return "", fmt.Errorf("--service %s is claimed by more than one project: %s. Ask for the qualified name — it is the one a listing prints, and the one a paste of it will find",
			want, strings.Join(names, ", "))
	}
	return "", fmt.Errorf("nothing proximo can see answers to --service %s. `proximo status` lists what it knows; a container with no route becomes known by carrying the label %s=true",
		want, docker.TranscriptLabel)
}

// quoteIncidents reads back what each listed Incident's container wrote in the
// window that Incident closes, keyed by Incident id — the same map an Exchange's
// Transcript lands in, because a Transcript is one thing however its window was
// fixed.
//
// all is every Incident in the window rather than the listed subset: the left
// edge of a window is the previous Incident of the same service, and one held
// back by --limit still happened.
func quoteIncidents(ctx context.Context, r *transcript.Reader, listed, all []docker.Incident, limit int) map[string]transcript.Transcript {
	out := make(map[string]transcript.Transcript, len(listed))
	for _, inc := range listed {
		out[inc.ID] = r.QuoteIncident(ctx, inc, all, limit)
	}
	return out
}

// writeIncident renders one Incident as a row of the same listing an Exchange is
// rendered into: the same instant and id, then the service and what the runtime
// declared where the method, path and status of a request would be. The request
// columns are not padded out with blanks — a hole in a column is a question, not
// information.
func writeIncident(w io.Writer, inc docker.Incident, tr transcript.Transcript) {
	fmt.Fprintf(w, "%s  %s  %s  %s\n",
		inc.At.Local().Format("15:04:05"), inc.ID, inc.Service, inc.Describe())
	if inc.Container != "" && inc.Container != inc.Service.String() {
		fmt.Fprintf(w, "  container %s\n", inc.Container)
	}
	writeTranscriptLines(w, tr, inc.ID)
	fmt.Fprintln(w)
}

// incidentsNote is what a listing owes when the second source could not be read.
// The reader is an agent that will not run `proximo doctor` to learn a one-word
// answer, so the Remedy is handed over on the spot: an absent Incident and an
// unreachable Incident store look identical, and one of them means a
// restart-looping worker is going unreported.
func incidentsNote(ctx context.Context, err error) string {
	if err == nil {
		return ""
	}
	info, statusErr := docker.StackStatus(ctx)
	return incidentsNoteFor(statusErr == nil && !info.Running)
}

// incidentsNoteFor is the wording, given which of the two causes it is. A stack
// that is down and a stack too old to publish Incidents produce the same silence
// and take different Remedies, so the note names one rather than hedging.
func incidentsNoteFor(stackDown bool) string {
	if stackDown {
		return warnPrefix + "The stack is not running, so no Incident is being recorded: nothing here can say whether a container exited or restarted. Run `proximo up`."
	}
	return warnPrefix + "This stack records no Incident, so nothing here can say whether a container exited, restarted or was OOM-killed — version skew, not an absence of Incidents. Run `proximo update` (`proximo doctor` reports it as a failed Check too)."
}

// watcherRestartNote is what an empty Incident listing owes: the store lives in
// the watcher's memory, so "nothing happened" and "it was all thrown away" are
// two very different answers and look identical from the output. The hop's
// listing already says this for Client reports; the same is true here, and
// `proximo up` is an easy thing to have done by accident.
func watcherRestartNote(listing docker.IncidentListing, empty bool) string {
	if !empty || listing.Started.IsZero() {
		return ""
	}
	if uptime := time.Since(listing.Started); uptime < 10*time.Minute {
		return fmt.Sprintf("%sNo Incident, and the watcher restarted %s ago — the Incidents it held were in memory only. Anything the runtime declared before that is gone.",
			warnPrefix, uptime.Round(time.Second))
	}
	return ""
}

// writeReading is what proximo says instead of nothing when a service produced
// neither an Exchange nor an Incident: the Reading it can take, and a refusal to
// draw the conclusion from it.
//
// This is the whole of what can honestly be said about a live container that is
// not progressing. An idle consumer and a stuck one are identical from out here —
// telling them apart means knowing whether there is work queued, which is the
// project's own business and the dependency ADR 0006 refused. So proximo reports
// and does not judge, the way a Check reports and never repairs.
func writeReading(w io.Writer, rd docker.Reading) {
	if rd.Empty() {
		return
	}
	fmt.Fprintf(w, "What proximo can see of %s right now: %s.\n", rd.Container, rd.Describe())
	fmt.Fprintln(w, "Whether that is wrong is not proximo's to say: a consumer with nothing to do and one blocked on a slow query look the same from outside the container, and only the project knows whether work was waiting.")
}
