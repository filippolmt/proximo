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
