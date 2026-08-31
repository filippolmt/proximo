package checks

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/dns"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/skill"
)

// Check IDs. They are the vocabulary prerequisites are written in, so they are
// named rather than spelled out at each use.
const (
	IDDocker       = "docker"
	IDPortHTTP     = "port-http"
	IDPortHTTPS    = "port-https"
	IDPortDNS      = "port-dns"
	IDCertutil     = "certutil"
	IDInstalled    = "installed"
	IDTrustSystem  = "trust-system"
	IDTrustNSS     = "trust-nss"
	IDStack        = "stack"
	IDStackVersion = "stack-version"
	IDStackImage   = "stack-image"
	IDDNSServer    = "dns-server"
	IDDNSResolver  = "dns-resolver"
	IDAccessLog    = "access-log"
	IDRoutes       = "routes"
	IDAgentSkill   = "agent-skill"
)

// All is the registry: every check proximo knows how to make, in the order a
// developer should read them. One list, so the pre-install subset and the full
// report can never drift apart.
func All(env Env) []Check {
	stack := once(env.Stack)
	sentinel := dns.Sentinel(env.TLD)

	return append(PreInstall(env),
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
					return unreadableStack(err, "proximo up")
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
					return unreadableStack(err, "proximo update")
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
					return unreadableStack(err, "proximo up")
				}
				if info.Image != "" && info.Image != env.CanonicalImage {
					return Failed("proximo up", "the stack runs %s, this CLI pins %s", info.Image, env.CanonicalImage)
				}
				return Passed("%s", env.CanonicalImage)
			},
		},
		Check{
			ID:    IDAccessLog,
			Name:  "The running stack records access logs",
			Doc:   "proximo-errors-shows-nothing-at-all",
			Needs: []string{IDStack},
			Run: func(ctx context.Context) Result {
				on, err := env.AccessLog(ctx)
				switch {
				case err != nil:
					return Failed("proximo update", "could not read whether the running Traefik records access logs: %v", err)
				case !on:
					return Failed("proximo update", "the running Traefik writes no access log, so no route produces an Exchange")
				}
				return Passed("every route produces an Exchange")
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
				unserved := unservedRoutes(routes)
				if len(unserved) == 0 {
					return Passed("%d route(s)", len(routes))
				}
				// A contested host and a mislabelled container are documented
				// apart and cured apart, so the report says which one this is.
				// A collision has no cure proximo may pick — stopping one of two
				// containers is the developer's call — so its remedy is the
				// command that lists every claimant.
				if collisions(unserved) {
					return Failed("docker ps --filter label=proximo.hosts", "%s", notesOf(unserved)).
						Explains("a-host-collision-is-reported")
				}
				return Failed("docker inspect --format '{{json .Config.Labels}}' "+strings.Join(containersOf(unserved), " "),
					"%s", notesOf(unserved))
			},
		},
		Check{
			ID:   IDAgentSkill,
			Name: "The agent skill matches the installed CLI",
			Doc:  "the-agent-skill-is-out-of-date",
			Run: func(context.Context) Result {
				return agentSkill(env)
			},
		},
	)
}

// agentSkill closes what auto-update deliberately cannot reach: a copy skipped
// because somebody edited it, and a project copy in a repository proximo is not
// being run from. The two have different cures, so they are reported apart.
//
// With no managed copy it is skipped rather than failed: a developer who uses
// no coding agent must never see a red line about one.
func agentSkill(env Env) Result {
	copies, err := env.AgentSkill()
	if err != nil {
		// A skip, not a failure: the environment could not answer, and there is
		// no command that cures it — the destinations could not be resolved at
		// all, so every command naming one would meet the same wall. A Remedy
		// here would be advice dressed as a cure.
		return Skipped("the installed agent skills could not be read: %v", err)
	}

	var managed int
	var stale, modified, unmanaged []skill.Copy
	for _, c := range copies {
		if c.State.Managed() {
			managed++
		}
		switch c.State {
		case skill.Stale:
			stale = append(stale, c)
		case skill.Modified:
			modified = append(modified, c)
		case skill.Unmanaged:
			unmanaged = append(unmanaged, c)
		}
	}

	switch {
	case managed == 0 && len(unmanaged) > 0:
		// Named rather than passed over in silence: a developer looking at the
		// directory would otherwise read "no agent skill" as flatly false.
		return Skipped("proximo installed no agent skill on this host (%s came from another channel, and proximo neither updates nor removes it)",
			skill.Dirs(unmanaged))
	case managed == 0:
		return Skipped("proximo installed no agent skill on this host")
	case len(modified) > 0:
		return Failed(skill.Command("install", modified, true),
			"%s was edited after proximo wrote it, so it is left at whatever it now says",
			skill.Dirs(modified))
	case len(stale) > 0:
		return Failed(skill.Command("install", stale, false),
			"%s was written by another version of proximo", skill.Dirs(stale))
	}

	noun := "copies"
	if managed == 1 {
		noun = "copy"
	}
	if len(unmanaged) > 0 {
		return Passed("%d %s at %s; %s is unmanaged, so nothing keeps it level",
			managed, noun, env.CLIVersion, skill.Dirs(unmanaged))
	}
	return Passed("%d %s at %s", managed, noun, env.CLIVersion)
}

// PreInstall is what `install` gates on: Preflight plus the one statement about
// a store `install` is about to write. It is not what `up` gates on, because
// `up` changes no host configuration and a machine that cannot install browser
// trust must still be allowed to start the stack.
func PreInstall(env Env) []Check {
	return append(Preflight(env), Check{
		ID:   IDCertutil,
		Name: "Browser trust can be installed",
		Doc:  "certificate-warnings-in-firefox-or-chrome",
		Run: func(context.Context) Result {
			if env.CertutilInstallable() {
				return Passed("")
			}
			return Failed(env.CertutilRemedy,
				"certutil is missing and no package manager proximo supports (Homebrew or apt) can fetch it")
		},
	})
}

// Preflight is the subset that is meaningful before the host has been changed:
// what `install` and `up` share, so a developer learns that Docker is missing
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
//
// It deliberately declares no prerequisite on Docker, though it asks Docker
// first: a port held by a host process is worth knowing precisely when the
// daemon is down, and skipping the question there would withhold the answer at
// the moment it is most useful. Without Docker the holder cannot be named, and
// the remedy falls back to the one that names it.
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

// unservedRoutes returns the routes that opted in but are not served.
//
// Two notes are deliberately not among them. A container that is still starting
// is a moment, not a fault, and a report that goes red during every restart is
// one nobody trusts. An InspectNote rides a route that *is* served — the
// inspection was refused, not the route — and this check is about being served.
func unservedRoutes(routes []docker.Route) []docker.Route {
	var unserved []docker.Route
	for _, r := range routes {
		if r.Note == "" || r.Note == docker.NoteStarting {
			continue
		}
		unserved = append(unserved, r)
	}
	return unserved
}

// collisions reports whether any unserved route lost its host to another
// claimant, which is documented and cured apart from every other reason.
func collisions(unserved []docker.Route) bool {
	return slices.ContainsFunc(unserved, func(r docker.Route) bool { return r.Collision })
}

// notesOf renders one line per unserved route, in the words the watcher used.
func notesOf(unserved []docker.Route) string {
	lines := make([]string, len(unserved))
	for i, r := range unserved {
		lines[i] = r.Container + ": " + r.Note
	}
	return strings.Join(lines, "\n")
}

// containersOf lists the containers to inspect, whose labels produced the notes.
func containersOf(unserved []docker.Route) []string {
	var names []string
	for _, r := range unserved {
		if !slices.Contains(names, r.Container) {
			names = append(names, r.Container)
		}
	}
	return names
}

// unreadableStack is the failure the three stack checks share: Docker answered,
// the stack did not, and each check keeps the remedy it would have offered.
func unreadableStack(err error, remedy string) Result {
	return Failed(remedy, "could not read the stack: %v", err)
}

// once memoizes a reading so the checks that share one observation cost a
// single Docker call. The registry is rebuilt for every command run, so
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
