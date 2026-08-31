package docker

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
)

// A Service is one named part of a Project — a Compose service — qualified by
// its Namespace: `shop/worker`. It is a type rather than a string because the
// bare-vs-qualified rule travels with it, and because a resolved Service and
// whatever a developer typed into --service are not the same thing: the second
// is a question, and only MatchService turns it into an answer.
type Service string

// Bare is the service name without its Namespace — the short name a developer
// writes when nothing contests it, and the one MatchService accepts when nothing
// contests it. The Namespace half needs no accessor: it is only ever printed as
// part of the qualified name.
func (s Service) Bare() string {
	if _, bare, ok := strings.Cut(string(s), "/"); ok {
		return bare
	}
	return string(s)
}

func (s Service) String() string { return string(s) }

// An Incident is a fact the runtime declares about a container: it exited
// non-zero, it was restarted, the kernel killed it, it turned unhealthy. Never a
// line that was read — proximo may remember what the runtime declares, never
// what the project wrote. See
// docs/adr/0007-proximo-remembers-what-the-runtime-declares.md.
type Incident struct {
	// ID is derived — service, instant and kind — rather than minted, so the
	// same Incident has the same id across two invocations, and the id stays
	// computable from what is on screen after proximo has forgotten it.
	ID string    `json:"id"`
	At time.Time `json:"at"`

	// Service is the qualified service the container belongs to (`shop/worker`),
	// which is what a developer asks about: "what did the worker say" is a
	// question about the service, and "what did worker-2 say" one you can only
	// ask after seeing all three.
	Service   Service `json:"service"`
	Container string  `json:"container"`

	Kind     IncidentKind `json:"kind"`
	ExitCode int          `json:"exit_code,omitempty"`
	// OOM says the kernel chose this exit. It is only ever set from the runtime's
	// own oom event: exit 137 is equally what a plain `docker kill` produces, and
	// claiming the kernel did it would be interpreting.
	OOM bool `json:"oom,omitempty"`
}

// IncidentKind is what the runtime declared. The set is closed on purpose: every
// member is a statement Docker makes, and the first text matcher added here
// would turn an Incident into a reading of the project's own output.
type IncidentKind string

const (
	IncidentExited    IncidentKind = "exited"
	IncidentRestarted IncidentKind = "restarted"
	IncidentUnhealthy IncidentKind = "unhealthy"
)

// Interesting reports whether an Incident belongs in the default listing —
// mirroring inspect.Exchange.Interesting, since the two share one listing.
//
// Unhealthy is deliberately out of it: a worker waiting on postgres is unhealthy
// for twenty seconds on every `compose up`, and a listing full of that noise
// stops being read exactly when a developer needs it. It stays visible under an
// explicit --service.
func (i Incident) Interesting() bool { return i.Kind != IncidentUnhealthy }

// Describe is the Incident in the words the runtime used.
func (i Incident) Describe() string {
	switch i.Kind {
	case IncidentExited:
		s := "exited " + strconv.Itoa(i.ExitCode)
		if i.OOM {
			s += " (OOM-killed)"
		}
		return s
	case IncidentRestarted:
		return "restarted"
	case IncidentUnhealthy:
		return "turned unhealthy"
	}
	return string(i.Kind)
}

// A Reading is what the runtime says about a container *right now*, where an
// Incident is dated history: whether it is running and since when, what its
// healthcheck says, how many times it has been restarted, and the instant its
// output last moved. Measured, never interpreted — when a stream moved is the
// runtime's to declare, what the line said is the project's.
//
// It is what proximo has to offer about a container that is alive and may not be
// progressing, and it stops short of the conclusion on purpose: an idle consumer
// and one blocked on a slow query read identically from out here.
type Reading struct {
	Container string `json:"container"`
	Running   bool   `json:"running"`
	// Since is when the container last started (not when it was created): for a
	// worker that has been restarted, the second would overstate its uptime.
	Since       time.Time `json:"since,omitzero"`
	Healthcheck string    `json:"healthcheck,omitempty"`
	Restarts    int       `json:"restarts,omitempty"`
	Replicas    int       `json:"replicas,omitempty"`

	// LastWrote is when the container last wrote a line — the instant, never the
	// line. WroteNothing is set instead when the stream was read and had nothing
	// in it, which is a fact about the project rather than about the read. With
	// neither set the stream could not be read at all, and Unread says so: a
	// reading nobody could take is not a reading of zero.
	LastWrote    time.Time `json:"last_wrote,omitzero"`
	WroteNothing bool      `json:"wrote_nothing,omitempty"`

	// Unread names what could not be read, and is empty when everything was. Every
	// absence in proximo says which absence it is; a missing reading presented as
	// a measurement is the one that sends a developer after the wrong thing.
	Unread []string `json:"unread,omitempty"`
}

// Empty reports whether there is nothing to report — no container answered to the
// Service at all.
func (rd Reading) Empty() bool { return rd.Container == "" }

// Describe renders the Reading as one clause per fact, in the order a developer
// checks them. What could not be read is named rather than left as a gap.
func (rd Reading) Describe() string {
	var parts []string
	switch {
	case !rd.Running:
		parts = append(parts, "not running")
	case rd.Since.IsZero():
		parts = append(parts, "running")
	default:
		parts = append(parts, "running for "+time.Since(rd.Since).Round(time.Second).String())
	}
	if rd.Healthcheck != "" && rd.Healthcheck != string(container.NoHealthcheck) {
		parts = append(parts, "its healthcheck says "+rd.Healthcheck)
	}
	switch rd.Restarts {
	case 0:
	case 1:
		parts = append(parts, "restarted once")
	default:
		parts = append(parts, fmt.Sprintf("restarted %d times", rd.Restarts))
	}
	if rd.Replicas > 1 {
		parts = append(parts, fmt.Sprintf("%d replicas running", rd.Replicas))
	}
	parts = append(parts, rd.Unread...)
	// The stream clause goes last: it is the one a developer reads for, and the
	// "and" that introduces it has to end the sentence.
	switch {
	case !rd.LastWrote.IsZero():
		parts = append(parts, fmt.Sprintf("and it last wrote %s ago", time.Since(rd.LastWrote).Round(time.Second)))
	case rd.WroteNothing:
		parts = append(parts, "and it has written nothing at all")
	}
	return strings.Join(parts, ", ")
}

// Retention defaults. The cap is per service rather than a global byte budget:
// an Incident is tens of bytes, so memory is never the risk — a worker
// restarting every three seconds evicting another service's only Incident is.
const (
	// DefaultIncidentsPerService is how many Incidents one service keeps.
	DefaultIncidentsPerService = 20
	// DefaultIncidentMaxAge is how far back the store remembers. Past it an
	// Incident is answering a question nobody is still asking, and the container
	// output it would anchor has rotated away regardless.
	DefaultIncidentMaxAge = 24 * time.Hour
	// oomWindow is how long an oom event may precede the exit it caused. The
	// kernel kills and the process dies in the same breath; a wider window would
	// start attributing unrelated kills to it.
	oomWindow = 10 * time.Second
)

// IncidentStore holds the Incidents the watcher observed, in memory. It lives in
// the watcher because that is the one stack service with both the Docker socket
// and the event subscription — the hop deliberately has neither, since it sits
// in the request path.
type IncidentStore struct {
	mu         sync.Mutex
	perService int
	maxAge     time.Duration
	// byService keys the retention: the cap is spent per service, so a looping
	// worker cannot push another service's only Incident out. Oldest first.
	byService map[Service][]Incident
	// oom remembers a container's last oom event so the die it precedes can carry
	// it, keyed by container id.
	oom map[string]time.Time
	// started is when this store came up. Incidents do not survive a restart, so
	// an empty listing means one of two very different things — nothing happened,
	// or it was all thrown away — and a developer must be told which.
	started time.Time
}

// NewIncidentStore returns a store keeping perService Incidents per service for
// maxAge. Zero or less for either uses the default.
func NewIncidentStore(perService int, maxAge time.Duration) *IncidentStore {
	if perService <= 0 {
		perService = DefaultIncidentsPerService
	}
	if maxAge <= 0 {
		maxAge = DefaultIncidentMaxAge
	}
	return &IncidentStore{
		perService: perService, maxAge: maxAge,
		byService: map[Service][]Incident{}, oom: map[string]time.Time{},
		started: time.Now(),
	}
}

// Started reports when the store came up, so an empty listing can say whether a
// restart is the reason.
func (s *IncidentStore) Started() time.Time { return s.started }

// Observe turns one Docker event into an Incident and records it, reporting
// whether it recorded one. Everything the runtime does not declare an Incident
// about — a start, a clean exit, a container proximo knows nothing about — is
// dropped here.
func (s *IncidentStore) Observe(msg events.Message) (Incident, bool) {
	if msg.Type != events.ContainerEventType || !isObserved(msg.Actor.Attributes) {
		return Incident{}, false
	}
	at := eventTime(msg)
	name := msg.Actor.Attributes["name"]

	s.mu.Lock()
	defer s.mu.Unlock()

	inc := Incident{
		At: at, Container: name,
		Service: ServiceOf(msg.Actor.Attributes, name),
	}
	switch msg.Action {
	case events.ActionOOM:
		// Not an Incident of its own: it is the reason for the exit that follows,
		// and two rows for one kill is a question rather than information. Stale
		// entries are dropped here, as each new oom arrives — so a kill that never
		// produced an exit (the kernel took a child, not PID 1) is held until the
		// next oom on any container. One instant per container is the whole cost,
		// which is why the prune is not chased any harder than that.
		for id, when := range s.oom {
			if at.Sub(when) > oomWindow {
				delete(s.oom, id)
			}
		}
		s.oom[msg.Actor.ID] = at
		return Incident{}, false
	case events.ActionDie:
		code, err := strconv.Atoi(msg.Actor.Attributes["exitCode"])
		if err != nil || code == 0 {
			// A clean exit is not an Incident, and an exit code that cannot be
			// read is not a fact the runtime declared.
			return Incident{}, false
		}
		inc.Kind, inc.ExitCode = IncidentExited, code
		if killed, ok := s.oom[msg.Actor.ID]; ok {
			inc.OOM = at.Sub(killed) < oomWindow && at.Sub(killed) > -oomWindow
			delete(s.oom, msg.Actor.ID)
		}
	case events.ActionRestart:
		inc.Kind = IncidentRestarted
	case events.ActionHealthStatusUnhealthy:
		inc.Kind = IncidentUnhealthy
	default:
		return Incident{}, false
	}

	inc.ID = inspect.DeriveIDParts(string(inc.Service), inc.At.UTC().Format(time.RFC3339Nano), string(inc.Kind))
	if !s.record(inc) {
		return Incident{}, false
	}
	return inc, true
}

// record files an Incident under its service, dropping a replay of one already
// held and then pruning by age and by the per-service cap. It reports whether
// the Incident was new.
func (s *IncidentStore) record(inc Incident) bool {
	held := s.byService[inc.Service]
	if slices.ContainsFunc(held, func(o Incident) bool { return o.ID == inc.ID }) {
		return false
	}
	held = append(held, inc)
	sort.SliceStable(held, func(i, j int) bool { return held[i].At.Before(held[j].At) })
	kept := s.fresh(held)
	if len(kept) > s.perService {
		kept = kept[len(kept)-s.perService:]
	}
	s.byService[inc.Service] = slices.Clone(kept)
	return true
}

// fresh is held with everything past the maximum age dropped. It filters in
// place, so the caller clones when the result outlives the input.
func (s *IncidentStore) fresh(held []Incident) []Incident {
	cutoff := time.Now().Add(-s.maxAge)
	kept := held[:0]
	for _, inc := range held {
		if inc.At.After(cutoff) {
			kept = append(kept, inc)
		}
	}
	return kept
}

// List returns every Incident newer than since, most recent first. A zero
// instant means no bound.
func (s *IncidentStore) List(since time.Time) []Incident {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Incident
	for svc, held := range s.byService {
		// Aged out since it was recorded: the prune happens here as well as on
		// the way in, because a store nobody writes to still gets older.
		kept := slices.Clone(s.fresh(held))
		for _, inc := range kept {
			if since.IsZero() || inc.At.After(since) {
				out = append(out, inc)
			}
		}
		s.byService[svc] = kept
	}
	SortIncidents(out)
	return out
}

// IncidentQuery narrows a listing of Incidents. The zero value holds nothing
// back. It mirrors inspect.Query, because the two halves of one listing must be
// narrowed by the same flags or `--since` means two different things depending on
// which source a row came from.
type IncidentQuery struct {
	// Service is an exact match on a resolved Service; empty matches any.
	Service Service
	// Services is the set a --host narrowed to. An Incident carries no host, so
	// the only honest way to keep it under a --host is by the Service that served
	// the requests on that host. A nil set means no host was asked for; an empty
	// non-nil one means a host was asked for and nothing could be attributed to it.
	Services map[Service]bool
	// Since bounds the window; the zero instant means no bound.
	Since time.Time
	// Limit keeps the most recent N; zero or less means no bound.
	Limit int
	// OnlyProblems drops what has nothing to say — today, a transition to
	// unhealthy. An explicit Service overrides it: the developer named the
	// service, so unhealthy is an answer rather than noise.
	OnlyProblems bool
}

// SelectIncidents narrows a listing of Incidents, most recent first — the way
// inspect.Select narrows Exchanges, and to the same flags.
func SelectIncidents(all []Incident, q IncidentQuery) []Incident {
	// One service named explicitly changes two things at once: it is the only
	// filter that applies, and it holds nothing back. Asked once here rather than
	// re-tested in every arm below.
	named := q.Service != ""
	out := make([]Incident, 0, len(all))
	for _, inc := range all {
		switch {
		case named && inc.Service != q.Service:
			continue
		case !named && q.Services != nil && !q.Services[inc.Service]:
			continue
		case !q.Since.IsZero() && inc.At.Before(q.Since):
			continue
		case !named && q.OnlyProblems && !inc.Interesting():
			continue
		}
		out = append(out, inc)
	}
	SortIncidents(out)
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}

// SortIncidents orders a listing most recent first — the order a listing of
// Exchanges is already read in, since the two share one.
func SortIncidents(all []Incident) {
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
}

// PreviousIncident is the Incident of the same service that came before inc.
// It is the left edge of the window a Transcript anchored to inc quotes: for a
// restart loop that is exactly one container lifetime, which no fixed duration
// can be — a fixed one truncates the worker that wrote the useful line five
// minutes before dying and drowns the one that restarts every three seconds.
func PreviousIncident(inc Incident, all []Incident) (Incident, bool) {
	var prev Incident
	found := false
	for _, o := range all {
		if o.Service != inc.Service || o.ID == inc.ID || !o.At.Before(inc.At) {
			continue
		}
		if !found || o.At.After(prev.At) {
			prev, found = o, true
		}
	}
	return prev, found
}

// MatchService resolves what a developer typed into --service against the
// services proximo can see. A qualified name is matched exactly; a bare one is
// accepted when nothing contests it, and when something does its candidates are
// returned rather than one of them chosen — the position proximo already holds
// on a Collision and on an Overlap. Neither a match nor candidates means nothing
// answers to that name at all.
func MatchService(want string, known []Service) (match Service, candidates []Service) {
	want = strings.TrimSpace(want)
	if want == "" {
		return "", nil
	}
	if strings.Contains(want, "/") {
		if slices.Contains(known, Service(want)) {
			return Service(want), nil
		}
		return "", nil
	}
	for _, k := range known {
		if k.Bare() == want {
			candidates = append(candidates, k)
		}
	}
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", candidates
}

// ServiceOf names the Compose service a container belongs to, qualified by its
// Namespace — the same qualifier a qualified host carries, because a qualified
// service and a Namespace are one concept, not two. A container outside a
// Compose project has neither, so it names itself.
func ServiceOf(labels map[string]string, containerName string) Service {
	svc := strings.TrimSpace(labels[ComposeServiceLabel])
	switch ns := namespaceOf(labels); {
	case svc == "":
		return Service(containerName)
	case ns == "":
		return Service(svc)
	default:
		return Service(ns + "/" + svc)
	}
}

// ServiceKey is ServiceOf for a container listing, so the watcher (which sees
// only an event's attributes) and every host-side reader name one service the
// same way.
func ServiceKey(c container.Summary) Service {
	return ServiceOf(c.Labels, primaryName(c))
}

// isObserved reports whether proximo may record Incidents about a container: one
// it routes, or one that asked to be observed with proximo.transcript. Incidents
// are orthogonal to routing — the label only makes an otherwise invisible
// container known, and the case that matters most is a routed container whose
// 502 is explained by the OOM kill three seconds earlier.
func isObserved(labels map[string]string) bool {
	if _, isStack := labels[RoleLabel]; isStack {
		return false
	}
	return isRoutedLabels(labels) || isTruthyLabel(labels, TranscriptLabel)
}

// eventTime is the instant an event carries. TimeNano is what Docker sets;
// Time (seconds) is the fallback, and a message with neither is stamped on
// arrival rather than dated to the epoch.
func eventTime(msg events.Message) time.Time {
	switch {
	case msg.TimeNano > 0:
		return time.Unix(0, msg.TimeNano)
	case msg.Time > 0:
		return time.Unix(msg.Time, 0)
	}
	return time.Now()
}
