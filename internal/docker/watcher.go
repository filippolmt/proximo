package docker

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
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
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
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

	// routerFilePrefix prefixes the per-container Traefik dynamic config files
	// the watcher writes into the file-provider directory.
	routerFilePrefix = "proximo-route-"
)

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
	name    string   // primary container name (used as the backend DNS name)
	id      string   // container ID (for collision disambiguation)
	safe    string   // sanitized name used for filenames / router ids
	hosts   []string // routed hostnames
	port    int      // resolved backend port (proximo path only)
	proximo bool     // true when routed via proximo.hosts (generate dynamic config)
}

// Watcher keeps Traefik attached to the Docker networks of routed containers and
// issues a CA-signed certificate per container, written to Traefik's
// file-provider directory.
type Watcher struct {
	cli        *client.Client
	caCert     *x509.Certificate
	caKey      *ecdsa.PrivateKey
	dynamicDir string
	// lastHosts caches the last-issued host set per container (keyed by safe
	// name) so certs are reissued only when a container's hosts change.
	lastHosts map[string]string
}

// NewWatcher creates a Watcher from the Docker environment and loads the CA (for
// issuing per-host certificates) from the mounted paths.
func NewWatcher() (*Watcher, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		cli:        cli,
		dynamicDir: getenv("PROXIMO_DYNAMIC_DIR", "/etc/traefik/dynamic"),
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

// buildRouted turns a routed container summary into the routing model: proximo
// path when proximo.hosts is set and enabled, native Traefik path otherwise.
// It returns ok=false when the container should not get a route/cert (e.g.
// ambiguous port, or a native container with no Host rule).
func (w *Watcher) buildRouted(ctx context.Context, c container.Summary) (routedContainer, bool) {
	rc := routedContainer{name: primaryName(c), id: c.ID}

	valid, invalid := splitHosts(c.Labels[proximoHostsLabel])
	for _, h := range invalid {
		log.Printf("proximo watcher: container %s: ignoring invalid host %q in %s", rc.name, h, proximoHostsLabel)
	}
	if len(valid) > 0 && isProximoEnabled(c.Labels) {
		port, ok := w.resolveBackendPort(ctx, c)
		if !ok {
			return routedContainer{}, false
		}
		rc.hosts = valid
		rc.port = port
		rc.proximo = true
		return rc, true
	}

	// Native Traefik path: hosts come from the router rule labels. Traefik's
	// Docker provider configures the route; the watcher only needs the hosts to
	// issue the certificate.
	rc.hosts = hostsFromLabels(c.Labels)
	if len(rc.hosts) == 0 {
		return routedContainer{}, false
	}
	return rc, true
}

// resolveBackendPort returns the backend port for a routed container. It uses
// proximo.port when set; otherwise it inspects the container and returns the
// single exposed TCP port. It returns ok=false (and logs) when the port is
// absent and the container exposes zero or multiple TCP ports.
func (w *Watcher) resolveBackendPort(ctx context.Context, c container.Summary) (int, bool) {
	name := primaryName(c)
	if v := strings.TrimSpace(c.Labels[proximoPortLabel]); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			log.Printf("proximo watcher: container %s has invalid %s=%q", name, proximoPortLabel, v)
			return 0, false
		}
		return p, true
	}

	info, err := w.cli.ContainerInspect(ctx, c.ID)
	if err != nil {
		log.Printf("proximo watcher: inspect %s: %v", name, err)
		return 0, false
	}
	var ports nat.PortSet
	if info.Config != nil {
		ports = info.Config.ExposedPorts
	}
	port, ok := portFromExposed(ports)
	if !ok {
		log.Printf("proximo watcher: container %s exposes %d TCP port(s); set %s to disambiguate",
			name, countTCP(ports), proximoPortLabel)
	}
	return port, ok
}

// portFromExposed returns the single exposed TCP port, or ok=false when the set
// contains zero or more than one TCP port.
func portFromExposed(ports nat.PortSet) (int, bool) {
	var tcp []int
	for p := range ports {
		if p.Proto() == "tcp" {
			tcp = append(tcp, p.Int())
		}
	}
	if len(tcp) == 1 {
		return tcp[0], true
	}
	return 0, false
}

func countTCP(ports nat.PortSet) int {
	n := 0
	for p := range ports {
		if p.Proto() == "tcp" {
			n++
		}
	}
	return n
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
		if err := os.WriteFile(path, renderRouter(rc), 0o644); err != nil {
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
// before reaching here, so templating them into the rule is safe.
func renderRouter(rc routedContainer) []byte {
	id := "proximo-" + rc.safe
	rules := make([]string, 0, len(rc.hosts))
	for _, h := range rc.hosts {
		rules = append(rules, "Host(`"+h+"`)")
	}
	rule := strings.Join(rules, " || ")
	url := "http://" + rc.name + ":" + strconv.Itoa(rc.port)

	var b strings.Builder
	b.WriteString("http:\n")
	b.WriteString("  routers:\n")
	b.WriteString("    " + id + ":\n")
	b.WriteString("      entryPoints:\n        - websecure\n")
	b.WriteString("      rule: \"" + rule + "\"\n")
	b.WriteString("      service: " + id + "\n")
	b.WriteString("      tls: {}\n")
	b.WriteString("  services:\n")
	b.WriteString("    " + id + ":\n")
	b.WriteString("      loadBalancer:\n")
	b.WriteString("        servers:\n")
	b.WriteString("          - url: \"" + url + "\"\n")
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
	b.WriteString("        certFile: " + filepath.Join(certsDir, first.safe+".crt") + "\n")
	b.WriteString("        keyFile: " + filepath.Join(certsDir, first.safe+".key") + "\n")
	b.WriteString("  certificates:\n")
	for _, rc := range entries {
		b.WriteString("    - certFile: " + filepath.Join(certsDir, rc.safe+".crt") + "\n")
		b.WriteString("      keyFile: " + filepath.Join(certsDir, rc.safe+".key") + "\n")
	}
	if err := os.WriteFile(tlsPath, []byte(b.String()), 0o644); err != nil {
		log.Printf("proximo watcher: write tls config: %v", err)
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
	if len(proximoHosts(c.Labels)) > 0 && isProximoEnabled(c.Labels) {
		return true
	}
	return strings.EqualFold(c.Labels[enableLabel], "true")
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
	for _, part := range strings.Split(raw, ",") {
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
	counts := map[string]int{}
	for _, rc := range rcs {
		counts[sanitizeName(rc.name)]++
	}
	for i := range rcs {
		base := sanitizeName(rcs[i].name)
		if counts[base] > 1 {
			rcs[i].safe = base + "-" + short(rcs[i].id)
		} else {
			rcs[i].safe = base
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
