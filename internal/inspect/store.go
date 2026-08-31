// Package inspect implements the proximo hop that sits in front of routes under
// Inspection: it proxies to the backend, injects the reporting agent into HTML
// responses, ingests what the agent sends back, and keeps the resulting
// Exchanges in memory. See docs/adr/0001-inspection-injects-into-the-response-path.md.
package inspect

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// Frame is one entry of a Client report's stack trace.
type Frame struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"col,omitempty"`
	Func string `json:"func,omitempty"`
}

// Breadcrumb is one thing the page did before a Client report: a console call, a
// request it made, a click, a navigation.
type Breadcrumb struct {
	At       time.Time `json:"at"`
	Category string    `json:"category"`
	Level    string    `json:"level"`
	Message  string    `json:"message"`
}

// Report is one Client report: what the browser observed, with the Breadcrumbs
// that led up to it.
type Report struct {
	At          time.Time    `json:"at"`
	Type        string       `json:"type"`
	Message     string       `json:"message"`
	Level       string       `json:"level"`
	Frames      []Frame      `json:"frames,omitempty"`
	Stack       string       `json:"stack,omitempty"` // as the browser formatted it
	Breadcrumbs []Breadcrumb `json:"breadcrumbs,omitempty"`
}

// Exchange is one request through an inspected route — the Access record — joined
// to the Client reports raised while the page it served was live.
type Exchange struct {
	ID       string        `json:"id"`
	At       time.Time     `json:"at"`
	Host     string        `json:"host"`
	Method   string        `json:"method"`
	Path     string        `json:"path"`
	Status   int           `json:"status"`
	Duration time.Duration `json:"duration_ns"`
	Bytes    int64         `json:"bytes"`
	Warnings []string      `json:"warnings,omitempty"`
	Reports  []Report      `json:"reports,omitempty"`

	// Backend is the address — host:port — of the container this request was
	// served by, in the form Traefik's access log names a server by. It is what
	// a Transcript is looked up from, so an Access record without it can only be
	// joined to the wrong container's output, or to none.
	Backend string `json:"backend,omitempty"`

	// Suppressed counts the reports this Exchange saw beyond maxReports. They are
	// bounded, not discarded quietly: a page in a render loop must not be able to
	// evict every other Exchange, and a developer must still be told it happened.
	Suppressed int `json:"suppressed,omitempty"`

	// HasSnapshot tells a consumer of the JSON that `proximo errors dom` would
	// return something for this Exchange. Set by List.
	HasSnapshot bool `json:"has_snapshot"`

	// Snapshot is the page's DOM as it stood when the first Client report was
	// raised. It is never rendered inline, and never serialized with the rest:
	// the CLI asks for it separately and writes it to a file.
	Snapshot []byte `json:"-"`
}

// size is the Store's accounting weight for one Exchange. It counts the payloads
// that actually vary by orders of magnitude and charges a flat rate for the rest.
//
// ponytail: rough estimate, not the real heap cost. Exact accounting would mean
// walking every string; swap it in if the buffer turns out to over- or
// under-shoot its budget in practice.
func (e *Exchange) size() int64 {
	n := int64(512 + len(e.Snapshot))
	for _, r := range e.Reports {
		n += int64(256 + len(r.Message) + len(r.Stack) + 128*len(r.Frames) + 128*len(r.Breadcrumbs))
	}
	return n
}

// Store holds recent Exchanges in memory, bounded by total bytes rather than by
// count: one Exchange carrying a DOM Snapshot can outweigh a thousand without
// one. Nothing is written to disk — a Client report carries whatever the page
// held, and in development that is routinely real data.
type Store struct {
	mu     sync.Mutex
	budget int64
	used   int64
	items  []*Exchange // oldest first
	byID   map[string]*Exchange

	// routeWarnings are the warnings that describe the route rather than one
	// request — today, that proximo had to relax the page's policy. They are kept
	// outside the ring buffer on purpose: `proximo status` has to show them for as
	// long as the route is inspected, not only while some Exchange survives
	// eviction.
	routeWarnings map[string][]string // host -> warnings, deduplicated

	// started is when this Store came up. Exchanges do not survive a restart, so
	// an empty list means one of two very different things — nothing went wrong,
	// or everything was just thrown away — and a developer must be told which.
	started time.Time
}

// DefaultBudget is how much memory the Store is allowed before it starts
// evicting the oldest Exchanges.
const DefaultBudget = 64 << 20

// maxReports is how many Client reports one Exchange keeps. Past it the rest are
// counted, not kept: one looping page would otherwise push every other Exchange
// out of the buffer.
const maxReports = 50

// NewStore returns a Store bounded to budget bytes. A budget of zero or less
// uses DefaultBudget.
func NewStore(budget int64) *Store {
	if budget <= 0 {
		budget = DefaultBudget
	}
	return &Store{
		budget: budget, byID: map[string]*Exchange{},
		routeWarnings: map[string][]string{}, started: time.Now(),
	}
}

// NewID mints the correlation id that joins the two halves of an Exchange.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on any supported platform; falling back to
		// the clock keeps a broken id from taking the whole proxy down.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))[:16]
	}
	return hex.EncodeToString(b[:])
}

// Add records the server half of an Exchange, evicting the oldest entries until
// the Store is back inside its budget.
func (s *Store) Add(e *Exchange) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, e)
	s.byID[e.ID] = e
	s.used += e.size()
	s.evict()
}

// Complete fills in the half of an Access record that is only known once the
// response has gone out. The Exchange was registered before that, so the page it
// served could already have reported against it.
func (s *Store) Complete(id string, status int, took time.Duration, bytes int64, warnings []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[id]
	if !ok {
		return
	}
	before := e.size()
	e.Status, e.Duration, e.Bytes = status, took, bytes
	e.Warnings = append(e.Warnings, warnings...)
	s.used += e.size() - before
	s.evict()
}

// Attach adds a Client report — and, the first time one carries it, the DOM
// Snapshot — to the Exchange that served the page. It reports whether the id was
// known: an unknown id means the Exchange has already been evicted.
func (s *Store) Attach(id string, in ingested) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.byID[id]
	if !ok {
		return false
	}
	if len(e.Reports) >= maxReports {
		e.Suppressed++
		return true
	}
	before := e.size()
	e.Reports = append(e.Reports, in.Report)
	if len(in.Snapshot) > 0 && len(e.Snapshot) == 0 {
		e.Snapshot = in.Snapshot
	}
	s.used += e.size() - before
	s.evict()
	return true
}

// NoteRouteWarning records a warning against a host, ignoring one already held.
func (s *Store) NoteRouteWarning(host, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.routeWarnings[host] {
		if w == msg {
			return
		}
	}
	s.routeWarnings[host] = append(s.routeWarnings[host], msg)
}

// RouteWarnings returns the per-host warnings, for `proximo status`.
func (s *Store) RouteWarnings() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]string, len(s.routeWarnings))
	for host, ws := range s.routeWarnings {
		out[host] = append([]string(nil), ws...)
	}
	return out
}

// evict drops the oldest Exchanges until the Store fits its budget. The most
// recent one is always kept, however big it is: a single oversized Exchange is
// still the one the developer is asking about.
func (s *Store) evict() {
	for s.used > s.budget && len(s.items) > 1 {
		old := s.items[0]
		s.items = s.items[1:]
		delete(s.byID, old.ID)
		s.used -= old.size()
	}
}

// Query narrows what List returns. The zero value holds nothing back.
type Query struct {
	Host  string        // exact host match; empty matches any
	Since time.Duration // only Exchanges newer than this; zero means no bound
	Limit int           // most recent N; zero or less means no bound

	// OnlyProblems drops the Exchanges with nothing to say. It exists because
	// the alternative buries the one page that broke under every clean request
	// that did not, and the limit then cuts the interesting one first.
	OnlyProblems bool
}

// Interesting reports whether an Exchange is worth a developer's attention: the
// browser reported something, proximo had to warn about something, or the stack
// itself answered with a failure.
func (e Exchange) Interesting() bool {
	return len(e.Reports) > 0 || len(e.Warnings) > 0 || e.Status >= 400
}

// activity is when something last happened on an Exchange. A page served ten
// minutes ago that threw just now is fresher news than a request served since,
// so ordering by the Exchange alone would sink it.
func (e *Exchange) activity() time.Time {
	at := e.At
	for _, r := range e.Reports {
		if r.At.After(at) {
			at = r.At
		}
	}
	return at
}

// List returns the Exchanges matching q, most recently active first.
func (s *Store) List(q Query) []Exchange {
	s.mu.Lock()
	defer s.mu.Unlock()

	var cutoff time.Time
	if q.Since > 0 {
		cutoff = time.Now().Add(-q.Since)
	}

	out := make([]Exchange, 0, len(s.items))
	for _, e := range s.items {
		if q.Host != "" && e.Host != q.Host {
			continue
		}
		if !cutoff.IsZero() && e.activity().Before(cutoff) {
			continue
		}
		if q.OnlyProblems && !e.Interesting() {
			continue
		}
		item := *e
		item.HasSnapshot = len(e.Snapshot) > 0
		item.Snapshot = nil
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].activity().After(out[j].activity()) })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}

// Started reports when the Store came up, so an empty listing can say whether a
// restart is the reason.
func (s *Store) Started() time.Time { return s.started }

// Snapshot returns the DOM captured for one Exchange, or nil when there is none.
func (s *Store) Snapshot(id string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.byID[id]; ok {
		return e.Snapshot
	}
	return nil
}
