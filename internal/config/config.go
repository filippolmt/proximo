// Package config manages proximo's persisted user configuration and the
// on-disk paths it uses (TLS material, the embedded stack, etc.).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// DefaultTLD is the default top-level domain used for local routing.
	// ".test" is reserved by RFC 6761 and never collides with mDNS (".local").
	DefaultTLD = "test"

	// DNSPort is the loopback UDP host port the DNS server is published on. A
	// high port avoids any privileged bind to :53; 5353 is intentionally
	// avoided because macOS mDNSResponder (Bonjour) already binds it.
	DNSPort = 5354

	// ObsHubPort is the loopback TCP host port the metrics hub is published on
	// (only when the observability profile is active) so the one-shot bootstrap
	// can reach the hub API directly, without depending on DNS/Traefik/TLS being
	// converged yet. A high, uncommon port avoids colliding with the hub's
	// default 8090.
	ObsHubPort = 48090

	// appDir is the per-user directory name; the state home is $HOME/.proximo
	// (a leading dot is prepended in HomePath).
	appDir = "proximo"
)

// Config holds the user-configurable settings persisted to disk.
type Config struct {
	// TLD is the top-level domain routed to the local proximo (without a dot).
	TLD string `json:"tld"`
}

// Default returns a Config populated with default values.
func Default() Config {
	return Config{TLD: DefaultTLD}
}

var tldPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// NormalizeTLD validates and normalizes a user-supplied TLD: it strips a leading
// dot, lowercases, enforces a single DNS label, and rejects reserved values.
func NormalizeTLD(raw string) (string, error) {
	tld := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "."))
	if !tldPattern.MatchString(tld) {
		return "", fmt.Errorf("invalid TLD %q: use a single DNS label of [a-z0-9-]", raw)
	}
	if tld == "local" {
		return "", fmt.Errorf(".local is reserved for mDNS and is not supported")
	}
	return tld, nil
}

// Dir returns (creating if needed) the per-user state home at $HOME/.proximo.
func Dir() (string, error) {
	dir, err := HomePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// HomePath resolves the state-home path ($HOME/.proximo) without creating it,
// so callers that only need the location (RemoveHome, `config ca-path`) don't
// trigger side effects.
func HomePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+appDir), nil
}

// SubDir returns (creating if needed) a named subdirectory under Dir.
func SubDir(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	sub := filepath.Join(dir, name)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", err
	}
	return sub, nil
}

// DataDir returns (creating if needed) the directory holding the stack's
// bind-mounted runtime data (Traefik routes/certs, Beszel metrics). It is the
// host side of the data bind mounts the materialized compose declares.
func DataDir() (string, error) {
	return SubDir("data")
}

// RemoveHome deletes the entire ~/.proximo state home (CA, secret, config,
// materialized stack, and bind-mounted data). uninstall calls it after the
// stack is down and host trust/resolver are reversed, for a full reversal. It
// resolves the path without creating it.
func RemoveHome() error {
	dir, err := HomePath()
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func filePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the persisted config, returning defaults when none exists yet.
func Load() (Config, error) {
	p, err := filePath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.TLD == "" {
		cfg.TLD = DefaultTLD
	}
	return cfg, nil
}

// Save persists the config to disk.
func (c Config) Save() error {
	p, err := filePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
