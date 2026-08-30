package inspect

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- store ------------------------------------------------------------------

func TestStoreEvictsByBytesAndKeepsTheNewest(t *testing.T) {
	s := NewStore(4096)
	for i := range 20 {
		e := &Exchange{ID: strconv.Itoa(i), At: time.Now(), Host: "web.test"}
		s.Add(e)
		s.Attach(e.ID, ingested{Report: Report{Message: "boom"}, Snapshot: make([]byte, 1024)})
	}
	got := s.List(Query{})
	if len(got) == 0 {
		t.Fatal("everything was evicted")
	}
	if got[0].ID != "19" {
		t.Fatalf("newest Exchange should survive, got %q", got[0].ID)
	}
	if len(got) > 6 {
		t.Fatalf("budget not enforced: %d Exchanges held for 4096 bytes", len(got))
	}
	if !got[0].HasSnapshot {
		t.Error("HasSnapshot not reported to JSON consumers")
	}
	if got[0].Snapshot != nil {
		t.Error("List must not carry the Snapshot inline")
	}
}

func TestStoreAttachToEvictedExchangeIsDropped(t *testing.T) {
	s := NewStore(4096)
	if s.Attach("nope", ingested{Report: Report{Message: "x"}}) {
		t.Fatal("attaching to an unknown id must report false, not panic")
	}
}

func TestStoreListFilters(t *testing.T) {
	s := NewStore(0)
	s.Add(&Exchange{ID: "old", At: time.Now().Add(-time.Hour), Host: "web.test"})
	s.Add(&Exchange{ID: "new", At: time.Now(), Host: "web.test"})
	s.Add(&Exchange{ID: "other", At: time.Now(), Host: "api.test"})

	if got := s.List(Query{Host: "web.test"}); len(got) != 2 {
		t.Fatalf("host filter: want 2, got %d", len(got))
	}
	got := s.List(Query{Since: time.Minute})
	if len(got) != 2 {
		t.Fatalf("since filter: want 2, got %d", len(got))
	}
	if got = s.List(Query{Limit: 1}); len(got) != 1 || got[0].At.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("limit must keep the most recent, got %+v", got)
	}
}

// --- the report format ------------------------------------------------------

// sample is what agent.js posts for an uncaught TypeError, with the stack shaped
// the way a browser writes it.
const sample = `{
  "at": 1756548000.5,
  "type": "TypeError",
  "level": "error",
  "message": "Cannot read properties of undefined (reading 'total')",
  "file": "https://web.test/src/checkout/Page.tsx",
  "line": 12,
  "col": 5,
  "stack": "TypeError: Cannot read properties of undefined (reading 'total')\n    at renderSummary (https://web.test/src/checkout/Summary.tsx:47:18)\n    at onMount (https://web.test/src/checkout/Page.tsx:12:5)\n    at r (https://web.test/.proximo/agent.js:16:7079)",
  "dom": "<html><body>state at the time</body></html>",
  "breadcrumbs": [
    {"at": 1756547999, "category": "console", "level": "warning", "message": "slow render"},
    {"at": 1756548000, "category": "fetch", "message": "GET /api/cart → 500"}
  ]
}`

func TestDecodeReport(t *testing.T) {
	in, err := decode([]byte(sample))
	if err != nil || !in.Found {
		t.Fatalf("decode: found=%v err=%v", in.Found, err)
	}
	r := in.Report

	if r.Type != "TypeError" || !strings.Contains(r.Message, "reading 'total'") {
		t.Errorf("report = %+v", r)
	}
	if !strings.Contains(string(in.Snapshot), "state at the time") {
		t.Errorf("snapshot = %q", in.Snapshot)
	}
	if r.At.IsZero() || r.At.Year() != 2025 {
		t.Errorf("timestamp not parsed: %v", r.At)
	}
	// The raw stack is kept verbatim: it is what the browser wrote, and nothing
	// parsed out of it can be as trustworthy.
	if !strings.Contains(r.Stack, "at renderSummary") {
		t.Errorf("stack not kept: %q", r.Stack)
	}
	// ...and parsed into frames for display, innermost first.
	if len(r.Frames) != 2 {
		t.Fatalf("frames = %+v, want 2 (the agent's own dropped)", r.Frames)
	}
	if r.Frames[0].Func != "renderSummary" || r.Frames[0].Line != 47 || r.Frames[0].Col != 18 {
		t.Errorf("first frame = %+v", r.Frames[0])
	}
	for _, f := range r.Frames {
		if strings.Contains(f.File, ReservedPath) {
			t.Errorf("the agent's own frame survived: %+v", f)
		}
	}
	if len(r.Breadcrumbs) != 2 || r.Breadcrumbs[1].Category != "fetch" {
		t.Errorf("breadcrumbs = %+v", r.Breadcrumbs)
	}
	if lvl := r.Breadcrumbs[1].Level; lvl != "info" {
		t.Errorf("a breadcrumb with no level should default to info, got %q", lvl)
	}
}

// TestDecodeWithoutAStack: a cross-origin script yields "Script error." with no
// stack at all. The location window.onerror gave us is the only thing left, and
// it must not be thrown away.
func TestDecodeWithoutAStack(t *testing.T) {
	in, err := decode([]byte(`{"message":"Script error.","file":"https://cdn.example/x.js","line":1,"col":0}`))
	if err != nil || !in.Found {
		t.Fatalf("decode: found=%v err=%v", in.Found, err)
	}
	if len(in.Report.Frames) != 1 || in.Report.Frames[0].File != "https://cdn.example/x.js" {
		t.Fatalf("the onerror location must survive a missing stack: %+v", in.Report.Frames)
	}
	if in.Report.Type != "Error" || in.Report.Level != "error" {
		t.Errorf("defaults not applied: %+v", in.Report)
	}
}

func TestDecodeAnonymousFrames(t *testing.T) {
	in, _ := decode([]byte(`{"message":"boom","stack":"Error: boom\n    at https://web.test/app.js:9:3"}`))
	if len(in.Report.Frames) != 1 || in.Report.Frames[0].Line != 9 || in.Report.Frames[0].Func != "" {
		t.Fatalf("anonymous frame not parsed: %+v", in.Report.Frames)
	}
}

func TestDecodeIgnoresAnEmptyReport(t *testing.T) {
	in, err := decode([]byte(`{"breadcrumbs":[]}`))
	if err != nil || in.Found {
		t.Fatalf("a report with nothing in it must be dropped quietly: found=%v err=%v", in.Found, err)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := decode([]byte("not json")); err == nil {
		t.Fatal("malformed JSON must be an error, not a silent empty report")
	}
}

// --- CSP --------------------------------------------------------------------

func TestReconcileCSP(t *testing.T) {
	tests := []struct {
		name        string
		policy      string
		wantNonce   string // "minted" means any fresh nonce
		wantRelaxed bool
	}{
		{"no policy", "", "", false},
		{"self is enough", "default-src 'self'; script-src 'self' https://cdn.example", "", false},
		{"scripts unconstrained", "img-src 'self'", "", false},
		{"reuse the page nonce", "script-src 'nonce-abc123' 'strict-dynamic'", "abc123", false},
		{"strict-dynamic voids self", "script-src 'self' 'strict-dynamic'", "minted", true},
		{"hash-only policy", "script-src 'sha256-Ab+/='", "minted", true},
		{"none", "default-src 'none'", "minted", true},
		{"script-src-elem wins over script-src", "script-src 'self'; script-src-elem 'none'", "minted", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.policy != "" {
				h.Set("Content-Security-Policy", tc.policy)
			}
			nonce, warning := reconcileCSP(h)

			switch tc.wantNonce {
			case "":
				if nonce != "" {
					t.Errorf("unexpected nonce %q", nonce)
				}
			case "minted":
				if nonce == "" {
					t.Error("expected a minted nonce")
				}
				if got := h.Get("Content-Security-Policy"); !strings.Contains(got, "'nonce-"+nonce+"'") {
					t.Errorf("nonce not written into the policy: %q", got)
				}
			default:
				if nonce != tc.wantNonce {
					t.Errorf("nonce = %q, want %q", nonce, tc.wantNonce)
				}
				if h.Get("Content-Security-Policy") != tc.policy {
					t.Error("policy must be left untouched when its own nonce is reused")
				}
			}
			if (warning != "") != tc.wantRelaxed {
				t.Errorf("warning = %q, wantRelaxed = %v", warning, tc.wantRelaxed)
			}
		})
	}
}

// --- the hop ----------------------------------------------------------------

var tagRe = regexp.MustCompile(`data-proximo-exchange="([0-9a-f]+)"`)

// hop wires a backend behind the handler and returns both, plus a helper that
// drives one request through it the way Traefik would.
func hop(t *testing.T, backend http.HandlerFunc) (*Handler, *Store, func(path string) *http.Response) {
	t.Helper()
	srv := httptest.NewServer(backend)
	t.Cleanup(srv.Close)

	store := NewStore(0)
	h := NewHandler(store, []byte("// agent"))

	return h, store, func(path string) *http.Response {
		r := httptest.NewRequest(http.MethodGet, "http://web.test"+path, nil)
		r.Header.Set("Accept-Encoding", "br, gzip, zstd")
		r.Header.Set(BackendHeader, srv.URL)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Result()
	}
}

func TestHopInjectsIntoHTMLAndRecordsTheExchange(t *testing.T) {
	page := "<html><head><title>hi</title></head><body>ok</body></html>"
	h, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
		// The browser's list is dropped: only the gzip Go's transport unwraps
		// itself may reach the backend.
		if got := r.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Errorf("Accept-Encoding = %q, want just gzip", got)
		}
		if got := r.Header.Get(BackendHeader); got != "" {
			t.Errorf("%s must not reach the backend, saw %q", BackendHeader, got)
		}
		if r.Host != "web.test" {
			t.Errorf("backend should see the browser's Host, saw %q", r.Host)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	})

	resp := do("/checkout")
	body := readAll(t, resp)

	m := tagRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("agent tag not injected: %s", body)
	}
	if !strings.Contains(body, `<script src="`+ReservedPath+h.agentPath+`"`) {
		t.Errorf("agent src wrong: %s", body)
	}
	if !strings.Contains(body, m[0]+`></script></head>`) {
		t.Errorf("tag must go immediately before </head>: %s", body)
	}
	if got, _ := strconv.Atoi(resp.Header.Get("Content-Length")); got != len(body) {
		t.Errorf("Content-Length = %s, body is %d bytes", resp.Header.Get("Content-Length"), len(body))
	}

	ex := store.List(Query{})
	if len(ex) != 1 {
		t.Fatalf("want 1 Exchange, got %d", len(ex))
	}
	if ex[0].ID != m[1] {
		t.Errorf("Exchange id %q does not match the injected tag %q", ex[0].ID, m[1])
	}
	if ex[0].Status != 200 || ex[0].Host != "web.test" || ex[0].Path != "/checkout" {
		t.Errorf("access record = %+v", ex[0])
	}
	if ex[0].Bytes != int64(len(body)) {
		t.Errorf("Bytes = %d, want %d", ex[0].Bytes, len(body))
	}
}

func TestHopLeavesNonHTMLAlone(t *testing.T) {
	payload := `{"cart":[]}`
	_, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, payload)
	})
	if got := readAll(t, do("/api/cart")); got != payload {
		t.Fatalf("JSON was rewritten: %q", got)
	}
	if ex := store.List(Query{}); len(ex) != 1 || ex[0].Status != 200 {
		t.Fatalf("the Access record is still recorded for non-HTML: %+v", ex)
	}
}

func TestHopWarnsWhenItCannotInject(t *testing.T) {
	_, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>no head here</body></html>")
	})
	do("/")
	ex := store.List(Query{})
	if len(ex) != 1 || len(ex[0].Warnings) != 1 || !strings.Contains(ex[0].Warnings[0], "</head>") {
		t.Fatalf("expected a warning about the missing </head>, got %+v", ex)
	}
}

func TestHopRelaxesCSPAndSaysSo(t *testing.T) {
	_, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		fmt.Fprint(w, "<html><head></head><body></body></html>")
	})
	resp := do("/")
	body := readAll(t, resp)

	policy := resp.Header.Get("Content-Security-Policy")
	nonce := regexp.MustCompile(`'nonce-([0-9a-f]+)'`).FindStringSubmatch(policy)
	if nonce == nil {
		t.Fatalf("policy not relaxed: %q", policy)
	}
	if !strings.Contains(body, `nonce="`+nonce[1]+`"`) {
		t.Errorf("injected tag is missing the nonce: %s", body)
	}
	ex := store.List(Query{})
	if len(ex) != 1 || len(ex[0].Warnings) != 1 || !strings.Contains(ex[0].Warnings[0], "Content-Security-Policy") {
		t.Fatalf("the relaxation must be declared, got %+v", ex)
	}
}

func TestHopRefusesWithoutABackend(t *testing.T) {
	h := NewHandler(NewStore(0), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://web.test/", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestReservedPathServesAgentAndIngestsReports(t *testing.T) {
	h, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("reserved path must never reach the backend")
	})
	_ = do

	// The agent is served from the page's own origin, so no CORS is involved.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://web.test"+ReservedPath+h.agentPath, nil))
	if w.Code != 200 || w.Body.String() != "// agent" {
		t.Fatalf("agent: %d %q", w.Code, w.Body.String())
	}

	store.Add(&Exchange{ID: "deadbeef", At: time.Now(), Host: "web.test"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://web.test/.proximo/ingest?x=deadbeef", strings.NewReader(sample)))
	if w.Code != 200 {
		t.Fatalf("ingest: %d %s", w.Code, w.Body.String())
	}

	ex := store.List(Query{})
	if len(ex[0].Reports) != 1 || ex[0].Reports[0].Type != "TypeError" {
		t.Fatalf("report not joined to its Exchange: %+v", ex[0])
	}
}

// TestAdminHandlerIsSeparate pins the reason AdminHandler exists: the read API
// must not be reachable from an inspected page.
func TestAdminHandlerIsSeparate(t *testing.T) {
	h, store, _ := hop(t, func(w http.ResponseWriter, r *http.Request) {})
	store.Add(&Exchange{ID: "abc", At: time.Now(), Host: "web.test"})

	for _, path := range []string{"/.proximo/exchanges", "/.proximo/dom?x=abc"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://web.test"+path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s is reachable from the page (status %d)", path, w.Code)
		}
	}

	w := httptest.NewRecorder()
	AdminHandler{Store: store}.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/exchanges?all=1", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"abc"`) {
		t.Fatalf("admin API: %d %s", w.Code, w.Body.String())
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// TestHopInjectsIntoAGzippingBackend covers the "works on any stack" case: a
// backend that compresses whatever it was asked for still gets the agent, because
// the transport unwraps the gzip it negotiated itself.
func TestHopInjectsIntoAGzippingBackend(t *testing.T) {
	h, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		fmt.Fprint(gz, "<html><head></head><body>ok</body></html>")
	})
	body := readAll(t, do("/"))
	if !strings.Contains(body, h.agentPath) {
		t.Fatalf("agent not injected into a gzipped page: %q", body)
	}
	if ex := store.List(Query{}); len(ex[0].Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", ex[0].Warnings)
	}
}

// TestReconcileCSPConnectSrc: a policy can admit the script and still forbid the
// report POST. The tunnel is same-origin, so 'self' is what connect-src needs —
// a nonce means nothing there.
func TestReconcileCSPConnectSrc(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Security-Policy", "script-src 'self'; connect-src https://api.example")
	nonce, warning := reconcileCSP(h)

	if nonce != "" {
		t.Errorf("script-src 'self' already admits the tag; no nonce needed, got %q", nonce)
	}
	got := h.Get("Content-Security-Policy")
	if !strings.Contains(got, "connect-src https://api.example 'self'") {
		t.Errorf("connect-src not widened: %q", got)
	}
	if !strings.Contains(warning, "connect-src") {
		t.Errorf("the relaxation must be named, got %q", warning)
	}

	// A policy that admits both is left completely alone.
	h = http.Header{}
	h.Set("Content-Security-Policy", "default-src 'self'")
	if nonce, warning = reconcileCSP(h); nonce != "" || warning != "" {
		t.Errorf("nothing to do, got nonce=%q warning=%q", nonce, warning)
	}
}

// TestAgentIsCacheableAndCompressed pins the two properties that keep the ~87 KB
// bundle from being re-fetched on every page load: the URL carries a digest of
// the content, so it can be immutable, and gzip is offered to anyone who asks.
func TestAgentIsCacheableAndCompressed(t *testing.T) {
	h, _, _ := hop(t, func(w http.ResponseWriter, r *http.Request) {})

	if !strings.HasPrefix(h.agentPath, "agent.") || !strings.HasSuffix(h.agentPath, ".js") {
		t.Fatalf("agent path = %q, want agent.<digest>.js", h.agentPath)
	}

	req := httptest.NewRequest(http.MethodGet, "http://web.test"+ReservedPath+h.agentPath, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("gzip not served to a client that asked: %q", w.Header().Get("Content-Encoding"))
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable — the URL is content-addressed", cc)
	}

	// A second load costs nothing.
	req = httptest.NewRequest(http.MethodGet, "http://web.test"+ReservedPath+h.agentPath, nil)
	req.Header.Set("If-None-Match", w.Header().Get("ETag"))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotModified {
		t.Errorf("revalidation = %d, want 304", w.Code)
	}
}

// TestRouteWarningsOutliveEviction: a relaxed security policy is a property of
// the route, not of one request, so `proximo status` must still see it after the
// Exchange that discovered it has been evicted.
func TestRouteWarningsOutliveEviction(t *testing.T) {
	h, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		fmt.Fprint(w, "<html><head></head><body></body></html>")
	})
	_ = h
	do("/")

	warnings := store.RouteWarnings()
	if len(warnings["web.test"]) != 1 || !strings.Contains(warnings["web.test"][0], "Content-Security-Policy") {
		t.Fatalf("route warning not recorded: %+v", warnings)
	}

	// Push the Exchange out of the buffer; the route warning must survive.
	for range 200 {
		e := &Exchange{ID: NewID(), At: time.Now(), Host: "web.test"}
		store.Add(e)
		store.Attach(e.ID, ingested{Report: Report{Message: "x"}, Snapshot: make([]byte, 4096)})
	}
	if len(store.RouteWarnings()["web.test"]) != 1 {
		t.Error("the warning was evicted with its Exchange")
	}
	// And it is never recorded twice.
	do("/")
	if n := len(store.RouteWarnings()["web.test"]); n != 1 {
		t.Errorf("warning recorded %d times, want 1", n)
	}
}

// TestListShowsProblemsFirst covers the failure that made Inspection look broken
// when it was not: a page that threw was served before a run of clean requests,
// so ordering and limiting by the Exchange alone pushed the only interesting one
// out of the list entirely.
func TestListShowsProblemsFirst(t *testing.T) {
	s := NewStore(0)
	base := time.Now().Add(-10 * time.Minute)

	broken := &Exchange{ID: "broken", At: base, Host: "web.test", Status: 200}
	s.Add(broken)
	s.Attach("broken", ingested{Report: Report{Message: "boom", At: time.Now()}})
	for i := range 30 {
		s.Add(&Exchange{ID: "ok" + strconv.Itoa(i), At: base.Add(time.Duration(i+1) * time.Second), Host: "web.test", Status: 200})
	}

	got := s.List(Query{Host: "web.test", Limit: 20, OnlyProblems: true})
	if len(got) != 1 || got[0].ID != "broken" {
		t.Fatalf("the only Exchange worth showing must survive the limit, got %d: %+v", len(got), got)
	}

	// A report that arrived just now keeps its Exchange in a narrow window, even
	// though the page itself was served well outside it.
	if got = s.List(Query{Since: time.Minute, OnlyProblems: true}); len(got) != 1 {
		t.Fatalf("--since must follow the report, not only the page load: %+v", got)
	}

	// --all still shows everything, newest first.
	if got = s.List(Query{Host: "web.test"}); len(got) != 31 {
		t.Fatalf("without OnlyProblems every Exchange is listed, got %d", len(got))
	}

	// A failing status is interesting on its own, with no client report.
	s.Add(&Exchange{ID: "500", At: time.Now(), Host: "api.test", Status: 500})
	if got = s.List(Query{Host: "api.test", OnlyProblems: true}); len(got) != 1 {
		t.Fatalf("a failing status must show without a client report: %+v", got)
	}

	// So is a warning proximo raised about the route.
	warned := &Exchange{ID: "warned", At: time.Now(), Host: "csp.test", Status: 200, Warnings: []string{"relaxed something"}}
	s.Add(warned)
	if got = s.List(Query{Host: "csp.test", OnlyProblems: true}); len(got) != 1 {
		t.Fatalf("a warning must show on its own: %+v", got)
	}
}
