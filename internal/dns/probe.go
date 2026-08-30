package dns

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"time"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/platform"
	"github.com/miekg/dns"
)

// sentinelLabel is the name a diagnosis resolves to prove the wildcard is
// answering. The server answers every name under the TLD, so the sentinel needs
// no container running — which is what makes it usable on a machine where
// nothing is up yet.
const sentinelLabel = "proximo-doctor"

// Sentinel is the name a Check resolves to prove DNS works, e.g.
// proximo-doctor.test.
func Sentinel(tld string) string { return sentinelLabel + "." + tld }

// probeTimeout bounds every probe. A check that runs out of time fails rather
// than hangs: a broken VPN is exactly the machine a diagnosis exists for, and a
// diagnostic tool that hangs is worse than one that is wrong.
const probeTimeout = 5 * time.Second

// QueryLocal asks the proximo DNS server directly, bypassing the host resolver,
// and returns the address it answered with (empty when it answered nothing).
// This is the half of the DNS diagnosis that says whether the server itself is
// alive; SystemResolves is the half that says whether the host uses it.
func QueryLocal(ctx context.Context, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	c := &dns.Client{Timeout: probeTimeout}
	resp, _, err := c.ExchangeContext(ctx, m, fmt.Sprintf("127.0.0.1:%d", config.DNSPort))
	if err != nil {
		return "", err
	}
	for _, rr := range resp.Answer {
		if a, ok := rr.(*dns.A); ok {
			return a.A.String(), nil
		}
	}
	return "", nil
}

// SystemResolves asks the host resolver — through the OS tool that honours the
// scoped resolver configuration — and returns the address it answered with.
//
// Go's own net.LookupHost is deliberately not used: the pure resolver reads
// /etc/resolv.conf and honours neither /etc/resolver/<tld> on macOS nor a
// Domains=~<tld> drop-in on Linux, so it would report a failure on a perfectly
// healthy machine.
func SystemResolves(ctx context.Context, name string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var out string
	err := platform.Dispatch(
		func() (err error) {
			out, err = platform.OutputContext(ctx, "dscacheutil", "-q", "host", "-a", "name", name)
			return err
		},
		func() (err error) {
			out, err = platform.OutputContext(ctx, "resolvectl", "query", name)
			return err
		},
	)
	if err != nil {
		return "", err
	}
	return firstIPv4(out), nil
}

var ipv4Pattern = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\b`)

// firstIPv4 extracts the first address out of a resolver tool's output. Both
// tools print prose around the answer (`ip_address: 127.0.0.1`,
// `name: 127.0.0.1`), and only the address is the fact.
func firstIPv4(out string) string {
	m := ipv4Pattern.FindString(out)
	if m == "" {
		return ""
	}
	if ip := net.ParseIP(m); ip == nil {
		return ""
	}
	return m
}

// ResolverPath is the host file that points the TLD at proximo's DNS server —
// the file ConfigureResolver writes. A Check reads it to tell a machine that
// was never installed from one whose resolver was removed.
func ResolverPath(tld string) (string, error) {
	osType, err := platform.Current()
	if err != nil {
		return "", err
	}
	switch osType {
	case platform.MacOS:
		return resolverFileDarwin(tld), nil
	default:
		return resolvedDropInPath(tld), nil
	}
}

// ResolverRemedy is the command whose own output names why the host resolver is
// not using proximo's DNS server. There is no cure to offer — a VPN's scoped
// resolver outranking the proximo one is not something proximo may undo — so
// the remedy is the question.
func ResolverRemedy() string {
	if osType, err := platform.Current(); err == nil && osType == platform.MacOS {
		return "scutil --dns"
	}
	return "resolvectl status"
}
