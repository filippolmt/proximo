// Package config manages proximo's persisted user configuration and the
// on-disk paths it uses (TLS material, the embedded stack, etc.).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

	// InspectAPIPort is the loopback TCP host port the Inspection hop publishes
	// its read API on, so `proximo errors` can reach it. The proxy side of the
	// hop is never published: only Traefik talks to it, over the stack network.
	InspectAPIPort = 48091

	// WatcherAPIPort is the loopback TCP host port the watcher publishes its
	// Incident read API on, so `proximo errors` can ask what the runtime declared
	// about a container. The watcher has no other listener: it is the one stack
	// service holding both the Docker socket and the event subscription, and this
	// is the only way anything on the host reads what it observed.
	WatcherAPIPort = 48092

	// appDir is the per-user directory name; the state home is $HOME/.proximo
	// (a leading dot is prepended in HomePath).
	appDir = "proximo"
)

// Config holds the user-configurable settings persisted to disk.
type Config struct {
	// TLD is the top-level domain routed to the local proximo (without a dot).
	TLD string `json:"tld"`

	// The peer-sharing values (docs/specs/peer-sharing.md). Each has no
	// default and is set on its own; any subset is a legitimate state, and a
	// machine with none of them set executes no peer behaviour at all.

	// Machine is this machine's label in its peer names.
	Machine string `json:"machine,omitempty"`
	// PeerSuffix is the multi-label suffix every peer name lives under.
	PeerSuffix string `json:"peer_suffix,omitempty"`
	// Address is the address proximo answers this machine's peer names with:
	// the machine's own address on the mesh.
	Address string `json:"address,omitempty"`
	// TeamRoot is the absolute path of the team root certificate (PEM).
	TeamRoot string `json:"team_root,omitempty"`
	// MeshRemedy overrides the Remedy the mesh Check offers. Stored verbatim.
	MeshRemedy string `json:"mesh_remedy,omitempty"`
}

// Default returns a Config populated with default values.
func Default() Config {
	return Config{TLD: DefaultTLD}
}

// labelPattern is one DNS label: a TLD, a machine label, a label of the Peer suffix.
var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// NormalizeTLD validates and normalizes a user-supplied TLD: it strips a leading
// dot, lowercases, enforces a single DNS label, and rejects reserved values.
func NormalizeTLD(raw string) (string, error) {
	tld := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "."))
	if !labelPattern.MatchString(tld) {
		return "", fmt.Errorf("invalid TLD %q: use a single DNS label of [a-z0-9-]", raw)
	}
	if tld == "local" {
		return "", fmt.Errorf(".local is reserved for mDNS and is not supported")
	}
	return tld, nil
}

// reservedTLDs are the labels carrying a guarantee they will never be
// delegated: RFC 6761's special-use names plus ICANN's .internal. Deliberately
// not the complement — a list of currently-delegated gTLDs would age, and an
// aging list lies.
var reservedTLDs = map[string]bool{
	"test":      true,
	"internal":  true,
	"example":   true,
	"invalid":   true,
	"localhost": true,
}

// TLDWarning returns advice for a TLD with no reservation behind it, or "" for
// a reserved one. It never rejects: the choice is the user's, but a label
// someone else may own shadows every public name under it.
func TLDWarning(tld string) string {
	if reservedTLDs[tld] {
		return ""
	}
	return fmt.Sprintf(".%s is not reserved for private use: it may be delegated on the public internet, and routing it locally shadows every name under it. Reserved alternatives: .%s (RFC 6761) and .internal.", tld, DefaultTLD)
}

// NormalizeMachine validates a machine label: one DNS label of [a-z0-9-],
// lowercased, at most 63 octets. Whether it names a person is not something a
// machine can judge, so it is not checked — MachineRule is printed instead.
func NormalizeMachine(raw string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(raw))
	if !labelPattern.MatchString(m) || len(m) > 63 {
		return "", fmt.Errorf("invalid machine label %q: use a single DNS label of [a-z0-9-], at most 63 characters", raw)
	}
	return m, nil
}

// MachineRule is printed on every `config machine`, unconditionally: the one
// intervention available for a value whose fault cannot be detected.
const MachineRule = "The machine label names a machine, not a person: colleagues bookmark it and write it into READMEs, so keep it neutral and stable (studio-01, never a person's name, a model or an office)."

// NormalizePeerSuffix validates a Peer suffix: at least two DNS labels of
// [a-z0-9-], lowercased, leading and trailing dots stripped. NormalizeTLD does
// not fit: it accepts exactly one label.
func NormalizePeerSuffix(raw string) (string, error) {
	s := strings.Trim(strings.ToLower(strings.TrimSpace(raw)), ".")
	labels := strings.Split(s, ".")
	if len(labels) < 2 || len(s) > 253 {
		return "", fmt.Errorf("invalid peer suffix %q: use at least two DNS labels of [a-z0-9-]", raw)
	}
	for _, l := range labels {
		if !labelPattern.MatchString(l) || len(l) > 63 {
			return "", fmt.Errorf("invalid peer suffix %q: %q is not a DNS label of [a-z0-9-]", raw, l)
		}
	}
	// mDNS answers .local ahead of any nameserver the mesh configures.
	if labels[len(labels)-1] == "local" {
		return "", fmt.Errorf("invalid peer suffix %q: .local is answered by mDNS first, so no peer name under it would resolve", raw)
	}
	return s, nil
}

// PeerSuffixWarning returns advice for a suffix whose right-most label nobody
// reserved, or "" for a reserved one. It never rejects, as TLDWarning does not.
func PeerSuffixWarning(suffix string) string {
	last := suffix[strings.LastIndex(suffix, ".")+1:]
	if reservedTLDs[last] {
		return ""
	}
	return fmt.Sprintf(".%s is not reserved from delegation: the day someone registers it, every peer name under %s stops resolving. Pick a suffix under a reserved label, such as .internal.", last, suffix)
}

// ParseAddress validates the address proximo answers peer names with.
func ParseAddress(raw string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil {
		return "", fmt.Errorf("invalid address %q: not an IP address", raw)
	}
	return ip.String(), nil
}

// AddressRule is printed on every `config address`: the held-by-an-interface
// check catches a typo or a stale value, never a wrong-but-local address.
const AddressRule = "The address must be this machine's own address on the mesh. proximo checks only that some interface holds it: it cannot tell the mesh address from the LAN address or 127.0.0.1."

// AddressHeld reports whether one of addrs (as net.InterfaceAddrs returns
// them) is exactly addr. Exact, never subnet membership: a mesh range is
// typically a /8, and membership in it asserts almost nothing.
func AddressHeld(addr string, addrs []net.Addr) bool {
	want := net.ParseIP(addr)
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip != nil && ip.Equal(want) {
			return true
		}
	}
	return false
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
