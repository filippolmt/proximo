// Package docker materializes and drives the embedded reverse-proxy stack
// (Traefik + DNS server + network watcher) via `docker compose`, and hosts the
// network-attach watcher used inside the stack.
package docker

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/version"
)

//go:embed all:assets
var assets embed.FS

// Sentinels replaced with configured values when assets are materialized.
const (
	tldSentinel     = "__TLD__"
	dnsPortSentinel = "__DNSPORT__"
)

// StackDir returns the directory the embedded stack is materialized into.
func StackDir() (string, error) {
	return config.SubDir("stack")
}

// Materialize writes the embedded stack assets to disk, substituting the TLD,
// copies the TLS material from certDir into the stack, writes the compose
// environment file, and returns the stack directory.
func Materialize(tld, certDir string) (string, error) {
	dir, err := StackDir()
	if err != nil {
		return "", err
	}
	walkErr := fs.WalkDir(assets, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("assets", path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dest := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		if sentinel := []byte(tldSentinel); bytes.Contains(data, sentinel) {
			data = bytes.ReplaceAll(data, sentinel, []byte(tld))
		}
		if sentinel := []byte(dnsPortSentinel); bytes.Contains(data, sentinel) {
			data = bytes.ReplaceAll(data, sentinel, []byte(strconv.Itoa(config.DNSPort)))
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if walkErr != nil {
		return "", walkErr
	}
	if err := copyCA(dir, certDir); err != nil {
		return "", err
	}
	if err := writeEnv(dir, tld); err != nil {
		return "", err
	}
	if err := writeDevOverride(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// devDockerfile is the local-source build file at the PROXIMO_SRC repo root.
const devDockerfile = "Dockerfile.dev"

// writeDevOverride wires a docker-compose.override.yml that builds the dns and
// watcher images from a local checkout when PROXIMO_SRC points at the source
// tree. When PROXIMO_SRC is unset it removes any stale override so the published
// images (go install <module>@<ref>) are used instead. The override is loaded
// automatically by `docker compose` alongside the base file.
func writeDevOverride(stackDir string) error {
	overridePath := filepath.Join(stackDir, "docker-compose.override.yml")

	src := os.Getenv("PROXIMO_SRC")
	if src == "" {
		if err := os.Remove(overridePath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	const svc = `  %s:
    build:
      context: %s
      dockerfile: %s
      args:
        CMD: %s
`
	content := "services:\n" +
		fmt.Sprintf(svc, "dns", abs, devDockerfile, "dnsserver") +
		fmt.Sprintf(svc, "watcher", abs, devDockerfile, "watcher")
	return os.WriteFile(overridePath, []byte(content), 0o644)
}

// copyCA places the CA certificate and key into the stack so the watcher can
// mount them and mint per-host certificates. Missing files are skipped (e.g.
// `proximo up` before `install`), in which case the watcher runs without TLS.
func copyCA(stackDir, certDir string) error {
	dest := filepath.Join(stackDir, "ca")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if certDir == "" {
		return nil
	}
	for _, name := range []string{"ca.pem", "ca-key.pem"} {
		data, err := os.ReadFile(filepath.Join(certDir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dest, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func writeEnv(stackDir, tld string) error {
	// For released binaries version.Version is the tag (e.g. v1.2.3), which
	// `go install ...@<tag>` resolves directly. For local/dev builds fall back
	// to the default branch, which resolves on a public repo without a tag.
	ref := version.Version
	if ref == "" || ref == "dev" {
		ref = "main"
	}
	content := fmt.Sprintf("PROXIMO_TLD=%s\nPROXIMO_REF=%s\n", tld, ref)
	return os.WriteFile(filepath.Join(stackDir, ".env"), []byte(content), 0o644)
}

func compose(stackDir string, args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = stackDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Up materializes the stack and brings it up (building images as needed).
func Up(tld, certDir string) error {
	dir, err := Materialize(tld, certDir)
	if err != nil {
		return err
	}
	return compose(dir, "up", "-d", "--build")
}

// Down stops and removes the stack without touching host configuration.
func Down() error {
	dir, err := StackDir()
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(dir, "docker-compose.yml")); statErr != nil {
		// Nothing materialized; nothing to tear down.
		return nil
	}
	return compose(dir, "down")
}
