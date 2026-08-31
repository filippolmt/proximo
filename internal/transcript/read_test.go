package transcript

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// framed encodes text the way Docker's log endpoint does for a container without
// a TTY: 8-byte header per chunk, stream type then a big-endian length. Built
// here from the documented wire format rather than from the reader's own idea of
// it. See github.com/moby/moby/api/pkg/stdcopy.
func framed(text string) []byte {
	var out []byte
	var hdr [8]byte
	hdr[0] = 1 // stdout
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(text)))
	out = append(out, hdr[:]...)
	return append(out, text...)
}

type fakeDocker struct {
	items  []container.Summary
	logs   map[string]string // container ID -> what it wrote
	tty    map[string]bool
	logErr map[string]error
	// state and restarts are what an inspect answers beyond the TTY flag: the
	// readings a Reading takes from the runtime rather than from the log stream.
	state    map[string]*container.State
	restarts map[string]int
	// inspectErr fails the inspect, so a Reading has to say what it could not read
	// instead of reporting an absent measurement as zero.
	inspectErr map[string]error
	// anyOutput is what an unwindowed read returns: the reader asks a second
	// time, with no window, to tell "quiet in this window" from "quiet always".
	anyOutput map[string]string
	// tailErr fails only the unwindowed read, leaving the windowed one to
	// succeed: the two are asked for different reasons and can fail apart.
	tailErr map[string]error
	asked   map[string]client.ContainerLogsOptions
}

func (f *fakeDocker) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{Items: f.items}, nil
}

func (f *fakeDocker) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	var r client.ContainerInspectResult
	if err := f.inspectErr[id]; err != nil {
		return r, err
	}
	r.Container.Config = &container.Config{Tty: f.tty[id]}
	r.Container.State = f.state[id]
	r.Container.RestartCount = f.restarts[id]
	return r, nil
}

func (f *fakeDocker) ContainerLogs(_ context.Context, id string, o client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	if f.asked == nil {
		f.asked = map[string]client.ContainerLogsOptions{}
	}
	f.asked[id] = o
	if err := f.logErr[id]; err != nil {
		return nil, err
	}
	text := f.logs[id]
	if o.Since == "" && o.Until == "" {
		if err := f.tailErr[id]; err != nil {
			return nil, err
		}
		text = f.anyOutput[id]
	}
	if f.tty[id] {
		return io.NopCloser(strings.NewReader(text)), nil
	}
	return io.NopCloser(strings.NewReader(string(framed(text)))), nil
}

func summary(id, name string, born time.Time, labels map[string]string) container.Summary {
	return container.Summary{
		ID: id, Names: []string{"/" + name}, Created: born.Unix(), Labels: labels,
		State: container.StateRunning,
	}
}

// exited is a container that ran and stopped without being removed — the state a
// worker sits in between restarts, and one Docker still answers `docker logs`
// for.
func exited(id, name string, born time.Time, labels map[string]string) container.Summary {
	c := summary(id, name, born, labels)
	c.State = container.StateExited
	return c
}

const (
	project = "com.docker.compose.project"
	service = "com.docker.compose.service"
)

var requestAt = time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)

// One real Traefik v3 JSON access line. Decoding is inspect's concern and is
// tested there; here it only has to be the genuine article.
const traefikAccessLine = `{"DownstreamContentSize":1521,"DownstreamStatus":500,"Duration":41234567,"RequestHost":"web.test","RequestMethod":"POST","RequestPath":"/checkout","RouterName":"proximo-web@file","ServiceAddr":"web-1:8080","StartUTC":"2026-08-31T10:00:00.123456789Z","entryPointName":"websecure","level":"info","msg":""}`

func exchange(backend string) inspect.Exchange {
	e := inspect.Exchange{
		At: requestAt, Host: "web.test", Method: "GET", Path: "/", Status: 500,
		Duration: 40 * time.Millisecond, Backend: backend,
	}
	e.ID = inspect.DeriveID(e)
	return e
}

func quote(t *testing.T, f *fakeDocker, e inspect.Exchange) Transcript {
	t.Helper()
	r, err := NewReader(t.Context(), f)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	return r.JoinOne(t.Context(), e, []inspect.Exchange{e}, DefaultLimit)
}

// The happy path: the address names a live container born before the request,
// so its output is quoted and the service's replica count comes with it.
func TestQuoteResolvesTheBackendAndCountsReplicas(t *testing.T) {
	labels := map[string]string{project: "shop", service: "web"}
	f := &fakeDocker{
		items: []container.Summary{
			summary("id-web-1", "web-1", requestAt.Add(-time.Hour), labels),
			summary("id-web-2", "web-2", requestAt.Add(-time.Hour), labels),
			summary("id-db-1", "db-1", requestAt.Add(-time.Hour), map[string]string{project: "shop", service: "db"}),
		},
		logs: map[string]string{"id-web-1": "panic: nil map\n\tmain.go:12\n"},
	}

	tr := quote(t, f, exchange("web-1:8080"))

	if tr.Container != "web-1" {
		t.Errorf("Container = %q, want web-1", tr.Container)
	}
	if tr.Replicas != 2 {
		t.Errorf("Replicas = %d, want 2 — without it an agent reads a stale replica as a race", tr.Replicas)
	}
	if tr.Silence != "" {
		t.Errorf("Silence = %q, but there is output to quote", tr.Silence)
	}
	if len(tr.Head) != 2 || tr.Head[0] != "panic: nil map" {
		t.Errorf("Head = %q, want the container's own output verbatim", tr.Head)
	}
	// The window is the Exchange's, not the whole life of the container.
	got := f.asked["id-web-1"]
	if got.Since == "" || got.Until == "" {
		t.Errorf("logs asked without a window: %+v", got)
	}
	if !got.ShowStdout || !got.ShowStderr {
		t.Error("a stack trace goes to stderr; both streams must be read")
	}
}

// A container with a TTY writes an unmultiplexed stream. Quoting the frame
// headers as if they were output would corrupt every line.
func TestQuoteReadsATTYContainer(t *testing.T) {
	f := &fakeDocker{
		items: []container.Summary{summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil)},
		logs:  map[string]string{"id-web-1": "listening on :8080\n"},
		tty:   map[string]bool{"id-web-1": true},
	}
	if tr := quote(t, f, exchange("web-1:8080")); len(tr.Head) != 1 || tr.Head[0] != "listening on :8080" {
		t.Errorf("Head = %q, want the line unframed", tr.Head)
	}
}

// Never quote another container's output. An address that names nothing live,
// or names a container born after the request, means the one that served is gone.
func TestQuoteRefusesAReassignedOrMissingAddress(t *testing.T) {
	cases := map[string]*fakeDocker{
		"no live container": {
			items: []container.Summary{summary("id-other", "other-1", requestAt.Add(-time.Hour), nil)},
		},
		"born after the request": {
			items: []container.Summary{summary("id-web-1", "web-1", requestAt.Add(time.Minute), nil)},
			logs:  map[string]string{"id-web-1": "this is a different container's output\n"},
		},
	}
	for name, f := range cases {
		tr := quote(t, f, exchange("web-1:8080"))
		if !tr.Empty() {
			t.Errorf("%s: quoted %q — it is not the container that served", name, tr.Head)
		}
		if !strings.Contains(tr.Silence, "gone") {
			t.Errorf("%s: Silence = %q, want it to say the container is gone", name, tr.Silence)
		}
	}
}

// Two silences that look alike and mean opposite things: a container that had
// nothing to say in this window, and one that has said nothing at all — which is
// a fact about the project, and a fixable one.
func TestQuoteTellsTheTwoSilencesApart(t *testing.T) {
	quiet := &fakeDocker{
		items: []container.Summary{summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil)},
		logs:  map[string]string{"id-web-1": ""},
	}
	if got := quote(t, quiet, exchange("web-1:8080")).Silence; !strings.Contains(got, "logs elsewhere") {
		t.Errorf("a container that never wrote anything: Silence = %q, want it to point at where it does log", got)
	}

	// Same container, but it does write — just not during this Exchange.
	spoke := &fakeDocker{
		items:     quiet.items,
		logs:      map[string]string{"id-web-1": ""},
		anyOutput: map[string]string{"id-web-1": "listening on :8080\n"},
	}
	if got := quote(t, spoke, exchange("web-1:8080")).Silence; strings.Contains(got, "logs elsewhere") {
		t.Errorf("a container that writes but was quiet in this window: Silence = %q, must not blame the project", got)
	}
}

// A log driver proximo cannot read is a third silence, and the one a developer
// can act on immediately.
func TestQuoteNamesAnUnreadableLogDriver(t *testing.T) {
	f := &fakeDocker{
		items:  []container.Summary{summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil)},
		logErr: map[string]error{"id-web-1": errors.New("configured logging driver does not support reading")},
	}
	tr := quote(t, f, exchange("web-1:8080"))
	if !strings.Contains(tr.Silence, "log driver") {
		t.Errorf("Silence = %q, want it to name the log driver", tr.Silence)
	}
}

// Two Exchanges of one container whose windows overlap interleave their lines,
// and a temporal cut cannot tell them apart. Report the overlap; never attribute.
func TestJoinReportsOverlappingExchanges(t *testing.T) {
	f := &fakeDocker{
		items: []container.Summary{summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil)},
		logs:  map[string]string{"id-web-1": "something happened\n"},
	}
	a := exchange("web-1:8080")
	b := a
	b.At = a.At.Add(10 * time.Millisecond) // still inside a's 40ms window
	b.ID = inspect.DeriveID(b)
	apart := a
	apart.At = a.At.Add(time.Hour)
	apart.ID = inspect.DeriveID(apart)

	r, err := NewReader(t.Context(), f)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	all := []inspect.Exchange{a, b, apart}
	got := r.Join(t.Context(), all, all, DefaultLimit)

	if got[a.ID].Overlap != 1 || got[b.ID].Overlap != 1 {
		t.Errorf("overlapping Exchanges reported %d and %d overlaps, want 1 each",
			got[a.ID].Overlap, got[b.ID].Overlap)
	}
	if got[apart.ID].Overlap != 0 {
		t.Errorf("an Exchange an hour away reported %d overlaps", got[apart.ID].Overlap)
	}
}

// Every route produces an Access record now, and Traefik's operational lines
// share the stream: only the JSON ones describing a request are Exchanges.
func TestAccessReadsTraefiksLog(t *testing.T) {
	stream := `time="2026-08-31T10:00:00Z" level=info msg="Configuration loaded from file."` + "\n" +
		traefikAccessLine + "\n" +
		`{"level":"info","msg":"Traefik started","time":"2026-08-31T10:00:00Z"}` + "\n"
	f := &fakeDocker{
		items: []container.Summary{summary("id-traefik", "proximo-traefik-1", requestAt.Add(-time.Hour),
			map[string]string{"proximo.role": "traefik"})},
		logs: map[string]string{"id-traefik": stream},
	}
	r, err := NewReader(t.Context(), f)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	got, err := r.Access(t.Context(), requestAt.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("Access: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d Exchanges from Traefik's stream, want 1", len(got))
	}
	if got[0].Host != "web.test" || got[0].Status != 500 || got[0].Backend != "web-1:8080" {
		t.Errorf("Exchange = %+v", got[0])
	}
}

// A request to an inspected route is in both sources: Traefik saw it go to the
// inspector, the hop saw the backend behind it. Only the hop's copy names the
// real backend and carries the Client reports, so Traefik's is dropped — and it
// is dropped by what it is, not by guessing which two records are the same one.
func TestMergePrefersTheHopsCopyOfAnInspectedRequest(t *testing.T) {
	f := &fakeDocker{items: []container.Summary{
		summary("id-inspector", "proximo-inspector-1", requestAt.Add(-time.Hour),
			map[string]string{"proximo.role": "inspector", project: "proximo", service: "inspector"}),
		summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil),
	}}
	r, err := NewReader(t.Context(), f)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	// What Traefik actually logs for an inspected route: the watcher points the
	// service at the hop by its compose service name, not by a container name.
	viaHop := exchange("inspector:9000")
	plain := exchange("web-1:8080") // a route not under Inspection
	plain.Host = "api.test"
	plain.ID = inspect.DeriveID(plain)
	fromHop := inspect.Exchange{
		ID: "minted01", At: requestAt, Host: "web.test", Method: "GET", Path: "/",
		Status: 500, Backend: "web-1:8080", Reports: []inspect.Report{{Message: "boom"}},
	}

	got := r.Merge([]inspect.Exchange{viaHop, plain}, []inspect.Exchange{fromHop})

	if len(got) != 2 {
		t.Fatalf("merged to %d Exchanges, want 2: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Backend == "inspector:9000" {
			t.Error("kept Traefik's view of an inspected request, which names the hop instead of the backend")
		}
	}
	// Most recent first, and the hop's copy — the one with the report — survived.
	if got[0].ID != "minted01" && got[1].ID != "minted01" {
		t.Errorf("the hop's Exchange was dropped: %+v", got)
	}
}

// Traefik logs an access line for a request no router matched, and for its own
// dashboard: both name no server. Nothing was served, so there is nothing to
// quote — and nothing about the stack's version to report either.
func TestQuoteWithoutABackendBlamesNoRouteNotTheStack(t *testing.T) {
	f := &fakeDocker{items: []container.Summary{summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil)}}
	got := quote(t, f, exchange("")).Silence
	if !strings.Contains(got, "no route matched") {
		t.Errorf("Silence = %q, want it to say no route matched", got)
	}
	if strings.Contains(got, "proximo update") {
		t.Errorf("a typo'd host was told the stack is out of date: %q", got)
	}
}

// Choosing between two wordings must not pull a container's whole log history
// into memory: a project's log is not bounded by proximo.
func TestExplainingASilenceReadsOnlyOneLine(t *testing.T) {
	f := &fakeDocker{
		items:     []container.Summary{summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil)},
		logs:      map[string]string{"id-web-1": ""},
		anyOutput: map[string]string{"id-web-1": "listening on :8080\n"},
	}
	quote(t, f, exchange("web-1:8080"))
	if got := f.asked["id-web-1"].Tail; got != "1" {
		t.Errorf("the unwindowed read asked for Tail %q, want \"1\"", got)
	}
}

// The three silences must stay three. A tail read that fails says nothing about
// whether the container logs elsewhere, and answering as if it did sends a
// developer to fix a logger that is fine.
func TestAFailedTailReadIsNotEvidenceOfLoggingElsewhere(t *testing.T) {
	f := &fakeDocker{
		items:   []container.Summary{summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil)},
		logs:    map[string]string{"id-web-1": ""},
		tailErr: map[string]error{"id-web-1": errors.New("stream closed")},
	}
	got := quote(t, f, exchange("web-1:8080")).Silence
	if strings.Contains(got, "logs elsewhere") {
		t.Errorf("Silence = %q, but nothing was learned about where it logs", got)
	}
	if !strings.Contains(got, "this request") {
		t.Errorf("Silence = %q, want the one thing that was observed: it was quiet in this window", got)
	}
}

// The window an Incident fixes: from the previous Incident of the same service
// to the Incident itself. For a restart loop that is one container lifetime.
func TestQuoteIncidentWindowsFromThePreviousIncident(t *testing.T) {
	born := requestAt.Add(-time.Hour)
	f := &fakeDocker{
		items: []container.Summary{summary("w1", "shop-worker-1", born, map[string]string{
			project: "shop", service: "worker", "proximo.transcript": "true",
		})},
		logs: map[string]string{"w1": "picked up job 41\npanic: nil map\n"},
	}
	r, err := NewReader(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	prev := docker.Incident{ID: "p", Service: "shop/worker", Container: "shop-worker-1", At: requestAt.Add(-10 * time.Minute), Kind: docker.IncidentExited, ExitCode: 1}
	inc := docker.Incident{ID: "i", Service: "shop/worker", Container: "shop-worker-1", At: requestAt, Kind: docker.IncidentExited, ExitCode: 137, OOM: true}

	tr := r.QuoteIncident(context.Background(), inc, []docker.Incident{inc, prev}, DefaultLimit)
	if tr.Silence != "" {
		t.Fatalf("silence %q, want the quoted lines", tr.Silence)
	}
	if len(tr.Head) != 2 || tr.Head[1] != "panic: nil map" {
		t.Errorf("quoted %q, want both lines", tr.Head)
	}
	asked := f.asked["w1"]
	if asked.Since != prev.At.UTC().Format(time.RFC3339Nano) {
		t.Errorf("read from %q, want the previous Incident at %s", asked.Since, prev.At)
	}
	if asked.Until != inc.At.UTC().Format(time.RFC3339Nano) {
		t.Errorf("read to %q, want the Incident's own instant with no grace", asked.Until)
	}
}

// A worker that dies three times in one second is the case an Incident-anchored
// window exists for: each window must hold one container lifetime, so it ends at
// its own Incident rather than reaching into the next lifetime's output.
func TestQuoteIncidentWindowsDoNotOverlapInARestartLoop(t *testing.T) {
	born := requestAt.Add(-time.Hour)
	f := &fakeDocker{
		items: []container.Summary{summary("w1", "shop-worker-1", born, map[string]string{
			project: "shop", service: "worker",
		})},
		logs: map[string]string{"w1": "attempt\n"},
	}
	r, _ := NewReader(context.Background(), f)
	first := docker.Incident{ID: "a", Service: "shop/worker", Container: "shop-worker-1", At: requestAt, Kind: docker.IncidentExited, ExitCode: 1}
	second := docker.Incident{ID: "b", Service: "shop/worker", Container: "shop-worker-1", At: requestAt.Add(200 * time.Millisecond), Kind: docker.IncidentExited, ExitCode: 1}
	other := docker.Incident{ID: "c", Service: "shop/db", Container: "shop-db-1", At: requestAt.Add(50 * time.Millisecond), Kind: docker.IncidentRestarted}

	all := []docker.Incident{first, second, other}

	r.QuoteIncident(context.Background(), first, all, DefaultLimit)
	if got, want := f.asked["w1"].Until, first.At.UTC().Format(time.RFC3339Nano); got != want {
		t.Errorf("read to %q, want %q — 200ms later the next lifetime is already writing", got, want)
	}
	// And the next window starts where this one ended, so the two are disjoint.
	r.QuoteIncident(context.Background(), second, all, DefaultLimit)
	asked := f.asked["w1"]
	if asked.Since != first.At.UTC().Format(time.RFC3339Nano) || asked.Until != second.At.UTC().Format(time.RFC3339Nano) {
		t.Errorf("read %s → %s, want %s → %s", asked.Since, asked.Until, first.At, second.At)
	}
}

// The first Incident of a service has no left edge: reading from the container's
// creation second can cut the first lines it ever wrote, and the byte cap already
// bounds the quote and declares its own elision.
func TestQuoteIncidentReadsFromTheStartWhenItIsTheFirst(t *testing.T) {
	f := &fakeDocker{
		items: []container.Summary{summary("w1", "shop-worker-1", requestAt.Add(-time.Hour), map[string]string{
			project: "shop", service: "worker",
		})},
		logs: map[string]string{"w1": "boot\n"},
	}
	r, _ := NewReader(context.Background(), f)
	inc := docker.Incident{ID: "i", Service: "shop/worker", Container: "shop-worker-1", At: requestAt, Kind: docker.IncidentRestarted}

	if tr := r.QuoteIncident(context.Background(), inc, []docker.Incident{inc}, DefaultLimit); tr.Silence != "" {
		t.Fatalf("silence %q, want the quoted line", tr.Silence)
	}
	if since := f.asked["w1"].Since; since != "" {
		t.Errorf("read from %q, want no left bound", since)
	}
}

// The fourth silence: proximo kept the Incident and cannot show what was written
// around it. Both ways it happens say so, and neither quotes another container.
func TestQuoteIncidentSaysWhenItOutlivedTheOutput(t *testing.T) {
	inc := docker.Incident{ID: "i", Service: "shop/worker", Container: "shop-worker-1", At: requestAt, Kind: docker.IncidentExited, ExitCode: 137}

	gone := &fakeDocker{}
	r, _ := NewReader(context.Background(), gone)
	tr := r.QuoteIncident(context.Background(), inc, []docker.Incident{inc}, DefaultLimit)
	if !strings.Contains(tr.Silence, "proximo remembers this Incident") || !strings.Contains(tr.Silence, "the container was removed") {
		t.Errorf("silence = %q, want the fourth silence for a container that was removed", tr.Silence)
	}

	replaced := &fakeDocker{items: []container.Summary{
		summary("w2", "shop-worker-1", requestAt.Add(time.Minute), map[string]string{project: "shop", service: "worker"}),
	}}
	r2, _ := NewReader(context.Background(), replaced)
	tr2 := r2.QuoteIncident(context.Background(), inc, []docker.Incident{inc}, DefaultLimit)
	if !strings.Contains(tr2.Silence, "proximo remembers this Incident") || !strings.Contains(tr2.Silence, "1m0s after it") {
		t.Errorf("silence = %q, want the fourth silence for a container recreated since", tr2.Silence)
	}
	if len(tr2.Head) != 0 {
		t.Error("the replacement container's output must not be quoted as this Incident's")
	}
}

func TestQuoteIncidentNamesTheSilenceOfAQuietWindow(t *testing.T) {
	f := &fakeDocker{
		items: []container.Summary{summary("w1", "shop-worker-1", requestAt.Add(-time.Hour), map[string]string{
			project: "shop", service: "worker",
		})},
		logs:      map[string]string{"w1": ""},
		anyOutput: map[string]string{"w1": "something, once\n"},
	}
	r, _ := NewReader(context.Background(), f)
	inc := docker.Incident{ID: "i", Service: "shop/worker", Container: "shop-worker-1", At: requestAt, Kind: docker.IncidentExited, ExitCode: 1}

	tr := r.QuoteIncident(context.Background(), inc, []docker.Incident{inc}, DefaultLimit)
	if want := "shop-worker-1 wrote nothing in the window this Incident closes"; tr.Silence != want {
		t.Errorf("silence = %q, want %q", tr.Silence, want)
	}
}

// The driving case: a worker that exited and was left in place. Docker still
// answers `docker logs` for it, so the window the Incident closes is quotable —
// reporting it as gone would call the output lost while it sits right there.
func TestQuoteIncidentReadsAContainerThatExitedAndStayed(t *testing.T) {
	f := &fakeDocker{
		items: []container.Summary{exited("w1", "shop-worker-1", requestAt.Add(-time.Hour), map[string]string{
			project: "shop", service: "worker", "proximo.transcript": "true",
		})},
		logs: map[string]string{"w1": "worker: panic: nil map\n"},
	}
	r, err := NewReader(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	inc := docker.Incident{ID: "i", Service: "shop/worker", Container: "shop-worker-1", At: requestAt, Kind: docker.IncidentExited, ExitCode: 1}

	tr := r.QuoteIncident(context.Background(), inc, []docker.Incident{inc}, DefaultLimit)
	if tr.Silence != "" {
		t.Fatalf("silence %q, want the quoted line — the container is stopped, not removed", tr.Silence)
	}
	if len(tr.Head) != 1 || tr.Head[0] != "worker: panic: nil map" {
		t.Errorf("quoted %q, want the worker's own line", tr.Head)
	}
	// Stopped is not a replica: "1 of N replicas" is about what is serving now.
	if tr.Replicas != 0 {
		t.Errorf("Replicas = %d, want a stopped container counted as none", tr.Replicas)
	}
}

// A dead backend must still resolve to its service, or a --host listing drops
// the Incident that explains the 502 it is showing.
func TestServiceOfBackendResolvesAContainerThatStopped(t *testing.T) {
	f := &fakeDocker{items: []container.Summary{
		exited("w1", "web-1", requestAt.Add(-time.Hour), map[string]string{project: "shop", service: "web"}),
	}}
	r, _ := NewReader(context.Background(), f)
	if got := r.ServiceOfBackend("web-1:8080"); got != "shop/web" {
		t.Errorf("ServiceOfBackend = %q, want shop/web even though the container is stopped", got)
	}
	// It is not a candidate for a bare --service, though: nothing is running.
	if len(r.Services()) != 0 {
		t.Errorf("Services() = %v, want none — a stopped service is named by its Incident, not by the listing", r.Services())
	}
}

// A Reading is what the runtime declares plus the instant a stream last moved —
// never the line it moved with.
func TestReadingOfTakesEveryReadingWithoutReadingTheLine(t *testing.T) {
	started := time.Now().Add(-3 * time.Hour)
	wrote := time.Now().Add(-14 * time.Minute)
	f := &fakeDocker{
		items: []container.Summary{summary("w1", "shop-worker-1", started, map[string]string{
			project: "shop", service: "worker", "proximo.transcript": "true",
		})},
		state: map[string]*container.State{"w1": {
			StartedAt: started.Format(time.RFC3339Nano),
			Health:    &container.Health{Status: container.Healthy},
		}},
		restarts: map[string]int{"w1": 2},
		// Docker stamps the instant on the line when asked; the reader takes the
		// stamp and leaves the text alone.
		anyOutput: map[string]string{"w1": wrote.Format(time.RFC3339Nano) + " worker: picked up job 41871\n"},
	}
	r, err := NewReader(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	rd := r.ReadingOf(context.Background(), "shop/worker")

	switch {
	case rd.Container != "shop-worker-1" || !rd.Running:
		t.Errorf("reading = %+v, want the running container named", rd)
	case !rd.Since.Equal(started.Truncate(0)) && rd.Since.Unix() != started.Unix():
		t.Errorf("Since = %s, want %s — the instant it last started", rd.Since, started)
	case rd.Health != string(container.Healthy):
		t.Errorf("Health = %q, want healthy", rd.Health)
	case rd.Restarts != 2:
		t.Errorf("Restarts = %d, want 2", rd.Restarts)
	case !rd.LastWrote.Equal(wrote.Truncate(0)) && rd.LastWrote.Unix() != wrote.Unix():
		t.Errorf("LastWrote = %s, want %s", rd.LastWrote, wrote)
	case len(rd.Unread) != 0:
		t.Errorf("Unread = %v, want nothing: every reading was taken", rd.Unread)
	}
	if !f.asked["w1"].Timestamps || f.asked["w1"].Tail != "1" {
		t.Errorf("read %+v, want one line with its timestamp", f.asked["w1"])
	}
	got := rd.Describe()
	for _, want := range []string{"running for 3h0m0s", "healthcheck says healthy", "restarted 2 times", "and it last wrote 14m0s ago"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() = %q, missing %q", got, want)
		}
	}
	// The line itself is never carried into a Reading.
	if strings.Contains(got, "41871") {
		t.Errorf("Describe() = %q, want no part of what the container wrote", got)
	}
}

// The three answers a stream can give are never collapsed: they send a developer
// to three different places.
func TestReadingOfTellsTheStreamsSilencesApart(t *testing.T) {
	labels := map[string]string{project: "shop", service: "worker"}
	born := time.Now().Add(-time.Hour)

	wroteNothing := &fakeDocker{
		items:     []container.Summary{summary("w1", "shop-worker-1", born, labels)},
		anyOutput: map[string]string{"w1": ""},
	}
	r, _ := NewReader(context.Background(), wroteNothing)
	rd := r.ReadingOf(context.Background(), "shop/worker")
	if !rd.WroteNothing || !rd.LastWrote.IsZero() || len(rd.Unread) != 0 {
		t.Errorf("reading = %+v, want a stream that was read and had nothing in it", rd)
	}
	if !strings.Contains(rd.Describe(), "and it has written nothing at all") {
		t.Errorf("Describe() = %q, want it to say nothing was ever written", rd.Describe())
	}

	// The bug this closes: a log driver Docker cannot replay is not a container
	// that wrote nothing, and saying so would send a developer to fix a logger
	// that is fine.
	unreadable := &fakeDocker{
		items:   []container.Summary{summary("w1", "shop-worker-1", born, labels)},
		tailErr: map[string]error{"w1": errors.New("configured logging driver does not support reading")},
	}
	r2, _ := NewReader(context.Background(), unreadable)
	rd2 := r2.ReadingOf(context.Background(), "shop/worker")
	if rd2.WroteNothing {
		t.Error("a stream that cannot be read back must not be reported as one that said nothing")
	}
	if len(rd2.Unread) != 1 || !strings.Contains(rd2.Unread[0], "when it last wrote could not be read") {
		t.Errorf("Unread = %v, want the unreadable stream named", rd2.Unread)
	}
	got := rd2.Describe()
	if strings.Contains(got, "written nothing") {
		t.Errorf("Describe() = %q, want no claim about what it wrote", got)
	}
	if !strings.Contains(got, "could not be read") {
		t.Errorf("Describe() = %q, want the absence named rather than left as a gap", got)
	}
}

// Three of the four readings come from the inspect. When it fails, the Reading
// says so rather than reporting them as zero.
func TestReadingOfNamesWhatTheInspectCouldNotAnswer(t *testing.T) {
	f := &fakeDocker{
		items: []container.Summary{summary("w1", "shop-worker-1", time.Now().Add(-time.Hour), map[string]string{
			project: "shop", service: "worker",
		})},
		inspectErr: map[string]error{"w1": errors.New("no such container")},
		anyOutput:  map[string]string{"w1": time.Now().Format(time.RFC3339Nano) + " still here\n"},
	}
	r, _ := NewReader(context.Background(), f)
	rd := r.ReadingOf(context.Background(), "shop/worker")

	if rd.Since.IsZero() != true || rd.Health != "" || rd.Restarts != 0 {
		t.Errorf("reading = %+v, want no measurements from a failed inspect", rd)
	}
	if len(rd.Unread) != 1 || !strings.Contains(rd.Unread[0], "what else the runtime says could not be read") {
		t.Errorf("Unread = %v, want the failed inspect named", rd.Unread)
	}
	if !strings.Contains(rd.Describe(), "could not be read") {
		t.Errorf("Describe() = %q, want the absence named", rd.Describe())
	}
}

func TestReadingOfIsEmptyForAServiceProximoCannotSee(t *testing.T) {
	r, _ := NewReader(context.Background(), &fakeDocker{})
	if rd := r.ReadingOf(context.Background(), "shop/ghost"); !rd.Empty() {
		t.Errorf("reading = %+v, want nothing for a service with no container", rd)
	}
}

// A container that has written before is quiet in this window, not silent
// forever — and one whose stream cannot be read must not be told it logs
// elsewhere, which is the third silence wearing the second one's words.
func TestExplainSilenceKeepsTheThreeSilencesApart(t *testing.T) {
	labels := map[string]string{project: "shop", service: "web"}
	c := summary("w1", "web-1", requestAt.Add(-time.Hour), labels)
	e := inspect.Exchange{ID: "x", At: requestAt, Backend: "web-1:8080", Status: 500}

	never := &fakeDocker{items: []container.Summary{c}, logs: map[string]string{"w1": ""}, anyOutput: map[string]string{"w1": ""}}
	r, _ := NewReader(context.Background(), never)
	if got := r.JoinOne(context.Background(), e, nil, DefaultLimit).Silence; !strings.Contains(got, "logs elsewhere") {
		t.Errorf("silence = %q, want the container that never wrote sent to its logger", got)
	}

	quiet := &fakeDocker{items: []container.Summary{c}, logs: map[string]string{"w1": ""}, anyOutput: map[string]string{"w1": "something, once\n"}}
	r2, _ := NewReader(context.Background(), quiet)
	got := r2.JoinOne(context.Background(), e, nil, DefaultLimit).Silence
	if strings.Contains(got, "logs elsewhere") || !strings.Contains(got, "wrote nothing while this request was live") {
		t.Errorf("silence = %q, want quiet-in-this-window", got)
	}

	unreadable := &fakeDocker{
		items: []container.Summary{c}, logs: map[string]string{"w1": ""},
		tailErr: map[string]error{"w1": errors.New("driver cannot be read")},
	}
	r3, _ := NewReader(context.Background(), unreadable)
	if got := r3.JoinOne(context.Background(), e, nil, DefaultLimit).Silence; strings.Contains(got, "logs elsewhere") {
		t.Errorf("silence = %q, want no claim about the logger when nothing was learned", got)
	}
}
