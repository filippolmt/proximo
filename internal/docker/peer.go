package docker

import (
	"crypto/ecdsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/tls"
)

// proximoShareLabel opts a route in to being served on its peer names
// (docs/specs/peer-sharing.md). A pure switch: it carries no name, no policy
// and no suffix, which are machine configuration.
const proximoShareLabel = "proximo.share"

// The peer material the CLI copies into the stack's ca directory, beside the
// local CA, when — and only when — every value the peer router needs is
// configured. Its absence is the unconfigured machine: the watcher reads
// nothing, emits no peer router and issues no peer leaf.
const (
	peerFile             = "peer.json"
	peerIntermediateFile = "intermediate.pem"
	peerKeyFile          = "machine-key.pem"
)

// peerCertSuffix names a shared container's peer leaf in the certs directory,
// <safe>.peer.pem. It never matches a local <safe>.crt nor the local sweep's
// *.crt glob, so the two leaves of one route have separate lifecycles.
const (
	peerCertSuffix = ".peer.pem"
	peerKeySuffix  = ".peer-key.pem"
)

// PeerNames is what deriving a peer name needs: this machine's label and the
// Peer suffix. The zero value derives nothing.
type PeerNames struct {
	Machine string `json:"machine"`
	Suffix  string `json:"suffix"`
}

// name derives the peer name of a local host: the host with .<tld> replaced by
// .<machine>.<suffix>. It refuses a host outside the TLD, and a name DNS could
// not carry — longer than 253 octets or with a label longer than 63.
func (p PeerNames) name(host, tld string) (string, bool) {
	base, ok := strings.CutSuffix(host, "."+tld)
	if !ok {
		return "", false
	}
	n := base + "." + tls.MachineSubtree(p.Machine, p.Suffix)
	if len(n) > 253 {
		return "", false
	}
	for _, l := range strings.Split(n, ".") {
		if len(l) > 63 {
			return "", false
		}
	}
	return n, true
}

// peerHosts derives the peer names of the hosts rc serves, in order. Called on
// a resolved route, it carries the local Collision over: a host rc lost has no
// peer name either. outside lists the hosts that produce none because they lie
// outside the TLD, and tooLong those whose peer name DNS could not carry —
// label faults the watcher reports.
func (rc routedContainer) peerHosts(tld string, p PeerNames) (hosts, outside, tooLong []string) {
	for _, h := range rc.hosts {
		if !strings.HasSuffix(h, "."+tld) {
			outside = append(outside, h)
			continue
		}
		if n, ok := p.name(h, tld); ok {
			hosts = append(hosts, n)
		} else {
			tooLong = append(tooLong, h)
		}
	}
	return hosts, outside, tooLong
}

// PeerMissing lists the values a shared route needs that this machine lacks,
// in the order `proximo status` names them: the words of the config
// subcommands. An intermediate constrained to another machine label or suffix
// counts as missing.
func PeerMissing(cfg config.Config) []string {
	var missing []string
	for _, v := range []struct{ name, value string }{
		{"machine", cfg.Machine}, {"peer-suffix", cfg.PeerSuffix},
		{"address", cfg.Address}, {"team-root", cfg.TeamRoot},
	} {
		if v.value == "" {
			missing = append(missing, v.name)
		}
	}
	if _, ok := tls.IntermediateFor(cfg.Machine, cfg.PeerSuffix); !ok {
		missing = append(missing, "intermediate")
	}
	return missing
}

// peerServed reports whether the peer router may be emitted: the machine label,
// the Peer suffix, the team root and the intermediate are all configured. The
// address is not needed for it — without one the names do not resolve, which
// is a stated state rather than a reason to withhold the route.
func peerServed(missing []string) bool {
	for _, m := range missing {
		if m != "address" {
			return false
		}
	}
	return true
}

// copyPeer places the peer material into the stack's ca directory when the
// peer router may be emitted, and removes it otherwise — so withdrawing a
// value withdraws every peer router at the next `up`.
func copyPeer(caDir, certDir string, cfg config.Config) error {
	files := []string{peerFile, peerIntermediateFile, peerKeyFile}
	if certDir == "" || !peerServed(PeerMissing(cfg)) {
		for _, f := range files {
			if err := os.Remove(filepath.Join(caDir, f)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	names, err := json.Marshal(PeerNames{Machine: cfg.Machine, Suffix: cfg.PeerSuffix})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(caDir, peerFile), names, 0o644); err != nil {
		return err
	}
	for _, f := range files[1:] {
		data, err := os.ReadFile(filepath.Join(certDir, f))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(caDir, f), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// peerMaterial is what the watcher signs peer leaves with.
type peerMaterial struct {
	names        PeerNames
	intermediate *x509.Certificate
	intPEM       []byte
	key          *ecdsa.PrivateKey
	// fingerprint identifies the intermediate, so a new ceremony reissues every
	// peer leaf rather than leaving them under the old one.
	fingerprint string
}

// loadPeerMaterial reads the peer material from the stack's ca directory, or
// returns nil when any of it is absent or unreadable: the unconfigured machine.
func loadPeerMaterial(caDir string) *peerMaterial {
	raw, err := os.ReadFile(filepath.Join(caDir, peerFile))
	if err != nil {
		return nil
	}
	var names PeerNames
	if json.Unmarshal(raw, &names) != nil || names.Machine == "" || names.Suffix == "" {
		return nil
	}
	intermediate, key, err := tls.LoadCA(filepath.Join(caDir, peerIntermediateFile), filepath.Join(caDir, peerKeyFile))
	if err != nil {
		return nil
	}
	return &peerMaterial{
		names: names, intermediate: intermediate, key: key,
		intPEM:      pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediate.Raw}),
		fingerprint: fmt.Sprintf("%X", sha1.Sum(intermediate.Raw)),
	}
}

// SyncPeer brings a materialized stack's peer material in line with the
// configuration, so a value set while the stack runs takes effect at the
// watcher's next reconcile — and `proximo status`, which reads the
// configuration, never reports names the watcher does not serve. A host with
// no materialized stack has nothing to sync.
func SyncPeer(cfg config.Config) error {
	dir, err := StackDir()
	if err != nil {
		return err
	}
	caDir := filepath.Join(dir, "ca")
	if _, err := os.Stat(caDir); err != nil {
		return nil
	}
	certDir, err := tls.Dir()
	if err != nil {
		return err
	}
	return copyPeer(caDir, certDir, cfg)
}
