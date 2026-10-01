package dns

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// The peer listener answers this machine's subtree, at any depth, with the
// configured address; every other name is REFUSED — no forwarding, no .test.
func TestPeerServer(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	srv := &Server{Addr: addr, PeerSubtree: "studio-01.mesh.internal", PeerAddress: net.ParseIP("100.89.88.2")}
	go func() { _ = srv.Run() }()

	c := &dns.Client{Timeout: 2 * time.Second}
	ask := func(name string, qtype uint16) *dns.Msg {
		t.Helper()
		msg := new(dns.Msg)
		msg.SetQuestion(name, qtype)
		for range 50 {
			resp, _, err := c.Exchange(msg, addr)
			if err == nil && resp != nil {
				return resp
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("no answer for %s", name)
		return nil
	}
	for _, name := range []string{"api.studio-01.mesh.internal.", "api.shop.studio-01.mesh.internal.", "proximo-doctor.studio-01.mesh.internal.", "API.Studio-01.Mesh.Internal."} {
		resp := ask(name, dns.TypeA)
		if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 || !resp.Answer[0].(*dns.A).A.Equal(net.ParseIP("100.89.88.2")) {
			t.Errorf("%s: %v", name, resp)
		}
	}
	if resp := ask("api.studio-01.mesh.internal.", dns.TypeAAAA); resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 0 {
		t.Errorf("AAAA: %v, want NOERROR with no records", resp)
	}
	for _, name := range []string{"api.test.", "api.studio-02.mesh.internal.", "example.com.", "xstudio-01.mesh.internal."} {
		if resp := ask(name, dns.TypeA); resp.Rcode != dns.RcodeRefused || len(resp.Answer) != 0 {
			t.Errorf("%s: rcode %d, want REFUSED", name, resp.Rcode)
		}
	}
}
