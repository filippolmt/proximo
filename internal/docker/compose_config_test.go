package docker

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The materialized Compose file is assembled from an embedded asset and Go
// templates (the peer services), and the other tests read it as text. This one
// hands it to Compose itself, every profile enabled, so an indentation or key
// mistake fails here rather than at the first `proximo up` that reaches it.
// Skipped where the docker CLI is absent (`make test` runs in golang:alpine);
// CI's go test runs on a host that has it.
func TestComposeAcceptsTheMaterializedFile(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	for _, sharing := range []bool{false, true} {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		if sharing {
			savePeerDNSConfig(t)
		}
		dir, err := Materialize("test", "", "ghcr.io/filippolmt/proximo:v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("docker", "compose", "--project-directory", dir,
			"-f", filepath.Join(dir, "docker-compose.yml"),
			"--profile", peerProfile, "--profile", "observability", "config", "-q")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("sharing=%v: docker compose config: %v\n%s", sharing, err, out)
		}
	}
}
