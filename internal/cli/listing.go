package cli

import (
	"io"
	"sort"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/filippolmt/proximo/internal/transcript"
)

// The shape of one `proximo errors` listing: two sources folded into one time
// order, and the narrowing that a --host or a --service applies to both halves.
// What proximo *says* about an Incident or a Reading lives in incidents.go —
// this file changes when the listing's shape does.

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

// byService groups a listing of Exchanges by the Service that served each one,
// which is the one question both halves of the narrowing ask: --service wants one
// group, and --host wants the set of keys.
//
// A backend whose container has been *removed* resolves to nothing and is left
// out. Under a --host that means an empty set, and proximo would rather show no
// Incident than one it cannot attribute to that host — drop the --host to see
// them all. Under a --service it means the Exchange is not that service's to
// claim.
func byService(r *transcript.Reader, exchanges []inspect.Exchange) map[docker.Service][]inspect.Exchange {
	out := map[docker.Service][]inspect.Exchange{}
	for _, e := range exchanges {
		if svc := r.ServiceOfBackend(e.Backend); svc != "" {
			out[svc] = append(out[svc], e)
		}
	}
	return out
}

// servicesServing is how a --host reaches the Incidents of the container behind
// that host: the page 502s, the Exchange says the backend was unreachable, and
// the reason — the container was OOM-killed three seconds earlier — is one row
// above it.
func servicesServing(grouped map[docker.Service][]inspect.Exchange) map[docker.Service]bool {
	out := make(map[docker.Service]bool, len(grouped))
	for svc := range grouped {
		out[svc] = true
	}
	return out
}
