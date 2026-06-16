package docker

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	// proximoAuthLabel requires HTTP basic auth on a container's routes. Its value
	// is a comma-separated list of `user:password` pairs; the password may be
	// plaintext (hashed at materialization) or already in htpasswd hash form.
	proximoAuthLabel = "proximo.auth"
	// proximoCorsLabel adds CORS response headers: `true` (or 1/yes) for permissive
	// CORS, or a comma-separated allowed-origin list to scope it.
	proximoCorsLabel = "proximo.cors"
	// proximoHeaderPrefix is the prefix for custom response-header labels:
	// `proximo.header.<Name>=<value>`. Multiple such labels accumulate.
	proximoHeaderPrefix = "proximo.header."
)

// headerNameRe validates an HTTP header (field) name against the RFC 7230 token
// charset, so a `proximo.header.<Name>` label cannot inject YAML or header
// separators into the emitted middleware.
var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_` + "`" + `|~-]+$`)

// htpasswdHashPrefixes are the password-hash prefixes Traefik's basicAuth
// accepts. A `proximo.auth` secret starting with one is passed through unhashed;
// anything else is treated as plaintext and bcrypt-hashed at materialization.
var htpasswdHashPrefixes = []string{"$2y$", "$2a$", "$2b$", "$apr1$", "$1$"}

// authCred is one basic-auth credential. secret is the password — plaintext
// until materializeAuth hashes it, or an already-hashed value passed through
// when hashed is true.
type authCred struct {
	user   string
	secret string
	hashed bool
}

// corsSpec is the parsed proximo.cors value: allowAll for the permissive form
// (`true`), otherwise an explicit allowed-origin list.
type corsSpec struct {
	allowAll bool
	origins  []string
}

// headerKV is one custom response header.
type headerKV struct{ name, value string }

// middlewareSet is the parsed, validated proximo middleware labels for a router,
// in the fixed chain order auth -> cors -> headers. Its zero value carries no
// middlewares, so a router with no labels renders exactly as before.
type middlewareSet struct {
	auth    []authCred
	cors    *corsSpec
	headers []headerKV
}

// empty reports whether no middleware is active.
func (m middlewareSet) empty() bool {
	return len(m.auth) == 0 && m.cors == nil && len(m.headers) == 0
}

// active lists the active middleware kinds in chain order, for `proximo status`.
func (m middlewareSet) active() []string {
	var names []string
	if len(m.auth) > 0 {
		names = append(names, "auth")
	}
	if m.cors != nil {
		names = append(names, "cors")
	}
	if len(m.headers) > 0 {
		names = append(names, "headers")
	}
	return names
}

// middlewareInfo carries per-middleware validation diagnostics for the watcher
// to log. `proximo status` ignores them (it stays quiet), mirroring classifyInfo.
type middlewareInfo struct {
	invalidAuth    []string // auth pairs missing the ":" separator (skipped)
	invalidHeaders []string // proximo.header.* names that failed validation (skipped)
	emptyCors      bool     // proximo.cors present but blank/all-empty (skipped)
}

// parseMiddlewares reads the curated proximo middleware labels into a
// middlewareSet. Each middleware validates independently: a malformed auth pair,
// an invalid header name, or a blank CORS value invalidates only that single
// middleware (recorded in middlewareInfo for the caller to warn), leaving the
// container's routing and its other middlewares intact.
func parseMiddlewares(labels map[string]string) (middlewareSet, middlewareInfo) {
	var m middlewareSet
	var info middlewareInfo

	m.auth, info.invalidAuth = parseAuth(labels[proximoAuthLabel])
	m.cors, info.emptyCors = parseCors(labels[proximoCorsLabel], labels)
	m.headers, info.invalidHeaders = parseHeaders(labels)

	return m, info
}

// parseAuth splits a comma-separated `user:password` list into credentials.
// A pair missing the ":" separator (or with an empty user) is invalid and
// returned for the caller to warn; the rest are kept.
func parseAuth(raw string) (creds []authCred, invalid []string) {
	for part := range strings.SplitSeq(raw, ",") {
		pair := strings.TrimSpace(part)
		if pair == "" {
			continue
		}
		user, secret, ok := strings.Cut(pair, ":")
		if !ok || user == "" || secret == "" {
			invalid = append(invalid, pair)
			continue
		}
		creds = append(creds, authCred{user: user, secret: secret, hashed: isPreHashed(secret)})
	}
	return creds, invalid
}

// isPreHashed reports whether a basic-auth secret is already an htpasswd hash,
// so it is passed through to the dynamic config instead of being re-hashed.
func isPreHashed(secret string) bool {
	for _, p := range htpasswdHashPrefixes {
		if strings.HasPrefix(secret, p) {
			return true
		}
	}
	return false
}

// parseCors interprets the proximo.cors value: a truthy value (true/1/yes) is
// permissive CORS, any other non-empty value is a comma-separated allowed-origin
// list. A present-but-blank value (or one whose entries are all empty) yields no
// middleware and empty=true for the caller to warn. An absent label yields nil.
func parseCors(raw string, labels map[string]string) (spec *corsSpec, empty bool) {
	if _, present := labels[proximoCorsLabel]; !present {
		return nil, false
	}
	if isTruthyLabel(labels, proximoCorsLabel) {
		return &corsSpec{allowAll: true}, false
	}
	var origins []string
	for part := range strings.SplitSeq(raw, ",") {
		if o := strings.TrimSpace(part); o != "" {
			origins = append(origins, o)
		}
	}
	if len(origins) == 0 {
		return nil, true
	}
	return &corsSpec{origins: origins}, false
}

// parseHeaders collects proximo.header.<Name>=<value> labels into a
// name-sorted list (deterministic emission). A label whose name fails
// validation is skipped and returned for the caller to warn.
func parseHeaders(labels map[string]string) (headers []headerKV, invalid []string) {
	for k, v := range labels {
		name, ok := strings.CutPrefix(k, proximoHeaderPrefix)
		if !ok {
			continue
		}
		if !headerNameRe.MatchString(name) {
			invalid = append(invalid, name)
			continue
		}
		headers = append(headers, headerKV{name: name, value: v})
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i].name < headers[j].name })
	sort.Strings(invalid)
	return headers, invalid
}

// chainRefs lists the middleware names a router must reference, in the fixed
// chain order auth -> cors -> headers. id is the router id (proximo-<safe>).
func (m middlewareSet) chainRefs(id string) []string {
	var refs []string
	if len(m.auth) > 0 {
		refs = append(refs, id+"-auth")
	}
	if m.cors != nil {
		refs = append(refs, id+"-cors")
	}
	if len(m.headers) > 0 {
		refs = append(refs, id+"-headers")
	}
	return refs
}

// renderDefs writes the middleware definition blocks (basicAuth, CORS headers,
// custom-header headers) under an already-open `http.middlewares:` section, in
// chain order. Secrets are emitted verbatim — the watcher hashes plaintext
// passwords (materializeAuth) before this runs.
func (m middlewareSet) renderDefs(b *strings.Builder, id string) {
	if len(m.auth) > 0 {
		fmt.Fprintf(b, "    %s-auth:\n", id)
		b.WriteString("      basicAuth:\n        users:\n")
		for _, c := range m.auth {
			fmt.Fprintf(b, "          - %q\n", c.user+":"+c.secret)
		}
	}
	if m.cors != nil {
		fmt.Fprintf(b, "    %s-cors:\n", id)
		b.WriteString("      headers:\n")
		b.WriteString("        accessControlAllowMethods:\n")
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"} {
			fmt.Fprintf(b, "          - %q\n", method)
		}
		b.WriteString("        accessControlAllowHeaders:\n          - \"*\"\n")
		b.WriteString("        accessControlAllowOriginList:\n")
		if m.cors.allowAll {
			b.WriteString("          - \"*\"\n")
		} else {
			for _, o := range m.cors.origins {
				fmt.Fprintf(b, "          - %q\n", o)
			}
		}
		b.WriteString("        accessControlMaxAge: 100\n")
		b.WriteString("        addVaryHeader: true\n")
	}
	if len(m.headers) > 0 {
		fmt.Fprintf(b, "    %s-headers:\n", id)
		b.WriteString("      headers:\n        customResponseHeaders:\n")
		for _, h := range m.headers {
			fmt.Fprintf(b, "          %s: %q\n", h.name, h.value)
		}
	}
}
