package dns

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestServerAnswersWildcardLocally(t *testing.T) {
	// Grab a free loopback UDP port, then hand it to the server.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()

	srv := &Server{TLD: "test", Addr: addr, Upstream: []string{"127.0.0.1:9"}}
	go func() { _ = srv.Run() }()

	c := &dns.Client{Timeout: 2 * time.Second}
	msg := new(dns.Msg)
	msg.SetQuestion("web.test.", dns.TypeA)

	var resp *dns.Msg
	for range 50 {
		resp, _, err = c.Exchange(msg, addr)
		if err == nil && resp != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(resp.Answer))
	}
	a, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}
	if a.A.String() != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1, got %s", a.A)
	}
}
