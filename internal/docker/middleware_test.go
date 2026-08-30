package docker

import (
	"context"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestParseAuth(t *testing.T) {
	creds, invalid := parseAuth("alice:secret, bob:$2y$pre, broken, :nouser, user:")
	if !slices.Equal(invalid, []string{"broken", ":nouser", "user:"}) {
		t.Errorf("invalid = %v, want [broken :nouser user:]", invalid)
	}
	if len(creds) != 2 {
		t.Fatalf("creds = %v, want 2 entries", creds)
	}
	if creds[0] != (authCred{user: "alice", secret: "secret", hashed: false}) {
		t.Errorf("creds[0] = %+v, want plaintext alice:secret", creds[0])
	}
	if creds[1] != (authCred{user: "bob", secret: "$2y$pre", hashed: true}) {
		t.Errorf("creds[1] = %+v, want pre-hashed bob:$2y$pre", creds[1])
	}
}

func TestParseCors(t *testing.T) {
	if spec, empty := parseCors(map[string]string{}); spec != nil || empty {
		t.Errorf("absent label = (%v, empty=%v), want (nil, false)", spec, empty)
	}
	if spec, _ := parseCors(map[string]string{proximoCorsLabel: "true"}); spec == nil || !spec.allowAll {
		t.Errorf("true = %+v, want allowAll", spec)
	}
	spec, _ := parseCors(map[string]string{proximoCorsLabel: "https://app.test, https://b.test"})
	if spec == nil || spec.allowAll || !slices.Equal(spec.origins, []string{"https://app.test", "https://b.test"}) {
		t.Errorf("origin list = %+v, want origins", spec)
	}
	if spec, empty := parseCors(map[string]string{proximoCorsLabel: "  "}); spec != nil || !empty {
		t.Errorf("blank = (%v, empty=%v), want (nil, true)", spec, empty)
	}
}

func TestParseHeaders(t *testing.T) {
	headers, invalid := parseHeaders(map[string]string{
		proximoHeaderPrefix + "X-Env":      "dev",
		proximoHeaderPrefix + "X-App":      "proximo",
		proximoHeaderPrefix + "bad header": "x",
		"proximo.hosts":                    "app.test",
	})
	// Sorted by name: X-App before X-Env.
	if len(headers) != 2 || headers[0] != (headerKV{"X-App", "proximo"}) || headers[1] != (headerKV{"X-Env", "dev"}) {
		t.Errorf("headers = %v, want sorted [X-App X-Env]", headers)
	}
	if !slices.Equal(invalid, []string{"bad header"}) {
		t.Errorf("invalid = %v, want [bad header]", invalid)
	}
}

func TestMiddlewareActiveAndChainOrder(t *testing.T) {
	m := middlewareSet{
		auth:    []authCred{{user: "a", secret: "b"}},
		cors:    &corsSpec{allowAll: true},
		headers: []headerKV{{"X-Env", "dev"}},
	}
	if got := m.active(); !slices.Equal(got, []string{"auth", "cors", "headers"}) {
		t.Errorf("active = %v, want [auth cors headers]", got)
	}
	if got := m.chainRefs("proximo-app"); !slices.Equal(got, []string{"proximo-app-auth", "proximo-app-cors", "proximo-app-headers"}) {
		t.Errorf("chainRefs = %v, want auth/cors/headers refs", got)
	}
	if !(middlewareSet{}).empty() || m.empty() {
		t.Error("empty() wrong for zero/non-zero middlewareSet")
	}
}

// TestActiveMatchesChainRefs: `proximo status` reports m.active() and the
// watcher attaches m.chainRefs(id); both must name the same middlewares in the
// same order for every subset, so status never disagrees with what the watcher
// actually wired (3.2).
func TestActiveMatchesChainRefs(t *testing.T) {
	auth := []authCred{{user: "a", secret: "b"}}
	cors := &corsSpec{allowAll: true}
	hdr := []headerKV{{"X-Env", "dev"}}
	for _, m := range []middlewareSet{
		{},
		{auth: auth},
		{cors: cors},
		{headers: hdr},
		{auth: auth, cors: cors},
		{auth: auth, headers: hdr},
		{cors: cors, headers: hdr},
		{auth: auth, cors: cors, headers: hdr},
	} {
		active := m.active()
		refs := m.chainRefs("proximo-app")
		if len(active) != len(refs) {
			t.Fatalf("active %v and chainRefs %v differ in length for %+v", active, refs, m)
		}
		for i := range active {
			if refs[i] != "proximo-app-"+active[i] {
				t.Errorf("ref %q does not match active %q for %+v", refs[i], active[i], m)
			}
		}
	}
}

// TestRenderRouterMiddlewares: a proximo router with auth + cors + headers
// references the chain in fixed order (before any strip) and emits each
// middleware definition block.
func TestRenderRouterMiddlewares(t *testing.T) {
	rc := routedContainer{
		name: "whoami", safe: "whoami", hosts: []string{"app.test"}, port: 80, proximo: true,
		path: "/api", strip: true,
		mw: middlewareSet{
			auth:    []authCred{{user: "alice", secret: "$2y$hashed", hashed: true}},
			cors:    &corsSpec{allowAll: true},
			headers: []headerKV{{"X-Env", "dev"}},
		},
	}
	out := string(renderRouter(rc))

	// Chain reference order on the websecure router: auth, cors, headers, strip.
	refs := []string{"- proximo-whoami-auth", "- proximo-whoami-cors", "- proximo-whoami-headers", "- proximo-whoami-strip"}
	idx := make([]int, len(refs))
	for i, r := range refs {
		idx[i] = strings.Index(out, r)
		if idx[i] < 0 {
			t.Fatalf("missing chain ref %q\n---\n%s", r, out)
		}
	}
	if !slices.IsSorted(idx) {
		t.Errorf("chain refs out of order: %v\n---\n%s", idx, out)
	}

	for _, w := range []string{
		"basicAuth:",
		`- "alice:$2y$hashed"`,
		"accessControlAllowOriginList:",
		"customResponseHeaders:",
		`X-Env: "dev"`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("renderRouter missing %q\n---\n%s", w, out)
		}
	}
}

// TestRenderRouterCorsOrigins: an origin-scoped CORS advertises only the listed
// origins, never the permissive wildcard.
func TestRenderRouterCorsOrigins(t *testing.T) {
	rc := routedContainer{
		name: "api", safe: "api", hosts: []string{"api.test"}, port: 80, proximo: true,
		mw: middlewareSet{cors: &corsSpec{origins: []string{"https://app.test"}}},
	}
	out := string(renderRouter(rc))
	if !strings.Contains(out, `- "https://app.test"`) {
		t.Errorf("missing scoped origin\n---\n%s", out)
	}
	if strings.Contains(out, "accessControlAllowOriginList:\n          - \"*\"") {
		t.Errorf("origin-scoped CORS must not advertise wildcard origin\n---\n%s", out)
	}
}

// TestMaterializeAuthHashesAndIsStable: plaintext secrets are bcrypt-hashed (and
// verify against the original), pre-hashed values pass through, and the cached
// hash is byte-stable across reconciles so the router file does not churn.
func TestMaterializeAuthHashesAndIsStable(t *testing.T) {
	w := &Watcher{authHashes: map[string]string{}}
	rc := routedContainer{name: "app", mw: middlewareSet{auth: []authCred{
		{user: "alice", secret: "secret"},
		{user: "bob", secret: "$2y$alreadyhashed", hashed: true},
	}}}

	w.materializeAuth(&rc)
	if len(rc.mw.auth) != 2 {
		t.Fatalf("auth = %v, want 2 creds", rc.mw.auth)
	}
	alice := rc.mw.auth[0]
	if !alice.hashed || alice.secret == "secret" {
		t.Errorf("alice not hashed: %+v", alice)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(alice.secret), []byte("secret")); err != nil {
		t.Errorf("alice hash does not verify against plaintext: %v", err)
	}
	if rc.mw.auth[1].secret != "$2y$alreadyhashed" {
		t.Errorf("pre-hashed secret should pass through, got %q", rc.mw.auth[1].secret)
	}

	// Re-materializing the same plaintext returns the cached hash unchanged.
	rc2 := routedContainer{name: "app", mw: middlewareSet{auth: []authCred{{user: "alice", secret: "secret"}}}}
	w.materializeAuth(&rc2)
	if rc2.mw.auth[0].secret != alice.secret {
		t.Errorf("hash not stable across reconciles: %q != %q", rc2.mw.auth[0].secret, alice.secret)
	}
}

// TestClassifyMiddlewares: classify parses middlewares on the proximo path,
// records per-middleware invalid entries, and a single invalid middleware leaves
// the route and the other middlewares intact (2.5).
func TestClassifyMiddlewares(t *testing.T) {
	c := makeSummary(map[string]string{
		proximoHostsLabel:             "app.test",
		proximoPortLabel:              "8080",
		proximoAuthLabel:              "broken", // invalid: no colon
		proximoCorsLabel:              "true",   // valid
		proximoHeaderPrefix + "X-Env": "dev",    // valid
	})
	rc, ok, info := classify(context.Background(), failInspect(t), c, "test")
	if !ok {
		t.Fatal("container with one invalid middleware should still route")
	}
	if len(rc.mw.auth) != 0 {
		t.Errorf("invalid auth should yield no credential, got %v", rc.mw.auth)
	}
	if rc.mw.cors == nil || len(rc.mw.headers) != 1 {
		t.Errorf("valid cors/header middlewares should survive an invalid auth: %+v", rc.mw)
	}
	if !slices.Equal(info.middleware.invalidAuth, []string{"broken"}) {
		t.Errorf("invalidAuth = %v, want [broken]", info.middleware.invalidAuth)
	}
}

// TestNativeRouteHasNoProximoMiddlewares: native traefik.* routes are not
// proximo-translated, so classify attaches no curated middlewares to them.
func TestNativeRouteHasNoProximoMiddlewares(t *testing.T) {
	c := makeSummary(map[string]string{
		enableLabel:                   "true",
		"traefik.http.routers.w.rule": "Host(`x.test`)",
		proximoAuthLabel:              "alice:secret",
	})
	rc, ok, _ := classify(context.Background(), failInspect(t), c, "test")
	if !ok || rc.proximo || !rc.mw.empty() {
		t.Errorf("native route should carry no proximo middlewares: proximo=%v mw=%+v", rc.proximo, rc.mw)
	}
}
