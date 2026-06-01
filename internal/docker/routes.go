package docker

import (
	"context"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Route is a routed container and a URL it is reachable at.
type Route struct {
	Container string
	Host      string
	URL       string
}

// Routes lists the routed containers and the hostnames they serve. Hosts come
// from the proximo.hosts label when present, otherwise from the container's
// native Traefik router rules.
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
		name := primaryName(c)
		for _, host := range routeHosts(c.Labels) {
			routes = append(routes, Route{Container: name, Host: host, URL: "https://" + host})
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Host < routes[j].Host })
	return routes, nil
}

// routeHosts returns the hostnames a container serves: the proximo.hosts label
// when present, otherwise the hosts declared in its native Traefik router rules.
func routeHosts(labels map[string]string) []string {
	if hosts := proximoHosts(labels); len(hosts) > 0 {
		return hosts
	}
	return hostsFromLabels(labels)
}

func primaryName(c container.Summary) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return short(c.ID)
}
