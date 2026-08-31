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

// selectIncidents narrows a listing of Incidents the way inspect.Select narrows
// Exchanges, and to the same flags: --service, --since, --limit and the default
// that holds back what has nothing to say.
//
// services is the set a --host narrowed to — an Incident carries no host, so the
// only honest way to keep it under --host is by the service that served the
// requests on that host. A nil set means no host was asked for.
func selectIncidents(all []docker.Incident, service string, services map[string]bool, since time.Time, limit int, onlyProblems bool) []docker.Incident {
	out := make([]docker.Incident, 0, len(all))
	for _, inc := range all {
		switch {
		case service != "" && inc.Service != service:
			continue
		case service == "" && services != nil && !services[inc.Service]:
			continue
		case !since.IsZero() && inc.At.Before(since):
			continue
		// An explicit --service holds nothing back: the developer named the
		// service, so a transition to unhealthy is an answer rather than noise.
		case onlyProblems && service == "" && !inc.Interesting():
			continue
		}
		out = append(out, inc)
	}
	docker.SortIncidents(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// servicesServing is the set of services that served the Exchanges in a listing.
// It is how a --host reaches the Incidents of the container behind that host:
// the page 502s, the Exchange says the backend was unreachable, and the reason —
// the container was OOM-killed three seconds earlier — is one row above it.
//
// A backend whose container has been *removed* resolves to nothing, and the set
// is then empty: under a --host, proximo would rather show no Incident than one
// it cannot attribute to that host. Drop the --host to see them all.
func servicesServing(r *transcript.Reader, exchanges []inspect.Exchange) map[string]bool {
	out := map[string]bool{}
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
func onlyService(r *transcript.Reader, exchanges []inspect.Exchange, service string) []inspect.Exchange {
	out := make([]inspect.Exchange, 0, len(exchanges))
	for _, e := range exchanges {
		if r.ServiceOfBackend(e.Backend) == service {
			out = append(out, e)
		}
	}
	return out
}

// resolveService turns what a developer typed into --service into the qualified
// name proximo prints. A bare name is accepted when nothing contests it; a
// contested one has its candidates reported rather than one of them chosen, the
// position proximo already holds on a Collision and on an Overlap.
func resolveService(want string, running []string, incidents []docker.Incident) (string, error) {
	known := append([]string(nil), running...)
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
		return "", fmt.Errorf("--service %s is claimed by more than one project: %s. Ask for the qualified name — it is the one a listing prints, and the one a paste of it will find",
			want, strings.Join(candidates, ", "))
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
	if inc.Container != "" && inc.Container != inc.Service {
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
