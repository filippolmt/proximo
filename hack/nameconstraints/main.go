// Command nameconstraints mints the X.509 name-constraint fixtures that the
// "Two machines, proved by hand" proof needs, serves them over TLS so a real
// browser can be pointed at them, and verifies them with Go's own
// crypto/x509.
//
// It is a measurement harness, not part of proximo: the certificates it mints
// mirror the shape decided on "Where the team root lives, and who signs an
// intermediate" — a name-constrained team root, one intermediate per machine,
// leaves signed locally — so that what a browser does to these fixtures is
// what it would do to the real thing.
//
// The proof is a comparison, never a single reading. The in-subtree case is
// the control: without it, a rejection of an out-of-subtree leaf is equally
// well explained by the root not being trusted at all.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The subject names carry the case they belong to, so a certificate opened in
// a browser's certificate viewer says which fixture it is without a lookup.
const (
	rootCN = "proof team root (name-constrained)"
	intCN  = "proof machine intermediate"
)

// A case is one fixture: a leaf, the URL host a browser must use to reach it,
// and what the amendment predicts the platform does with it.
type kase struct {
	stem   string
	host   string   // the host in the URL — must match a SAN, or the reading is a name mismatch
	dns    []string // DNS SANs
	ips    []net.IP // IP SANs
	port   int
	expect string // "accepted" or "rejected"
	why    string
}

func cases(machine, suffix string) []kase {
	inSubtree := "app." + machine + "." + suffix
	return []kase{
		{
			stem:   "in-subtree",
			host:   inSubtree,
			dns:    []string{inSubtree},
			port:   8443,
			expect: "accepted",
			why:    "control: the chain is trusted and the name is inside the permitted subtree",
		},
		{
			stem:   "dns-out",
			host:   "out-of-subtree.example.com",
			dns:    []string{"out-of-subtree.example.com"},
			port:   8444,
			expect: "rejected",
			why:    "DNS name outside the root's PermittedDNSDomains — the amendment's core claim",
		},
		{
			stem:   "ip-san",
			host:   "127.0.0.1",
			ips:    []net.IP{net.IPv4(127, 0, 0, 1)},
			port:   8445,
			expect: "rejected",
			why:    "SAN is an IP address: unrestricted under RFC 5280 unless ExcludedIPRanges says otherwise",
		},
	}
}

func main() {
	out := flag.String("out", "proof-out", "directory the PEM fixtures are written to and read from")
	suffix := flag.String("suffix", "mesh.internal", "peer suffix the team root is constrained to")
	machine := flag.String("machine", "machine-a", "machine label the intermediate is constrained to")
	mint := flag.Bool("mint", false, "mint the fixtures")
	verify := flag.Bool("verify", false, "verify the fixtures with Go's crypto/x509")
	serve := flag.Bool("serve", false, "serve each leaf over TLS on its own port")
	flag.Parse()

	if !*mint && !*verify && !*serve {
		log.Fatal("nothing to do: pass -mint, -verify or -serve")
	}

	ks := cases(*machine, *suffix)
	if *mint {
		if err := doMint(*out, *suffix, *machine, ks); err != nil {
			log.Fatalf("mint: %v", err)
		}
	}
	if *verify {
		if err := doVerify(*out, ks); err != nil {
			log.Fatalf("verify: %v", err)
		}
	}
	if *serve {
		if err := doServe(*out, ks); err != nil {
			log.Fatalf("serve: %v", err)
		}
	}
}

// doMint writes the root, the intermediate and one leaf per case. The root's
// constraints exclude every name type RFC 5280 leaves unrestricted by
// omission, which is what the IP-SAN case exists to prove.
func doMint(dir, suffix, machine string, ks []kase) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	now := time.Now()
	_, allIPv4, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		return err
	}
	_, allIPv6, err := net.ParseCIDR("::/0")
	if err != nil {
		return err
	}

	rootTmpl := &x509.Certificate{
		Subject:               pkix.Name{CommonName: rootCN},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,

		PermittedDNSDomains:         []string{"." + suffix},
		PermittedDNSDomainsCritical: true,
		// An empty constraint matches every name of its type, so these three
		// exclude the name types a DNS-only constraint would leave open.
		ExcludedIPRanges:       []*net.IPNet{allIPv4, allIPv6},
		ExcludedEmailAddresses: []string{""},
		ExcludedURIDomains:     []string{""},
	}
	rootCert, rootKey, err := issue(rootTmpl, nil, nil, filepath.Join(dir, "root.pem"), filepath.Join(dir, "root-key.pem"))
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}

	intTmpl := &x509.Certificate{
		Subject:               pkix.Name{CommonName: intCN + " " + machine},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,

		PermittedDNSDomains:         []string{"." + machine + "." + suffix},
		PermittedDNSDomainsCritical: true,
		ExcludedIPRanges:            []*net.IPNet{allIPv4, allIPv6},
		ExcludedEmailAddresses:      []string{""},
		ExcludedURIDomains:          []string{""},
	}
	intCert, intKey, err := issue(intTmpl, rootCert, rootKey, filepath.Join(dir, "int.pem"), filepath.Join(dir, "int-key.pem"))
	if err != nil {
		return fmt.Errorf("intermediate: %w", err)
	}

	for _, k := range ks {
		leafTmpl := &x509.Certificate{
			Subject:     pkix.Name{CommonName: k.host},
			DNSNames:    k.dns,
			IPAddresses: k.ips,
			NotBefore:   now.Add(-time.Hour),
			NotAfter:    now.AddDate(0, 0, 397),
			KeyUsage:    x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		leafPath := filepath.Join(dir, k.stem+".pem")
		leaf, _, err := issue(leafTmpl, intCert, intKey, leafPath, filepath.Join(dir, k.stem+"-key.pem"))
		if err != nil {
			return fmt.Errorf("%s: %w", k.stem, err)
		}
		// A chain file, so a client given only the root can be handed the
		// intermediate the way Traefik would serve it.
		chain := append(encode("CERTIFICATE", leaf.Raw), encode("CERTIFICATE", intCert.Raw)...)
		if err := os.WriteFile(filepath.Join(dir, k.stem+"-chain.pem"), chain, 0o644); err != nil {
			return err
		}
		fmt.Printf("minted %-11s host=%-30s expect=%s\n", k.stem, k.host, k.expect)
	}
	fmt.Printf("\nroot constrained to %q, intermediate to %q\nfixtures in %s\n",
		"."+suffix, "."+machine+"."+suffix, dir)
	return nil
}

// doVerify is the reading Go's crypto/x509 contributes: it needs no browser
// and no trust store, so it separates "the fixtures are wrong" from "this
// platform does not enforce constraints".
func doVerify(dir string, ks []kase) error {
	root, err := loadCert(filepath.Join(dir, "root.pem"))
	if err != nil {
		return err
	}
	inter, err := loadCert(filepath.Join(dir, "int.pem"))
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	inters := x509.NewCertPool()
	inters.AddCert(inter)

	fmt.Println("\nGo crypto/x509:")
	failed := false
	for _, k := range ks {
		leaf, err := loadCert(filepath.Join(dir, k.stem+".pem"))
		if err != nil {
			return err
		}
		_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inters})
		got := "accepted"
		detail := ""
		if err != nil {
			got = "rejected"
			detail = err.Error()
		}
		mark := "ok"
		if got != k.expect {
			mark = "UNEXPECTED"
			failed = true
		}
		fmt.Printf("  %-11s expect=%-8s got=%-8s %s\n", k.stem, k.expect, got, mark)
		if detail != "" {
			fmt.Printf("               %s\n", detail)
		}
	}
	if failed {
		return fmt.Errorf("a case did not behave as the amendment predicts")
	}
	return nil
}

// doServe listens on one port per case so a browser reaches each fixture at a
// URL whose host matches that fixture's SAN. Anything else would read as a
// name mismatch rather than as a constraint decision.
func doServe(dir string, ks []kase) error {
	var wg sync.WaitGroup
	for _, k := range ks {
		cert, err := tls.LoadX509KeyPair(
			filepath.Join(dir, k.stem+"-chain.pem"),
			filepath.Join(dir, k.stem+"-key.pem"),
		)
		if err != nil {
			return fmt.Errorf("%s: %w", k.stem, err)
		}
		this := k
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "case %s\nexpected: %s\nwhy: %s\nrequested host: %s\n",
				this.stem, this.expect, this.why, r.Host)
		})
		srv := &http.Server{
			Addr:              fmt.Sprintf(":%d", k.port),
			Handler:           mux,
			TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			ReadHeaderTimeout: 10 * time.Second,
		}
		fmt.Printf("serving %-11s https://%s:%d/  (expect %s)\n", k.stem, k.host, k.port, k.expect)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.ListenAndServeTLS("", ""); err != nil {
				log.Printf("%s: %v", this.stem, err)
			}
		}()
	}
	wg.Wait()
	return nil
}

// issue mirrors internal/tls.issue: a fresh P-256 key, a random serial, and
// both PEM files written. A nil parent self-signs with the generated key.
func issue(tmpl, parent *x509.Certificate, signer *ecdsa.PrivateKey, certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, nil, err
	}
	tmpl.SerialNumber = serial
	if parent == nil {
		parent = tmpl
	}
	if signer == nil {
		signer = key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signer)
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(certPath, encode("CERTIFICATE", der), 0o644); err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(keyPath, encode("PRIVATE KEY", keyDER), 0o600); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func encode(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}

func loadCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	return x509.ParseCertificate(block.Bytes)
}
