package docker

import (
	"slices"
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
