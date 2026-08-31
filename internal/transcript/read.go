package transcript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	// grace widens the window at both ends. A framework writes the line that
	// matters as it is returning, and Traefik's instant and the container's clock
	// are not the same clock. Widening trades precision for not missing the one
	// line asked about — and what it costs is reported as an Overlap rather than
	// hidden.
	grace = time.Second
)

// Docker is the narrow slice of the Docker client this package depends on —
// exactly the three calls a join makes. *client.Client satisfies it unchanged,
// so production passes the real client and tests pass a fake. It mirrors the
// watcher's dockerAPI seam.
type Docker interface {
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
}

// Reader quotes Transcripts. It holds one container listing for the life of a
// CLI invocation: a backend address is resolved against the containers alive
// now, which is the only listing that can be checked against a request's instant.
type Reader struct {
	d      Docker
	byName map[string]container.Summary
	// tty caches whether a container's log stream is multiplexed. It is asked
	// once per container rather than once per read: it cannot change while the
	// container is running.
	tty map[string]bool
	// replicas counts the containers of each compose service, keyed the way
	// serviceKey keys them.
	replicas map[string]int
	// services are the qualified names of the Project services running now — the
	// candidates a bare --service is resolved against. proximo's own stack is
	// excluded: it is a Stack, not a Project, and nothing selects it.
	services map[string]bool
	// stackAddrs are the names proximo's own services answer to. Traefik points
	// an inspected route at the hop by its compose *service* name, while a
	// project's backend is a container name, so both forms have to be recognised.
	stackAddrs map[string]bool
}

// NewReader lists the containers a join resolves against.
//
// Stopped containers are listed too (All). A container that exited is exactly
// what an Incident is usually about — a worker that dies and is restarted, or a
// job that ran once — and Docker still answers `docker logs` for it, so leaving
// it out would report the output as gone while it is sitting right there. What a
// stopped container is *not* is a replica: the count below stays running-only,
// because "1 of 3 replicas" is a statement about what is serving now.
func NewReader(ctx context.Context, d Docker) (*Reader, error) {
	res, err := d.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	r := &Reader{
		d: d, byName: map[string]container.Summary{}, replicas: map[string]int{},
		tty: map[string]bool{}, stackAddrs: map[string]bool{}, services: map[string]bool{},
	}
	for _, c := range res.Items {
		for _, n := range c.Names {
			name := strings.TrimPrefix(n, "/")
			r.byName[name] = c
			if c.Labels[docker.RoleLabel] != "" {
				r.stackAddrs[name] = true
			}
		}
		if key := serviceKey(c); key != "" && c.State == container.StateRunning {
			r.replicas[key]++
			if c.Labels[docker.RoleLabel] == "" {
				r.services[key] = true
			}
		}
		if c.Labels[docker.RoleLabel] != "" && c.Labels[docker.ComposeServiceLabel] != "" {
			r.stackAddrs[c.Labels[docker.ComposeServiceLabel]] = true
		}
	}
	return r, nil
}

// Services are the qualified names of the Project services running now, sorted.
// They are the candidates a bare --service is resolved against: what is printed
// must be what works when pasted back, and a bare name is only accepted when
// nothing contests it.
func (r *Reader) Services() []string {
	out := make([]string, 0, len(r.services))
	for svc := range r.services {
		out = append(out, svc)
	}
	slices.Sort(out)
	return out
}

// ServiceOfBackend names the service of the container a backend address names,
// or "" when nothing running answers to it. It is what lets one selector narrow
// both halves of the listing: the Exchanges a service served, and the Incidents
// the runtime declared about it.
func (r *Reader) ServiceOfBackend(backend string) string {
	c, ok := r.byName[backendName(backend)]
	if !ok {
		return ""
	}
	return serviceKey(c)
}

// backendName is the container or service a backend address names. The address
// is `host:port` because that is the form Traefik logs a server in; the port is
// never what a Transcript is looked up by.
func backendName(backend string) string {
	name, _, _ := strings.Cut(backend, ":")
	return name
}

// serviceKey names the service a container belongs to. It is docker.ServiceKey
// so that a replica count, an Incident and a --service selector all name one
// service the same way — the watcher sees only an event's labels and this sees a
// container listing, and the two must agree.
func serviceKey(c container.Summary) string {
	return docker.ServiceKey(c)
}

// Access reads the Access records Traefik logged since t. Traefik's operational
// log shares the stream, so lines that are not JSON describing a request are
// skipped: that is the whole of the filter, and DecodeAccessLine owns it.
func (r *Reader) Access(ctx context.Context, since time.Time) ([]inspect.Exchange, error) {
	traefik, ok := r.stackContainer("traefik")
	if !ok {
		return nil, errors.New("the stack's Traefik is not running, so no Access record can be read — run `proximo up` (`proximo doctor` reports which stack services are missing)")
	}
	raw, err := r.readLogs(ctx, traefik, since, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("reading Traefik's log: %w", err)
	}
	var out []inspect.Exchange
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if e, ok := inspect.DecodeAccessLine(line); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *Reader) stackContainer(role string) (container.Summary, bool) {
	for _, c := range r.byName {
		if c.Labels[docker.RoleLabel] == role {
			return c, true
		}
	}
	return container.Summary{}, false
}

// Join returns the Transcript of each Exchange in quote, keyed by Exchange id.
//
// The two lists are separate because they answer different questions: an
// Exchange is quoted only if the caller will show it — reading a container's log
// back is a round trip to Docker, and doing it for a clean request nobody will
// look at is pure waste — while the overlaps can only be seen across every
// Exchange in the window, shown or not. Two Exchanges of one container whose
// windows meet interleave their lines, and nothing after the fact can tell them
// apart.
func (r *Reader) Join(ctx context.Context, quote, window []inspect.Exchange, limit int) map[string]Transcript {
	out := make(map[string]Transcript, len(quote))
	for _, e := range quote {
		out[e.ID] = r.JoinOne(ctx, e, window, limit)
	}
	return out
}

// JoinOne is Join for a single Exchange, with the same window to count overlaps
// against.
func (r *Reader) JoinOne(ctx context.Context, e inspect.Exchange, window []inspect.Exchange, limit int) Transcript {
	tr := r.quote(ctx, e, limit)
	tr.Overlap = countOverlaps(e, window)
	return tr
}

// countOverlaps counts the other Exchanges served by the same container whose
// windows meet this one's.
func countOverlaps(e inspect.Exchange, all []inspect.Exchange) int {
	if e.Backend == "" {
		return 0
	}
	from, to := window(e)
	n := 0
	for _, other := range all {
		if other.ID == e.ID || other.Backend != e.Backend {
			continue
		}
		otherFrom, otherTo := window(other)
		if otherFrom.Before(to) && from.Before(otherTo) {
			n++
		}
	}
	return n
}

// window is the span of one Exchange, widened by grace at both ends.
func window(e inspect.Exchange) (from, to time.Time) {
	return e.At.Add(-grace), e.At.Add(e.Duration + grace)
}

// quote reads back what the container that served e wrote while it was live. It
// never guesses: an address it cannot resolve to the container that actually
// served yields a named silence rather than another container's output.
func (r *Reader) quote(ctx context.Context, e inspect.Exchange, limit int) Transcript {
	if e.Backend == "" {
		// Traefik logs an access line for a request no router matched, and for
		// its own dashboard: both name no server. Nothing was served, so there is
		// no container whose output could be quoted — and saying anything about
		// the stack's version here would send a developer after the wrong thing.
		return Transcript{Silence: "no route matched this host, so no container served it and there is nothing to quote — `proximo status` lists the hosts that are routed"}
	}
	name := backendName(e.Backend)
	c, ok := r.byName[name]
	if !ok {
		return Transcript{Container: name, Silence: fmt.Sprintf(
			"the container that served this request (%s) is gone — it was removed since, and its output went with it", name)}
	}
	// Second granularity is all a container listing carries, and all this needs:
	// the case it closes is an address reused by a container started after the
	// request, which is a restart apart, not a second.
	if born := time.Unix(c.Created, 0); born.After(e.At) {
		return Transcript{Container: name, Silence: fmt.Sprintf(
			"the container that served this request (%s) is gone — the one answering to that address now started %s after it, so its output is not this request's",
			name, born.Sub(e.At).Round(time.Second))}
	}

	from, to := window(e)
	return r.readInto(ctx, Transcript{Container: name, Replicas: r.replicas[serviceKey(c)]},
		c, from, to, limit, "while this request was live")
}

// readInto reads a container's output for one window and cuts it into tr,
// naming the silence when there is nothing to quote. Every window a Transcript
// can be cut to ends here — an Exchange's, an Incident's, a plain one — so the
// declared elision and the named silence cannot be dropped from one of them.
func (r *Reader) readInto(ctx context.Context, tr Transcript, c container.Summary, from, to time.Time, limit int, window string) Transcript {
	raw, err := r.readLogs(ctx, c, from, to)
	if err != nil {
		tr.Silence = fmt.Sprintf("%s's log driver cannot be read back: %v", tr.Container, err)
		return tr
	}
	cutTr := cut(raw, limit)
	tr.Head, tr.Tail, tr.Dropped = cutTr.Head, cutTr.Tail, cutTr.Dropped
	if tr.Empty() {
		tr.Silence = r.explainSilence(ctx, c, tr.Container, window)
	}
	return tr
}

// explainSilence tells apart a container that had nothing to say in this window
// from one that has said nothing at all — the second is a fact about the
// project, and a fixable one.
func (r *Reader) explainSilence(ctx context.Context, c container.Summary, name, window string) string {
	// One line answers it. Reading a container's whole history to choose between
	// two wordings would pull an uncapped project log into memory — the compose
	// logging anchor bounds proximo's own containers, not a developer's.
	raw, err := r.readTail(ctx, c)
	if err != nil {
		// Nothing was learned. Report only what was: it was quiet in this window.
		// Saying it logs elsewhere would send a developer to fix a logger that is
		// fine, which is the third silence wearing the second one's words.
		return name + " wrote nothing " + window
	}
	if len(splitLines(raw)) == 0 {
		return name + " has written nothing at all since it started, so it probably logs elsewhere — to a file inside the container, or to a collector. Only what a container writes to stdout or stderr can be quoted here."
	}
	return name + " wrote nothing " + window
}

// QuoteIncident quotes what a container wrote in the window one Incident closes.
// The window's right edge is the Incident; its left edge is the previous Incident
// of the same service, or the container's birth when there is none — for a
// restart loop that is exactly one container lifetime, which no fixed duration
// can be: a fixed one truncates the worker that wrote the useful line five
// minutes before dying and drowns the one that restarts every three seconds.
//
// Nothing here reads the text. An Incident is a statement the runtime made, and
// the window it fixes is the same kind of frame an Exchange fixes — which is what
// lets a Transcript stand on its own without proximo ever deciding which lines
// of a routeless container look like errors.
func (r *Reader) QuoteIncident(ctx context.Context, inc docker.Incident, all []docker.Incident, limit int) Transcript {
	c, ok := r.byName[inc.Container]
	if !ok {
		return Transcript{Container: inc.Container, Silence: incidentOutlived(inc.Container,
			"the container was removed, and a Transcript is quoted from the container's own output rather than held")}
	}
	// Second granularity is all a container listing carries, and all this needs:
	// what it closes is a name reused by a container created after the Incident,
	// which is a `compose up` apart, not a second.
	if born := time.Unix(c.Created, 0); born.After(inc.At) {
		return Transcript{Container: inc.Container, Silence: incidentOutlived(inc.Container, fmt.Sprintf(
			"the container answering to that name now started %s after it, so its output is not the one this Incident ended",
			born.Sub(inc.At).Round(time.Second)))}
	}

	// Left edge unbounded when the Incident is the service's first: the byte cap
	// bounds the quote and declares its own elision, while a bound taken from the
	// container's creation second can cut the first lines it ever wrote.
	var from time.Time
	if prev, ok := docker.PreviousIncident(inc, all); ok {
		from = prev.At
	}
	return r.readInto(ctx, Transcript{Container: inc.Container, Replicas: r.replicas[serviceKey(c)]},
		c, from, inc.At.Add(grace), limit, "in the window this Incident closes")
}

// QuoteService quotes what a service's container wrote in a plain window. It is
// the fallback for a service with no Incident to anchor to: the runtime declared
// nothing, so the only window left is the one the developer asked for — which is
// still a window fixed outside the text, never a reading of it.
func (r *Reader) QuoteService(ctx context.Context, service string, from, to time.Time, limit int) Transcript {
	c, name, ok := r.containerOfService(service)
	if !ok {
		return Transcript{Silence: fmt.Sprintf(
			"no container of %s is running, so there is no output to quote — `proximo status` lists what proximo can see", service)}
	}
	return r.readInto(ctx, Transcript{Container: name, Replicas: r.replicas[service]},
		c, from, to, limit, "in this window")
}

// containerOfService picks the container a service is quoted from: a running one
// before a stopped one — a plain window asks what the service is writing, and a
// container that exited is not — then the first by name, so two invocations quote
// the same replica of a scaled service rather than alternating between them. The
// count rides the Transcript, so which one it is stays visible.
func (r *Reader) containerOfService(service string) (container.Summary, string, bool) {
	var pick, stopped string
	for name, c := range r.byName {
		if serviceKey(c) != service {
			continue
		}
		if c.State != container.StateRunning {
			if stopped == "" || name < stopped {
				stopped = name
			}
			continue
		}
		if pick == "" || name < pick {
			pick = name
		}
	}
	if pick == "" {
		pick = stopped
	}
	if pick == "" {
		return container.Summary{}, "", false
	}
	return r.byName[pick], pick, true
}

// incidentOutlived is the fourth silence, alongside the three explainSilence
// tells apart: proximo kept the Incident and cannot show what was written around
// it. It is the declared price of remembering only what the runtime says — an
// Incident is tens of bytes of runtime metadata, a Transcript is the project's
// own output, and proximo holds none of the second.
func incidentOutlived(name, because string) string {
	return fmt.Sprintf("proximo remembers this Incident, not what %s wrote: %s", name, because)
}

// hasTTY reports whether a container's log stream comes back unmultiplexed.
func (r *Reader) hasTTY(ctx context.Context, id string) bool {
	if got, asked := r.tty[id]; asked {
		return got
	}
	tty := false
	if res, err := r.d.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); err == nil && res.Container.Config != nil {
		tty = res.Container.Config.Tty
	}
	r.tty[id] = tty
	return tty
}

// readTail returns the last line a container wrote, at any time. It answers one
// question only: has this container ever written anything?
func (r *Reader) readTail(ctx context.Context, c container.Summary) ([]byte, error) {
	return r.read(ctx, c, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: "1",
	})
}

// readLogs returns what a container wrote between from and to, either bound
// left zero for unbounded. Both streams are read: a stack trace goes to stderr
// and a request log to stdout, and the one being looked for is whichever the
// project chose.
func (r *Reader) readLogs(ctx context.Context, c container.Summary, from, to time.Time) ([]byte, error) {
	opts := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true}
	if !from.IsZero() {
		opts.Since = from.UTC().Format(time.RFC3339Nano)
	}
	if !to.IsZero() {
		opts.Until = to.UTC().Format(time.RFC3339Nano)
	}
	return r.read(ctx, c, opts)
}

// read runs one log request and returns the bytes, demultiplexed when the
// container's stream is framed.
func (r *Reader) read(ctx context.Context, c container.Summary, opts client.ContainerLogsOptions) ([]byte, error) {
	stream, err := r.d.ContainerLogs(ctx, c.ID, opts)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	// A container with a TTY writes one unmultiplexed stream; every other
	// container's is framed. Which it is comes from the container itself rather
	// than from sniffing the bytes: quoting frame headers as output would corrupt
	// every line, and a guess is not something to hand to whoever is about to
	// edit code.
	if r.hasTTY(ctx, c.ID) {
		return io.ReadAll(stream)
	}
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, stream); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Merge folds the Exchanges the hop recorded into the ones Traefik logged.
// Ordering is the caller's: it follows the most recent activity rather than the
// request instant, which is a rule about how a listing is read, not about how
// two sources are joined.
//
// A request to an inspected route appears in both sources, and the hop's copy is
// the one to keep: it names the container behind the hop rather than the hop
// itself, and it carries the Client reports. Traefik's copy is recognised by
// what it is — its backend is a stack service — rather than by matching two
// records on host and instant, which is a guess and would fail exactly when two
// requests arrive together.
func (r *Reader) Merge(logged, inspected []inspect.Exchange) []inspect.Exchange {
	out := make([]inspect.Exchange, 0, len(logged)+len(inspected))
	for _, e := range logged {
		if r.servedByTheStack(e.Backend) {
			continue
		}
		out = append(out, e)
	}
	return append(out, inspected...)
}

// servedByTheStack reports whether a backend address names one of proximo's own
// services — the hop, for an inspected route. It matches a compose service name
// as well as a container name: the watcher addresses the hop as `inspector`,
// which is what Traefik then logs, while a project's backend is a container name.
func (r *Reader) servedByTheStack(backend string) bool {
	if backend == "" {
		return false
	}
	return r.stackAddrs[backendName(backend)]
}
