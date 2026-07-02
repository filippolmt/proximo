package docker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"golang.org/x/crypto/bcrypt"
)

const (
	// roleLabel marks the proximo stack's own containers.
	roleLabel = "proximo.role"
	// enableLabel opts a container in to routing (Traefik's native label).
	enableLabel = "traefik.enable"
	// networkLabel disambiguates which network to use for a multi-network
	// container.
	networkLabel = "traefik.docker.network"

	// proximoHostsLabel is the proximo-native opt-in label: a comma-separated
	// list of hostnames. Its presence (non-empty) opts a container in.
	proximoHostsLabel = "proximo.hosts"
	// proximoPortLabel sets the backend port explicitly; when absent the
	// watcher auto-detects a single exposed port.
	proximoPortLabel = "proximo.port"
	// proximoEnableLabel is the opt-out switch; defaults to true.
	proximoEnableLabel = "proximo.enable"
	// proximoRedirectLabel opts a routed container in to an HTTP->HTTPS redirect
	// for its hosts; defaults to false (truthy: true/1/yes, case-insensitive).
	proximoRedirectLabel = "proximo.redirect"
	// proximoPathLabel scopes a container's routes to a URL path prefix (must
	// start with "/"), letting several containers share one host on distinct
	// prefixes. Absent means match all paths, as before.
	proximoPathLabel = "proximo.path"
	// proximoPathStripLabel strips the matched path prefix before the request
	// reaches the backend; defaults to false (truthy: true/1/yes).
	proximoPathStripLabel = "proximo.path.strip"
	// proximoHealthLabel gates route publication on the container's Docker
	// health. It defaults to true: a container that declares a healthcheck is
	// routed only while healthy. Setting it to a falsy value (false/0/no) opts
	// out — the container routes as soon as it is running, regardless of health.
	proximoHealthLabel = "proximo.health"

	// proximoTCPPortLabel opts a container's hosts into TCP-over-TLS routing on
	// the given backend port; the connection's TLS SNI (the host) is the routing
	// key. proximoTCPPortsLabel is the comma-separated multi-port form.
	proximoTCPPortLabel  = "proximo.tcp.port"
	proximoTCPPortsLabel = "proximo.tcp.ports"
	// proximoTCPTLSLabel selects the TLS mode for a container's TCP routes:
	// "terminate" (default — the proxy terminates with the per-host proximo cert
	// and forwards plaintext to the backend) or "passthrough" (the proxy routes
	// the raw TLS stream by SNI and the backend terminates TLS end-to-end).
	proximoTCPTLSLabel = "proximo.tcp.tls"
	// tcpTLSTerminate and tcpTLSPassthrough are the two valid proximo.tcp.tls
	// values; tcpTLSTerminate is the default when the label is absent.
	tcpTLSTerminate   = "terminate"
	tcpTLSPassthrough = "passthrough"

	// routerFilePrefix prefixes the per-container Traefik dynamic config files
	// the watcher writes into the file-provider directory.
	routerFilePrefix = "proximo-route-"

	// dashboardSafe is the reserved safe id of the watcher's dashboard
	// self-route (cert files certs/dashboard.crt/.key). assignSafeNames keeps
	// user containers away from it, and the self-route is rebuilt every
	// reconcile, so stale cleanup always sees it as active.
	dashboardSafe = "dashboard"
	// dashboardFile is the stable dynamic-config filename of the dashboard
	// self-route. It deliberately does not match the proximo-route-* cleanup
	// glob, so the per-container stale sweep can never collect it.
	dashboardFile = "proximo-dashboard.yml"
)

// dashboardHost returns the reserved hostname serving Traefik's dashboard for
// a TLD — shared by the watcher self-route and `proximo status`.
func dashboardHost(tld string) string { return "traefik." + tld }

// hostRuleRe extracts the host from a Traefik router rule label, e.g.
// `traefik.http.routers.web.rule = Host(`web.test`)`.
var hostRuleRe = regexp.MustCompile("Host\\(`([^`]+)`\\)")

// hostnameRe validates a single hostname (RFC 1123 label charset, dot-joined).
var hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`)

// pathPrefixRe validates a proximo.path prefix: a leading slash followed by the
// URL pchar set. It deliberately excludes backticks, quotes and whitespace so
// the prefix is safe to template into the PathPrefix(`…`) router rule.
var pathPrefixRe = regexp.MustCompile(`^/[A-Za-z0-9._~%!$&'()*+,;=:@/-]*$`)

// unsafeNameRe matches runs of characters not allowed in a safe filename /
// router id.
var unsafeNameRe = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// routedContainer is the per-container routing model built during reconcile.
type routedContainer struct {
	name     string        // primary container name (used as the backend DNS name)
	id       string        // container ID (for collision disambiguation)
	safe     string        // sanitized name used for filenames / router ids
	hosts    []string      // routed hostnames
	port     int           // resolved backend port (proximo path only)
	proximo  bool          // true when routed via proximo.hosts (generate dynamic config)
	redirect bool          // true when proximo.redirect opts in to an HTTP->HTTPS redirect
	path     string        // proximo.path prefix scoping the routes ("" = match all paths)
	strip    bool          // true when proximo.path.strip removes the prefix before the backend
	internal bool          // routed to a Traefik internal service (api@internal): no backend port is resolved
	mw       middlewareSet // curated proximo middlewares (auth/cors/headers) attached to the router
	tcpPorts []int         // TCP backend ports (proximo.tcp.port/.ports); non-empty makes the container TCP-routed instead of HTTP
	tcpTLS   string        // TCP TLS mode: tcpTLSTerminate (default) or tcpTLSPassthrough (only meaningful when tcpPorts is set)
	servers  []string      // backend container names for the load balancer; read via backends(), never directly (nil means the single backend rc.name; >1 => round-robin replicas)
}

// Invariant (enforced at the sole constructor, classify): a routedContainer is
// either TCP-routed (tcpPorts non-empty; port/path/strip are then unused and
// tcpTLS is one of tcpTLSTerminate/tcpTLSPassthrough) or HTTP-routed. Direct
// struct literals in tests may break this; production code must not.

// isTCP reports whether a routed container is TCP-routed (SNI) rather than HTTP.
// A container declaring any valid TCP port is served over TCP-over-TLS for its
// hosts and gets no HTTP router; port resolution and the path prefix are
// HTTP-only concepts that do not apply to it.
func (rc routedContainer) isTCP() bool { return len(rc.tcpPorts) > 0 }

// backends returns the backend container names for rc's load balancer: the
// merged replica set when resolveRouteConflicts grouped several containers under
// this route, or just rc.name for a lone container. A single backend renders
// identically to the pre-replica model.
func (rc routedContainer) backends() []string {
	if len(rc.servers) > 0 {
		return rc.servers
	}
	return []string{rc.name}
}

// dockerAPI is the narrow slice of the Docker client the watcher depends on —
// exactly the methods reconcile and Run call. *client.Client satisfies it
// unchanged, so production passes the real client (built by newClient) and
// tests pass a fake. It mirrors the existing inspector seam: a minimal
// interface, not the whole SDK surface.
type dockerAPI interface {
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	NetworkConnect(context.Context, string, client.NetworkConnectOptions) (client.NetworkConnectResult, error)
	NetworkDisconnect(context.Context, string, client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error)
	Events(context.Context, client.EventsListOptions) client.EventsResult
}

// Watcher keeps Traefik attached to the Docker networks of routed containers and
// issues a CA-signed certificate per container, written to Traefik's
// file-provider directory.
type Watcher struct {
	cli        dockerAPI
	caCert     *x509.Certificate
	caKey      *ecdsa.PrivateKey
	dynamicDir string
	// tld is the configured proximo TLD (from PROXIMO_TLD), used to build the
	// dashboard self-route host traefik.<tld>.
	tld string
	// lastHosts caches the last-issued host set per container (keyed by safe
	// name) so certs are reissued only when a container's hosts change.
	lastHosts map[string]string
	// authHashes caches the bcrypt hash of each plaintext basic-auth secret
	// (keyed by user+"\x00"+plaintext) so a stable hash is reused across
	// reconciles. bcrypt's random salt would otherwise produce a different hash
	// every pass, rewriting the router file and thrashing Traefik's file-provider
	// reload on every reconcile.
	authHashes map[string]string
}

// NewWatcher creates a Watcher from the Docker environment and loads the CA (for
// issuing per-host certificates) from the mounted paths.
func NewWatcher() (*Watcher, error) {
	cli, err := newClient()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		cli:        cli,
		dynamicDir: getenv("PROXIMO_DYNAMIC_DIR", "/etc/traefik/dynamic"),
		tld:        getenv("PROXIMO_TLD", config.DefaultTLD),
		lastHosts:  map[string]string{},
		authHashes: map[string]string{},
	}

	caCert, caKey, err := tls.LoadCA(
		getenv("PROXIMO_CA_CERT", "/ca/ca.pem"),
		getenv("PROXIMO_CA_KEY", "/ca/ca-key.pem"),
	)
	if err != nil {
		log.Printf("proximo watcher: CA not available, HTTPS certs disabled: %v", err)
	} else {
		w.caCert, w.caKey = caCert, caKey
	}
	return w, nil
}

// Run reconciles once, then reacts to Docker events with a periodic resync.
func (w *Watcher) Run(ctx context.Context) error {
	// Sweep temp files a prior crash mid-write may have stranded: atomicWrite
	// removes its own temps, so only a hard kill leaves one behind — one sweep at
	// startup is enough (covers the certs dir and the dynamic root).
	cleanStrayTemps(filepath.Join(w.dynamicDir, "certs"), w.dynamicDir)
	w.reconcileLogged(ctx)

	// Subscribe to all container and network events. The container type already
	// carries Docker `health_status` actions (no event-action filter is added —
	// that would whitelist and drop start/stop), so a container turning
	// healthy/unhealthy triggers an immediate reconcile that re-evaluates
	// isHealthRoutable, not just the 30s resync.
	flt := make(client.Filters).
		Add("type", string(events.ContainerEventType)).
		Add("type", string(events.NetworkEventType))
	res := w.cli.Events(ctx, client.EventsListOptions{Filters: flt})
	msgs, errs := res.Messages, res.Err

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.reconcileLogged(ctx)
		case <-msgs:
			w.reconcileLogged(ctx)
		case err := <-errs:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("proximo watcher: event stream error: %v; reconnecting", err)
			time.Sleep(2 * time.Second)
			res := w.cli.Events(ctx, client.EventsListOptions{Filters: flt})
			msgs, errs = res.Messages, res.Err
		}
	}
}

func (w *Watcher) reconcileLogged(ctx context.Context) {
	if err := w.reconcile(ctx); err != nil {
		log.Printf("proximo watcher: reconcile error: %v", err)
	}
}

func (w *Watcher) reconcile(ctx context.Context) error {
	result, err := w.cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return err
	}
	containers := result.Items

	traefikID, traefikNets := findTraefik(containers)
	if traefikID == "" {
		return nil // Traefik not running yet.
	}

	desired := map[string]bool{}
	var routed []routedContainer
	for _, c := range containers {
		if !isRouted(c) {
			continue
		}
		if !isHealthRoutable(c) {
			// Health-gated and not yet healthy: withhold the network attach,
			// router, and cert. Leaving it out of `desired`/`routed` makes the
			// existing stale-route/cert sweep and the traefik-network disconnect
			// withdraw a route when a healthy container later turns unhealthy.
			continue
		}
		for _, netID := range targetNetworks(c) {
			desired[netID] = true
		}
		if rc, ok := w.buildRouted(ctx, c); ok {
			routed = append(routed, rc)
		}
	}
	assignSafeNames(routed)
	// The dashboard self-route is rebuilt every pass, independently of the
	// container list: the Traefik container itself stays excluded by
	// isRouted/classifyHosts (proximo.role), so it can never reach this path
	// through classification. Appending after assignSafeNames keeps its
	// reserved safe id fixed; appending before warnDuplicateHosts flags a user
	// container that claims the reserved traefik.<tld> host.
	routed = append(routed, w.dashboardRoute())
	warnDuplicateHosts(containers, routed)
	routed, merges, conflicts := resolveRouteConflicts(routed)
	for _, c := range conflicts {
		log.Printf("proximo watcher: container %s conflicts on host %q path %q with an already-routed container; the lexicographically-first name wins, %s is not routed", c.name, c.host, c.path, c.name)
	}
	for _, m := range merges {
		log.Printf("proximo watcher: container %s joins %s as a round-robin replica on host %q (identical host and backend); traffic is balanced across them", m.member, m.rep, m.host)
	}

	for netID := range desired {
		if _, already := traefikNets[netID]; already {
			continue
		}
		if _, err := w.cli.NetworkConnect(ctx, netID, client.NetworkConnectOptions{Container: traefikID, EndpointConfig: &network.EndpointSettings{}}); err != nil {
			log.Printf("proximo watcher: connect traefik to %s: %v", short(netID), err)
			continue
		}
		log.Printf("proximo watcher: connected traefik to network %s", short(netID))
	}

	for netID, name := range traefikNets {
		if isStackNetwork(name) || desired[netID] {
			continue
		}
		if _, err := w.cli.NetworkDisconnect(ctx, netID, client.NetworkDisconnectOptions{Container: traefikID, Force: true}); err != nil {
			log.Printf("proximo watcher: disconnect traefik from %s: %v", short(netID), err)
			continue
		}
		log.Printf("proximo watcher: disconnected traefik from network %s (%s)", short(netID), name)
	}

	w.syncDynamic(routed)
	w.syncCerts(routed)
	return nil
}

// dashboardRoute synthesizes the self-route serving Traefik's dashboard at
// https://traefik.<tld>. It is marked internal so it targets api@internal and
// never flows through resolveBackendPort (there is no backend container).
// redirect is always on — the dashboard is a stack-owned host, so unlike user
// containers (opt-in via proximo.redirect) http://traefik.<tld> always
// redirects to https:// instead of 404ing on :80.
// w.tld is always set — NewWatcher defaults it to config.DefaultTLD.
func (w *Watcher) dashboardRoute() routedContainer {
	return routedContainer{
		name:     "traefik",
		safe:     dashboardSafe,
		hosts:    []string{dashboardHost(w.tld)},
		proximo:  true,
		internal: true,
		redirect: true,
	}
}

// inspector inspects a container by ID. Both the watcher (w.cli.ContainerInspect)
// and the host CLI (cli.ContainerInspect) satisfy it, so classify runs in either
// context without a *Watcher receiver.
type inspector func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)

// classifyInfo carries diagnostics produced by classify for the caller to log.
// The watcher logs them; `proximo status` ignores them (it stays quiet).
type classifyInfo struct {
	invalidHosts    []string       // invalid entries found in proximo.hosts
	invalidPath     string         // proximo.path value that failed validation (skips the container)
	portFailed      bool           // proximo route whose backend port could not be resolved
	port            portResult     // detail explaining a port-resolution failure
	middleware      middlewareInfo // per-middleware validation diagnostics (auth/cors/headers)
	invalidTCPPorts []string       // proximo.tcp.port/.ports entries that failed to parse (skipped)
	invalidTCPTLS   string         // proximo.tcp.tls value that was neither terminate nor passthrough
	tcpDowngraded   bool           // TCP labels were set but all invalid, so the container is not TCP-routed
	tcpIgnoredHTTP  []string       // HTTP-only labels (middlewares, proximo.path) set on a TCP route and dropped
}

// portResult explains why backend-port resolution failed; its zero value means
// success or not attempted.
type portResult struct {
	badLabel   string // proximo.port value that failed to parse/validate
	inspectErr error  // ContainerInspect error
	exposedTCP int    // number of exposed TCP ports (set when ambiguous)
}

// classify is the single full routing decision shared by the watcher
// (buildRouted) and `proximo status` (Routes): it runs classifyHosts and, on the
// proximo branch, resolves the backend port via the injected inspect function so
// the two cannot diverge about what is actually served. It returns the routing
// model, whether the container is effectively routed, and diagnostics for the
// caller to log. The returned routedContainer keeps its hosts even when ok is
// false (unresolved port) so callers can flag it. classify never logs.
func classify(ctx context.Context, inspect inspector, c container.Summary) (routedContainer, bool, classifyInfo) {
	hosts, proximo, invalid := classifyHosts(c.Labels)
	info := classifyInfo{invalidHosts: invalid}
	rc := routedContainer{name: primaryName(c), id: c.ID, hosts: hosts, proximo: proximo, redirect: isProximoRedirect(c.Labels)}
	if len(hosts) == 0 {
		return rc, false, info
	}

	// Curated middlewares apply only to proximo-translated routers; native
	// traefik.* routes carry whatever middleware labels the user wrote directly.
	if proximo {
		rc.mw, info.middleware = parseMiddlewares(c.Labels)
	}

	// Proximo routes are translated into Traefik dynamic config by the watcher,
	// so they parse the path prefix and resolve a backend port; native traefik.*
	// routes are configured by Traefik's Docker provider and only need a cert.
	if proximo {
		// TCP routing takes over the container's hosts: a container declaring any
		// valid TCP port is served over TCP-over-TLS by SNI and gets no HTTP
		// router, so the HTTP-only path prefix and backend-port resolution are
		// skipped. Invalid port entries are dropped with a warning, leaving any
		// valid ports intact.
		rc.tcpPorts, info.invalidTCPPorts = parseTCPPorts(c.Labels)
		rc.tcpTLS, info.invalidTCPTLS = parseTCPTLSMode(c.Labels)
		if rc.isTCP() {
			// Middlewares and the path prefix are HTTP-layer concepts: a TCP (SNI)
			// route cannot apply them. Flag any that were set — a user expecting
			// proximo.auth to guard a TCP service would otherwise be silently
			// exposed — and drop them so they never leak into the route or its
			// replica identity.
			if !rc.mw.empty() {
				info.tcpIgnoredHTTP = append(info.tcpIgnoredHTTP, "middlewares (auth/cors/headers)")
			}
			if strings.TrimSpace(c.Labels[proximoPathLabel]) != "" {
				info.tcpIgnoredHTTP = append(info.tcpIgnoredHTTP, proximoPathLabel)
			}
			rc.mw = middlewareSet{}
			return rc, true, info
		}
		// TCP labels were present but every port was invalid: the container is not
		// TCP-routed and falls back to HTTP classification below. Record it so the
		// watcher can warn that the intended TCP route did not take effect.
		info.tcpDowngraded = len(info.invalidTCPPorts) > 0

		prefix, ok := parseProximoPath(c.Labels)
		if !ok {
			info.invalidPath = strings.TrimSpace(c.Labels[proximoPathLabel])
			return rc, false, info
		}
		rc.path = prefix
		rc.strip = isProximoPathStrip(c.Labels)

		port, ok, res := resolveBackendPort(ctx, inspect, c)
		if !ok {
			info.portFailed = true
			info.port = res
			return rc, false, info
		}
		rc.port = port
	}
	return rc, true, info
}

// buildRouted is the watcher's thin wrapper over classify: it resolves ports
// through the watcher's Docker client and logs the diagnostics classify returns.
// ok=false means the container gets no route/cert (e.g. ambiguous port, or a
// native container with no Host rule).
func (w *Watcher) buildRouted(ctx context.Context, c container.Summary) (routedContainer, bool) {
	rc, ok, info := classify(ctx, w.cli.ContainerInspect, c)
	for _, h := range info.invalidHosts {
		log.Printf("proximo watcher: container %s: ignoring invalid host %q in %s", rc.name, h, proximoHostsLabel)
	}
	if info.invalidPath != "" {
		log.Printf("proximo watcher: container %s: invalid %s=%q (must start with %q); not routed", rc.name, proximoPathLabel, info.invalidPath, "/")
	}
	if info.portFailed {
		logPortFailure(rc.name, info.port)
	}
	for _, p := range info.invalidTCPPorts {
		log.Printf("proximo watcher: container %s: ignoring invalid TCP port %q in %s/%s", rc.name, p, proximoTCPPortLabel, proximoTCPPortsLabel)
	}
	if info.tcpDowngraded {
		log.Printf("proximo watcher: container %s: no valid TCP port left; not TCP-routed, falling back to HTTP classification", rc.name)
	}
	// The TLS mode only matters for a TCP route; warn about a bad value only when
	// the container is actually TCP-routed, so a stray label on an HTTP container
	// is not reported as if it changed anything.
	if info.invalidTCPTLS != "" && rc.isTCP() {
		log.Printf("proximo watcher: container %s: invalid %s=%q (want %s or %s); defaulting to %s", rc.name, proximoTCPTLSLabel, info.invalidTCPTLS, tcpTLSTerminate, tcpTLSPassthrough, tcpTLSTerminate)
	}
	for _, lbl := range info.tcpIgnoredHTTP {
		log.Printf("proximo watcher: container %s: %s set on a TCP route but ignored — SNI routing has no HTTP layer; remove the label or drop %s if you need HTTP middlewares/path", rc.name, lbl, proximoTCPPortLabel)
	}
	logMiddlewareWarnings(rc.name, info.middleware)
	return rc, ok
}

// logMiddlewareWarnings logs (watcher-side) each per-middleware validation
// failure: a malformed auth pair, a blank CORS value, or an invalid header name.
// Each leaves the container's routing and its other middlewares intact.
func logMiddlewareWarnings(name string, info middlewareInfo) {
	for _, pair := range info.invalidAuth {
		log.Printf("proximo watcher: container %s: ignoring invalid %s entry %q (want user:password); other routing unaffected", name, proximoAuthLabel, pair)
	}
	if info.emptyCors {
		log.Printf("proximo watcher: container %s: ignoring blank %s (want true or an origin list); other routing unaffected", name, proximoCorsLabel)
	}
	for _, h := range info.invalidHeaders {
		log.Printf("proximo watcher: container %s: ignoring %s%s with invalid header name; other routing unaffected", name, proximoHeaderPrefix, h)
	}
}

// resolveBackendPort returns the backend port for a proximo-routed container. It
// uses proximo.port when set; otherwise it inspects the container and returns the
// single exposed TCP port. ok=false (with a portResult explaining why) when the
// label is invalid, the inspect fails, or the container exposes zero or multiple
// TCP ports. It does not log — the caller decides (watcher logs; status stays
// quiet).
func resolveBackendPort(ctx context.Context, inspect inspector, c container.Summary) (port int, ok bool, res portResult) {
	if v := strings.TrimSpace(c.Labels[proximoPortLabel]); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return 0, false, portResult{badLabel: v}
		}
		return p, true, portResult{}
	}

	result, err := inspect(ctx, c.ID, client.ContainerInspectOptions{})
	if err != nil {
		return 0, false, portResult{inspectErr: err}
	}
	info := result.Container
	var ports network.PortSet
	if info.Config != nil {
		ports = info.Config.ExposedPorts
	}
	p, count, ok := portFromExposed(ports)
	if !ok {
		return 0, false, portResult{exposedTCP: count}
	}
	return p, true, portResult{}
}

// logPortFailure logs (watcher-side) why a proximo route's backend port could not
// be resolved, reproducing the original per-cause messages.
func logPortFailure(name string, res portResult) {
	switch {
	case res.badLabel != "":
		log.Printf("proximo watcher: container %s has invalid %s=%q", name, proximoPortLabel, res.badLabel)
	case res.inspectErr != nil:
		log.Printf("proximo watcher: inspect %s: %v", name, res.inspectErr)
	default:
		log.Printf("proximo watcher: container %s exposes %d TCP port(s); set %s to disambiguate",
			name, res.exposedTCP, proximoPortLabel)
	}
}

// hint is the short, user-facing reason a proximo route's backend port could not
// be resolved, for `proximo status` to display. It mirrors the cause split of
// logPortFailure so status and the watcher logs agree on the why.
func (res portResult) hint() string {
	switch {
	case res.badLabel != "":
		return "invalid " + proximoPortLabel + "=" + strconv.Quote(res.badLabel)
	case res.inspectErr != nil:
		return "cannot detect backend port (inspect failed)"
	default:
		return "set " + proximoPortLabel + " (exposes " + strconv.Itoa(res.exposedTCP) + " TCP ports)"
	}
}

// portFromExposed returns the single exposed TCP port (ok=true) and the number
// of TCP ports found. ok is false when the set holds zero or more than one TCP
// port; count then explains the ambiguity for logging.
func portFromExposed(ports network.PortSet) (port, count int, ok bool) {
	var tcp []int
	for p := range ports {
		if p.Proto() == network.TCP {
			tcp = append(tcp, int(p.Num()))
		}
	}
	if len(tcp) == 1 {
		return tcp[0], 1, true
	}
	return 0, len(tcp), false
}

// syncDynamic writes one Traefik dynamic config file per proximo-routed
// container (HTTP router + service) and removes stale files for containers that
// are no longer routed.
func (w *Watcher) syncDynamic(routed []routedContainer) {
	active := map[string]bool{}
	for _, rc := range routed {
		if !rc.proximo {
			continue
		}
		active[rc.safe] = true
		w.materializeAuth(&rc)
		path := filepath.Join(w.dynamicDir, routerFilePrefix+rc.safe+".yml")
		if rc.internal {
			// The dashboard self-route lives under its own stable filename,
			// outside the proximo-route-* glob, so the stale sweep below can
			// never collect it.
			path = filepath.Join(w.dynamicDir, dashboardFile)
		}
		if err := writeFileIfChanged(path, renderRouter(rc), 0o644); err != nil {
			log.Printf("proximo watcher: write router config %s: %v", rc.safe, err)
		}
	}

	matches, _ := filepath.Glob(filepath.Join(w.dynamicDir, routerFilePrefix+"*.yml"))
	for _, path := range matches {
		safe := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), routerFilePrefix), ".yml")
		if active[safe] {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("proximo watcher: remove stale router config %s: %v", safe, err)
			continue
		}
		log.Printf("proximo watcher: removed stale router config for %s", safe)
	}
}

// materializeAuth replaces each plaintext basic-auth secret in rc with its
// bcrypt hash (the form Traefik's basicAuth consumes), passing through secrets
// already in htpasswd hash form. Hashes are cached per plaintext so the emitted
// router file is byte-stable across reconciles (see Watcher.authHashes). A
// hashing failure drops that one credential with a warning, leaving the rest.
func (w *Watcher) materializeAuth(rc *routedContainer) {
	kept := rc.mw.auth[:0]
	for _, c := range rc.mw.auth {
		if c.hashed {
			kept = append(kept, c)
			continue
		}
		key := c.user + "\x00" + c.secret
		h, ok := w.authHashes[key]
		if !ok {
			b, err := bcrypt.GenerateFromPassword([]byte(c.secret), bcrypt.DefaultCost)
			if err != nil {
				log.Printf("proximo watcher: container %s: hashing %s password for %q: %v; credential dropped", rc.name, proximoAuthLabel, c.user, err)
				continue
			}
			h = string(b)
			w.authHashes[key] = h
		}
		kept = append(kept, authCred{user: c.user, secret: h, hashed: true})
	}
	rc.mw.auth = kept
}

// routerRule builds a container's Traefik rule: the host alternation, combined
// with PathPrefix when rc.path is set. Hosts and the path prefix are validated
// to safe charsets before reaching here, so templating them into the rule is
// safe. The host alternation is parenthesized when a prefix is present so the
// `&&` binds across all hosts (`(Host(a) || Host(b)) && PathPrefix(/p)`).
func routerRule(rc routedContainer) string {
	rules := make([]string, 0, len(rc.hosts))
	for _, h := range rc.hosts {
		rules = append(rules, "Host(`"+h+"`)")
	}
	rule := strings.Join(rules, " || ")
	if rc.path == "" {
		return rule
	}
	if len(rules) > 1 {
		rule = "(" + rule + ")"
	}
	return rule + " && PathPrefix(`" + rc.path + "`)"
}

// renderRouter renders the Traefik dynamic config (HTTP router + service) for a
// single proximo-routed container. Hosts are validated to a hostname charset
// before reaching here, so templating them into the rule is safe. When rc.path
// is set the router rule gains a PathPrefix matcher and a prefix-length priority
// so the most specific prefix wins; when rc.strip is also set a StripPrefix
// middleware removes the prefix before the backend. When rc.redirect is set it
// additionally emits a web-entrypoint router (same rule) attached to a
// redirectScheme middleware, so http://<host> 302-redirects to https://; the
// middlewares ride this same file, so they are cleaned up with the rest of the config.
func renderRouter(rc routedContainer) []byte {
	if rc.isTCP() {
		return renderTCPRouter(rc)
	}
	id := "proximo-" + rc.safe
	rule := routerRule(rc)

	// Internal self-route (the dashboard): targets Traefik's built-in
	// api@internal service, so there is no backend service/loadbalancer
	// block and no port to resolve.
	service := id
	if rc.internal {
		service = "api@internal"
	}

	redirectID := id + "-redirect"
	stripID := id + "-strip"
	stripping := rc.strip && rc.path != ""

	// The websecure router references its curated middlewares in the fixed chain
	// order auth -> cors -> headers, then the path-strip (closest to the backend).
	websecureMW := rc.mw.chainRefs(id)
	if stripping {
		websecureMW = append(websecureMW, stripID)
	}

	var b strings.Builder
	// writeRuleAndService emits the rule, prefix-length priority, and service —
	// shared verbatim by the websecure router and the optional web (redirect)
	// router. Priority from prefix byte length makes the most specific prefix
	// win, and keeps a bare host (no priority => Traefik default) below any
	// PathPrefix.
	writeRuleAndService := func() {
		fmt.Fprintf(&b, "      rule: %q\n", rule)
		if rc.path != "" {
			fmt.Fprintf(&b, "      priority: %d\n", len(rc.path))
		}
		fmt.Fprintf(&b, "      service: %s\n", service)
	}

	b.WriteString("http:\n")
	b.WriteString("  routers:\n")
	fmt.Fprintf(&b, "    %s:\n", id)
	b.WriteString("      entryPoints:\n        - websecure\n")
	writeRuleAndService()
	if len(websecureMW) > 0 {
		b.WriteString("      middlewares:\n")
		for _, name := range websecureMW {
			fmt.Fprintf(&b, "        - %s\n", name)
		}
	}
	b.WriteString("      tls: {}\n")
	if rc.redirect {
		// HTTP router on :80 for the same rule plus the redirectScheme
		// middleware it references. Strip is not applied here: the redirect
		// router 302s to https:// without forwarding to the backend.
		fmt.Fprintf(&b, "    %s:\n", redirectID)
		b.WriteString("      entryPoints:\n        - web\n")
		writeRuleAndService()
		b.WriteString("      middlewares:\n")
		fmt.Fprintf(&b, "        - %s\n", redirectID)
	}
	// middlewares: is a sibling of routers:/services: under http:, so emitting
	// it here (before services) is order-free. The curated, strip, and redirect
	// middlewares all ride this same file, so they are cleaned up with the router.
	if !rc.mw.empty() || stripping || rc.redirect {
		b.WriteString("  middlewares:\n")
		rc.mw.renderDefs(&b, id)
		if stripping {
			fmt.Fprintf(&b, "    %s:\n", stripID)
			b.WriteString("      stripPrefix:\n")
			b.WriteString("        prefixes:\n")
			fmt.Fprintf(&b, "          - %q\n", rc.path)
		}
		if rc.redirect {
			fmt.Fprintf(&b, "    %s:\n", redirectID)
			b.WriteString("      redirectScheme:\n")
			b.WriteString("        scheme: https\n")
			b.WriteString("        permanent: false\n")
		}
	}
	if rc.internal {
		return []byte(b.String())
	}
	b.WriteString("  services:\n")
	fmt.Fprintf(&b, "    %s:\n", id)
	b.WriteString("      loadBalancer:\n")
	b.WriteString("        servers:\n")
	for _, name := range rc.backends() {
		fmt.Fprintf(&b, "          - url: %q\n", "http://"+name+":"+strconv.Itoa(rc.port))
	}
	return []byte(b.String())
}

// tcpRouterRule builds a TCP router's HostSNI rule: the alternation of the
// container's hosts. Hosts are validated to a hostname charset before reaching
// here, so templating them into the rule is safe. The catch-all HostSNI(`*`) is
// never emitted, so HTTP routers on the same entrypoint keep serving SNIs that
// match no TCP router.
func tcpRouterRule(rc routedContainer) string {
	rules := make([]string, 0, len(rc.hosts))
	for _, h := range rc.hosts {
		rules = append(rules, "HostSNI(`"+h+"`)")
	}
	return strings.Join(rules, " || ")
}

// renderTCPRouter renders the Traefik dynamic config (TCP routers + services) for
// a TCP-routed container: one router/service per declared TCP port, each matching
// the container's hosts by SNI on the shared websecure (:443) entrypoint. By
// default the router terminates TLS with the per-host proximo cert (the shared
// cert store from proximo-tls.yml) and forwards plaintext to the backend; a
// passthrough container instead routes the raw TLS stream by SNI.
//
// ponytail: SNI is the only routing key, so several ports sharing the same host
// set emit routers with identical HostSNI rules that Traefik cannot demux by
// port. The single-port-per-host case (the real use) is unambiguous; give each
// port a distinct host when a container must expose more than one TCP service.
func renderTCPRouter(rc routedContainer) []byte {
	rule := tcpRouterRule(rc)
	var b strings.Builder
	b.WriteString("tcp:\n")
	b.WriteString("  routers:\n")
	for _, p := range rc.tcpPorts {
		id := fmt.Sprintf("proximo-tcp-%s-%d", rc.safe, p)
		fmt.Fprintf(&b, "    %s:\n", id)
		b.WriteString("      entryPoints:\n        - websecure\n")
		fmt.Fprintf(&b, "      rule: %q\n", rule)
		fmt.Fprintf(&b, "      service: %s\n", id)
		if rc.tcpTLS == tcpTLSPassthrough {
			b.WriteString("      tls:\n        passthrough: true\n")
		} else {
			b.WriteString("      tls: {}\n")
		}
	}
	b.WriteString("  services:\n")
	for _, p := range rc.tcpPorts {
		id := fmt.Sprintf("proximo-tcp-%s-%d", rc.safe, p)
		fmt.Fprintf(&b, "    %s:\n", id)
		b.WriteString("      loadBalancer:\n")
		b.WriteString("        servers:\n")
		for _, name := range rc.backends() {
			fmt.Fprintf(&b, "          - address: %q\n", fmt.Sprintf("%s:%d", name, p))
		}
	}
	return []byte(b.String())
}

// syncCerts issues one CA-signed certificate per routed container (SANs = that
// container's hosts), regenerates the Traefik dynamic TLS file listing them all,
// and removes certs for containers no longer routed. A container's cert is
// reissued only when its host set changes.
func (w *Watcher) syncCerts(routed []routedContainer) {
	if w.caCert == nil {
		return
	}
	certsDir := filepath.Join(w.dynamicDir, "certs")
	if err := os.MkdirAll(certsDir, 0o755); err != nil {
		log.Printf("proximo watcher: mkdir certs: %v", err)
		return
	}

	active := map[string]bool{}
	var entries []routedContainer
	for _, rc := range routed {
		if len(rc.hosts) == 0 {
			continue
		}
		active[rc.safe] = true
		entries = append(entries, rc)

		hosts := append([]string(nil), rc.hosts...)
		sort.Strings(hosts)
		key := strings.Join(hosts, ",")
		crt := filepath.Join(certsDir, rc.safe+".crt")
		pkey := filepath.Join(certsDir, rc.safe+".key")
		if w.lastHosts[rc.safe] == key && fileExists(crt) && fileExists(pkey) {
			continue
		}

		certPEM, keyPEM, err := tls.IssueHostCert(w.caCert, w.caKey, hosts)
		if err != nil {
			log.Printf("proximo watcher: issue cert for %s: %v", rc.safe, err)
			continue
		}
		if err := atomicWrite(crt, certPEM, 0o644); err != nil {
			log.Printf("proximo watcher: write cert %s: %v", rc.safe, err)
			continue
		}
		if err := atomicWrite(pkey, keyPEM, 0o600); err != nil {
			log.Printf("proximo watcher: write key %s: %v", rc.safe, err)
			continue
		}
		w.lastHosts[rc.safe] = key
		log.Printf("proximo watcher: issued certificate for %s: %s", rc.safe, key)
	}

	// Order matters: regenerate the TLS config (dropping references to departed
	// containers) before unlinking their cert files. The reverse order leaves a
	// window where proximo-tls.yml still points at an already-removed .crt, so
	// Traefik's file provider reloads against a missing cert and logs
	// "failed to find any PEM data". Writing the config first makes the
	// intermediate state "cert present but no longer referenced" — harmless.
	w.writeTLSConfig(certsDir, entries)
	w.removeStaleCerts(certsDir, active)
}

// removeStaleCerts deletes cert/key files (and forgets cached host sets) only
// for containers absent from the current routed set. A still-routed container
// keeps its cert: when its host set changes the cert is rewritten in place via
// atomicWrite (see syncCerts), so a host-set change never goes through a
// remove-then-recreate empty-file window — only a genuinely departed container's
// cert is removed here. Callers must run writeTLSConfig first (see syncCerts):
// the certs removed here are already unreferenced by proximo-tls.yml, so
// Traefik's reload on the deletion event never hits a referenced-but-absent cert.
func (w *Watcher) removeStaleCerts(certsDir string, active map[string]bool) {
	matches, _ := filepath.Glob(filepath.Join(certsDir, "*.crt"))
	for _, crt := range matches {
		safe := strings.TrimSuffix(filepath.Base(crt), ".crt")
		if active[safe] {
			continue
		}
		pkey := filepath.Join(certsDir, safe+".key")
		_ = os.Remove(crt)
		_ = os.Remove(pkey)
		delete(w.lastHosts, safe)
		log.Printf("proximo watcher: removed certificate for %s", safe)
	}
}

// writeTLSConfig regenerates proximo-tls.yml listing every per-container
// certificate. The first (sorted) cert backs the default store so Traefik does
// not fall back to its built-in self-signed default.
func (w *Watcher) writeTLSConfig(certsDir string, entries []routedContainer) {
	tlsPath := filepath.Join(w.dynamicDir, "proximo-tls.yml")
	if len(entries) == 0 {
		_ = os.Remove(tlsPath)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].safe < entries[j].safe })

	var b strings.Builder
	b.WriteString("tls:\n")
	first := entries[0]
	b.WriteString("  stores:\n    default:\n      defaultCertificate:\n")
	fmt.Fprintf(&b, "        certFile: %s\n", filepath.Join(certsDir, first.safe+".crt"))
	fmt.Fprintf(&b, "        keyFile: %s\n", filepath.Join(certsDir, first.safe+".key"))
	b.WriteString("  certificates:\n")
	for _, rc := range entries {
		fmt.Fprintf(&b, "    - certFile: %s\n", filepath.Join(certsDir, rc.safe+".crt"))
		fmt.Fprintf(&b, "      keyFile: %s\n", filepath.Join(certsDir, rc.safe+".key"))
	}
	if err := writeFileIfChanged(tlsPath, []byte(b.String()), 0o644); err != nil {
		log.Printf("proximo watcher: write tls config: %v", err)
	}
}

// writeFileIfChanged writes data to path only when the current content differs.
// The reconcile loop regenerates every dynamic-config file each pass (30s + on
// Docker events); skipping no-op writes spares Traefik's file-provider watcher
// spurious change events and config reloads. The write itself is atomic so the
// file provider never reloads against a half-written router/TLS file.
func writeFileIfChanged(path string, data []byte, perm os.FileMode) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}
	return atomicWrite(path, data, perm)
}

// atomicWrite materializes data at path atomically: it writes a temp file in
// path's own directory (so the rename stays on one filesystem), Syncs and Closes
// it, Chmods to mode, then renames it over path. A concurrent reader (Traefik's
// file provider) therefore only ever sees the old complete file or the new
// complete file, never a torn write. The temp file is removed on any error,
// leaving the original untouched. The "*.tmp-*" name matches cleanStrayTemps so a
// temp stranded by a hard crash is swept on the next startup.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	committed := false
	defer func() {
		if !committed {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// os.CreateTemp makes the file 0600; Chmod sets the exact requested mode
	// (0644 cert / 0600 key) regardless of the process umask.
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// cleanStrayTemps removes leftover atomicWrite temp files. A crash mid-write can
// strand a "*.tmp-*" file; Traefik ignores non-.crt/.yml files so a stray temp is
// inert, but sweeping them keeps the dynamic dir tidy.
func cleanStrayTemps(dirs ...string) {
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
		for _, p := range matches {
			_ = os.Remove(p)
		}
	}
}

func findTraefik(cs []container.Summary) (id string, nets map[string]string) {
	for _, c := range cs {
		if c.Labels[roleLabel] == "traefik" {
			return c.ID, networksOf(c)
		}
	}
	return "", nil
}

func networksOf(c container.Summary) map[string]string {
	out := map[string]string{}
	if c.NetworkSettings == nil {
		return out
	}
	for name, ep := range c.NetworkSettings.Networks {
		out[ep.NetworkID] = name
	}
	return out
}

// isRouted reports whether a container opted in to routing — via proximo labels
// (non-empty proximo.hosts and not proximo.enable=false) or the native Traefik
// label (traefik.enable=true) — and is not part of the proximo stack.
func isRouted(c container.Summary) bool {
	if _, isStack := c.Labels[roleLabel]; isStack {
		return false
	}
	if isProximoRoute(c.Labels) {
		return true
	}
	return strings.EqualFold(c.Labels[enableLabel], "true")
}

// isProximoRoute reports whether a container opts in via a non-empty, enabled
// proximo.hosts label.
func isProximoRoute(labels map[string]string) bool {
	return len(proximoHosts(labels)) > 0 && isProximoEnabled(labels)
}

// classifyHosts is the single source of truth for a container's host-based
// routing decision, shared by the watcher (buildRouted) and `proximo status`
// (Routes) — both via classify — so the two never disagree. It returns the
// hostnames to route,
// whether routing is via proximo.hosts (vs native traefik.* rules), and any
// invalid proximo.hosts entries for the caller to log. Empty hosts means no
// host-based route. Port resolution is intentionally left to the watcher.
func classifyHosts(labels map[string]string) (hosts []string, proximo bool, invalid []string) {
	if _, isStack := labels[roleLabel]; isStack {
		return nil, false, nil
	}
	valid, invalid := splitHosts(labels[proximoHostsLabel])
	if len(valid) > 0 && isProximoEnabled(labels) {
		return valid, true, invalid
	}
	if strings.EqualFold(labels[enableLabel], "true") {
		return hostsFromLabels(labels), false, invalid
	}
	return nil, false, invalid
}

// proximoHosts parses the proximo.hosts label into a clean host list:
// comma-split, trimmed, empties dropped, invalid-charset entries rejected.
func proximoHosts(labels map[string]string) []string {
	valid, _ := splitHosts(labels[proximoHostsLabel])
	return valid
}

// splitHosts splits a comma-separated host list into valid and invalid entries.
// Whitespace is trimmed and empty entries are dropped.
func splitHosts(raw string) (valid, invalid []string) {
	for _, h := range splitCommaTrim(raw) {
		if hostnameRe.MatchString(h) {
			valid = append(valid, h)
		} else {
			invalid = append(invalid, h)
		}
	}
	return valid, invalid
}

// splitCommaTrim splits a comma-separated label value into trimmed, non-empty
// entries — the shared first step of every comma-list proximo label
// (proximo.hosts, proximo.auth, the proximo.cors origin list).
func splitCommaTrim(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// isProximoEnabled reports whether routing is enabled for a container. It
// defaults to true and is disabled only by an explicit falsy proximo.enable
// value (false/0/no, case-insensitive).
func isProximoEnabled(labels map[string]string) bool {
	return !isFalsyLabel(labels, proximoEnableLabel)
}

// isProximoHealthGated reports whether route publication should be gated on the
// container's Docker health. It defaults to true and is disabled only by an
// explicit falsy proximo.health value (false/0/no, case-insensitive) — mirroring
// isProximoEnabled, so the opt-out reads the same as the other proximo bools.
func isProximoHealthGated(labels map[string]string) bool {
	return !isFalsyLabel(labels, proximoHealthLabel)
}

// isHealthRoutable reports whether a container's health permits publishing its
// route now. A container is routable when gating is opted out (proximo.health
// falsy), when it declares no healthcheck (Health nil or "none"), or when its
// healthcheck reports healthy. A health-gated container that is starting or
// unhealthy is withheld until it becomes healthy — the running filter is already
// applied by ContainerList, so being in the list means the container is up.
func isHealthRoutable(c container.Summary) bool {
	if !isProximoHealthGated(c.Labels) {
		return true
	}
	if c.Health == nil {
		return true
	}
	switch c.Health.Status {
	case container.Healthy, container.NoHealthcheck, "":
		return true
	}
	return false
}

// healthGateNote returns the `proximo status` note for a health-gated container
// that is not yet routable, or "" when the container is routable (no note). It
// distinguishes a not-yet-ready container (starting) from one whose route was
// withdrawn (unhealthy), so status reads as "not serving yet" rather than
// "misconfigured/absent".
func healthGateNote(c container.Summary) string {
	if isHealthRoutable(c) {
		return ""
	}
	// Not routable here implies gating is on and c.Health is non-nil with a
	// starting/unhealthy status (a nil or healthy/none status is routable above).
	if c.Health.Status == container.Unhealthy {
		return "unhealthy (route withdrawn until healthy)"
	}
	return "starting (waiting for healthy)"
}

// isTruthyLabel reports whether labels[key] holds an explicit truthy value
// (true/1/yes, case-insensitive). It is the shared opt-in bool helper behind
// the off-by-default proximo labels (proximo.redirect, proximo.path.strip).
func isTruthyLabel(labels map[string]string, key string) bool {
	switch strings.ToLower(strings.TrimSpace(labels[key])) {
	case "true", "1", "yes":
		return true
	}
	return false
}

// isFalsyLabel reports whether labels[key] holds an explicit falsy value
// (false/0/no, case-insensitive). It is the inverse of isTruthyLabel and the
// shared opt-out bool helper behind the on-by-default proximo labels
// (proximo.enable, proximo.health).
func isFalsyLabel(labels map[string]string, key string) bool {
	switch strings.ToLower(strings.TrimSpace(labels[key])) {
	case "false", "0", "no":
		return true
	}
	return false
}

// isProximoRedirect reports whether a container opted in to an HTTP->HTTPS
// redirect for its hosts. It defaults to false and is enabled only by an
// explicit truthy proximo.redirect value (true/1/yes, case-insensitive) —
// mirroring isProximoEnabled with the inverted default.
func isProximoRedirect(labels map[string]string) bool {
	return isTruthyLabel(labels, proximoRedirectLabel)
}

// isProximoPathStrip reports whether a container opted in to stripping the
// matched path prefix before the backend (default false), reusing the same
// truthy-value helper as proximo.redirect.
func isProximoPathStrip(labels map[string]string) bool {
	return isTruthyLabel(labels, proximoPathStripLabel)
}

// parseProximoPath reads the proximo.path prefix. An absent (empty) label is
// valid and means "match all paths" (prefix ""). A present value is valid only
// when it starts with "/" and matches the safe path charset; otherwise ok is
// false and the caller skips the container with a warning.
func parseProximoPath(labels map[string]string) (prefix string, ok bool) {
	v := strings.TrimSpace(labels[proximoPathLabel])
	if v == "" {
		return "", true
	}
	if !pathPrefixRe.MatchString(v) {
		return v, false
	}
	return v, true
}

// parseTCPPorts reads the proximo.tcp.port and proximo.tcp.ports labels into a
// deduplicated, order-preserving list of backend ports for TCP-over-TLS routing.
// Both labels are honored and merged (port first, then the ports list). Each
// entry must be an integer in 1–65535; entries that fail are returned in invalid
// for the caller to warn about, leaving the valid ports intact. No TCP labels
// yields an empty list (the container stays HTTP-routed).
func parseTCPPorts(labels map[string]string) (ports []int, invalid []string) {
	seen := map[int]bool{}
	add := func(raw string) {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			invalid = append(invalid, raw)
			return
		}
		if !seen[p] {
			seen[p] = true
			ports = append(ports, p)
		}
	}
	for _, v := range splitCommaTrim(labels[proximoTCPPortLabel]) {
		add(v)
	}
	for _, v := range splitCommaTrim(labels[proximoTCPPortsLabel]) {
		add(v)
	}
	return ports, invalid
}

// parseTCPTLSMode reads proximo.tcp.tls. An absent or empty label defaults to
// tcpTLSTerminate. A "terminate"/"passthrough" value (case-insensitive) is
// honored; any other value defaults to terminate and is returned as invalid so
// the caller can warn. The mode only matters when the container has TCP ports.
func parseTCPTLSMode(labels map[string]string) (mode string, invalid string) {
	switch v := strings.ToLower(strings.TrimSpace(labels[proximoTCPTLSLabel])); v {
	case "", tcpTLSTerminate:
		return tcpTLSTerminate, ""
	case tcpTLSPassthrough:
		return tcpTLSPassthrough, ""
	default:
		return tcpTLSTerminate, strings.TrimSpace(labels[proximoTCPTLSLabel])
	}
}

// hostsFromLabels extracts the Host(...) values from a container's Traefik
// router rule labels.
func hostsFromLabels(labels map[string]string) []string {
	var hosts []string
	for k, v := range labels {
		if strings.HasPrefix(k, "traefik.http.routers.") && strings.HasSuffix(k, ".rule") {
			for _, m := range hostRuleRe.FindAllStringSubmatch(v, -1) {
				hosts = append(hosts, m[1])
			}
		}
	}
	return hosts
}

// assignSafeNames sets each container's safe name (sanitized filename / router
// id), disambiguating collisions with a short container-ID suffix.
func assignSafeNames(rcs []routedContainer) {
	bases := make([]string, len(rcs))
	// Seed the reserved dashboard id so a user container named "dashboard" is
	// always suffixed away from the self-route's cert files.
	counts := map[string]int{dashboardSafe: 1}
	for i, rc := range rcs {
		bases[i] = sanitizeName(rc.name)
		counts[bases[i]]++
	}
	for i := range rcs {
		if counts[bases[i]] > 1 {
			rcs[i].safe = bases[i] + "-" + short(rcs[i].id)
		} else {
			rcs[i].safe = bases[i]
		}
	}
}

// sanitizeName reduces a container name to the safe filename/router-id charset
// [a-zA-Z0-9_.-].
func sanitizeName(name string) string {
	s := unsafeNameRe.ReplaceAllString(name, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "container"
	}
	return s
}

// warnDuplicateHosts logs when a host appears both in a proximo.hosts route and
// in a native traefik.* router rule, which would create duplicate routers
// across providers.
func warnDuplicateHosts(containers []container.Summary, routed []routedContainer) {
	proximoSet := map[string]bool{}
	for _, rc := range routed {
		if rc.proximo {
			for _, h := range rc.hosts {
				proximoSet[h] = true
			}
		}
	}
	if len(proximoSet) == 0 {
		return
	}
	seen := map[string]bool{}
	for _, c := range containers {
		if !isRouted(c) {
			continue
		}
		for _, h := range hostsFromLabels(c.Labels) {
			if proximoSet[h] && !seen[h] {
				seen[h] = true
				log.Printf("proximo watcher: host %q is declared in both proximo.hosts and a traefik.* router rule; use one scheme per host to avoid duplicate routers", h)
			}
		}
	}
}

// routeConflict names a proximo route dropped because another container already
// claimed the same (host, path prefix) pair.
type routeConflict struct {
	name string // dropped container's name
	host string // the host it collided on
	path string // the path prefix both claimed ("" = bare host)
}

// replicaKey identifies containers that back the same logical service — same
// hosts, port (HTTP or TCP), path, middlewares, redirect and TLS mode — so they
// can be merged into one round-robin route. It is the rendered router config with
// the backend identity (name/safe/servers) neutralized: two containers are
// replicas exactly when they would produce byte-identical routers except for the
// backend server. Deriving the key from renderRouter keeps it in lockstep with
// what actually affects a route, so any field rendered into the router config can
// never silently merge containers that differ on it. (A routing decision applied
// outside renderRouter — e.g. health gating — would not enter the key; keep such
// fields out of the merge or fold them into the render.)
func replicaKey(rc routedContainer) string {
	norm := rc
	norm.name = "\x00"
	norm.safe = "\x00"
	norm.servers = nil
	// Host order must not affect replica identity: two containers listing the same
	// hosts in a different order are the same route. Sort a copy for the key (the
	// representative keeps its own host order for the rendered rule).
	norm.hosts = append([]string(nil), rc.hosts...)
	sort.Strings(norm.hosts)
	return string(renderRouter(norm))
}

// routeMerge names a proximo container merged into an existing route as a
// round-robin replica: identical routing config (see replicaKey) to the
// representative, differing only in the backend. The watcher logs merges so an
// accidental duplicate (a stale or mislabeled container) is visible, not silently
// balanced; `proximo status` stays quiet and shows the (balanced ×N) marker.
type routeMerge struct {
	rep    string // representative (winning) container whose route absorbs the replica
	member string // the container merged in as an extra backend
	host   string // a host they share
}

// resolveRouteConflicts merges replica containers and drops genuine conflicts.
// Containers with identical routing config (see replicaKey) but different
// backends are merged into one route whose load balancer carries every backend
// (round-robin), the lexicographically-first name representing the group — plain
// host routes only, since path-scoped routes are never replica-merged. Distinct
// routes that still collide on a (host, path prefix) pair are resolved as before:
// the lexicographically-first group wins, the rest are returned as conflicts for
// the caller to log. Two routes on the same host with different prefixes do not
// collide. Native (non-proximo) routes are never merged or conflicted — Traefik's
// Docker provider owns them. It is the shared resolver behind both the watcher
// (which logs the losers and the merges) and `proximo status` (which stays quiet),
// so the two agree on which routes are served and which are balanced.
func resolveRouteConflicts(routed []routedContainer) (kept []routedContainer, merges []routeMerge, conflicts []routeConflict) {
	sorted := append([]routedContainer(nil), routed...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })

	// First pass: merge exact replicas. Iterating in name order makes the first
	// occurrence the representative and appends the rest as extra backends. Only
	// plain host routes (no path prefix) are replica-eligible; a path-scoped route
	// always gets its own group and goes through conflict resolution, so two
	// identical (host, path) containers conflict rather than being balanced —
	// replica detection is for same-host/same-port services, not path splits.
	var groups []*routedContainer
	byKey := map[string]*routedContainer{}
	for _, rc := range sorted {
		if !rc.proximo || rc.path != "" {
			g := rc
			if rc.proximo {
				g.servers = []string{rc.name}
			}
			groups = append(groups, &g)
			continue
		}
		key := replicaKey(rc)
		if g, ok := byKey[key]; ok {
			g.servers = append(g.servers, rc.name)
			merges = append(merges, routeMerge{rep: g.name, member: rc.name, host: g.hosts[0]})
			continue
		}
		g := rc
		g.servers = []string{rc.name}
		byKey[key] = &g
		groups = append(groups, &g)
	}

	// Second pass: resolve (host, path prefix) collisions between distinct groups.
	claimed := map[[2]string]string{} // {host, prefix} -> winning representative name
	for _, g := range groups {
		if !g.proximo {
			kept = append(kept, *g)
			continue
		}
		clash := ""
		for _, h := range g.hosts {
			if owner, ok := claimed[[2]string{h, g.path}]; ok && owner != g.name {
				clash = h
				break
			}
		}
		if clash != "" {
			conflicts = append(conflicts, routeConflict{name: g.name, host: clash, path: g.path})
			continue
		}
		for _, h := range g.hosts {
			claimed[[2]string{h, g.path}] = g.name
		}
		kept = append(kept, *g)
	}
	return kept, merges, conflicts
}

// targetNetworks returns the network IDs Traefik must join to reach a backend,
// honoring the traefik.docker.network label for multi-network containers.
func targetNetworks(c container.Summary) []string {
	if c.NetworkSettings == nil {
		return nil
	}
	if name := c.Labels[networkLabel]; name != "" {
		if ep, ok := c.NetworkSettings.Networks[name]; ok {
			return []string{ep.NetworkID}
		}
	}
	var ids []string
	for name, ep := range c.NetworkSettings.Networks {
		if isUnroutableNetwork(name) {
			continue
		}
		ids = append(ids, ep.NetworkID)
	}
	return ids
}

func isUnroutableNetwork(name string) bool {
	switch name {
	case "bridge", "host", "none":
		return true
	}
	return false
}

func isStackNetwork(name string) bool {
	if isUnroutableNetwork(name) {
		return true
	}
	return strings.HasPrefix(name, "proximo_") || name == "proximo"
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
