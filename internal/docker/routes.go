package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// newClient builds a Docker API client from the environment (moby/client
// negotiates the daemon API version automatically) — the single construction
// used by every host-side Docker query (Routes, StackVersion) and the in-stack
// watcher, so they cannot drift.
func newClient() (*client.Client, error) {
	return client.New(client.FromEnv)
}

// Route is a routed container and a URL it is reachable at. When Note is set the
// container opted in but is not effectively served (e.g. an unresolved backend
// port the watcher skips); URL is then empty and Note explains why.
type Route struct {
	Container string
	Host      string
	Path      string // proximo.path prefix scoping the route ("" = all paths)
	URL       string
	Note      string
}

// Routes lists the effective routing state `proximo status` reports. It uses the
// same classifier (classify) the watcher uses to generate routers and
// certificates, so the two cannot disagree about which hosts are served. A
// proximo container whose backend port is ambiguous/unresolved (the watcher skips
// it) is flagged with a Note rather than listed as a working route. The
// classifyInfo diagnostics are ignored here so status stays quiet — no watcher
// warnings. tld names the Traefik dashboard self-route host (traefik.<tld>),
// which the watcher injects outside container classification and is therefore
// surfaced here the same way: present whenever the stack's Traefik is running.
func Routes(ctx context.Context, tld string) ([]Route, error) {
	cli, err := newClient()
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	res, err := cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, err
	}
	cs := res.Items

	routes := dashboardRoutes(cs, tld)
	var served []routedContainer
	for _, c := range cs {
		if !isRouted(c) {
			continue
		}
		rc, ok, info := classify(ctx, cli.ContainerInspect, c)
		if !ok && !info.portFailed {
			continue // not a host route (e.g. a native container with no Host rule)
		}
		if !ok {
			// A proximo route the watcher skips for an unresolved port is
			// surfaced with the same reason the watcher logs, so status explains
			// why it is missing instead of hiding it.
			for _, host := range rc.hosts {
				routes = append(routes, Route{Container: rc.name, Host: host, Note: info.port.hint()})
			}
			continue
		}
		served = append(served, rc)
	}
	// Apply the same (host, prefix) conflict resolution the watcher uses so
	// status lists only the routes actually served (status stays quiet about
	// the dropped losers — the watcher logs them).
	kept, _ := resolveRouteConflicts(served)
	for _, rc := range kept {
		for _, host := range rc.hosts {
			routes = append(routes, Route{Container: rc.name, Host: host, Path: rc.path, URL: "https://" + host + rc.path})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Host != routes[j].Host {
			return routes[i].Host < routes[j].Host
		}
		return routes[i].Path < routes[j].Path
	})
	return routes, nil
}

// dashboardRoutes returns the Traefik dashboard self-route when the stack's
// Traefik container is running. The watcher writes that route on every
// reconcile (watcher.dashboardRoute), so status mirrors the same condition
// instead of classifying the role-labeled container (which never routes).
func dashboardRoutes(cs []container.Summary, tld string) []Route {
	for _, c := range cs {
		if c.Labels[roleLabel] == "traefik" {
			host := dashboardHost(tld)
			return []Route{{Container: primaryName(c), Host: host, URL: "https://" + host}}
		}
	}
	return nil
}

func primaryName(c container.Summary) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return short(c.ID)
}

// versionLabel is set on every stack service to the CLI version that
// materialized it, so the running stack's version can be read back.
const versionLabel = "proximo.version"

// StackVersion reads the proximo.version label from a running stack container
// (any service carries the role label). running reports whether a stack
// container was found at all, so callers can tell "no stack" (running false)
// from "stack predating version stamping" (running true, empty version — a
// pre-0.4.0 stack carries no version label). Docker errors are returned so
// callers can tell "stack down" from "Docker broken".
func StackVersion(ctx context.Context) (ver string, running bool, err error) {
	cli, err := newClient()
	if err != nil {
		return "", false, err
	}
	defer cli.Close()

	res, err := cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return "", false, err
	}
	for _, c := range res.Items {
		if _, isStack := c.Labels[roleLabel]; isStack {
			return c.Labels[versionLabel], true, nil
		}
	}
	return "", false, nil
}

// DisplayVersion names a stack version for human output. A running stack
// without a version label predates version stamping (the proximo.version label
// was introduced in 0.4.0), so an empty version is shown as "pre-0.4.0".
func DisplayVersion(ver string) string {
	if ver == "" {
		return "pre-0.4.0"
	}
	return ver
}

// VersionSkew returns a human-readable warning when the running stack version
// differs from the installed CLI version, or "" when they match or the stack is
// not running. An unlabeled (pre-0.4.0) stack never matches, so it always
// warns — it must not be mistaken for a stack that is down.
func VersionSkew(stackVer string, running bool, cliVer string) string {
	if !running || stackVer == cliVer {
		return ""
	}
	return fmt.Sprintf("stack is running %s but the CLI is %s; run `proximo update` to converge", DisplayVersion(stackVer), cliVer)
}
