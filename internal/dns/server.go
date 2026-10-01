// Package dns implements the wildcard DNS server and the host-resolver wiring
// that route the configured TLD to the local proximo.
package dns

import (
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// DefaultUpstream is used when no upstream resolver is configured.
var DefaultUpstream = []string{"1.1.1.1:53", "8.8.8.8:53"}

// Server answers wildcard queries for a TLD with a loopback address and
// forwards every other query to an upstream resolver.
type Server struct {
	// TLD is the routed top-level domain without a leading dot (e.g. "test").
	TLD string
	// Addr is the listen address (e.g. ":5353").
	Addr string
	// Upstream is the list of upstream resolvers as host:port.
	Upstream []string
	// PeerSubtree, when set, makes this the peer listener instead: it answers
	// this machine's peer subtree (<machine>.<suffix>) with PeerAddress and
	// refuses every other name. The loopback listener never sets it — the two
	// are kept apart by container port, since Docker Desktop rewrites the
	// source address of a published port.
	PeerSubtree string
	PeerAddress net.IP

	answer net.IP
	client *dns.Client
}

// Run starts the UDP and TCP listeners and blocks until one of them fails.
func (s *Server) Run() error {
	if s.answer == nil {
		s.answer = net.IPv4(127, 0, 0, 1)
	}
	if s.Addr == "" {
		s.Addr = ":5353"
	}
	if len(s.Upstream) == 0 {
		s.Upstream = DefaultUpstream
	}
	if s.client == nil {
		s.client = &dns.Client{Timeout: 5 * time.Second}
	}

	mux := dns.NewServeMux()
	if s.PeerSubtree != "" {
		mux.HandleFunc(".", s.handlePeer)
	} else {
		mux.HandleFunc(".", s.handle)
	}

	errCh := make(chan error, 2)
	for _, network := range []string{"udp", "tcp"} {
		srv := &dns.Server{Addr: s.Addr, Net: network, Handler: mux}
		go func() { errCh <- srv.ListenAndServe() }()
	}
	return <-errCh
}

func (s *Server) handle(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		dns.HandleFailed(w, r)
		return
	}
	q := r.Question[0]
	name := strings.ToLower(q.Name)
	tld := dns.Fqdn(s.TLD)
	if name == tld || strings.HasSuffix(name, "."+tld) {
		s.answerLocal(w, r, q)
		return
	}
	s.forward(w, r)
}

func (s *Server) answerLocal(w dns.ResponseWriter, r *dns.Msg, q dns.Question) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	if q.Qtype == dns.TypeA {
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
			A:   s.answer,
		})
	}
	// Other query types (AAAA, etc.) return NOERROR with no records: the TLD
	// is IPv4 loopback only.
	_ = w.WriteMsg(m)
}

func (s *Server) forward(w dns.ResponseWriter, r *dns.Msg) {
	for _, up := range s.Upstream {
		resp, _, err := s.client.Exchange(r, up)
		if err == nil && resp != nil {
			_ = w.WriteMsg(resp)
			return
		}
	}
	m := new(dns.Msg)
	m.SetRcode(r, dns.RcodeServerFailure)
	_ = w.WriteMsg(m)
}

// handlePeer answers A for any name under the peer subtree, at any depth, with
// PeerAddress, and every other type there with NOERROR and no records, as the
// loopback handler does for the TLD. Any name outside it is REFUSED: no
// upstream forwarding and no .test, so a publishing machine is never a
// recursive resolver for the mesh.
func (s *Server) handlePeer(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		dns.HandleFailed(w, r)
		return
	}
	q := r.Question[0]
	name := strings.ToLower(q.Name)
	subtree := dns.Fqdn(strings.ToLower(s.PeerSubtree))
	m := new(dns.Msg)
	if name != subtree && !strings.HasSuffix(name, "."+subtree) {
		m.SetRcode(r, dns.RcodeRefused)
		_ = w.WriteMsg(m)
		return
	}
	m.SetReply(r)
	m.Authoritative = true
	if q.Qtype == dns.TypeA {
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
			A:   s.PeerAddress,
		})
	}
	_ = w.WriteMsg(m)
}
