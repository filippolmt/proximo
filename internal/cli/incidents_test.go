package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/filippolmt/proximo/internal/transcript"
)

func incident(id string, service docker.Service, at time.Time, kind docker.IncidentKind) docker.Incident {
	return docker.Incident{
		ID: id, Service: service, At: at, Kind: kind, ExitCode: 1,
		Container: strings.ReplaceAll(service.String(), "/", "-") + "-1",
	}
}

// One listing, one time order: an Incident is a differently-shaped row among the
// Exchanges, because time order is the only thing tying a failing request to the
// container that died seconds before it.
func TestRowsAreOneListingInTimeOrder(t *testing.T) {
	base := time.Date(2026, 8, 31, 14, 5, 0, 0, time.UTC)
	exchanges := []inspect.Exchange{
		{ID: "e-late", At: base.Add(9 * time.Second), Method: "POST", Path: "/checkout", Status: 502},
		{ID: "e-early", At: base, Method: "GET", Path: "/", Status: 200},
	}
	incidents := []docker.Incident{incident("i1", "shop/worker", base.Add(2*time.Second), docker.IncidentExited)}

	rows := mergeRows(exchanges, incidents)
	var order []string
	for _, r := range rows {
		if r.incident != nil {
			order = append(order, r.incident.ID)
			continue
		}
		order = append(order, r.exchange.ID)
	}
	want := []string{"e-late", "i1", "e-early"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("listing order = %v, want %v", order, want)
		}
	}
}

func TestResolveServiceReportsCandidatesRatherThanChoosing(t *testing.T) {
	running := []docker.Service{"shop/web", "blog/web", "shop/worker"}
	incidents := []docker.Incident{incident("i1", "shop/importer", time.Now(), docker.IncidentExited)}

	if got, err := resolveService("worker", running, incidents); err != nil || got != "shop/worker" {
		t.Errorf("resolveService(worker) = %q, %v; want shop/worker", got, err)
	}
	// An Incident outlives the container that produced it, so a service that is
	// no longer running is still a name that answers.
	if got, err := resolveService("importer", running, incidents); err != nil || got != "shop/importer" {
		t.Errorf("resolveService(importer) = %q, %v; want shop/importer", got, err)
	}
	_, err := resolveService("web", running, incidents)
	if err == nil || !strings.Contains(err.Error(), "blog/web") || !strings.Contains(err.Error(), "shop/web") {
		t.Errorf("contested bare name error = %v, want both candidates named", err)
	}
	_, err = resolveService("queue", running, incidents)
	if err == nil || !strings.Contains(err.Error(), docker.TranscriptLabel) {
		t.Errorf("unknown service error = %v, want the label that makes a container known", err)
	}
}

func TestWriteIncidentRowNamesTheServiceAndWhatTheRuntimeSaid(t *testing.T) {
	inc := docker.Incident{
		ID: "9b3e1a7c5d2f8e04", At: time.Date(2026, 8, 31, 14, 5, 2, 0, time.Local),
		Service: "shop/worker", Container: "shop-worker-1",
		Kind: docker.IncidentExited, ExitCode: 137, OOM: true,
	}
	var b strings.Builder
	writeIncident(&b, inc, transcript.Transcript{Container: "shop-worker-1", Head: []string{"picked up job 41871"}})
	out := b.String()

	for _, want := range []string{
		"14:05:02  9b3e1a7c5d2f8e04  shop/worker  exited 137 (OOM-killed)",
		"container shop-worker-1",
		"transcript of shop-worker-1:",
		"picked up job 41871",
		"whole transcript — `proximo errors transcript 9b3e1a7c5d2f8e04`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("row is missing %q:\n%s", want, out)
		}
	}
	// The request columns are not padded out: a hole in a column is a question.
	if strings.Contains(out, "---") || strings.Contains(out, "→") {
		t.Errorf("row borrows the request shape:\n%s", out)
	}
}

// A listing that cannot reach the Incident store must not read as a quiet
// machine, and each cause takes its own Remedy.
func TestIncidentsNoteCarriesTheRemedyForItsCause(t *testing.T) {
	down := incidentsNoteFor(true)
	if !strings.Contains(down, "proximo up") {
		t.Errorf("stack-down note = %q, want the `proximo up` remedy", down)
	}
	skew := incidentsNoteFor(false)
	if !strings.Contains(skew, "proximo update") || !strings.Contains(skew, "version skew") {
		t.Errorf("skew note = %q, want the `proximo update` remedy and the named cause", skew)
	}
}

// An empty listing under --service says which of the two silences it is.
func TestNothingFoundForAServiceSaysWhatSilenceMeans(t *testing.T) {
	var b strings.Builder
	writeNothingFound(&b, false, "", "shop/worker")
	out := b.String()
	if !strings.Contains(out, "shop/worker") || !strings.Contains(out, "no Incident") {
		t.Errorf("message = %q, want the service and the absence of an Incident named", out)
	}
	if !strings.Contains(out, "not that nothing is wrong") {
		t.Errorf("message = %q, want it to refuse to read silence as all-clear", out)
	}
}

// The debt this closes with: proximo cannot tell an idle consumer from a stuck
// one, so it reports the readings and says the conclusion is not its to draw.
// Silence with no readings at all was the thing that let "I have nothing to say"
// be read as "all fine".
func TestReadingReportsAndRefusesToJudge(t *testing.T) {
	var b strings.Builder
	writeReading(&b, docker.Reading{
		Container: "shop-worker-1", Running: true,
		Since:       time.Now().Add(-3 * time.Hour),
		Healthcheck: "healthy",
		Restarts:    2,
		LastWrote:   time.Now().Add(-14 * time.Minute),
	})
	out := b.String()
	for _, want := range []string{"shop-worker-1", "running for 3h0m0s", "healthy", "restarted 2 times", "and it last wrote 14m0s ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Reading is missing %q:\n%s", want, out)
		}
	}
	// The refusal is the point: the readings are facts, never a verdict.
	if !strings.Contains(out, "only the project knows whether work was waiting") {
		t.Errorf("the Reading draws a conclusion proximo cannot draw:\n%s", out)
	}
	if strings.Contains(out, "is stuck") || strings.Contains(out, "looks idle") {
		t.Errorf("the Reading judges the container:\n%s", out)
	}
}

// A container proximo cannot see produces no Reading, and no half-empty one.
func TestReadingSaysNothingWhenThereIsNothingToRead(t *testing.T) {
	var b strings.Builder
	writeReading(&b, docker.Reading{})
	if b.Len() != 0 {
		t.Errorf("a Reading for an unseen container = %q, want nothing", b.String())
	}
	if readingOrNil(docker.Reading{}) != nil {
		t.Error("the JSON must omit the reading member rather than carry zero values")
	}
	rd := docker.Reading{Container: "shop-worker-1", Running: true}
	if readingOrNil(rd) == nil {
		t.Error("a Reading that was taken must reach the JSON")
	}
}

// The clause a developer reads for goes last, whatever else the Reading carries.
func TestReadingEndsOnTheStreamClause(t *testing.T) {
	rd := docker.Reading{
		Container: "shop-worker-1", Running: true, Replicas: 3, Restarts: 1,
		Since: time.Now().Add(-time.Minute), LastWrote: time.Now().Add(-time.Second),
	}
	got := rd.Describe()
	if !strings.HasSuffix(got, "and it last wrote 1s ago") {
		t.Errorf("Describe() = %q, want the stream clause last", got)
	}
	for _, want := range []string{"restarted once", "3 replicas running"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() = %q, missing %q", got, want)
		}
	}
}

// The Incident store lives in the watcher's memory, so an empty listing means
// either that nothing happened or that a restart threw it away. `proximo up` is
// an easy thing to have done by accident, so the two are told apart.
func TestWatcherRestartNoteTellsAnEmptyListingApart(t *testing.T) {
	fresh := docker.IncidentListing{Started: time.Now().Add(-30 * time.Second)}
	got := watcherRestartNote(fresh, true)
	if !strings.Contains(got, "the watcher restarted") || !strings.Contains(got, "in memory only") {
		t.Errorf("note = %q, want the restart named as the reason the listing is empty", got)
	}
	// Not when there is something to show: the listing speaks for itself.
	if note := watcherRestartNote(fresh, false); note != "" {
		t.Errorf("note = %q, want nothing when the listing is not empty", note)
	}
	// Nor for a watcher that has been up a while: then empty means empty.
	old := docker.IncidentListing{Started: time.Now().Add(-3 * time.Hour)}
	if note := watcherRestartNote(old, true); note != "" {
		t.Errorf("note = %q, want nothing when no restart can explain it", note)
	}
	// And nothing to say when the store never answered.
	if note := watcherRestartNote(docker.IncidentListing{}, true); note != "" {
		t.Errorf("note = %q, want nothing when there is no start instant", note)
	}
}
