package docker

import (
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
)

// dieEvent is the event Docker emits when a container's main process exits.
func dieEvent(name string, code string, at time.Time, labels map[string]string) events.Message {
	attrs := map[string]string{"name": name, "exitCode": code}
	for k, v := range labels {
		attrs[k] = v
	}
	return events.Message{
		Type:     events.ContainerEventType,
		Action:   events.ActionDie,
		Actor:    events.Actor{ID: "c-" + name, Attributes: attrs},
		TimeNano: at.UnixNano(),
	}
}

// workerLabels are a routeless worker's: observed because it says so, routed by
// nothing.
func workerLabels(project, service string) map[string]string {
	return map[string]string{
		TranscriptLabel:     "true",
		composeProjectLabel: project,
		ComposeServiceLabel: service,
	}
}

func TestObserveRecordsWhatTheRuntimeDeclares(t *testing.T) {
	at := time.Now().Add(-time.Minute).Truncate(time.Millisecond)

	tests := []struct {
		name string
		msg  events.Message
		want Incident
		ok   bool
	}{
		{
			name: "a non-zero exit",
			msg:  dieEvent("shop-worker-1", "1", at, workerLabels("shop", "worker")),
			want: Incident{At: at, Service: "shop/worker", Container: "shop-worker-1", Kind: IncidentExited, ExitCode: 1},
			ok:   true,
		},
		{
			name: "a clean exit is not an Incident",
			msg:  dieEvent("shop-worker-1", "0", at, workerLabels("shop", "worker")),
		},
		{
			name: "a container proximo knows nothing about",
			msg:  dieEvent("postgres", "1", at, map[string]string{composeProjectLabel: "shop", ComposeServiceLabel: "db"}),
		},
		{
			name: "a routed container needs no proximo.transcript",
			msg:  dieEvent("shop-web-1", "2", at, map[string]string{proximoHostsLabel: "shop.test", composeProjectLabel: "shop", ComposeServiceLabel: "web"}),
			want: Incident{At: at, Service: "shop/web", Container: "shop-web-1", Kind: IncidentExited, ExitCode: 2},
			ok:   true,
		},
		{
			name: "the stack's own containers are not Projects",
			msg:  dieEvent("proximo-watcher-1", "1", at, map[string]string{RoleLabel: "watcher", TranscriptLabel: "true"}),
		},
		{
			name: "outside a Compose project the container names itself",
			msg:  dieEvent("lonely", "1", at, map[string]string{TranscriptLabel: "true"}),
			want: Incident{At: at, Service: "lonely", Container: "lonely", Kind: IncidentExited, ExitCode: 1},
			ok:   true,
		},
		{
			name: "a restart",
			msg: events.Message{
				Type: events.ContainerEventType, Action: events.ActionRestart, TimeNano: at.UnixNano(),
				Actor: events.Actor{ID: "c1", Attributes: withName("shop-worker-1", workerLabels("shop", "worker"))},
			},
			want: Incident{At: at, Service: "shop/worker", Container: "shop-worker-1", Kind: IncidentRestarted},
			ok:   true,
		},
		{
			// A healthcheck that never passed declares nothing when it fails; the
			// one that was passing and stopped is asserted on its own below.
			name: "a transition to unhealthy on a check that never passed",
			msg: events.Message{
				Type: events.ContainerEventType, Action: events.ActionHealthStatusUnhealthy, TimeNano: at.UnixNano(),
				Actor: events.Actor{ID: "c1", Attributes: withName("shop-worker-1", workerLabels("shop", "worker"))},
			},
		},
		{
			name: "starting is not an Incident",
			msg: events.Message{
				Type: events.ContainerEventType, Action: events.ActionStart, TimeNano: at.UnixNano(),
				Actor: events.Actor{ID: "c1", Attributes: withName("shop-worker-1", workerLabels("shop", "worker"))},
			},
		},
		{
			name: "a network event is not a container's",
			msg: events.Message{
				Type: events.NetworkEventType, Action: events.ActionDie, TimeNano: at.UnixNano(),
				Actor: events.Actor{ID: "n1", Attributes: withName("shop-worker-1", workerLabels("shop", "worker"))},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewIncidentStore(0, 0)
			got, ok := s.Observe(tt.msg)
			if ok != tt.ok {
				t.Fatalf("Observe recorded %v, want %v (%+v)", ok, tt.ok, got)
			}
			if !ok {
				if len(s.List(time.Time{})) != 0 {
					t.Fatalf("nothing was recorded, yet the store holds %d", len(s.List(time.Time{})))
				}
				return
			}
			tt.want.ID = got.ID // derived; asserted on its own below
			if got != tt.want {
				t.Errorf("Observe = %+v, want %+v", got, tt.want)
			}
			if got.ID == "" {
				t.Error("an Incident with no id cannot be asked about")
			}
		})
	}
}

func withName(name string, labels map[string]string) map[string]string {
	out := map[string]string{"name": name}
	for k, v := range labels {
		out[k] = v
	}
	return out
}

// An OOM kill reaches the watcher as two events. It is one Incident: the row a
// developer reads says both what the exit code was and that the kernel chose it.
func TestObserveFoldsAnOOMIntoTheExitItCaused(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	s := NewIncidentStore(0, 0)

	oom := events.Message{
		Type: events.ContainerEventType, Action: events.ActionOOM, TimeNano: at.UnixNano(),
		Actor: events.Actor{ID: "c1", Attributes: withName("shop-worker-1", workerLabels("shop", "worker"))},
	}
	if _, ok := s.Observe(oom); ok {
		t.Fatal("the oom event is folded into the die it precedes, not recorded on its own")
	}
	die := dieEvent("shop-worker-1", "137", at.Add(time.Millisecond), workerLabels("shop", "worker"))
	die.Actor.ID = "c1"
	got, ok := s.Observe(die)
	if !ok {
		t.Fatal("the die after an oom was not recorded")
	}
	if !got.OOM || got.ExitCode != 137 {
		t.Errorf("got %+v, want the exit code and the OOM flag", got)
	}
	if want := "exited 137 (OOM-killed)"; got.Describe() != want {
		t.Errorf("Describe() = %q, want %q", got.Describe(), want)
	}
}

// An OOM long past says nothing about this exit: 137 is also what a plain
// `docker kill` produces, and claiming the kernel did it would be interpreting.
func TestObserveDoesNotAttributeAnOldOOM(t *testing.T) {
	s := NewIncidentStore(0, 0)
	at := time.Now().Add(-time.Hour)
	oom := events.Message{
		Type: events.ContainerEventType, Action: events.ActionOOM, TimeNano: at.UnixNano(),
		Actor: events.Actor{ID: "c1", Attributes: withName("shop-worker-1", workerLabels("shop", "worker"))},
	}
	s.Observe(oom)
	die := dieEvent("shop-worker-1", "137", time.Now(), workerLabels("shop", "worker"))
	die.Actor.ID = "c1"
	got, _ := s.Observe(die)
	if got.OOM {
		t.Errorf("got %+v, want no OOM claim", got)
	}
}

// The cap is per service, not global: a worker restarting every three seconds
// must not evict the only Incident another service ever produced.
func TestStoreCapsEachServiceOnItsOwn(t *testing.T) {
	s := NewIncidentStore(3, 0)
	base := time.Now().Add(-time.Hour)

	quiet := dieEvent("shop-db-1", "1", base, workerLabels("shop", "db"))
	s.Observe(quiet)
	for i := range 10 {
		s.Observe(dieEvent("shop-worker-1", "1", base.Add(time.Duration(i)*time.Second), workerLabels("shop", "worker")))
	}

	all := s.List(time.Time{})
	var worker, db int
	for _, inc := range all {
		switch inc.Service {
		case "shop/worker":
			worker++
		case "shop/db":
			db++
		}
	}
	if worker != 3 {
		t.Errorf("kept %d of the worker's Incidents, want the last 3", worker)
	}
	if db != 1 {
		t.Errorf("kept %d of the db's Incidents, want the 1 it produced", db)
	}
	// Most recent first, and the oldest of the worker's is the one dropped.
	if all[0].At.Before(all[len(all)-1].At) {
		t.Error("List must be most recent first")
	}
	for _, inc := range all {
		if inc.Service == "shop/worker" && inc.At.Before(base.Add(7*time.Second)) {
			t.Errorf("kept %s, want only the last 3", inc.At)
		}
	}
}

func TestStoreForgetsWhatIsOlderThanItsMaxAge(t *testing.T) {
	s := NewIncidentStore(0, time.Hour)
	s.Observe(dieEvent("shop-worker-1", "1", time.Now().Add(-2*time.Hour), workerLabels("shop", "worker")))
	fresh := dieEvent("shop-worker-1", "1", time.Now().Add(-time.Minute), workerLabels("shop", "worker"))
	s.Observe(fresh)

	all := s.List(time.Time{})
	if len(all) != 1 {
		t.Fatalf("holding %d Incidents, want only the fresh one", len(all))
	}
}

// The id is derived, so the same Incident has the same id across two
// invocations — and a replay of the same event records nothing twice.
func TestTheSameIncidentIsRecordedOnce(t *testing.T) {
	s := NewIncidentStore(0, 0)
	msg := dieEvent("shop-worker-1", "1", time.Now().Add(-time.Minute), workerLabels("shop", "worker"))
	first, _ := s.Observe(msg)
	if _, ok := s.Observe(msg); ok {
		t.Error("the same event was recorded twice")
	}
	if got := s.List(time.Time{}); len(got) != 1 || got[0].ID != first.ID {
		t.Errorf("store holds %+v, want the one Incident %s", got, first.ID)
	}
}

func TestListWindowsBySince(t *testing.T) {
	s := NewIncidentStore(0, 0)
	s.Observe(dieEvent("shop-worker-1", "1", time.Now().Add(-time.Hour), workerLabels("shop", "worker")))
	s.Observe(dieEvent("shop-worker-1", "2", time.Now().Add(-time.Minute), workerLabels("shop", "worker")))

	if got := s.List(time.Now().Add(-30 * time.Minute)); len(got) != 1 || got[0].ExitCode != 2 {
		t.Errorf("List(since) = %+v, want only the Incident inside the window", got)
	}
}

func TestPreviousOfTheSameService(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	a := Incident{ID: "a", Service: "shop/worker", At: base}
	b := Incident{ID: "b", Service: "shop/worker", At: base.Add(time.Minute)}
	other := Incident{ID: "c", Service: "shop/db", At: base.Add(30 * time.Second)}
	all := []Incident{b, other, a}

	if got, ok := PreviousIncident(b, all); !ok || got.ID != "a" {
		t.Errorf("previous of b = %+v (%v), want a", got, ok)
	}
	if _, ok := PreviousIncident(a, all); ok {
		t.Error("a is the first of its service; there is no previous one")
	}
}

func TestMatchServiceTakesTheBareNameWhenNothingContestsIt(t *testing.T) {
	known := []Service{"shop/worker", "shop/web", "blog/web", "lonely"}
	tests := []struct {
		want       string
		match      Service
		candidates []Service
	}{
		{want: "worker", match: "shop/worker"},
		{want: "shop/web", match: "shop/web"},
		{want: "lonely", match: "lonely"},
		{want: "web", candidates: []Service{"blog/web", "shop/web"}},
		{want: "queue", candidates: nil},
		{want: "shop/queue", candidates: nil},
	}
	for _, tt := range tests {
		match, candidates := MatchService(tt.want, known)
		if match != tt.match {
			t.Errorf("MatchService(%q) = %q, want %q", tt.want, match, tt.match)
		}
		if len(candidates) != len(tt.candidates) {
			t.Fatalf("MatchService(%q) candidates = %v, want %v", tt.want, candidates, tt.candidates)
		}
		for i := range candidates {
			if candidates[i] != tt.candidates[i] {
				t.Errorf("MatchService(%q) candidates = %v, want %v", tt.want, candidates, tt.candidates)
			}
		}
	}
}

// A Service carries the bare-vs-qualified rule, so nothing has to re-derive it.
func TestServiceKnowsItsBareName(t *testing.T) {
	if got := Service("shop/worker").Bare(); got != "worker" {
		t.Errorf("Service(\"shop/worker\").Bare() = %q, want worker", got)
	}
	// A container outside a Compose project has no Namespace and names itself.
	if got := Service("lonely").Bare(); got != "lonely" {
		t.Errorf("Service(\"lonely\").Bare() = %q, want lonely", got)
	}
}

func TestSelectIncidents(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	inc := func(id string, svc Service, when time.Duration, kind IncidentKind) Incident {
		return Incident{ID: id, Service: svc, At: at(when), Kind: kind}
	}
	worker := inc("i1", "shop/worker", time.Minute, IncidentExited)
	sick := inc("i2", "shop/worker", 2*time.Minute, IncidentUnhealthy)
	db := inc("i3", "shop/db", 3*time.Minute, IncidentRestarted)
	old := inc("i4", "shop/worker", time.Hour, IncidentExited)
	all := []Incident{worker, sick, db, old}

	tests := []struct {
		name  string
		query IncidentQuery
		want  []string
	}{
		{"the default holds nothing back", IncidentQuery{Since: at(30 * time.Minute)}, []string{"i1", "i2", "i3"}},
		{"one service keeps only its own", IncidentQuery{Service: "shop/worker", Since: at(30 * time.Minute)}, []string{"i1", "i2"}},
		{"a host narrows by the services that served it", IncidentQuery{Services: map[Service]bool{"shop/db": true}, Since: at(30 * time.Minute)}, []string{"i3"}},
		{"a host nothing can be attributed to keeps none", IncidentQuery{Services: map[Service]bool{}}, nil},
		{"since bounds the window", IncidentQuery{Since: at(90 * time.Second)}, []string{"i1"}},
		{"limit keeps the most recent", IncidentQuery{Limit: 1}, []string{"i1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectIncidents(all, tt.query)
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

// healthEvent is what Docker emits when a container's healthcheck changes state.
func healthEvent(action events.Action, id, name string, at time.Time, labels map[string]string) events.Message {
	return events.Message{
		Type: events.ContainerEventType, Action: action, TimeNano: at.UnixNano(),
		Actor: events.Actor{ID: id, Attributes: withName(name, labels)},
	}
}

// The healthcheck Incident is a check that was passing and stopped, never one
// that merely reports unhealthy: a worker waiting on postgres at boot goes
// starting → unhealthy and was never healthy, and a stall is healthy → unhealthy
// by construction. See docs/adr/0008-proximo-measures-the-project-concludes.md.
func TestUnhealthyIsAnIncidentOnlyForAHealthcheckThatWasPassing(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	labels := workerLabels("shop", "worker")

	t.Run("a check that never passed declares nothing", func(t *testing.T) {
		s := NewIncidentStore(0, 0)
		if inc, ok := s.Observe(healthEvent(events.ActionHealthStatusUnhealthy, "c1", "shop-worker-1", at, labels)); ok {
			t.Errorf("recorded %+v, want nothing: the container was never healthy", inc)
		}
	})

	t.Run("a check that was passing and stopped is an Incident", func(t *testing.T) {
		s := NewIncidentStore(0, 0)
		if _, ok := s.Observe(healthEvent(events.ActionHealthStatusHealthy, "c1", "shop-worker-1", at, labels)); ok {
			t.Error("turning healthy is not an Incident of its own")
		}
		inc, ok := s.Observe(healthEvent(events.ActionHealthStatusUnhealthy, "c1", "shop-worker-1", at.Add(time.Second), labels))
		if !ok {
			t.Fatal("a healthcheck that was passing and stopped is an Incident")
		}
		if inc.Kind != IncidentUnhealthy || inc.Container != "shop-worker-1" {
			t.Errorf("recorded %+v, want the unhealthy transition of shop-worker-1", inc)
		}
	})

	t.Run("a flapping check declares one Incident per fall", func(t *testing.T) {
		s := NewIncidentStore(0, 0)
		for i, action := range []events.Action{
			events.ActionHealthStatusHealthy, events.ActionHealthStatusUnhealthy,
			events.ActionHealthStatusHealthy, events.ActionHealthStatusUnhealthy,
		} {
			s.Observe(healthEvent(action, "c1", "shop-worker-1", at.Add(time.Duration(i)*time.Second), labels))
		}
		if got := s.List(time.Time{}); len(got) != 2 {
			t.Errorf("store holds %d Incidents, want 2 — flapping is the fact, and each fall is one", len(got))
		}
	})

	t.Run("a restart resets what the check declared", func(t *testing.T) {
		// The container id survives a restart, its healthcheck does not: it goes
		// back to starting, so the unhealthy that follows a die is a check that
		// never passed in this life.
		s := NewIncidentStore(0, 0)
		s.Observe(healthEvent(events.ActionHealthStatusHealthy, "c-shop-worker-1", "shop-worker-1", at, labels))
		s.Observe(dieEvent("shop-worker-1", "1", at.Add(time.Second), labels))
		if inc, ok := s.Observe(healthEvent(events.ActionHealthStatusUnhealthy, "c-shop-worker-1", "shop-worker-1", at.Add(2*time.Second), labels)); ok {
			t.Errorf("recorded %+v, want nothing: the check had not passed since the container restarted", inc)
		}
	})

	t.Run("a container proximo knows nothing about declares nothing either", func(t *testing.T) {
		s := NewIncidentStore(0, 0)
		unknown := map[string]string{composeProjectLabel: "shop", ComposeServiceLabel: "db"}
		s.Observe(healthEvent(events.ActionHealthStatusHealthy, "c2", "postgres", at, unknown))
		if _, ok := s.Observe(healthEvent(events.ActionHealthStatusUnhealthy, "c2", "postgres", at.Add(time.Second), unknown)); ok {
			t.Error("a container with neither a Route nor proximo.transcript produces no Incident")
		}
	})
}

// A watcher that restarted has seen no health_status event for a container that
// was already healthy, and would read its next stall as a check that never
// passed. SawHealthy is how the reconcile's container listing closes that hole.
func TestSawHealthyMakesAnAlreadyHealthyContainerOneThatCanFall(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	s := NewIncidentStore(0, 0)
	s.SawHealthy("c1")
	if _, ok := s.Observe(healthEvent(events.ActionHealthStatusUnhealthy, "c1", "shop-worker-1", at, workerLabels("shop", "worker"))); !ok {
		t.Error("a container proximo found already healthy must be one whose fall is an Incident")
	}
}
