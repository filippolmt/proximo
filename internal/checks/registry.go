package checks

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/dns"
	"github.com/filippolmt/proximo/internal/docker"
)

// Check IDs. They are the vocabulary prerequisites are written in, so they are
// named rather than spelled out at each use.
const (
	IDDocker       = "docker"
	IDPortHTTP     = "port-http"
	IDPortHTTPS    = "port-https"
	IDPortDNS      = "port-dns"
	IDInstalled    = "installed"
	IDTrustSystem  = "trust-system"
	IDTrustNSS     = "trust-nss"
	IDStack        = "stack"
	IDStackVersion = "stack-version"
	IDStackImage   = "stack-image"
	IDDNSServer    = "dns-server"
	IDDNSResolver  = "dns-resolver"
	IDRoutes       = "routes"
)

// All is the registry: every check proximo knows how to make, in the order a
// developer should read them. One list, so the pre-install subset and the full
// diagnosis can never drift apart.
func All(env Env) []Check {
	stack := once(env.Stack)
	sentinel := dns.Sentinel(env.TLD)

	return append(Preflight(env),
		Check{
			ID:   IDInstalled,
			Name: "proximo is installed on this host",
			Doc:  "proximo-is-not-installed-on-this-host",
			Run: func(context.Context) Result {
				var missing []string
				if !env.FileExists(env.CAPath) {
					missing = append(missing, "the local CA ("+env.CAPath+")")
				}
				if !env.FileExists(env.ResolverPath) {
					missing = append(missing, "the host resolver file ("+env.ResolverPath+")")
				}
				if len(missing) > 0 {
					return Failed("proximo install", "missing %s", strings.Join(missing, " and "))
				}
				return Passed("CA and host resolver are in place")
			},
		},
		Check{
			ID:    IDTrustSystem,
			Name:  "The local CA is in the system trust store",
			Doc:   "certificate-warnings-in-firefox-or-chrome",
			Needs: []string{IDInstalled},
			Run: func(ctx context.Context) Result {
				trusted, err := env.SystemTrusted(ctx)
				switch {
				case err != nil:
					return Failed("proximo trust", "could not read the system trust store: %v", err)
				case !trusted:
					return Failed("proximo trust", "the CA is on disk but not in the system store")
				}
				return Passed("")
			},
		},
		Check{
			ID:    IDTrustNSS,
			Name:  "The local CA is in the browser (NSS) trust stores",
			Doc:   "certificate-warnings-in-firefox-or-chrome",
			Needs: []string{IDInstalled},
			Run: func(ctx context.Context) Result {
				found, total, err := env.NSSTrusted(ctx)
				switch {
				case err != nil:
					return Failed(env.CertutilRemedy, "could not read the browser trust stores: %v", err)
				case total == 0:
					return Skipped("no Firefox or Chrome NSS database on this host")
				case found < total:
					return Failed("proximo trust", "%d of %d NSS databases hold the CA", found, total)
				}
				return Passed("%d NSS database(s) hold the CA", total)
			},
		},
		Check{
			ID:    IDStack,
			Name:  "The proximo stack is running",
			Doc:   "degraded-stack",
			Needs: []string{IDDocker},
			Run: func(ctx context.Context) Result {
				info, err := stack(ctx)
				switch {
				case err != nil:
					return Failed("proximo up", "could not read the stack: %v", err)
				case !info.Running:
					return Failed("proximo up", "no stack container is running")
				}
				if missing := info.MissingRoles(); len(missing) > 0 {
					return Failed("proximo up", "%s is not running", strings.Join(missing, " and "))
				}
				return Passed("%s", strings.Join(info.Roles, ", "))
			},
		},
		Check{
			ID:    IDStackVersion,
			Name:  "The stack matches the installed CLI version",
			Doc:   "degraded-stack",
			Needs: []string{IDStack},
			Run: func(ctx context.Context) Result {
				info, err := stack(ctx)
				if err != nil {
					return Failed("proximo update", "could not read the stack version: %v", err)
				}
				if running := docker.DisplayVersion(info.Version); running != env.CLIVersion {
					return Failed("proximo update", "the stack runs %s, the CLI is %s", running, env.CLIVersion)
				}
				return Passed("%s", env.CLIVersion)
			},
		},
		Check{
			ID:    IDStackImage,
			Name:  "The stack runs the image this CLI pins",
			Doc:   "the-stack-runs-an-overridden-image",
			Needs: []string{IDStack},
			Run: func(ctx context.Context) Result {
				info, err := stack(ctx)
				if err != nil {
					return Failed("proximo up", "could not read the stack image: %v", err)
				}
				if info.Image != "" && info.Image != env.CanonicalImage {
					return Failed("proximo up", "the stack runs %s, this CLI pins %s", info.Image, env.CanonicalImage)
				}
				return Passed("%s", env.CanonicalImage)
			},
		},
		Check{
			ID:    IDDNSServer,
			Name:  "The proximo DNS server answers",
			Doc:   "dns-name-does-not-resolve",
			Needs: []string{IDStack},
			Run: func(ctx context.Context) Result {
				addr, err := env.QueryLocal(ctx, sentinel)
				switch {
				case err != nil:
					return Failed("proximo up", "127.0.0.1:%d did not answer %s: %v", config.DNSPort, sentinel, err)
				case addr != loopback:
					return Failed("proximo up", "%s answered %q, want %s", sentinel, addr, loopback)
				}
				return Passed("%s answers %s on 127.0.0.1:%d", sentinel, loopback, config.DNSPort)
			},
		},
		Check{
			ID:    IDDNSResolver,
			Name:  "The host resolver uses the proximo DNS server",
			Doc:   "vpn-or-corporate-dns-overrides-the-resolver",
			Needs: []string{IDInstalled, IDDNSServer},
			Run: func(ctx context.Context) Result {
				addr, err := env.SystemResolve(ctx, sentinel)
				switch {
				case err != nil:
					return Failed(env.ResolverRemedy, "the host resolver could not be asked for %s: %v", sentinel, err)
				case addr != loopback:
					return Failed(env.ResolverRemedy, "%s resolves to %q through the host resolver, not %s", sentinel, addr, loopback)
				}
				return Passed("%s resolves to %s", sentinel, loopback)
			},
		},
		Check{
			ID:    IDRoutes,
			Name:  "Every routed container is served",
			Doc:   "container-not-routed",
			Needs: []string{IDStack},
			Run: func(ctx context.Context) Result {
				routes, err := env.Routes(ctx)
				if err != nil {
					return Failed("docker ps --filter label=proximo.hosts", "could not list routes: %v", err)
				}
				notes, containers := unservedRoutes(routes)
				if len(notes) > 0 {
					return Failed("docker inspect --format '{{json .Config.Labels}}' "+strings.Join(containers, " "),
						"%s", strings.Join(notes, "\n"))
				}
				return Passed("%d route(s)", len(routes))
			},
		},
	)
}

// Preflight is the subset that is meaningful before the host has been changed:
// what `install` and `up` gate on, so a developer learns that Docker is missing
// or that a stranger holds :443 before proximo touches anything.
func Preflight(env Env) []Check {
	return []Check{
		{
			ID:   IDDocker,
			Name: "The Docker daemon is reachable",
			Doc:  "the-docker-daemon-is-not-reachable",
			Run: func(ctx context.Context) Result {
				if err := env.Docker(ctx); err != nil {
					return Failed("docker version", "%v", err)
				}
				return Passed("")
			},
		},
		portCheck(env, IDPortHTTP, 80, "tcp", "port-443-or-80-already-in-use"),
		portCheck(env, IDPortHTTPS, 443, "tcp", "port-443-or-80-already-in-use"),
		portCheck(env, IDPortDNS, config.DNSPort, "udp", "dns-port-already-in-use"),
	}
}

// loopback is the address every proximo name resolves to.
const loopback = "127.0.0.1"

// portCheck states that nobody but proximo holds a port. It asks who holds it
// rather than whether it is free: a healthy machine has :443 bound — by
// proximo — and the holder is also what picks the remedy.
func portCheck(env Env, id string, port int, proto, doc string) Check {
	label := fmt.Sprintf(":%d/%s", port, proto)
	return Check{
		ID:   id,
		Name: "Nothing but proximo holds " + label,
		Doc:  doc,
		Run: func(ctx context.Context) Result {
			holder, who := env.PortHeldBy(ctx, port, proto)
			switch holder {
			case PortFree:
				return Passed("free")
			case PortStack:
				return Passed("held by the proximo stack (%s)", who)
			case PortContainer:
				return Failed(fmt.Sprintf("docker ps --filter publish=%d", port),
					"%s is held by the container %s", label, who)
			default:
				return Failed(lsofCmd(port, proto), "%s is held by a process on this host", label)
			}
		},
	}
}

// lsofCmd is the question to ask about a port no container publishes: proximo
// cannot name a host process, but lsof can.
func lsofCmd(port int, proto string) string {
	if proto == "udp" {
		return fmt.Sprintf("sudo lsof -nP -iUDP:%d", port)
	}
	return fmt.Sprintf("sudo lsof -nP -iTCP:%d -sTCP:LISTEN", port)
}

// unservedRoutes returns one line per route that opted in but is not served,
// and the containers to inspect — whose labels are what produced the note.
//
// Two notes are deliberately not failures. A container that is still starting
// is a moment, not a fault, and a diagnosis that goes red during every restart
// is one nobody trusts. An InspectNote rides a route that *is* served — the
// inspection was refused, not the route — and this check is about being served.
func unservedRoutes(routes []docker.Route) (notes, containers []string) {
	for _, r := range routes {
		note := r.Note
		if note == "" || note == docker.NoteStarting {
			continue
		}
		notes = append(notes, r.Container+": "+note)
		if !slices.Contains(containers, r.Container) {
			containers = append(containers, r.Container)
		}
	}
	return notes, containers
}

// once memoizes a probe so the checks that read the same observation cost one
// Docker call between them. The registry is rebuilt for every command run, so
// nothing is ever cached across two passes.
func once[T any](f func(context.Context) (T, error)) func(context.Context) (T, error) {
	var (
		done bool
		val  T
		err  error
	)
	return func(ctx context.Context) (T, error) {
		if !done {
			val, err = f(ctx)
			done = true
		}
		return val, err
	}
}
