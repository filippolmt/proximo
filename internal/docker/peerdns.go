package docker

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// The peer DNS service answers this machine's peer subtree on the mesh
// address (docs/specs/peer-sharing.md, "The peer DNS service"). It is a Compose
// service of its own, present only when machine, peer-suffix and address are
// all configured, and profile-gated so the core `up` never waits on it: a
// publish bound to an address no interface holds leaves its container
// `created`, and inside the dns service that would take .test down whenever
// the mesh is down at start.
const (
	peerProfile = "peer"
	peerDNSRole = "peer-dns"
	peerDNSPort = 5354
)

// peerDNSUpCmd starts the peer DNS service after the core converge;
// peerDNSRemoveCmd removes one the previous Compose file still describes.
var (
	peerDNSUpCmd     = []string{"--profile", peerProfile, "up", "-d", peerDNSRole}
	peerDNSRemoveCmd = []string{"--profile", peerProfile, "rm", "-s", "-f", peerDNSRole}
)

// peerDNSConfigured reports whether the peer DNS service belongs in the stack:
// machine, peer-suffix and an address it can answer with. The address is
// judged again here, so a value saved before config address refused IPv6, or
// a hand-edited file, yields no service rather than one that crash-loops.
func peerDNSConfigured(cfg config.Config) bool {
	if cfg.Machine == "" || cfg.PeerSuffix == "" {
		return false
	}
	_, err := config.ParseAddress(cfg.Address)
	return err == nil
}

// composeHasPeerDNS reports whether the Compose file materialized last time
// describes the peer DNS service — the only state in which unsetting a value
// leaves one behind to remove.
func composeHasPeerDNS(stackDir string) bool {
	data, err := os.ReadFile(filepath.Join(stackDir, "docker-compose.yml"))
	return err == nil && strings.Contains(string(data), "\n  "+peerDNSRole+":\n")
}

// peerDNSService is the service appended to the materialized Compose file.
// It runs the dnsserver binary with the peer handler, published only on the
// mesh address — every interface would expose it on the LAN, and Docker
// refuses a second publish of the loopback port. It is the last top-level
// mapping's last entry, so appending keeps the file well-formed.
func peerDNSService(cfg config.Config) string {
	return fmt.Sprintf(`
  %[1]s:
    # The peer DNS service: answers %[2]s on the mesh address. Present only
    # because machine, peer-suffix and address are configured.
    image: *proximo-image
    entrypoint: ["/usr/local/bin/dnsserver"]
    profiles: [%[3]s]
    restart: unless-stopped
    logging: *proximo-logging
    environment:
      PROXIMO_DNS_ADDR: ":%[4]d"
      PROXIMO_PEER_SUBTREE: "%[2]s"
      PROXIMO_PEER_ADDRESS: "%[5]s"
    ports:
      - "%[5]s:%[4]d:%[4]d/udp"
      - "%[5]s:%[4]d:%[4]d/tcp"
    labels:
      - "proximo.role=%[1]s"
      - "proximo.image=${PROXIMO_IMAGE:-%[6]s}"
      - "proximo.version=${PROXIMO_VERSION:-dev}"
`, peerDNSRole, tls.MachineSubtree(cfg.Machine, cfg.PeerSuffix), peerProfile, peerDNSPort, cfg.Address, CanonicalImage())
}

// appendPeerDNS adds the peer DNS service to the materialized Compose file.
func appendPeerDNS(stackDir string, cfg config.Config) error {
	f, err := os.OpenFile(filepath.Join(stackDir, "docker-compose.yml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(peerDNSService(cfg)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// startPeerDNS starts the peer DNS service and, when it cannot start, warns
// and returns nothing: a peer-side failure never fails the command that serves
// .test. The one cause of a failed bind it can name is the address being held
// by no interface — the mesh client was down — which the watcher then retries.
func startPeerDNS(c Composer, dir, tld string, cfg config.Config) {
	if err := c.Compose(dir, peerDNSUpCmd...); err == nil {
		return
	}
	cause := "see the error above"
	if addrs, err := net.InterfaceAddrs(); err == nil && !config.AddressHeld(cfg.Address, addrs) {
		cause = fmt.Sprintf("%s is held by no interface of this machine; the watcher starts the service once it is", cfg.Address)
	}
	fmt.Fprintf(os.Stderr, "⚠ %s: the peer DNS service did not start (%s). .%s is unaffected.\n", peerDNSRole, cause, tld)
}

// restartPeerDNS starts the peer DNS service whenever it is created or exited.
// Docker Desktop and the mesh client both start at login, in no fixed order,
// and Docker's restart policy never retries a container left `created`. It
// runs on the watcher's fixed 30 s tick, never on an event, so a failed start
// is retried at that pace and logged once until it succeeds. It inspects no
// host interface, touches no other service, and finds nothing on a machine
// where the service does not exist.
func (w *Watcher) restartPeerDNS(ctx context.Context) {
	res, err := w.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", RoleLabel+"="+peerDNSRole),
	})
	if err != nil {
		return
	}
	for _, c := range res.Items {
		if c.Labels[RoleLabel] != peerDNSRole {
			continue
		}
		if c.State != container.StateCreated && c.State != container.StateExited {
			continue
		}
		if _, err := w.cli.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
			if !w.peerDNSFailing {
				log.Printf("proximo watcher: start %s: %v; retrying every 30s", peerDNSRole, err)
			}
			w.peerDNSFailing = true
			continue
		}
		w.peerDNSFailing = false
		log.Printf("proximo watcher: started %s", peerDNSRole)
	}
}
