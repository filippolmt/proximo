package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/filippolmt/proximo/internal/transcript"
)

func incident(id, service string, at time.Time, kind docker.IncidentKind) docker.Incident {
	return docker.Incident{ID: id, Service: service, Container: strings.ReplaceAll(service, "/", "-") + "-1", At: at, Kind: kind, ExitCode: 1}
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

func TestSelectIncidents(t *testing.T) {
	now := time.Now()
	worker := incident("i1", "shop/worker", now.Add(-time.Minute), docker.IncidentExited)
	sick := incident("i2", "shop/worker", now.Add(-2*time.Minute), docker.IncidentUnhealthy)
	db := incident("i3", "shop/db", now.Add(-3*time.Minute), docker.IncidentRestarted)
	old := incident("i4", "shop/worker", now.Add(-time.Hour), docker.IncidentExited)
	all := []docker.Incident{worker, sick, db, old}

	tests := []struct {
		name     string
		service  string
		services map[string]bool
		since    time.Time
		limit    int
		problems bool
		want     []string
	}{
		{name: "default holds unhealthy back", since: now.Add(-30 * time.Minute), problems: true, want: []string{"i1", "i3"}},
		{name: "--all keeps it", since: now.Add(-30 * time.Minute), want: []string{"i1", "i2", "i3"}},
		{name: "an explicit service holds nothing back", service: "shop/worker", since: now.Add(-30 * time.Minute), problems: true, want: []string{"i1", "i2"}},
		{name: "a host narrows by the services that served it", services: map[string]bool{"shop/db": true}, since: now.Add(-30 * time.Minute), problems: true, want: []string{"i3"}},
		{name: "since bounds the window", since: now.Add(-90 * time.Second), problems: true, want: []string{"i1"}},
		{name: "limit keeps the most recent", since: time.Time{}, limit: 1, problems: true, want: []string{"i1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectIncidents(all, tt.service, tt.services, tt.since, tt.limit, tt.problems)
			if len(got) != len(tt.want) {
				t.Fatalf("selected %+v, want %v", got, tt.want)
			}
			for i, id := range tt.want {
				if got[i].ID != id {
					t.Errorf("selected[%d] = %s, want %s", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestResolveServiceReportsCandidatesRatherThanChoosing(t *testing.T) {
	running := []string{"shop/web", "blog/web", "shop/worker"}
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
	if !strings.Contains(out, "not the same as nothing being wrong") {
		t.Errorf("message = %q, want it to refuse to read silence as all-clear", out)
	}
}
