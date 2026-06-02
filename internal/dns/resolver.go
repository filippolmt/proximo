package dns

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/platform"
)

// ConfigureResolver wires the host resolver so that lookups for the TLD are
// sent to the local DNS server on 127.0.0.1:<DNSPort>.
func ConfigureResolver(tld string) error {
	return platform.Dispatch(
		func() error { return configureResolverDarwin(tld) },
		func() error { return configureResolverLinux(tld) },
	)
}

// RemoveResolver removes the host resolver configuration created by
// ConfigureResolver and reloads the resolver.
func RemoveResolver(tld string) error {
	return platform.Dispatch(
		func() error { return removeResolverDarwin(tld) },
		func() error { return removeResolverLinux(tld) },
	)
}

// ---- macOS: /etc/resolver/<tld> ----

func resolverFileDarwin(tld string) string {
	return filepath.Join("/etc/resolver", tld)
}

func configureResolverDarwin(tld string) error {
	content := fmt.Sprintf("nameserver 127.0.0.1\nport %d\n", config.DNSPort)
	return platform.WriteFilePrivileged(resolverFileDarwin(tld), []byte(content), 0o644)
}

func removeResolverDarwin(tld string) error {
	return platform.RemoveFilePrivileged(resolverFileDarwin(tld))
}

// ---- Linux: systemd-resolved drop-in ----

const resolvedDropInDir = "/etc/systemd/resolved.conf.d"

func resolvedDropInPath(tld string) string {
	return filepath.Join(resolvedDropInDir, "proximo-"+tld+".conf")
}

func configureResolverLinux(tld string) error {
	if err := requireSystemdResolved(); err != nil {
		return err
	}
	content := fmt.Sprintf("[Resolve]\nDNS=127.0.0.1:%d\nDomains=~%s\n", config.DNSPort, tld)
	if err := platform.WriteFilePrivileged(resolvedDropInPath(tld), []byte(content), 0o644); err != nil {
		return err
	}
	return reloadResolved()
}

func removeResolverLinux(tld string) error {
	if err := platform.RemoveFilePrivileged(resolvedDropInPath(tld)); err != nil {
		return err
	}
	return reloadResolved()
}

func reloadResolved() error {
	return platform.Sudo("systemctl", "restart", "systemd-resolved")
}

// requireSystemdResolved aborts when systemd-resolved is not the active
// resolver, with a message identifying what was detected instead.
func requireSystemdResolved() error {
	if platform.IsActiveService("systemd-resolved") {
		return nil
	}
	return fmt.Errorf("systemd-resolved is not the active resolver (detected: %s); only systemd-resolved is supported in v1", detectLinuxResolver())
}

// detectLinuxResolver makes a best-effort guess at the active resolver for a
// clearer error message.
func detectLinuxResolver() string {
	if target, err := os.Readlink("/etc/resolv.conf"); err == nil {
		switch {
		case strings.Contains(target, "systemd"):
			return "systemd-resolved (inactive)"
		case strings.Contains(target, "NetworkManager"):
			return "NetworkManager"
		case strings.Contains(target, "resolvconf"):
			return "resolvconf"
		}
	}
	if platform.Has("NetworkManager") {
		return "NetworkManager"
	}
	return "unknown (static /etc/resolv.conf?)"
}
