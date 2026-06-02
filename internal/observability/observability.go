// Package observability bootstraps the opt-in dev-time observability stack
// (Dozzle logs + Beszel metrics hub/agent). It owns the runtime-generated hub
// password — generated with crypto/rand on first use and persisted in the
// per-user config dir at 0600, mirroring the local CA private key in
// internal/tls — and the env files that inject the hub/agent configuration into
// the compose services. The proximo binary embeds no credential.
package observability

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/filippolmt/proximo/internal/config"
)

const (
	// secretFile holds the generated Beszel hub password under the per-user
	// config dir (0600), reused idempotently across runs.
	secretFile = "observability-secret"

	// hubEnvFile injects the generated password into the Beszel hub service.
	// The non-secret email/auto-login are set via the compose __OBS_USER_EMAIL__
	// sentinel, so only the secret lives in this 0600 file.
	hubEnvFile = "beszel-hub.env"

	// agentEnvFile injects HUB_URL/KEY/TOKEN into the Beszel agent service after
	// the hub bootstrap retrieves them.
	agentEnvFile = "beszel-agent.env"

	// HubService is the in-stack hostname the agent reaches the hub on.
	HubService = "beszel"
	// HubPort is the port the Beszel hub listens on inside the stack.
	HubPort = 8090
)

// HubURL is the in-stack URL the agent uses to reach the hub.
func HubURL() string {
	return fmt.Sprintf("http://%s:%d", HubService, HubPort)
}

// Email returns the fixed, non-secret hub user email for a TLD. It is
// deterministic so it can also be substituted into the compose file as a
// sentinel without persisting anything. The domain is dotted
// (proximo.<tld>) on purpose: Beszel/PocketBase validates USER_EMAIL with a
// format check that rejects a bare single-label domain, so "proximo@test"
// fails its first-run migration and the hub crash-loops — "proximo@proximo.test"
// passes.
func Email(tld string) string {
	return "proximo@proximo." + tld
}

// SecretPath returns the per-user path of the stored hub password.
func SecretPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, secretFile), nil
}

// EnsureSecret returns the stored hub password, generating and persisting a new
// one (crypto/rand, 0600) on first use. It is idempotent: a stored secret is
// reused rather than regenerated.
func EnsureSecret() (string, error) {
	path, err := SecretPath()
	if err != nil {
		return "", err
	}
	if data, err := os.ReadFile(path); err == nil {
		if pw := string(data); pw != "" {
			return pw, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	pw, err := generatePassword()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(pw), 0o600); err != nil {
		return "", err
	}
	return pw, nil
}

// generatePassword returns a URL-safe random password with ~192 bits of entropy.
func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// WriteHubEnv writes the hub's generated password into stackDir/beszel-hub.env
// at 0600. The hub reads USER_PASSWORD from it to seed the first user.
func WriteHubEnv(stackDir, password string) error {
	content := fmt.Sprintf("USER_PASSWORD=%s\n", password)
	return os.WriteFile(filepath.Join(stackDir, hubEnvFile), []byte(content), 0o600)
}

// WriteAgentEnv writes the hub URL, public key, and registration token into
// stackDir/beszel-agent.env at 0600, consumed by the beszel-agent service.
func WriteAgentEnv(stackDir, hubURL, key, token string) error {
	content := fmt.Sprintf("HUB_URL=%s\nKEY=%s\nTOKEN=%s\n", hubURL, key, token)
	return os.WriteFile(filepath.Join(stackDir, agentEnvFile), []byte(content), 0o600)
}

// Purge removes the stored secret and the generated env files. Missing files are
// ignored so teardown is idempotent. stackDir may be empty when the stack was
// never materialized.
func Purge(stackDir string) error {
	secret, err := SecretPath()
	if err != nil {
		return err
	}
	paths := []string{secret}
	if stackDir != "" {
		paths = append(paths,
			filepath.Join(stackDir, hubEnvFile),
			filepath.Join(stackDir, agentEnvFile),
		)
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
