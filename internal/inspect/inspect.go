package inspect

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// ReservedPath is the prefix proximo answers for on a project's own origin,
	// so an inspected page can report same-origin. It is unavailable to the
	// project for as long as the route carries the label.
	ReservedPath = "/.proximo/"

	// BackendHeader carries the address of the container this request is really
	// for. Traefik always overwrites it, so it cannot be forged from outside.
	BackendHeader = "X-Proximo-Backend"
)

// headClose matches the insertion point for the agent tag in any casing and with
// stray whitespace, because Inspection has to work on whatever a project serves.
var headClose = regexp.MustCompile(`(?i)</head\s*>`)

// Handler is the hop itself: it proxies an inspected route to its backend,
// injects the reporting agent into HTML on the way back, and serves the reserved
// path. It is reachable from the browser, so it exposes nothing but the agent
// and the ingest endpoint — reading Exchanges back is AdminHandler's job, on a
// loopback-only listener.
type Handler struct {
	store *Store
	proxy *httputil.ReverseProxy

	// The agent is fixed for the life of the binary, so it is addressed by a
	// digest of its own content and served immutable: a page pays for it once per
	// proximo version instead of on every load. Both encodings are prepared up
	// front — it is ~87 KB raw and ~30 KB gzipped, and compressing it per request
	// would be work repeated for no reason.
	agent     []byte
	agentGzip []byte
	agentPath string // "agent.<digest>.js"
	agentETag string
}

// NewHandler returns a hop recording into store and serving agent as the
// injected script.
func NewHandler(store *Store, agent []byte) *Handler {
	sum := sha256.Sum256(agent)
	digest := hex.EncodeToString(sum[:8])

	var gz bytes.Buffer
	if w, err := gzip.NewWriterLevel(&gz, gzip.BestCompression); err == nil {
		if _, err := w.Write(agent); err == nil && w.Close() == nil {
			// only trusted when the whole round-trip succeeded
		} else {
			gz.Reset()
		}
	}

	h := &Handler{
		store:     store,
		agent:     agent,
		agentGzip: gz.Bytes(),
		agentPath: "agent." + digest + ".js",
		agentETag: `"` + digest + `"`,
	}
	h.proxy = &httputil.ReverseProxy{
		Rewrite:        h.rewrite,
		ModifyResponse: h.modifyResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proximo inspect: %s %s: %v", r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return h
}

// state is the per-request scratch space shared between rewrite, modifyResponse
// and ServeHTTP: the correlation id is minted before the request goes out and
// the response is what fills in the rest.
type state struct {
	id       string
	host     string
	backend  *url.URL
	status   int
	warnings []string
}

type stateKey struct{}

func stateOf(ctx context.Context) *state {
	st, _ := ctx.Value(stateKey{}).(*state)
	return st
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, ReservedPath) {
		h.serveReserved(w, r)
		return
	}

	backend, err := url.Parse(r.Header.Get(BackendHeader))
	if err != nil || backend.Host == "" {
		http.Error(w, "proximo inspect: no backend for this route", http.StatusBadGateway)
		return
	}

	st := &state{id: NewID(), host: r.Host, backend: backend}
	cw := &countingWriter{ResponseWriter: w}
	start := time.Now()
	h.proxy.ServeHTTP(cw, r.WithContext(context.WithValue(r.Context(), stateKey{}, st)))

	h.store.Add(&Exchange{
		ID:       st.id,
		At:       start,
		Host:     r.Host,
		Method:   r.Method,
		Path:     r.URL.Path,
		Status:   st.status,
		Duration: time.Since(start),
		Bytes:    cw.n,
		Warnings: st.warnings,
	})
}

func (h *Handler) rewrite(pr *httputil.ProxyRequest) {
	st := stateOf(pr.In.Context())
	pr.Out.URL.Scheme = st.backend.Scheme
	pr.Out.URL.Host = st.backend.Host
	// The backend routes on the name the browser asked for, not on ours.
	pr.Out.Host = pr.In.Host
	pr.Out.Header.Del(BackendHeader)
	// Drop the browser's encoding list. Go's transport then advertises gzip on
	// its own and decompresses the response transparently, so injection always
	// sees clear text — including from a backend that compresses regardless.
	// Passing the browser's list through would invite br or zstd, which the
	// transport does not decode.
	pr.Out.Header.Del("Accept-Encoding")
	pr.SetXForwarded()
}

func (h *Handler) modifyResponse(resp *http.Response) error {
	st := stateOf(resp.Request.Context())
	st.status = resp.StatusCode

	if !isHTML(resp) || resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" && enc != "identity" {
		// Only gzip is advertised upstream and the transport unwraps it, so any
		// encoding still set here is one we cannot decode. Leave the bytes alone
		// and say why.
		st.warnings = append(st.warnings, "agent not injected: backend forced Content-Encoding "+enc)
		return nil
	}

	// ponytail: the whole page is buffered to inject into it. Bounded in practice
	// — this only ever runs on HTML, on a local dev route the developer opted in.
	// Streaming injection is the upgrade if a project ever serves a huge document.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()

	nonce, warning := reconcileCSP(resp.Header)
	if warning != "" {
		st.warnings = append(st.warnings, warning)
		// Also recorded against the host, where eviction cannot reach it: a
		// relaxed security policy has to stay visible in `proximo status` for as
		// long as the route is inspected.
		h.store.NoteRouteWarning(st.host, warning)
	}

	if loc := headClose.FindIndex(body); loc != nil {
		tag := agentTag(h.agentPath, st.id, nonce)
		body = append(body[:loc[0]:loc[0]], append(tag, body[loc[0]:]...)...)
	} else {
		st.warnings = append(st.warnings, "agent not injected: no </head> in the response")
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}

func agentTag(agentPath, id, nonce string) []byte {
	attr := ""
	if nonce != "" {
		attr = ` nonce="` + html.EscapeString(nonce) + `"`
	}
	return fmt.Appendf(nil, `<script src=%q data-proximo-exchange=%q%s></script>`,
		ReservedPath+agentPath, id, attr)
}

func isHTML(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html")
}

// countingWriter records how many bytes reached the browser, which is the one
// part of an Access record the response headers cannot be trusted for.
type countingWriter struct {
	http.ResponseWriter
	n int64
}

func (w *countingWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.n += int64(n)
	return n, err
}

// serveReserved answers the reserved path. Only two endpoints live here: the
// agent, and where it reports to.
func (h *Handler) serveReserved(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, ReservedPath) {
	case h.agentPath:
		h.serveAgent(w, r)
	case "ingest":
		h.ingest(w, r)
	default:
		http.NotFound(w, r)
	}
}

// serveAgent hands over the injected script. Its URL carries a digest of its own
// content, so it can be cached hard: a new proximo version changes the URL.
func (h *Handler) serveAgent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", h.agentETag)
	if r.Header.Get("If-None-Match") == h.agentETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := h.agent
	if len(h.agentGzip) > 0 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		body = h.agentGzip
	}
	if _, err := w.Write(body); err != nil {
		log.Printf("proximo inspect: serving the agent: %v", err)
	}
}

func (h *Handler) ingest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("x")
	if id == "" {
		http.Error(w, "missing exchange id", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReportBody))
	if err != nil {
		http.Error(w, "envelope too large", http.StatusRequestEntityTooLarge)
		return
	}
	in, err := decode(body)
	if err != nil {
		log.Printf("proximo inspect: envelope for %s: %v", id, err)
		http.Error(w, "malformed envelope", http.StatusBadRequest)
		return
	}
	if in.Found {
		h.store.Attach(id, in)
	}
	// The agent must never be told anything useful about the store, and never
	// has to retry: a dropped report is not worth a failed page.
	w.WriteHeader(http.StatusOK)
}

// AdminHandler is the read side of the hop. It is deliberately a separate
// handler so it can be bound to a loopback-only listener: served on the request
// path it would let any inspected page read every Exchange recorded for its host.
type AdminHandler struct{ Store *Store }

func (a AdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch r.URL.Path {
	case "/exchanges":
		since, _ := time.ParseDuration(q.Get("since"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		writeJSON(w, a.Store.List(Query{
			Host: q.Get("host"), Since: since, Limit: limit,
			OnlyProblems: q.Get("all") != "1",
		}))
	case "/warnings":
		writeJSON(w, a.Store.RouteWarnings())
	case "/info":
		writeJSON(w, map[string]any{"started": a.Store.Started()})
	case "/dom":
		snap := a.Store.Snapshot(q.Get("x"))
		if snap == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(snap); err != nil {
			log.Printf("proximo inspect: serving a snapshot: %v", err)
		}
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("proximo inspect: encoding a response: %v", err)
	}
}
