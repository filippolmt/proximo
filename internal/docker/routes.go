package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
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
	Qualified   string // the qualified host the route also answers on ("" when it has none)
	Path        string // proximo.path prefix scoping the route ("" = all paths)
	URL         string
	Note        string
	Middlewares []string // active proximo middlewares (auth/cors/headers), in chain order
	TCPPorts    []int    // backend ports for a TCP-over-TLS (SNI) route; empty means an HTTP route
	TLSMode     string   // TCP TLS mode (terminate/passthrough); set only for TCP routes
	Backends    int      // number of backend containers serving this route; >1 means round-robin balanced
	Inspect     bool     // proximo.inspect asked for, and honoured: the route is served through the hop
	InspectNote string   // set when proximo.inspect was asked for but could not be honoured
	// Collision marks the row as a host another container claimed. It is said
	// structurally rather than read back out of Note, so a Check can route a
	// collision to its own explanation without pattern-matching prose that is
	// written for a human.
	Collision bool
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
	// The qualified host rides the bare host's row rather than getting one of its
	// own: it is always present, and doubling every listing to say so would make
	// the common case unreadable.
	if r.Qualified != "" {
		s += "  + " + r.Qualified
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
		rc, ok, info := classify(ctx, cli.ContainerInspect, c, tld)
		if !ok && !info.portFailed {
			continue // not a host route (e.g. a native container with no Host rule)
		}
		if note := healthGateNote(c); note != "" {
			// Health-gated and not yet healthy: the watcher withholds the route,
			// so surface it as starting/unhealthy (recognized, opted in, not
			// serving) instead of as a working URL or an absent container.
			for _, host := range rc.bareHosts() {
				routes = append(routes, Route{Container: rc.name, Host: host, Path: rc.path, Note: note})
			}
			continue
		}
		if !ok {
			// A proximo route the watcher skips for an unresolved port is
			// surfaced with the same reason the watcher logs, so status explains
			// why it is missing instead of hiding it.
			for _, host := range rc.bareHosts() {
				routes = append(routes, Route{Container: rc.name, Host: host, Note: info.port.hint()})
			}
			continue
		}
		if slices.Contains(info.tcpIgnoredHTTP, proximoInspectLabel) {
			refused[rc.name] = "inspection off: a TCP route has no response body to inject into"
		}
		served = append(served, rc)
	}
	// Apply the same host-by-host resolution the watcher uses, so status lists
	// exactly what is served — and, unlike before, what is not.
	resolved := resolveRoutes(served)
	for _, name := range resolved.inspectDropped {
		refused[name] = "inspection off: route balances across replicas"
	}
	routes = append(routes, servedRoutes(resolved, refused)...)
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Host != routes[j].Host {
			return routes[i].Host < routes[j].Host
		}
		return routes[i].Path < routes[j].Path
	})
	return routes, nil
}

// servedRoutes renders a resolution as status rows: one row per declared host of
// a served route, carrying the qualified host it also answers on, plus one row
// per host a route did not get. A Collision is reported, never silently
// resolved — a loser absent from the listing is exactly how the condition used
// to hide. refused maps a container to the reason its proximo.inspect could not
// be honoured.
func servedRoutes(resolved routeResolution, refused map[string]string) []Route {
	var routes []Route
	for _, c := range resolved.collisions {
		routes = append(routes, Route{Container: c.name, Host: c.host, Path: c.path, Note: c.note, Collision: true})
	}
	for _, rc := range resolved.kept {
		backends := len(rc.backends())
		for _, host := range rc.bareHosts() {
			// The qualified host is served by the same router and covered by the
			// same certificate, so it rides this row rather than getting another.
			qualified := rc.servedQualified(host)
			if rc.isTCP() {
				routes = append(routes, Route{Container: rc.name, Host: host, Qualified: qualified, TCPPorts: rc.tcpPorts, TLSMode: rc.tcpTLS, Backends: backends, InspectNote: refused[rc.name]})
				continue
			}
			routes = append(routes, Route{Container: rc.name, Host: host, Qualified: qualified, Path: rc.path, URL: "https://" + host + rc.path, Middlewares: rc.mw.active(), Backends: backends, Inspect: rc.inspect, InspectNote: refused[rc.name]})
		}
	}
	return routes
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
	// Roles are the proximo.role values of the running stack containers, so a
	// degraded stack (traefik up, watcher gone) is distinguishable from a
	// healthy one — which "is anything running?" alone cannot tell.
	Roles []string
}

// coreRoles are the stack services routing depends on — the three that
// docs/troubleshooting.md#degraded-stack names, since a stack missing one of
// them stops updating routes while still looking like it is running. The
// inspector is deliberately not one: it is idle unless a route carries
// proximo.inspect, and an older stack that predates it is a version skew, not a
// degraded stack.
var coreRoles = []string{"traefik", "dns", "watcher"}

// MissingRoles returns the core stack services that are not running, in the
// order they are named above. Empty means the stack is whole.
func (s StackInfo) MissingRoles() []string {
	var missing []string
	for _, role := range coreRoles {
		if !slices.Contains(s.Roles, role) {
			missing = append(missing, role)
		}
	}
	return missing
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
		if role := c.Labels[roleLabel]; role != "" && !slices.Contains(info.Roles, role) {
			info.Roles = append(info.Roles, role)
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

// traefikConfigPath is where the stack mounts Traefik's static configuration
// inside its container. It is the anchor for finding that file back on the host.
const traefikConfigPath = "/etc/traefik/traefik.yml"

// StackRecordsAccessLog reports whether the running Traefik writes an access
// log. Without one there is no Access record for any route, and `proximo errors`
// would be silent for a reason that has nothing to do with a developer's code.
//
// It reads the config the running container has mounted rather than the copy
// this CLI would materialize: Traefik reads its static configuration once, at
// startup, so a stack brought up before the access log existed keeps running
// without it however new the file on disk is. That skew is the whole point of
// the question.
func StackRecordsAccessLog(ctx context.Context) (bool, error) {
	cli, err := newClient()
	if err != nil {
		return false, err
	}
	defer cli.Close()

	res, err := cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return false, err
	}
	for _, c := range res.Items {
		if c.Labels[roleLabel] != "traefik" {
			continue
		}
		got, err := cli.ContainerInspect(ctx, c.ID, client.ContainerInspectOptions{})
		if err != nil {
			return false, err
		}
		return accessLogConfigured(got.Container.Mounts, os.ReadFile)
	}
	return false, errors.New("the stack's Traefik is not running")
}

// accessLogConfigured reads the mounted traefik.yml back off the host and
// reports whether it turns the access log on. An unmounted or unreadable config
// is an error rather than a "no": not knowing is not the same as knowing it is
// off, and a Check that confuses the two sends a developer after the wrong thing.
func accessLogConfigured(mounts []container.MountPoint, read func(string) ([]byte, error)) (bool, error) {
	for _, m := range mounts {
		if m.Destination != traefikConfigPath {
			continue
		}
		raw, err := read(m.Source)
		if err != nil {
			return false, fmt.Errorf("reading the running Traefik's config (%s): %w", m.Source, err)
		}
		return accessLogRe.Match(raw), nil
	}
	return false, fmt.Errorf("the running Traefik has no %s mounted", traefikConfigPath)
}

// accessLogRe matches the access log key at the top level of Traefik's config,
// so a mention of it in a comment does not count as having it on.
var accessLogRe = regexp.MustCompile(`(?m)^accessLog:`)
