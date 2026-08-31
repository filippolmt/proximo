package docker

import (
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
	Service   string `json:"service"`
	Container string `json:"container"`

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
	byService map[string][]Incident
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
		byService: map[string][]Incident{}, oom: map[string]time.Time{},
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
		// and two rows for one kill is a question rather than information. Any
		// entry older than the window is dropped on the way in — a kill with no
		// exit behind it would otherwise sit in the map for the life of the stack.
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

	inc.ID = inspect.DeriveIDParts(inc.Service, inc.At.UTC().Format(time.RFC3339Nano), string(inc.Kind))
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
func MatchService(want string, known []string) (match string, candidates []string) {
	want = strings.TrimSpace(want)
	if want == "" {
		return "", nil
	}
	if strings.Contains(want, "/") {
		if slices.Contains(known, want) {
			return want, nil
		}
		return "", nil
	}
	for _, k := range known {
		if k == want || strings.HasSuffix(k, "/"+want) {
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
func ServiceOf(labels map[string]string, containerName string) string {
	svc := strings.TrimSpace(labels[composeServiceLabel])
	switch ns := namespaceOf(labels); {
	case svc == "":
		return containerName
	case ns == "":
		return svc
	default:
		return ns + "/" + svc
	}
}

// ServiceKey is ServiceOf for a container listing, so the watcher (which sees
// only an event's attributes) and every host-side reader name one service the
// same way.
func ServiceKey(c container.Summary) string {
	return ServiceOf(c.Labels, primaryName(c))
}

// isObserved reports whether proximo may record Incidents about a container: one
// it routes, or one that asked to be observed with proximo.transcript. Incidents
// are orthogonal to routing — the label only makes an otherwise invisible
// container known, and the case that matters most is a routed container whose
// 502 is explained by the OOM kill three seconds earlier.
func isObserved(labels map[string]string) bool {
	if _, isStack := labels[roleLabel]; isStack {
		return false
	}
	return isRoutedLabels(labels) || isTruthyLabel(labels, proximoTranscriptLabel)
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
