package observability

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureSecretWritesAndReuses covers the secret contract: first call
// generates a 0600 file, the second call reuses the same value (idempotent), and
// Purge removes the secret together with both generated env files.
func TestEnsureSecretWritesAndReuses(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	first, err := EnsureSecret()
	if err != nil {
		t.Fatalf("EnsureSecret: %v", err)
	}
	if first == "" {
		t.Fatal("EnsureSecret returned an empty password")
	}

	path, err := SecretPath()
	if err != nil {
		t.Fatalf("SecretPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat secret: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("secret permissions = %o, want 600", perm)
	}

	second, err := EnsureSecret()
	if err != nil {
		t.Fatalf("EnsureSecret (reuse): %v", err)
	}
	if second != first {
		t.Errorf("EnsureSecret regenerated: %q != %q", second, first)
	}
}

func TestPurgeRemovesSecretAndEnvFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := EnsureSecret(); err != nil {
		t.Fatalf("EnsureSecret: %v", err)
	}
	stackDir := t.TempDir()
	if err := WriteHubEnv(stackDir, "pw"); err != nil {
		t.Fatalf("WriteHubEnv: %v", err)
	}
	if err := WriteAgentEnv(stackDir, "http://beszel:8090", "ssh-key", "tok"); err != nil {
		t.Fatalf("WriteAgentEnv: %v", err)
	}

	if err := Purge(stackDir); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	secret, _ := SecretPath()
	for _, p := range []string{
		secret,
		filepath.Join(stackDir, hubEnvFile),
		filepath.Join(stackDir, agentEnvFile),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("Purge left %s (err=%v)", p, err)
		}
	}

	// Purge is idempotent: a second call with nothing to remove is a no-op.
	if err := Purge(stackDir); err != nil {
		t.Errorf("second Purge: %v", err)
	}
}

func TestWriteHubEnvPermissions(t *testing.T) {
	stackDir := t.TempDir()
	if err := WriteHubEnv(stackDir, "secret-pw"); err != nil {
		t.Fatalf("WriteHubEnv: %v", err)
	}
	info, err := os.Stat(filepath.Join(stackDir, hubEnvFile))
	if err != nil {
		t.Fatalf("stat hub env: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("hub env permissions = %o, want 600", perm)
	}
}

func TestEmail(t *testing.T) {
	if got := Email("test"); got != "proximo@test" {
		t.Errorf("Email(test) = %q, want proximo@test", got)
	}
	if got := Email("dev"); got != "proximo@dev" {
		t.Errorf("Email(dev) = %q, want proximo@dev", got)
	}
}
