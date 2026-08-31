package docker

import (
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func makeSummary(labels map[string]string) container.Summary {
	return container.Summary{Labels: labels}
}

func TestHostsFromLabels(t *testing.T) {
	labels := map[string]string{
		"traefik.enable":                                     "true",
		"traefik.http.routers.web.rule":                      "Host(`web.test`)",
		"traefik.http.routers.api.rule":                      "Host(`api.test`) || Host(`api2.test`)",
		"traefik.http.services.web.loadbalancer.server.port": "80",
		"unrelated": "Host(`nope.test`)",
	}
	got := hostsFromLabels(labels)
	slices.Sort(got)
	want := []string{"api.test", "api2.test", "web.test"}
	if !slices.Equal(got, want) {
		t.Fatalf("hostsFromLabels = %v, want %v", got, want)
	}
}

func TestHealthRoutableAndNote(t *testing.T) {
	mk := func(status container.HealthStatus, labels map[string]string) container.Summary {
		c := container.Summary{Labels: labels}
		if status != "" {
			c.Health = &container.HealthSummary{Status: status}
		}
		return c
	}

	cases := []struct {
		name     string
		c        container.Summary
		routable bool
		noteHas  string // substring expected in healthGateNote ("" = empty note)
	}{
		{"no healthcheck", mk("", nil), true, ""},
		{"explicit none", mk(container.NoHealthcheck, nil), true, ""},
		{"healthy", mk(container.Healthy, nil), true, ""},
		{"starting gated", mk(container.Starting, nil), false, "starting"},
		{"unhealthy gated", mk(container.Unhealthy, nil), false, "unhealthy"},
		{"starting opt-out", mk(container.Starting, map[string]string{proximoHealthLabel: "false"}), true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHealthRoutable(tc.c); got != tc.routable {
				t.Errorf("isHealthRoutable = %v, want %v", got, tc.routable)
			}
			note := healthGateNote(tc.c)
			if tc.noteHas == "" {
				if note != "" {
					t.Errorf("healthGateNote = %q, want empty", note)
				}
			} else if !strings.Contains(note, tc.noteHas) {
				t.Errorf("healthGateNote = %q, want substring %q", note, tc.noteHas)
			}
		})
	}
}

func TestIsRoutedOptIn(t *testing.T) {
	if !isRouted(makeSummary(map[string]string{"traefik.enable": "true"})) {
		t.Error("traefik.enable=true should be routed")
	}
	if isRouted(makeSummary(map[string]string{})) {
		t.Error("no label should not be routed (opt-in)")
	}
	if isRouted(makeSummary(map[string]string{"proximo.role": "traefik", "traefik.enable": "true"})) {
		t.Error("stack containers must never be routed")
	}
}

// TestRouteDisplay: an HTTP route shows its URL; a TCP route shows a
// tcp://host:ports (mode) summary; a route with more than one backend is marked
// balanced.
func TestRouteDisplay(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		want  string
	}{
		{"http", Route{Host: "web.test", URL: "https://web.test", Backends: 1}, "https://web.test"},
		{"http balanced", Route{Host: "app.test", URL: "https://app.test", Backends: 2}, "https://app.test (balanced ×2)"},
		{"tcp terminate", Route{Host: "db.test", TCPPorts: []int{5432}, TLSMode: "terminate", Backends: 1}, "tcp://db.test:5432 (terminate)"},
		{"tcp passthrough multi-port", Route{Host: "mqtt.test", TCPPorts: []int{8883, 1883}, TLSMode: "passthrough", Backends: 1}, "tcp://mqtt.test:8883,1883 (passthrough)"},
		{"tcp balanced", Route{Host: "db.test", TCPPorts: []int{5432}, TLSMode: "terminate", Backends: 3}, "tcp://db.test:5432 (terminate) (balanced ×3)"},
		{"qualified rides the bare host's row", Route{Host: "api.test", Qualified: "api.shop.test", URL: "https://api.test", Backends: 1}, "https://api.test  + api.shop.test"},
		{"qualified on a tcp route", Route{Host: "db.test", Qualified: "db.shop.test", TCPPorts: []int{5432}, TLSMode: "terminate", Backends: 1}, "tcp://db.test:5432 (terminate)  + db.shop.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.route.Display(); got != tt.want {
				t.Errorf("Display() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestServedRoutes: a resolution becomes status rows — one row per declared
// host carrying its qualified host, and one row per host a route did not get,
// so a Collision is visible rather than an absence.
func TestServedRoutes(t *testing.T) {
	resolved := routeResolution{
		kept: []routedContainer{{
			name: "shop-api-1", hosts: []string{"api.test", "api.shop.test"}, port: 80, proximo: true,
			ns: "shop", qual: map[string]string{"api.test": "api.shop.test"},
		}},
		collisions: []hostCollision{{name: "work-api-1", host: "api.test", note: "api.test is served by shop-api-1"}},
	}
	routes := servedRoutes(resolved, nil)
	if len(routes) != 2 {
		t.Fatalf("routes = %+v, want one served row and one collision row", routes)
	}
	if routes[0].Container != "work-api-1" || routes[0].Note == "" || routes[0].URL != "" {
		t.Errorf("collision row = %+v, want the loser with a note and no URL", routes[0])
	}
	served := routes[1]
	if served.Host != "api.test" || served.Qualified != "api.shop.test" || served.URL != "https://api.test" {
		t.Errorf("served row = %+v, want api.test carrying api.shop.test", served)
	}
}

// TestServedRoutesDropsWithdrawnQualified: a qualified host that went to another
// claimant must not be advertised on the bare host's row.
func TestServedRoutesDropsWithdrawnQualified(t *testing.T) {
	resolved := routeResolution{kept: []routedContainer{{
		name: "shop-api-1", hosts: []string{"api.test"}, port: 80, proximo: true,
		ns: "shop", qual: map[string]string{"api.test": "api.shop.test"},
	}}}
	routes := servedRoutes(resolved, nil)
	if len(routes) != 1 || routes[0].Qualified != "" {
		t.Fatalf("routes = %+v, want no qualified host advertised", routes)
	}
}

// A stack is whole only when the services routing depends on it are running:
// traefik alone is a degraded stack, which is exactly the state that stops
// routes from updating.
func TestMissingRolesNamesTheServicesThatAreDown(t *testing.T) {
	cases := []struct {
		name  string
		roles []string
		want  string
	}{
		{"whole", []string{"traefik", "dns", "watcher", "inspector"}, ""},
		{"no watcher", []string{"traefik", "dns"}, "watcher"},
		{"nothing", nil, "traefik,dns,watcher"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(StackInfo{Running: true, Roles: tc.roles}.MissingRoles(), ",")
			if got != tc.want {
				t.Errorf("MissingRoles = %q, want %q", got, tc.want)
			}
		})
	}
}

// Who publishes a host port is the question the port checks ask, and the
// stack's own claim must be told apart from a stranger's: only the second is a
// failure, and only the second has a container to point the developer at.
func TestPortOwnersNamesTheContainerAndWhetherItIsTheStack(t *testing.T) {
	cs := []container.Summary{
		{
			Names:  []string{"/proximo-traefik-1"},
			Labels: map[string]string{roleLabel: "traefik"},
			Ports: []container.PortSummary{
				{PublicPort: 80, PrivatePort: 80, Type: "tcp"},
				{PublicPort: 443, PrivatePort: 443, Type: "tcp"},
			},
		},
		{
			Names: []string{"/some-nginx"},
			Ports: []container.PortSummary{{PublicPort: 8080, PrivatePort: 80, Type: "tcp"}},
		},
		{
			// A container port nobody published: not a claim on the host.
			Names: []string{"/internal-only"},
			Ports: []container.PortSummary{{PrivatePort: 5432, Type: "tcp"}},
		},
	}

	owners := portOwners(cs)
	if got := owners[PortKey(443, "tcp")]; got.Container != "proximo-traefik-1" || !got.Stack {
		t.Errorf(":443 owner = %+v, want the stack's traefik", got)
	}
	if got := owners[PortKey(8080, "tcp")]; got.Container != "some-nginx" || got.Stack {
		t.Errorf(":8080 owner = %+v, want a non-stack container", got)
	}
	if _, ok := owners[PortKey(5432, "tcp")]; ok {
		t.Error("an unpublished container port was reported as holding a host port")
	}
	if _, ok := owners[PortKey(443, "udp")]; ok {
		t.Error(":443/tcp answered for :443/udp")
	}
}

// A container that carries proximo.transcript and no host is inventory: it is
// listed, marked as having no route, and it is not a warning. Without the row
// the only way to learn the label took effect is to wait for something to go
// wrong.
func TestObservedRoutesListTheContainersProximoWatchesWithoutRouting(t *testing.T) {
	worker := container.Summary{
		Names:  []string{"/shop-worker-1"},
		Labels: map[string]string{TranscriptLabel: "true", composeProjectLabel: "shop", composeServiceLabel: "worker"},
	}
	routed := container.Summary{
		Names:  []string{"/shop-web-1"},
		Labels: map[string]string{proximoHostsLabel: "app.test", TranscriptLabel: "true"},
	}
	plain := container.Summary{Names: []string{"/postgres"}}
	stack := container.Summary{
		Names:  []string{"/proximo-watcher-1"},
		Labels: map[string]string{roleLabel: "watcher", TranscriptLabel: "true"},
	}

	got := observedRoutes([]container.Summary{worker, routed, plain, stack})
	if len(got) != 1 {
		t.Fatalf("observed rows = %+v, want only the routeless worker", got)
	}
	row := got[0]
	switch {
	case row.Container != "shop-worker-1":
		t.Errorf("row names %q, want shop-worker-1", row.Container)
	case !row.Observed:
		t.Error("the row must be marked observed, so status prints it as inventory rather than as a warning")
	case row.Host != "" || row.URL != "":
		t.Errorf("row = %+v, want no host and no URL", row)
	case !strings.Contains(row.Note, "no route") || !strings.Contains(row.Note, "shop/worker"):
		t.Errorf("note = %q, want it to say there is no route and name the service to ask about", row.Note)
	case strings.Contains(row.Note, "`"):
		t.Errorf("note = %q, want no command: `status` is an inventory and never prints a Remedy", row.Note)
	}
}
