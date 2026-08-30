package docker

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// newClient builds a Docker API client from the environment (moby/client
// negotiates the daemon API version automatically) — the single construction
// used by every host-side Docker query (Routes, StackStatus) and the in-stack
// watcher, so they cannot drift.
func newClient() (*client.Client, error) {
	return client.New(client.FromEnv)
}

// Route is a routed container and a URL it is reachable at. When Note is set the
// container opted in but is not effectively served (e.g. an unresolved backend
// port the watcher skips); URL is then empty and Note explains why.
type Route struct {
	Container   string
	Host        string
	Path        string // proximo.path prefix scoping the route ("" = all paths)
	URL         string
	Note        string
	Middlewares []string // active proximo middlewares (auth/cors/headers), in chain order
	TCPPorts    []int    // backend ports for a TCP-over-TLS (SNI) route; empty means an HTTP route
	TLSMode     string   // TCP TLS mode (terminate/passthrough); set only for TCP routes
	Backends    int      // number of backend containers serving this route; >1 means round-robin balanced
	Inspect     bool     // proximo.inspect asked for, and honoured: the route is served through the hop
	InspectNote string   // set when proximo.inspect was asked for but could not be honoured
}

// Display renders the route's target for `proximo status`: the HTTPS URL for an
// HTTP route, or a `tcp://host:ports (mode)` summary for a TCP-over-TLS route,
// suffixed with a balanced marker when more than one backend serves it.
func (r Route) Display() string {
	s := r.URL
	if len(r.TCPPorts) > 0 {
		ports := make([]string, len(r.TCPPorts))
		for i, p := range r.TCPPorts {
			ports[i] = strconv.Itoa(p)
		}
		s = fmt.Sprintf("tcp://%s:%s (%s)", r.Host, strings.Join(ports, ","), r.TLSMode)
	}
	if r.Backends > 1 {
		s += fmt.Sprintf(" (balanced ×%d)", r.Backends)
	}
	return s
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
	// Reasons a proximo.inspect label could not be honoured, keyed by container.
	// A refusal must reach `proximo status`: one that lives only in the watcher's
	// log is indistinguishable, from here, from Inspection simply not working.
	refused := map[string]string{}
	for _, c := range cs {
		if !isRouted(c) {
			continue
		}
		rc, ok, info := classify(ctx, cli.ContainerInspect, c)
		if !ok && !info.portFailed {
			continue // not a host route (e.g. a native container with no Host rule)
		}
		if note := healthGateNote(c); note != "" {
			// Health-gated and not yet healthy: the watcher withholds the route,
			// so surface it as starting/unhealthy (recognized, opted in, not
			// serving) instead of as a working URL or an absent container.
			for _, host := range rc.hosts {
				routes = append(routes, Route{Container: rc.name, Host: host, Path: rc.path, Note: note})
			}
			continue
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
		if slices.Contains(info.tcpIgnoredHTTP, proximoInspectLabel) {
			refused[rc.name] = "inspection off: a TCP route has no response body to inject into"
		}
		served = append(served, rc)
	}
	// Apply the same (host, prefix) conflict resolution the watcher uses so
	// status lists only the routes actually served (status stays quiet about
	// the dropped losers — the watcher logs them).
	resolved := resolveRouteConflicts(served)
	for _, name := range resolved.inspectDropped {
		refused[name] = "inspection off: route balances across replicas"
	}
	kept := resolved.kept
	for _, rc := range kept {
		backends := len(rc.backends())
		for _, host := range rc.hosts {
			if rc.isTCP() {
				routes = append(routes, Route{Container: rc.name, Host: host, TCPPorts: rc.tcpPorts, TLSMode: rc.tcpTLS, Backends: backends, InspectNote: refused[rc.name]})
				continue
			}
			routes = append(routes, Route{Container: rc.name, Host: host, Path: rc.path, URL: "https://" + host + rc.path, Middlewares: rc.mw.active(), Backends: backends, Inspect: rc.inspect, InspectNote: refused[rc.name]})
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

const (
	// versionLabel is set on every stack service to the CLI version that
	// materialized it, so the running stack's version can be read back.
	versionLabel = "proximo.version"
	// imageLabel is set on the three services that run the published stack
	// image, to the ref they were actually started from. Traefik and the
	// observability services carry none — they run their own upstream images.
	imageLabel = "proximo.image"
)

// StackInfo is what the host can read back off a running stack: whether one is
// running at all, the CLI version that materialized it, and the image ref its
// Go services were started from.
//
// Running distinguishes "no stack" from "stack predating version stamping"
// (running, empty Version — a pre-0.4.0 stack carries no version label).
type StackInfo struct {
	Running bool
	Version string
	Image   string
}

// StackStatus reads the proximo.version and proximo.image labels off the
// running stack. Docker errors are returned so callers can tell "stack down"
// from "Docker broken".
func StackStatus(ctx context.Context) (StackInfo, error) {
	cli, err := newClient()
	if err != nil {
		return StackInfo{}, err
	}
	defer cli.Close()

	res, err := cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return StackInfo{}, err
	}
	var info StackInfo
	for _, c := range res.Items {
		if _, isStack := c.Labels[roleLabel]; !isStack {
			continue
		}
		if !info.Running {
			info.Running, info.Version = true, c.Labels[versionLabel]
		}
		// Only the image-backed services carry the ref; traefik may well be the
		// first stack container listed, so keep looking until one has it.
		if info.Image == "" {
			info.Image = c.Labels[imageLabel]
		}
	}
	return info, nil
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
