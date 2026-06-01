package dns

import (
	"fmt"
	"net"

	"github.com/filippolmt/proximo/internal/config"
)

// CheckPortFree verifies that the DNS publish port (127.0.0.1:<DNSPort>/udp) is
// not already bound by another process, so install can abort before making any
// host changes.
func CheckPortFree() error {
	addr := fmt.Sprintf("127.0.0.1:%d", config.DNSPort)
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("DNS port %s/udp is already in use: %w", addr, err)
	}
	return conn.Close()
}
