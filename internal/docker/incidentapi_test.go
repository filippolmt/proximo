package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIncidentAPIServesTheListingAndItsStart(t *testing.T) {
	store := NewIncidentStore(0, 0)
	store.Observe(dieEvent("shop-worker-1", "1", time.Now().Add(-time.Minute), workerLabels("shop", "worker")))
	store.Observe(dieEvent("shop-worker-1", "2", time.Now().Add(-2*time.Hour), workerLabels("shop", "worker")))

	rec := httptest.NewRecorder()
	IncidentAPI{Store: store}.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/incidents?since=30m", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got IncidentListing
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the listing: %v (%s)", err, rec.Body)
	}
	if len(got.Incidents) != 1 || got.Incidents[0].ExitCode != 1 {
		t.Errorf("incidents = %+v, want only the one inside the window", got.Incidents)
	}
	// started travels with the listing: an empty one means either that nothing
	// happened or that a restart threw everything away.
	if got.Started.IsZero() {
		t.Error("the listing carries no start instant, so an empty one cannot explain itself")
	}
}

func TestIncidentAPIServesNothingElse(t *testing.T) {
	rec := httptest.NewRecorder()
	IncidentAPI{Store: NewIncidentStore(0, 0)}.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/exchanges", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d for an unknown path, want 404", rec.Code)
	}
}

func TestIncidentAPIWindowsOnlyOnAPositiveDuration(t *testing.T) {
	store := NewIncidentStore(0, 0)
	store.Observe(dieEvent("shop-worker-1", "1", time.Now().Add(-2*time.Hour), workerLabels("shop", "worker")))

	for _, q := range []string{"", "?since=", "?since=nonsense", "?since=-5m"} {
		rec := httptest.NewRecorder()
		IncidentAPI{Store: store}.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/incidents"+q, nil))
		var got IncidentListing
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%q: decoding: %v", q, err)
		}
		if len(got.Incidents) != 1 {
			t.Errorf("%q: incidents = %+v, want everything the store holds", q, got.Incidents)
		}
	}
}

// Only a refused connection means "nothing is listening". Everything else stays
// an error, because not knowing is not the same as knowing there are no
// Incidents — and a Check that confuses the two prescribes `proximo update` for
// a watcher that is merely slow.
func TestIncidentAPIAbsentIsAskedOfTheErrorNotItsText(t *testing.T) {
	refused := fmt.Errorf("cannot reach the watcher: %w",
		&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)})
	if !incidentAPIAbsent(refused) {
		t.Error("a refused connection must read as an API that is not published")
	}
	timeout := fmt.Errorf("cannot reach the watcher: %w",
		&net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded})
	if incidentAPIAbsent(timeout) {
		t.Error("a timeout must stay an error: it says nothing about whether Incidents are recorded")
	}
	if incidentAPIAbsent(errors.New("the watcher's Incident API returned 500 Internal Server Error")) {
		t.Error("prose must not decide this — the error's own type does")
	}
}

// Zero means "everything the store holds", and the CLI depends on it: the left
// edge of the window an Incident fixes is the previous Incident of that service,
// which is routinely older than the --since being listed.
func TestIncidentAPIURLAsksForEverythingOnZero(t *testing.T) {
	if got := incidentAPIURL(0); strings.Contains(got, "since") {
		t.Errorf("incidentAPIURL(0) = %q, want no window: an anchor older than --since must still come back", got)
	}
	if got := incidentAPIURL(5 * time.Minute); !strings.HasSuffix(got, "?since=5m0s") {
		t.Errorf("incidentAPIURL(5m) = %q, want the window in the query", got)
	}
}
