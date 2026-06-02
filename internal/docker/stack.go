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
	"strings"

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
	// PROXIMO_VERSION stamps the materialized services with the CLI version (a
	// proximo.version label) so the running stack's version can be read back for
	// skew detection; PROXIMO_REF is the canonical module ref the images build at.
	content := fmt.Sprintf("PROXIMO_TLD=%s\nPROXIMO_REF=%s\nPROXIMO_VERSION=%s\n",
		tld, moduleRef(version.Version), version.Version)
	return os.WriteFile(filepath.Join(stackDir, ".env"), []byte(content), 0o644)
}

// moduleRef turns the build version into a ref that `go install ...@<ref>` can
// resolve. Released binaries carry a bare semver because GoReleaser's
// {{ .Version }} strips the leading "v"; restore it so the value is a canonical
// module version (vX.Y.Z) the module proxy serves directly — a bare "0.1.0" is
// treated as a VCS query and fails when git is unavailable. Local/dev builds
// ("dev" or empty) fall back to the default branch, which resolves without a tag.
func moduleRef(v string) string {
	switch {
	case v == "" || v == "dev":
		return "main"
	case strings.HasPrefix(v, "v"):
		return v
	default:
		return "v" + v
	}
}

func compose(stackDir string, args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = stackDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ConvergeOpts tunes how Converge rebuilds the stack.
type ConvergeOpts struct {
	// Force rebuilds the in-stack images without the build cache, regardless of
	// whether the ref is mobile.
	Force bool
}

// Converge materializes the embedded stack to disk and brings it up with
// ref-aware build freshness. It is the single bring-up path shared by `proximo
// up`, `install`, and `proximo update`, so "update now" and "update on next
// start" cannot drift. The bring-up always re-pulls (so the pinned Traefik tag
// picks up security patches); a mobile ref or Force prepends a --no-cache
// rebuild of the buildable services to defeat a stale `go install @<ref>` layer.
func Converge(tld, certDir string, opts ConvergeOpts) error {
	dir, err := Materialize(tld, certDir)
	if err != nil {
		return err
	}
	for _, args := range composeConvergeCmds(moduleRef(version.Version), opts.Force) {
		if err := compose(dir, args...); err != nil {
			return err
		}
	}
	return nil
}

// Up materializes the stack and brings it up (building images as needed),
// applying any pending convergence to the installed CLI version.
func Up(tld, certDir string) error {
	return Converge(tld, certDir, ConvergeOpts{})
}

// isMobileRef reports whether ref is a mutable ref whose build cache must be
// busted on converge. main, dev, and empty resolve to a moving target; a
// canonical release tag (vX.Y.Z) is immutable and safe to cache.
func isMobileRef(ref string) bool {
	switch ref {
	case "", "main", "dev":
		return true
	default:
		return false
	}
}

// composeConvergeCmds returns the docker compose command(s) Converge runs, in
// order, for a given module ref and force flag. The bring-up always re-pulls
// (--pull always) so the pinned Traefik tag picks up security patches. A mobile
// ref (main/dev/empty) or force prepends a --no-cache rebuild of the buildable
// services (`docker compose up` has no --no-cache), defeating a stale
// `go install @<ref>` layer. An immutable tag reuses the cache — a new tag
// changes the build arg and cache-misses naturally.
func composeConvergeCmds(ref string, force bool) [][]string {
	var cmds [][]string
	if force || isMobileRef(ref) {
		cmds = append(cmds, []string{"build", "--no-cache", "--pull", "dns", "watcher"})
	}
	cmds = append(cmds, []string{"up", "-d", "--build", "--pull", "always"})
	return cmds
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
