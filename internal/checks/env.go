package checks

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/filippolmt/proximo/internal/dns"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/platform"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/filippolmt/proximo/internal/version"
	"github.com/moby/moby/client"
)

// PortHolder says who holds a host port. Three of the four outcomes are
// healthy, which is why the check asks who rather than whether.
type PortHolder string

const (
	// PortFree means nothing is listening.
	PortFree PortHolder = "free"
	// PortStack means a proximo stack container publishes it.
	PortStack PortHolder = "stack"
	// PortContainer means another container publishes it — nameable, so the
	// remedy can be a docker question.
	PortContainer PortHolder = "container"
	// PortProcess means no container publishes it, so a host process does.
	PortProcess PortHolder = "process"
)

// Env is everything the checks read the host through. Checks take their
// dependencies as parameters, the way the install host steps do, so the suite
// can build a machine with no resolver, a port held by a stranger, or no NSS
// store, without touching the host running the tests.
type Env struct {
	// TLD is the domain proximo claims, and the suffix of the sentinel name the
	// DNS checks resolve.
	TLD string
	// CLIVersion is the version of this binary, compared against the stack's.
	CLIVersion string
	// CanonicalImage is the stack image this CLI pins itself to.
	CanonicalImage string

	// CAPath and ResolverPath are the two host artifacts that together mean
	// "installed": the CA on disk and the file pointing the TLD at proximo.
	CAPath       string
	ResolverPath string

	// ResolverRemedy and CertutilRemedy are OS-specific commands, resolved once
	// here so the registry stays free of platform branching.
	ResolverRemedy string
	CertutilRemedy string

	FileExists    func(path string) bool
	PortHeldBy    func(ctx context.Context, port int, proto string) (PortHolder, string)
	QueryLocal    func(ctx context.Context, name string) (string, error)
	SystemResolve func(ctx context.Context, name string) (string, error)
	SystemTrusted func(ctx context.Context) (bool, error)
	NSSTrusted    func(ctx context.Context) (found, total int, err error)
	// CertutilInstallable reports whether browser trust can be installed at
	// all — before `install` writes anything that would have to be undone.
	CertutilInstallable func() bool
	Docker              func(ctx context.Context) error
	Stack               func(ctx context.Context) (docker.StackInfo, error)
	Routes              func(ctx context.Context) ([]docker.Route, error)
}

// DefaultEnv wires the checks to the real host. It fails only where proximo
// itself cannot run — an unsupported platform, or no home directory — since
// every other unknown is a check's answer to give, not an error to return.
func DefaultEnv(tld string) (Env, error) {
	// CACertLocation, not CACertPath: resolving the path must not create the
	// state home. A check reads the host; it never writes it — not even a
	// directory, and least of all on the machine that was never installed.
	caPath, err := tls.CACertLocation()
	if err != nil {
		return Env{}, err
	}
	resolverPath, err := dns.ResolverPath(tld)
	if err != nil {
		return Env{}, err
	}

	return Env{
		TLD:                 tld,
		CLIVersion:          version.Version,
		CanonicalImage:      docker.CanonicalImage(),
		CAPath:              caPath,
		ResolverPath:        resolverPath,
		ResolverRemedy:      dns.ResolverRemedy(),
		CertutilRemedy:      tls.CertutilRemedy(),
		FileExists:          fileExists,
		QueryLocal:          dns.QueryLocal,
		SystemResolve:       dns.SystemResolves,
		SystemTrusted:       tls.SystemTrusted,
		NSSTrusted:          tls.NSSTrusted,
		CertutilInstallable: tls.CertutilInstallable,
		PortHeldBy:          portHeldBy(once(docker.PublishedPorts)),
		Docker:              DockerReachable,
		Stack:               docker.StackStatus,
		Routes: func(ctx context.Context) ([]docker.Route, error) {
			return docker.Routes(ctx, tld)
		},
	}, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DockerReachable pings the Docker daemon. It is the one reading the rest of
// the CLI shares: every command that talks to Docker needs the same answer, and
// this way it is worded once.
func DockerReachable(ctx context.Context) error {
	if !platform.Has("docker") {
		return fmt.Errorf("docker is not installed or not on PATH")
	}
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		return fmt.Errorf("cannot reach the Docker daemon (is Docker running?): %w", err)
	}
	return nil
}

// portHeldBy answers who holds a host port. Docker is asked first because it is
// the only authoritative answer: it names the container, which is what picks
// the remedy, and a bind cannot be trusted to fail against a published port on
// every OS. Only when no container publishes the port does a bind decide
// between "free" and "a host process holds it" — the one holder proximo cannot
// name, and the one whose remedy is a question rather than a cure.
func portHeldBy(ports func(context.Context) (map[string]docker.PortOwner, error)) func(context.Context, int, string) (PortHolder, string) {
	return func(ctx context.Context, port int, proto string) (PortHolder, string) {
		if published, err := ports(ctx); err == nil {
			if owner, ok := published[docker.PortKey(port, proto)]; ok {
				if owner.Stack {
					return PortStack, owner.Container
				}
				return PortContainer, owner.Container
			}
		}
		if held(port, proto) {
			return PortProcess, ""
		}
		return PortFree, ""
	}
}

// held reports whether something outside Docker is on the port.
//
// A TCP port is asked by connecting, not by binding: :80 and :443 cannot be
// bound by the unprivileged user proximo runs as, so a bind would answer EACCES
// on a perfectly free port and report a stranger holding it — a false failure
// that would refuse to install on a healthy machine. A connection that is
// accepted proves a listener; one that is refused proves none.
//
// UDP has no handshake to lean on, so the DNS port is asked by binding it and
// closing it again. That port is unprivileged by design, so the bind is
// permitted, and the check never keeps a port it was asked about.
func held(port int, proto string) bool {
	addr := net.JoinHostPort(loopback, strconv.Itoa(port))
	if proto == "udp" {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			return true
		}
		return c.Close() != nil
	}
	c, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return false
	}
	return c.Close() == nil
}

// dialTimeout bounds the TCP question. It only ever dials loopback, where a
// listener answers or the kernel refuses immediately; the timeout is for the
// pathological case, not the normal one.
const dialTimeout = time.Second
