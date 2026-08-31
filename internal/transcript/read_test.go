package transcript

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

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
	// anyOutput is what an unwindowed read returns: the reader asks a second
	// time, with no window, to tell "quiet in this window" from "quiet always".
	anyOutput map[string]string
	asked     map[string]client.ContainerLogsOptions
}

func (f *fakeDocker) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{Items: f.items}, nil
}

func (f *fakeDocker) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	var r client.ContainerInspectResult
	r.Container.Config = &container.Config{Tty: f.tty[id]}
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
	}
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

// An Exchange the stack recorded before backends were named cannot be joined at
// all, and says so rather than quoting whatever is at hand.
func TestQuoteWithoutABackend(t *testing.T) {
	f := &fakeDocker{items: []container.Summary{summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil)}}
	if got := quote(t, f, exchange("")).Silence; got == "" {
		t.Error("an Exchange with no backend produced a silence with no cause")
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
			map[string]string{"proximo.role": "inspector"}),
		summary("id-web-1", "web-1", requestAt.Add(-time.Hour), nil),
	}}
	r, err := NewReader(t.Context(), f)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	viaHop := exchange("proximo-inspector-1:8080") // what Traefik logged
	plain := exchange("web-1:8080")                // a route not under Inspection
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
		if e.Backend == "proximo-inspector-1:8080" {
			t.Error("kept Traefik's view of an inspected request, which names the hop instead of the backend")
		}
	}
	// Most recent first, and the hop's copy — the one with the report — survived.
	if got[0].ID != "minted01" && got[1].ID != "minted01" {
		t.Errorf("the hop's Exchange was dropped: %+v", got)
	}
}
