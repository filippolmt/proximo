package transcript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	// The compose labels a container's replicas are recognised by. A developer
	// asking "how many replicas" means the containers of one compose service.
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"

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
	// stackAddrs are the names proximo's own services answer to. Traefik points
	// an inspected route at the hop by its compose *service* name, while a
	// project's backend is a container name, so both forms have to be recognised.
	stackAddrs map[string]bool
}

// NewReader lists the containers a join resolves against.
func NewReader(ctx context.Context, d Docker) (*Reader, error) {
	res, err := d.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, err
	}
	r := &Reader{
		d: d, byName: map[string]container.Summary{}, replicas: map[string]int{},
		tty: map[string]bool{}, stackAddrs: map[string]bool{},
	}
	for _, c := range res.Items {
		for _, n := range c.Names {
			name := strings.TrimPrefix(n, "/")
			r.byName[name] = c
			if c.Labels[docker.RoleLabel] != "" {
				r.stackAddrs[name] = true
			}
		}
		if key := serviceKey(c); key != "" {
			r.replicas[key]++
		}
		if c.Labels[docker.RoleLabel] != "" && c.Labels[composeServiceLabel] != "" {
			r.stackAddrs[c.Labels[composeServiceLabel]] = true
		}
	}
	return r, nil
}

// backendName is the container or service a backend address names. The address
// is `host:port` because that is the form Traefik logs a server in; the port is
// never what a Transcript is looked up by.
func backendName(backend string) string {
	name, _, _ := strings.Cut(backend, ":")
	return name
}

func serviceKey(c container.Summary) string {
	svc := c.Labels[composeServiceLabel]
	if svc == "" {
		return ""
	}
	return c.Labels[composeProjectLabel] + "/" + svc
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
			"the container that served this request (%s) is gone — it was stopped or replaced since", name)}
	}
	// Second granularity is all a container listing carries, and all this needs:
	// the case it closes is an address reused by a container started after the
	// request, which is a restart apart, not a second.
	if born := time.Unix(c.Created, 0); born.After(e.At) {
		return Transcript{Container: name, Silence: fmt.Sprintf(
			"the container that served this request (%s) is gone — the one answering to that address now started %s after it, so its output is not this request's",
			name, born.Sub(e.At).Round(time.Second))}
	}

	tr := Transcript{Container: name, Replicas: r.replicas[serviceKey(c)]}
	from, to := window(e)
	raw, err := r.readLogs(ctx, c, from, to)
	if err != nil {
		tr.Silence = fmt.Sprintf("%s's log driver cannot be read back: %v", name, err)
		return tr
	}

	cutTr := cut(raw, limit)
	tr.Head, tr.Tail, tr.Dropped = cutTr.Head, cutTr.Tail, cutTr.Dropped
	if tr.Empty() {
		tr.Silence = r.explainSilence(ctx, c, name)
	}
	return tr
}

// explainSilence tells apart a container that had nothing to say in this window
// from one that has said nothing at all — the second is a fact about the
// project, and a fixable one.
func (r *Reader) explainSilence(ctx context.Context, c container.Summary, name string) string {
	// One line answers it. Reading a container's whole history to choose between
	// two wordings would pull an uncapped project log into memory — the compose
	// logging anchor bounds proximo's own containers, not a developer's.
	raw, err := r.readTail(ctx, c)
	if err != nil {
		// Nothing was learned. Report only what was: it was quiet in this window.
		// Saying it logs elsewhere would send a developer to fix a logger that is
		// fine, which is the third silence wearing the second one's words.
		return name + " wrote nothing while this request was live"
	}
	if len(splitLines(raw)) == 0 {
		return name + " has written nothing at all since it started, so it probably logs elsewhere — to a file inside the container, or to a collector. Only what a container writes to stdout or stderr can be quoted here."
	}
	return name + " wrote nothing while this request was live"
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
