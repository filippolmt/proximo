package dns

import (
	"fmt"
	"testing"

	"github.com/filippolmt/proximo/internal/config"
)

// The resolver wiring itself is privileged/OS-bound (sudo, systemctl) and out of
// unit-test scope; only the pure path builders are covered here.

func TestResolverFileDarwin(t *testing.T) {
	if got, want := resolverFileDarwin("test"), "/etc/resolver/test"; got != want {
		t.Fatalf("resolverFileDarwin = %q, want %q", got, want)
	}
}

func TestResolvedDropInPath(t *testing.T) {
	want := fmt.Sprintf("%s/proximo-dev.conf", resolvedDropInDir)
	if got := resolvedDropInPath("dev"); got != want {
		t.Fatalf("resolvedDropInPath = %q, want %q", got, want)
	}
}

func TestResolverPathsAreTLDSpecific(t *testing.T) {
	// Distinct TLDs must yield distinct files so concurrent TLDs never collide.
	if resolverFileDarwin("a") == resolverFileDarwin("b") {
		t.Error("darwin resolver files collide across TLDs")
	}
	if resolvedDropInPath("a") == resolvedDropInPath("b") {
		t.Error("linux drop-in paths collide across TLDs")
	}
	// Sanity: the constant is referenced so the test tracks the real default.
	if config.DNSPort == 0 {
		t.Error("DNSPort constant unexpectedly zero")
	}
}
