package docker

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
)

// What a container's labels declare, and nothing about what proximo does with
// it. Every reader of a container agrees here — the watcher building routers,
// `proximo status` listing them, the Incident store judging an event — so the
// question "is this container ours, and what did it ask for" has exactly one
// answer. The label names themselves are in watcher.go, beside the stack
// constants they sit with.

// hostnameRe validates a single hostname (RFC 1123 label charset, dot-joined).
var hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`)

// dnsLabelRe validates a single DNS label — the shape a Namespace must have to
// be insertable into a host name.
var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// namespaceOf returns a container's Namespace: its Compose project name,
// lowercased and sanitized to a DNS label (Compose allows "_", host names do
// not). A container outside a Compose project — or one whose project name does
// not survive sanitization — has no Namespace, and therefore no qualified host.
func namespaceOf(labels map[string]string) string {
	ns := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(labels[composeProjectLabel])), "_", "-")
	if !dnsLabelRe.MatchString(ns) || ns == stackProject {
		return ""
	}
	return ns
}

// qualifiedHost inserts ns before the TLD in a declared host: api.test becomes
// api.shop.test. It returns "" when there is nothing to qualify — no Namespace,
// a host outside the configured TLD (the resolver answers for <tld> only, so
// the name would never resolve), or a host already carrying the Namespace.
func qualifiedHost(host, ns, tld string) string {
	if ns == "" {
		return ""
	}
	base, ok := strings.CutSuffix(host, "."+tld)
	// A host already ending in the Namespace (api.shop.test in project shop) is
	// its own qualified form and needs nothing added. Every other host under the
	// TLD gets one, so "always present" keeps exactly one exception: a container
	// with no Compose project.
	if !ok || base == "" || strings.HasSuffix(base, "."+ns) {
		return ""
	}
	return base + "." + ns + "." + tld
}

// pathPrefixRe validates a proximo.path prefix: a leading slash followed by the
// URL pchar set. It deliberately excludes backticks, quotes and whitespace so
// the prefix is safe to template into the PathPrefix(`…`) router rule.
var pathPrefixRe = regexp.MustCompile(`^/[A-Za-z0-9._~%!$&'()*+,;=:@/-]*$`)

// unsafeNameRe matches runs of characters not allowed in a safe filename /
// router id.

// isRouted reports whether a container opted in to routing — via proximo labels
// (non-empty proximo.hosts and not proximo.enable=false) or the native Traefik
// label (traefik.enable=true) — and is not part of the proximo stack.
func isRouted(c container.Summary) bool {
	return isRoutedLabels(c.Labels)
}

// isRoutedLabels is isRouted on the labels alone, so a Docker event — which
// carries a container's labels and nothing else — is judged by the same rule as
// a container listing.
func isRoutedLabels(labels map[string]string) bool {
	if _, isStack := labels[RoleLabel]; isStack {
		return false
	}
	if isProximoRoute(labels) {
		return true
	}
	return strings.EqualFold(labels[enableLabel], "true")
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
	if _, isStack := labels[RoleLabel]; isStack {
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

// NoteStarting is the note carried by a health-gated container whose
// healthcheck has not passed yet. It is a moment in a container's life, not a
// broken environment, which is why a Check must not read it as a failure —
// exported so the two cannot drift apart as two copies of one string.

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
		return NoteUnhealthy
	}
	return NoteStarting
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

// isProximoInspect reports whether a container opted in to Inspection (default
// false), reusing the same truthy-value helper as proximo.redirect.
func isProximoInspect(labels map[string]string) bool {
	return isTruthyLabel(labels, proximoInspectLabel)
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

// safeBase names a route for its files and router id. Inside a Compose project
// that is the Namespace and the service (shop-api), so a file in the certificate
// directory traces back to a container by being read, and the name survives a
// service being scaled or recreated. A container outside a project falls back to
// its own name, the only thing it has.
