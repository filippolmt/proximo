package docker

import (
	"slices"
	"testing"

	"github.com/docker/docker/api/types/container"
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

func TestRouteHosts(t *testing.T) {
	// proximo.hosts is the primary source.
	got := routeHosts(map[string]string{"proximo.hosts": "app.test, api.test"})
	if !slices.Equal(got, []string{"app.test", "api.test"}) {
		t.Fatalf("proximo route hosts = %v", got)
	}

	// proximo.hosts takes precedence over native Traefik rules.
	got = routeHosts(map[string]string{
		"proximo.hosts":               "primary.test",
		"traefik.http.routers.x.rule": "Host(`legacy.test`)",
	})
	if !slices.Equal(got, []string{"primary.test"}) {
		t.Fatalf("proximo should win over traefik rule, got %v", got)
	}

	// Falls back to Traefik rule hosts when no proximo label.
	got = routeHosts(map[string]string{"traefik.http.routers.web.rule": "Host(`web.test`)"})
	if !slices.Equal(got, []string{"web.test"}) {
		t.Fatalf("traefik fallback hosts = %v", got)
	}
}
