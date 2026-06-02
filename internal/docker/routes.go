package docker

import (
	"context"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Route is a routed container and a URL it is reachable at. When Note is set the
// container opted in but is not effectively served (e.g. an unresolved backend
// port the watcher skips); URL is then empty and Note explains why.
type Route struct {
	Container string
	Host      string
	URL       string
	Note      string
}

// Routes lists the effective routing state `proximo status` reports. It uses the
// same classifier (classify) the watcher uses to generate routers and
// certificates, so the two cannot disagree about which hosts are served. A
// proximo container whose backend port is ambiguous/unresolved (the watcher skips
// it) is flagged with a Note rather than listed as a working route. The
// classifyInfo diagnostics are ignored here so status stays quiet — no watcher
// warnings.
func Routes(ctx context.Context) ([]Route, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	cs, err := cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}

	var routes []Route
	for _, c := range cs {
		if !isRouted(c) {
			continue
		}
		rc, ok, info := classify(ctx, cli.ContainerInspect, c)
		if !ok && !info.portFailed {
			continue // not a host route (e.g. a native container with no Host rule)
		}
		// A proximo route the watcher skips for an unresolved port is surfaced
		// with the same reason the watcher logs, so status explains why it is
		// missing instead of hiding it.
		note := ""
		if !ok {
			note = info.port.hint()
		}
		for _, host := range rc.hosts {
			r := Route{Container: rc.name, Host: host}
			if ok {
				r.URL = "https://" + host
			} else {
				r.Note = note
			}
			routes = append(routes, r)
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Host < routes[j].Host })
	return routes, nil
}

func primaryName(c container.Summary) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return short(c.ID)
}
