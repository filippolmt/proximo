package docker

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// The peer DNS service answers this machine's peer subtree on the mesh
// address (docs/architecture.md#the-peer-dns-service). It is a Compose
// service of its own, present only when machine, peer-suffix and address are
// all configured, and profile-gated so the core `up` never waits on it: a
// publish bound to an address no interface holds leaves its container
// `created`, and inside the dns service that would take .test down whenever
// the mesh is down at start.
//
// The peer relay is its sibling, for the same reason: Traefik listens on the
// loopback only, and the relay is what publishes :443 on the mesh address
// (docs/adr/0014-the-stack-listens-on-the-loopback.md). Both appear, start and
// go together; each is restarted on its own.
const (
	peerProfile   = "peer"
	peerDNSRole   = "peer-dns"
	peerRelayRole = "peer-relay"
	peerDNSPort   = config.PeerDNSPort
	peerRelayPort = config.PeerRelayPort
)

// peerRoles are the peer services, in the order they are appended.
var peerRoles = []string{peerDNSRole, peerRelayRole}

// peerUpCmd starts the peer services after the core converge.
var peerUpCmd = append([]string{"--profile", peerProfile, "up", "-d"}, peerRoles...)

// peerRemoveCmd removes the peer services the previous Compose file still
// describes. Only those: Compose refuses the whole command over a service the
// file does not define, which a file from before the peer relay is.
func peerRemoveCmd(roles []string) []string {
	return append([]string{"--profile", peerProfile, "rm", "-s", "-f"}, roles...)
}

// peerConfigured reports whether the peer services belong in the stack:
// machine, peer-suffix and an address it can answer with. The address is
// judged again here, so a value saved before config address refused IPv6, or
// a hand-edited file, yields no service rather than one that crash-loops.
func peerConfigured(cfg config.Config) bool {
	if cfg.Machine == "" || cfg.PeerSuffix == "" {
		return false
	}
	_, err := config.ParseAddress(cfg.Address)
	return err == nil
}

// composePeerRoles returns the peer services the Compose file materialized
// last time describes — the only state in which unsetting a value leaves them
// behind to remove.
func composePeerRoles(stackDir string) []string {
	data, err := os.ReadFile(filepath.Join(stackDir, "docker-compose.yml"))
	if err != nil {
		return nil
	}
	var roles []string
	for _, r := range peerRoles {
		if strings.Contains(string(data), "\n  "+r+":\n") {
			roles = append(roles, r)
		}
	}
	return roles
}

// peerServices is the pair of peer services appended to the materialized
// Compose file. Both are published only on the mesh address — every interface
// would expose them on the LAN, and Docker refuses a second publish of the
// loopback port. The stack's services mapping is the file's last entry, so
// appending keeps the file well-formed.
func peerServices(cfg config.Config) string {
	return peerDNSService(cfg) + peerRelayService(cfg)
}

// peerDNSService runs the dnsserver binary with the peer handler.
func peerDNSService(cfg config.Config) string {
	subtree := tls.MachineSubtree(cfg.Machine, cfg.PeerSuffix)
	return fmt.Sprintf(`
  %s:
    # The peer DNS service: answers %s on the mesh address. Present only
    # because machine, peer-suffix and address are configured.
%s    entrypoint: ["/usr/local/bin/dnsserver"]
    environment:
      PROXIMO_DNS_ADDR: ":%d"
      PROXIMO_PEER_SUBTREE: "%s"
      PROXIMO_PEER_ADDRESS: "%s"
    ports:
      - "%s:%d:%d/udp"
      - "%s:%d:%d/tcp"
%s`, peerDNSRole, subtree, peerServiceBase, peerDNSPort, subtree, cfg.Address,
		cfg.Address, peerDNSPort, peerDNSPort, cfg.Address, peerDNSPort, peerDNSPort, stackLabels(peerDNSRole))
}

// peerRelayService runs the peer relay, which copies TCP from the mesh
// address to Traefik. Its target is the binary's own default, traefik:443.
func peerRelayService(cfg config.Config) string {
	return fmt.Sprintf(`
  %s:
    # The peer relay: copies TCP from the mesh address to Traefik, which
    # listens on the loopback only. TLS terminates at Traefik, SNI intact.
%s    entrypoint: ["/usr/local/bin/peerrelay"]
    environment:
      PROXIMO_RELAY_LISTEN: ":%d"
    ports:
      - "%s:%d:%d/tcp"
%s`, peerRelayRole, peerServiceBase, peerRelayPort, cfg.Address, peerRelayPort, peerRelayPort, stackLabels(peerRelayRole))
}

// peerServiceBase is what every peer service shares: the stack image, the
// peer profile, the restart policy and the log caps.
const peerServiceBase = `    image: *proximo-image
    profiles: [` + peerProfile + `]
    restart: unless-stopped
    logging: *proximo-logging
`

// stackLabels are the labels every image-backed stack service carries: its
// role, and the image and version it was started from.
func stackLabels(role string) string {
	return fmt.Sprintf(`    labels:
      - "proximo.role=%s"
      - "proximo.image=${PROXIMO_IMAGE:-%s}"
      - "proximo.version=${PROXIMO_VERSION:-dev}"
`, role, CanonicalImage())
}

// appendPeerServices adds the peer services to the materialized Compose file.
func appendPeerServices(stackDir string, cfg config.Config) error {
	f, err := os.OpenFile(filepath.Join(stackDir, "docker-compose.yml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(peerServices(cfg)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// startPeerServices starts the peer services and, when they cannot start, warns
// and returns nothing: a peer-side failure never fails the command that serves
// .test. The one cause of a failed bind it can name is the address being held
// by no interface — the mesh client was down — which the watcher then retries.
func startPeerServices(c Composer, dir, tld string, cfg config.Config) {
	if err := c.Compose(dir, peerUpCmd...); err == nil {
		return
	}
	cause := "see the error above"
	if addrs, err := net.InterfaceAddrs(); err == nil && !config.AddressHeld(cfg.Address, addrs) {
		cause = fmt.Sprintf("%s %s; the watcher starts the services once it is", cfg.Address, config.AddressNotHeld)
	}
	fmt.Fprintf(os.Stderr, "⚠ %s: the peer services did not start (%s). .%s is unaffected.\n", strings.Join(peerRoles, ", "), cause, tld)
}

// restartPeerServices starts each peer service whenever it is created or exited.
// Docker Desktop and the mesh client both start at login, in no fixed order,
// and Docker's restart policy never retries a container left `created`. It
// runs on the watcher's fixed 30 s tick, never on an event, so a failed start
// is retried at that pace and logged once per service until it succeeds. It
// inspects no host interface, touches no other service, and finds nothing on
// a machine where the services do not exist.
func (w *Watcher) restartPeerServices(ctx context.Context) {
	// Docker ANDs repeated label filters, so filter on the key and pick the
	// roles here.
	res, err := w.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", RoleLabel),
	})
	if err != nil {
		return
	}
	for _, c := range res.Items {
		role := c.Labels[RoleLabel]
		if !slices.Contains(peerRoles, role) {
			continue
		}
		if c.State != container.StateCreated && c.State != container.StateExited {
			continue
		}
		if _, err := w.cli.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
			if !w.peerFailing[role] {
				log.Printf("proximo watcher: start %s: %v; retrying every 30s", role, err)
			}
			w.peerFailing[role] = true
			continue
		}
		delete(w.peerFailing, role)
		log.Printf("proximo watcher: started %s", role)
	}
}
