package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"syscall"
	"time"

	"github.com/filippolmt/proximo/internal/config"
)

// IncidentListing is what the watcher hands back, and what the CLI decodes.
// Started travels with the Incidents rather than on a second endpoint: an empty
// listing means either that nothing happened or that a restart threw everything
// away, and those are very different answers to give a developer.
type IncidentListing struct {
	Started   time.Time  `json:"started"`
	Incidents []Incident `json:"incidents"`
}

// IncidentAPI is the read side of the watcher's Incident store, published on
// loopback only — the same shape the hop's read API has, and for the same
// reason: nothing a browser can reach may read back what proximo observed.
type IncidentAPI struct{ Store *IncidentStore }

func (a IncidentAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/incidents" {
		http.NotFound(w, r)
		return
	}
	var since time.Time
	if d, err := time.ParseDuration(r.URL.Query().Get("since")); err == nil && d > 0 {
		since = time.Now().Add(-d)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(IncidentListing{
		Started:   a.Store.Started(),
		Incidents: a.Store.List(since),
	}); err != nil {
		log.Printf("proximo watcher: encoding the Incident listing: %v", err)
	}
}

// incidentAPIURL addresses the watcher's read API. It is published on loopback
// only, so the CLI is the only thing that can reach it — a container cannot.
func incidentAPIURL(since time.Duration) string {
	q := ""
	if since > 0 {
		q = "?since=" + since.String()
	}
	return fmt.Sprintf("http://127.0.0.1:%d/incidents%s", config.WatcherAPIPort, q)
}

// IncidentsFromStack asks the running watcher what the runtime declared about the
// containers proximo knows, within since of now (zero for everything it holds).
//
// It is the second source `proximo errors` reads, beside the hop: the Incident
// store lives in the watcher because that is the only stack service with both the
// Docker socket and the event subscription. That is also a second way for the
// command to fail, which is why the error is worded for a reader rather than
// swallowed.
func IncidentsFromStack(ctx context.Context, since time.Duration) (IncidentListing, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, incidentAPIURL(since), nil)
	if err != nil {
		return IncidentListing{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return IncidentListing{}, fmt.Errorf("cannot reach the watcher's Incident API on 127.0.0.1:%d: %w", config.WatcherAPIPort, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return IncidentListing{}, fmt.Errorf("the watcher's Incident API returned %s", resp.Status)
	}
	var out IncidentListing
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return IncidentListing{}, fmt.Errorf("the watcher's Incident API returned something unreadable: %w", err)
	}
	return out, nil
}

// StackRecordsIncidents reports whether the running watcher publishes the
// Incident API at all. A stack older than this CLI does not, and then `proximo
// errors` is silent about a restart-looping worker for a reason that has nothing
// to do with a developer's code — which is exactly the kind of silence a Check
// exists to name.
//
// A refused connection is a "no" rather than an error: the port is published by
// the stack itself, so nothing else can be holding it, and a stack that is not
// running at all is already a failed Check of its own. Every other failure —
// a timeout, an HTTP status, an unreadable body — stays an error: not knowing is
// not the same as knowing there are no Incidents, and a Check that confuses the
// two prescribes `proximo update` for a watcher that is merely slow.
func StackRecordsIncidents(ctx context.Context) (bool, error) {
	if _, err := IncidentsFromStack(ctx, time.Second); err != nil {
		if incidentAPIAbsent(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// incidentAPIAbsent reports whether an error means nothing is listening at all.
// It is asked of the error's own type rather than of its text: proximo does not
// pattern-match prose written for a person, here any more than it does for a
// Collision.
//
// ponytail: one errno. A stack whose watcher is up but wedged answers neither
// "absent" nor "present" — the dial times out, which stays an error and reports
// as one. Widen this only if a wedged watcher turns out to be common, and give it
// its own wording rather than folding it in here.
func incidentAPIAbsent(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
