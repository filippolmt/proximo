// Command dnsserver is the wildcard DNS server that runs inside the proximo
// stack. It answers *.<tld> with 127.0.0.1 and forwards everything else
// upstream. It is built into a container image and published on
// 127.0.0.1:5353/udp.
package main

import (
	"log"
	"os"
	"strings"

	"github.com/filippolmt/proximo/internal/dns"
)

func main() {
	tld := getenv("PROXIMO_TLD", "test")
	addr := getenv("PROXIMO_DNS_ADDR", ":5353")

	var upstream []string
	if v := os.Getenv("PROXIMO_DNS_UPSTREAM"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				upstream = append(upstream, p)
			}
		}
	}

	srv := &dns.Server{TLD: tld, Addr: addr, Upstream: upstream}
	log.Printf("proximo dns: serving *.%s -> 127.0.0.1 on %s", tld, addr)
	if err := srv.Run(); err != nil {
		log.Fatalf("proximo dns: %v", err)
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
