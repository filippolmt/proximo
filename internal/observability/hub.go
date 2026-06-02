package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// HubAPI is the subset of Beszel hub operations the bootstrap needs, behind an
// interface so the sequencing can be unit-tested against a stub without a live
// hub. The production implementation is *HubClient.
type HubAPI interface {
	// WaitReady blocks until the hub answers as healthy or ctx is done.
	WaitReady(ctx context.Context) error
	// Authenticate logs in the seeded local user and returns an auth token.
	Authenticate(ctx context.Context, email, password string) (token string, err error)
	// PublicKey returns the hub's public SSH key the agent verifies against.
	PublicKey(ctx context.Context, authToken string) (string, error)
	// UniversalToken returns an enabled universal registration token the agent
	// self-registers with (idempotent: reuses the existing token).
	UniversalToken(ctx context.Context, authToken string) (string, error)
}

// Bootstrap registers the metrics agent with the hub with no manual step: it
// waits for the hub, authenticates the seeded user, retrieves the hub public key
// and a universal registration token, and writes the agent env file consumed by
// the beszel-agent service. It is idempotent — on repeat runs the user and
// registration already exist, so it re-authenticates and re-fetches and the
// agent re-registers by fingerprint without creating a duplicate system.
func Bootstrap(ctx context.Context, api HubAPI, stackDir, hubURL, email, password string) error {
	if err := api.WaitReady(ctx); err != nil {
		return fmt.Errorf("metrics hub did not become ready: %w", err)
	}
	authToken, err := api.Authenticate(ctx, email, password)
	if err != nil {
		return fmt.Errorf("authenticate with metrics hub: %w", err)
	}
	key, err := api.PublicKey(ctx, authToken)
	if err != nil {
		return fmt.Errorf("retrieve metrics hub public key: %w", err)
	}
	token, err := api.UniversalToken(ctx, authToken)
	if err != nil {
		return fmt.Errorf("retrieve metrics hub registration token: %w", err)
	}
	if err := WriteAgentEnv(stackDir, hubURL, key, token); err != nil {
		return fmt.Errorf("write agent env: %w", err)
	}
	return nil
}

// HubClient talks to the Beszel hub REST API (PocketBase + Beszel routes). The
// exact env/endpoints are upstream behaviors pinned to the henrygd/beszel image
// tag in docker-compose.yml; verify them on each image bump (see design risks).
type HubClient struct {
	// BaseURL is the hub origin reachable from the host, e.g. http://127.0.0.1:443
	// fronted by Traefik, or a direct published port.
	BaseURL string
	HTTP    *http.Client
	// ReadyTimeout caps the readiness poll; PollInterval spaces the attempts.
	ReadyTimeout time.Duration
	PollInterval time.Duration
}

// NewHubClient returns a HubClient with sane defaults for the in-process
// bootstrap that runs inside the one-shot `up`.
func NewHubClient(baseURL string) *HubClient {
	return &HubClient{
		BaseURL:      baseURL,
		HTTP:         &http.Client{Timeout: 10 * time.Second},
		ReadyTimeout: 90 * time.Second,
		PollInterval: time.Second,
	}
}

// WaitReady polls the PocketBase health endpoint until it answers 200.
func (c *HubClient) WaitReady(ctx context.Context) error {
	deadline := time.Now().Add(c.ReadyTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/health", nil)
		if err != nil {
			return err
		}
		resp, err := c.HTTP.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("hub not healthy after %s", c.ReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.PollInterval):
		}
	}
}

// Authenticate logs the seeded local user in via the PocketBase `users`
// collection. Per the Beszel maintainer the universal-token / getkey routes
// require a `users` token, not a `_superusers` one.
func (c *HubClient) Authenticate(ctx context.Context, email, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"identity": email, "password": password})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/api/collections/users/auth-with-password", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("hub returned an empty auth token")
	}
	return out.Token, nil
}

// PublicKey fetches the hub public SSH key.
func (c *HubClient) PublicKey(ctx context.Context, authToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/beszel/getkey", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", authToken)
	var out struct {
		Key string `json:"key"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.Key == "" {
		return "", fmt.Errorf("hub returned an empty public key")
	}
	return out.Key, nil
}

// UniversalToken returns an enabled universal registration token. It reads the
// current token, then enables it (idempotent — re-enabling the same token is a
// no-op), mirroring the documented read-then-enable dance.
func (c *HubClient) UniversalToken(ctx context.Context, authToken string) (string, error) {
	current, err := c.universalToken(ctx, authToken, nil)
	if err != nil {
		return "", err
	}
	token, err := c.universalToken(ctx, authToken, url.Values{
		"token":  {current},
		"enable": {"1"},
	})
	if err != nil {
		return "", err
	}
	if token == "" {
		token = current
	}
	if token == "" {
		return "", fmt.Errorf("hub returned an empty universal token")
	}
	return token, nil
}

func (c *HubClient) universalToken(ctx context.Context, authToken string, q url.Values) (string, error) {
	u := c.BaseURL + "/api/beszel/universal-token"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", authToken)
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// do executes req and decodes a JSON response into v, treating non-2xx as an
// error with the response body for context.
func (c *HubClient) do(req *http.Request, v any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hub %s %s: %s: %s", req.Method, req.URL.Path, resp.Status, string(data))
	}
	if v == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}
