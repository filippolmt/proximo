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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/tls"
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

// unsafeNameRe matches runs of characters not allowed in a safe filename /
// router id.
var unsafeNameRe = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// routedContainer is the per-container routing model built during reconcile.
type routedContainer struct {
	name     string   // primary container name (used as the backend DNS name)
	id       string   // container ID (for collision disambiguation)
	safe     string   // sanitized name used for filenames / router ids
	hosts    []string // routed hostnames
	port     int      // resolved backend port (proximo path only)
	proximo  bool     // true when routed via proximo.hosts (generate dynamic config)
	redirect bool     // true when proximo.redirect opts in to an HTTP->HTTPS redirect
	internal bool     // routed to a Traefik internal service (api@internal): no backend port is resolved
}

// dockerAPI is the narrow slice of the Docker client the watcher depends on —
// exactly the methods reconcile and Run call. *client.Client satisfies it
// unchanged, so production passes the real client (built by newClient) and
// tests pass a fake. It mirrors the existing inspector seam: a minimal
// interface, not the whole SDK surface.
type dockerAPI interface {
	ContainerList(context.Context, container.ListOptions) ([]container.Summary, error)
	ContainerInspect(context.Context, string) (container.InspectResponse, error)
	NetworkConnect(context.Context, string, string, *network.EndpointSettings) error
	NetworkDisconnect(context.Context, string, string, bool) error
	Events(context.Context, events.ListOptions) (<-chan events.Message, <-chan error)
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
	w.reconcileLogged(ctx)

	flt := filters.NewArgs(
		filters.Arg("type", string(events.ContainerEventType)),
		filters.Arg("type", string(events.NetworkEventType)),
	)
	msgs, errs := w.cli.Events(ctx, events.ListOptions{Filters: flt})

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
			msgs, errs = w.cli.Events(ctx, events.ListOptions{Filters: flt})
		}
	}
}

func (w *Watcher) reconcileLogged(ctx context.Context) {
	if err := w.reconcile(ctx); err != nil {
		log.Printf("proximo watcher: reconcile error: %v", err)
	}
}

func (w *Watcher) reconcile(ctx context.Context) error {
	containers, err := w.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return err
	}

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

	for netID := range desired {
		if _, already := traefikNets[netID]; already {
			continue
		}
		if err := w.cli.NetworkConnect(ctx, netID, traefikID, &network.EndpointSettings{}); err != nil {
			log.Printf("proximo watcher: connect traefik to %s: %v", short(netID), err)
			continue
		}
		log.Printf("proximo watcher: connected traefik to network %s", short(netID))
	}

	for netID, name := range traefikNets {
		if isStackNetwork(name) || desired[netID] {
			continue
		}
		if err := w.cli.NetworkDisconnect(ctx, netID, traefikID, true); err != nil {
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
type inspector func(context.Context, string) (container.InspectResponse, error)

// classifyInfo carries diagnostics produced by classify for the caller to log.
// The watcher logs them; `proximo status` ignores them (it stays quiet).
type classifyInfo struct {
	invalidHosts []string   // invalid entries found in proximo.hosts
	portFailed   bool       // proximo route whose backend port could not be resolved
	port         portResult // detail explaining a port-resolution failure
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

	// Proximo routes are translated into Traefik dynamic config by the watcher,
	// so they need a backend port; native traefik.* routes are configured by
	// Traefik's Docker provider and only need a certificate.
	if proximo {
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
	if info.portFailed {
		logPortFailure(rc.name, info.port)
	}
	return rc, ok
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

	info, err := inspect(ctx, c.ID)
	if err != nil {
		return 0, false, portResult{inspectErr: err}
	}
	var ports nat.PortSet
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
func portFromExposed(ports nat.PortSet) (port, count int, ok bool) {
	var tcp []int
	for p := range ports {
		if p.Proto() == "tcp" {
			tcp = append(tcp, p.Int())
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

// renderRouter renders the Traefik dynamic config (HTTP router + service) for a
// single proximo-routed container. Hosts are validated to a hostname charset
// before reaching here, so templating them into the rule is safe. When rc.redirect
// is set it additionally emits a web-entrypoint router (same Host rule) attached
// to a redirectScheme middleware, so http://<host> 302-redirects to https://; the
// middleware rides this same file, so it is cleaned up with the rest of the config.
func renderRouter(rc routedContainer) []byte {
	id := "proximo-" + rc.safe
	rules := make([]string, 0, len(rc.hosts))
	for _, h := range rc.hosts {
		rules = append(rules, "Host(`"+h+"`)")
	}
	rule := strings.Join(rules, " || ")

	// Internal self-route (the dashboard): targets Traefik's built-in
	// api@internal service, so there is no backend service/loadbalancer
	// block and no port to resolve.
	service := id
	if rc.internal {
		service = "api@internal"
	}

	var b strings.Builder
	b.WriteString("http:\n")
	b.WriteString("  routers:\n")
	fmt.Fprintf(&b, "    %s:\n", id)
	b.WriteString("      entryPoints:\n        - websecure\n")
	fmt.Fprintf(&b, "      rule: %q\n", rule)
	fmt.Fprintf(&b, "      service: %s\n", service)
	b.WriteString("      tls: {}\n")
	if rc.redirect {
		// HTTP router on :80 for the same hosts plus the redirectScheme
		// middleware it references — emitted together so both ride this file and
		// are cleaned up together. middlewares: is a sibling of routers:/services:
		// under http:, so writing it here (before services) is order-free.
		redirectID := id + "-redirect"
		fmt.Fprintf(&b, "    %s:\n", redirectID)
		b.WriteString("      entryPoints:\n        - web\n")
		fmt.Fprintf(&b, "      rule: %q\n", rule)
		fmt.Fprintf(&b, "      service: %s\n", service)
		b.WriteString("      middlewares:\n")
		fmt.Fprintf(&b, "        - %s\n", redirectID)
		b.WriteString("  middlewares:\n")
		fmt.Fprintf(&b, "    %s:\n", redirectID)
		b.WriteString("      redirectScheme:\n")
		b.WriteString("        scheme: https\n")
		b.WriteString("        permanent: false\n")
	}
	if rc.internal {
		return []byte(b.String())
	}
	url := "http://" + rc.name + ":" + strconv.Itoa(rc.port)
	b.WriteString("  services:\n")
	fmt.Fprintf(&b, "    %s:\n", id)
	b.WriteString("      loadBalancer:\n")
	b.WriteString("        servers:\n")
	fmt.Fprintf(&b, "          - url: %q\n", url)
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
		if err := os.WriteFile(crt, certPEM, 0o644); err != nil {
			log.Printf("proximo watcher: write cert %s: %v", rc.safe, err)
			continue
		}
		if err := os.WriteFile(pkey, keyPEM, 0o600); err != nil {
			log.Printf("proximo watcher: write key %s: %v", rc.safe, err)
			continue
		}
		w.lastHosts[rc.safe] = key
		log.Printf("proximo watcher: issued certificate for %s: %s", rc.safe, key)
	}

	w.removeStaleCerts(certsDir, active)
	w.writeTLSConfig(certsDir, entries)
}

// removeStaleCerts deletes cert/key files (and forgets cached host sets) for
// containers that are no longer routed.
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
// spurious change events and config reloads.
func writeFileIfChanged(path string, data []byte, perm os.FileMode) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}
	return os.WriteFile(path, data, perm)
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
	for part := range strings.SplitSeq(raw, ",") {
		h := strings.TrimSpace(part)
		if h == "" {
			continue
		}
		if hostnameRe.MatchString(h) {
			valid = append(valid, h)
		} else {
			invalid = append(invalid, h)
		}
	}
	return valid, invalid
}

// isProximoEnabled reports whether routing is enabled for a container. It
// defaults to true and is disabled only by an explicit falsy proximo.enable
// value (false/0/no, case-insensitive).
func isProximoEnabled(labels map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(labels[proximoEnableLabel])) {
	case "false", "0", "no":
		return false
	}
	return true
}

// isProximoRedirect reports whether a container opted in to an HTTP->HTTPS
// redirect for its hosts. It defaults to false and is enabled only by an
// explicit truthy proximo.redirect value (true/1/yes, case-insensitive) —
// mirroring isProximoEnabled with the inverted default.
func isProximoRedirect(labels map[string]string) bool {
	switch strings.ToLower(strings.TrimSpace(labels[proximoRedirectLabel])) {
	case "true", "1", "yes":
		return true
	}
	return false
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
