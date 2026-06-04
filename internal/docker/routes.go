package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// newClient builds a Docker API client from the environment with API-version
// negotiation — the single construction used by every host-side Docker query
// (Routes, StackVersion) and the in-stack watcher, so they cannot drift.
func newClient() (*client.Client, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

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
	cli, err := newClient()
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

	cs, err := cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return "", false, err
	}
	for _, c := range cs {
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
