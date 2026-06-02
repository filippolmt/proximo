package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubHub is an in-memory Beszel hub: it implements the endpoints the bootstrap
// calls and records how many times the registration token was read, so
// idempotent reuse can be asserted.
type stubHub struct {
	authToken   string
	wantEmail   string
	wantPass    string
	publicKey   string
	uniToken    string
	authCalls   int
	tokenReads  int
	tokenEnable int
}

func (s *stubHub) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/collections/users/auth-with-password", func(w http.ResponseWriter, r *http.Request) {
		s.authCalls++
		var body struct{ Identity, Password string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode auth body: %v", err)
		}
		if body.Identity != s.wantEmail || body.Password != s.wantPass {
			http.Error(w, "bad creds", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]string{"token": s.authToken})
	})
	mux.HandleFunc("/api/beszel/getkey", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != s.authToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]string{"key": s.publicKey})
	})
	mux.HandleFunc("/api/beszel/universal-token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != s.authToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("enable") == "1" {
			s.tokenEnable++
			writeJSON(w, map[string]any{"token": s.uniToken, "active": true})
			return
		}
		s.tokenReads++
		writeJSON(w, map[string]any{"token": s.uniToken, "active": false})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newTestClient(base string) *HubClient {
	c := NewHubClient(base)
	c.ReadyTimeout = 2 * time.Second
	c.PollInterval = 10 * time.Millisecond
	return c
}

// TestHubClientEndpoints drives the production HubClient against the stub,
// covering auth (users collection), key retrieval, and the universal-token
// read-then-enable dance.
func TestHubClientEndpoints(t *testing.T) {
	hub := &stubHub{
		authToken: "auth-jwt",
		wantEmail: "proximo@test",
		wantPass:  "s3cret",
		publicKey: "ssh-ed25519 AAAAkey",
		uniToken:  "universal-123",
	}
	srv := httptest.NewServer(hub.handler(t))
	defer srv.Close()

	c := newTestClient(srv.URL)
	ctx := context.Background()

	if err := c.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	tok, err := c.Authenticate(ctx, "proximo@test", "s3cret")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if tok != "auth-jwt" {
		t.Errorf("auth token = %q, want auth-jwt", tok)
	}
	key, err := c.PublicKey(ctx, tok)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if key != "ssh-ed25519 AAAAkey" {
		t.Errorf("public key = %q", key)
	}
	ut, err := c.UniversalToken(ctx, tok)
	if err != nil {
		t.Fatalf("UniversalToken: %v", err)
	}
	if ut != "universal-123" {
		t.Errorf("universal token = %q", ut)
	}
	if hub.tokenReads != 1 || hub.tokenEnable != 1 {
		t.Errorf("universal-token reads=%d enable=%d, want 1/1", hub.tokenReads, hub.tokenEnable)
	}
}

// TestBootstrapWritesAgentEnvIdempotent runs the full bootstrap against the stub
// twice and asserts the agent env file is written with the retrieved key/token
// and that a repeat run reuses the same registration (no duplicate, same output).
func TestBootstrapWritesAgentEnvIdempotent(t *testing.T) {
	hub := &stubHub{
		authToken: "auth-jwt",
		wantEmail: "proximo@test",
		wantPass:  "s3cret",
		publicKey: "ssh-ed25519 AAAAkey",
		uniToken:  "universal-123",
	}
	srv := httptest.NewServer(hub.handler(t))
	defer srv.Close()

	c := newTestClient(srv.URL)
	stackDir := t.TempDir()
	hubURL := "http://beszel:8090"

	run := func() string {
		if err := Bootstrap(context.Background(), c, stackDir, hubURL, "proximo@test", "s3cret"); err != nil {
			t.Fatalf("Bootstrap: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(stackDir, agentEnvFile))
		if err != nil {
			t.Fatalf("read agent env: %v", err)
		}
		return string(data)
	}

	got := run()
	for _, want := range []string{"HUB_URL=http://beszel:8090", "KEY=ssh-ed25519 AAAAkey", "TOKEN=universal-123"} {
		if !strings.Contains(got, want) {
			t.Errorf("agent env missing %q in:\n%s", want, got)
		}
	}

	again := run()
	if again != got {
		t.Errorf("idempotent bootstrap diverged:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
	if hub.authCalls != 2 {
		t.Errorf("auth calls = %d, want 2 (one per run, reusing the same user)", hub.authCalls)
	}
}

// TestWaitReadyTimesOut covers the readiness poll giving up when the hub never
// answers, so a stuck hub surfaces an error instead of hanging the one-shot up.
func TestWaitReadyTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "starting", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewHubClient(srv.URL)
	c.ReadyTimeout = 50 * time.Millisecond
	c.PollInterval = 10 * time.Millisecond
	if err := c.WaitReady(context.Background()); err == nil {
		t.Fatal("WaitReady should error when the hub never becomes healthy")
	}
}
