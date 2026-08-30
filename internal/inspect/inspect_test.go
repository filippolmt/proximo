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
		s.Attach(e.ID, Report{Message: "boom"}, make([]byte, 1024))
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
	if s.Attach("nope", Report{Message: "x"}, nil) {
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

// --- envelope ---------------------------------------------------------------

// envelope builds a Sentry envelope the way the browser SDK's tunnel would.
func envelope(items ...[2]string) string {
	var b strings.Builder
	b.WriteString(`{"event_id":"7b1c","sent_at":"2026-08-30T10:00:00Z"}` + "\n")
	for _, it := range items {
		fmt.Fprintf(&b, "%s\n%s\n", strings.Replace(it[0], "$LEN", strconv.Itoa(len(it[1])), 1), it[1])
	}
	return b.String()
}

const eventPayload = `{
  "timestamp": 1756548000.5,
  "level": "error",
  "exception": {"values": [{
    "type": "TypeError",
    "value": "Cannot read properties of undefined (reading 'total')",
    "stacktrace": {"frames": [
      {"filename": "src/checkout/Page.tsx", "function": "onMount", "lineno": 12, "colno": 5},
      {"filename": "src/checkout/Summary.tsx", "function": "renderSummary", "lineno": 47, "colno": 18}
    ]}
  }]},
  "breadcrumbs": {"values": [
    {"timestamp": 1756547999, "category": "console", "level": "warning", "message": "slow render"},
    {"timestamp": 1756548000, "category": "fetch", "message": "GET /api/cart"}
  ]}
}`

func TestDecodeEventAndSnapshot(t *testing.T) {
	dom := "<html><body>state at the time</body></html>"
	raw := envelope(
		[2]string{`{"type":"event","length":$LEN}`, eventPayload},
		[2]string{`{"type":"attachment","length":$LEN,"filename":"dom.html"}`, dom},
	)

	r, snap, ok, err := decode([]byte(raw))
	if err != nil || !ok {
		t.Fatalf("decode: ok=%v err=%v", ok, err)
	}
	if string(snap) != dom {
		t.Errorf("snapshot = %q", snap)
	}
	if r.Type != "TypeError" || !strings.Contains(r.Message, "reading 'total'") {
		t.Errorf("report = %+v", r)
	}
	// The innermost frame is what actually failed, so it must come first.
	if len(r.Frames) != 2 || r.Frames[0].Func != "renderSummary" || r.Frames[0].Line != 47 {
		t.Errorf("frames = %+v", r.Frames)
	}
	if len(r.Breadcrumbs) != 2 || r.Breadcrumbs[1].Category != "fetch" {
		t.Errorf("breadcrumbs = %+v", r.Breadcrumbs)
	}
	if r.At.IsZero() {
		t.Error("epoch-seconds timestamp not parsed")
	}
	if lvl := r.Breadcrumbs[1].Level; lvl != "info" {
		t.Errorf("missing breadcrumb level should default to info, got %q", lvl)
	}
}

// TestDecodeDropsAgentFrames: the agent is served from the page's own origin, so
// Sentry does not filter its own frames the way it would for a CDN-hosted SDK.
// A stack ending in /.proximo/agent.js points the reader at proximo instead of at
// their bug, so those frames never reach a Client report.
func TestDecodeDropsAgentFrames(t *testing.T) {
	payload := `{"exception":{"values":[{"type":"TypeError","value":"boom","stacktrace":{"frames":[
		{"filename":"https://web.test/.proximo/agent.js","function":"i","lineno":16,"colno":7079},
		{"filename":"src/app.tsx","function":"render","lineno":9,"colno":3}
	]}}]}}`
	r, _, ok, err := decode([]byte(envelope([2]string{`{"type":"event","length":$LEN}`, payload})))
	if err != nil || !ok {
		t.Fatalf("decode: ok=%v err=%v", ok, err)
	}
	if len(r.Frames) != 1 || r.Frames[0].File != "src/app.tsx" {
		t.Fatalf("agent frames must be dropped, got %+v", r.Frames)
	}
}

func TestDecodeRFC3339TimestampAndPayloadWithNewlines(t *testing.T) {
	// A length-prefixed payload may contain newlines — that is why the header
	// carries a length at all.
	payload := "{\n\"timestamp\": \"2026-08-30T10:00:00Z\",\n\"message\": \"plain\"\n}"
	r, _, ok, err := decode([]byte(envelope([2]string{`{"type":"event","length":$LEN}`, payload})))
	if err != nil || !ok {
		t.Fatalf("decode: ok=%v err=%v", ok, err)
	}
	if r.Message != "plain" || r.At.Year() != 2026 {
		t.Fatalf("report = %+v", r)
	}
}

func TestDecodeIgnoresEnvelopeWithoutEvent(t *testing.T) {
	_, _, ok, err := decode([]byte(envelope([2]string{`{"type":"session","length":$LEN}`, `{"sid":"1"}`})))
	if err != nil || ok {
		t.Fatalf("a session-only envelope must be dropped quietly: ok=%v err=%v", ok, err)
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
	_, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
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
	if !strings.Contains(body, `<script src="/.proximo/agent.js"`) {
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
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://web.test/.proximo/agent.js", nil))
	if w.Code != 200 || w.Body.String() != "// agent" {
		t.Fatalf("agent.js: %d %q", w.Code, w.Body.String())
	}

	store.Add(&Exchange{ID: "deadbeef", At: time.Now(), Host: "web.test"})
	raw := envelope([2]string{`{"type":"event","length":$LEN}`, eventPayload})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://web.test/.proximo/ingest?x=deadbeef", strings.NewReader(raw)))
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
	AdminHandler{Store: store}.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/exchanges", nil))
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
	_, store, do := hop(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		fmt.Fprint(gz, "<html><head></head><body>ok</body></html>")
	})
	body := readAll(t, do("/"))
	if !strings.Contains(body, "/.proximo/agent.js") {
		t.Fatalf("agent not injected into a gzipped page: %q", body)
	}
	if ex := store.List(Query{}); len(ex[0].Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", ex[0].Warnings)
	}
}
