// Command peerrelay is the peer relay: it accepts TCP on this machine's mesh
// address and copies every connection, byte for byte, to Traefik's websecure
// entrypoint over the Stack network. TLS terminates at Traefik, so the SNI a
// colleague's browser sends arrives intact. It exists so Traefik itself can
// stay on the loopback (docs/adr/0014-the-stack-listens-on-the-loopback.md).
package main

import (
	"io"
	"log"
	"net"
	"os"
	"time"
)

func main() {
	listen := getenv("PROXIMO_RELAY_LISTEN", ":443")
	target := getenv("PROXIMO_RELAY_TARGET", "traefik:443")
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatalf("proximo peer-relay: %v", err)
	}
	log.Printf("proximo peer-relay: relaying %s to %s", listen, target)
	serve(ln, target)
}

// serve relays every connection ln accepts to target until ln is closed.
func serve(ln net.Listener, target string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		go relay(c, target)
	}
}

func relay(c net.Conn, target string) {
	defer c.Close()
	up, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		log.Printf("proximo peer-relay: dial %s: %v", target, err)
		return
	}
	defer up.Close()
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		// Half-close so the other direction can still drain its last bytes.
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(up, c)
	go pipe(c, up)
	<-done
	<-done
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
