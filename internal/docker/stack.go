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
	"regexp"
	"strconv"
	"strings"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/observability"
	"github.com/filippolmt/proximo/internal/version"
)

//go:embed all:assets
var assets embed.FS

// Sentinels replaced with configured values when assets are materialized.
const (
	tldSentinel         = "__TLD__"
	dnsPortSentinel     = "__DNSPORT__"
	dataDirSentinel     = "__DATADIR__"
	obsEmailSentinel    = "__OBS_USER_EMAIL__"
	obsHubPortSentinel  = "__OBS_HUBPORT__"
	inspectPortSentinel = "__INSPECTPORT__"
	watcherPortSentinel = "__WATCHERPORT__"
	imageSentinel       = "__IMAGE__"
)

// Observability profile + service names, used to bring the opt-in services up
// out-of-band from the core converge.
const (
	observabilityProfile = "observability"
	svcDozzle            = "dozzle"
	svcBeszel            = "beszel"
	svcBeszelAgent       = "beszel-agent"
)

// StackDir returns the directory the embedded stack is materialized into.
func StackDir() (string, error) {
	return config.SubDir("stack")
}

// Materialize writes the embedded stack assets to disk, substituting the TLD
// and the host data-dir path, creates the bind-mounted data subdirectories,
// copies the TLS material from certDir into the stack, writes the compose
// environment file pinning the stack image, and returns the stack directory.
func Materialize(tld, certDir, image string) (string, error) {
	dir, err := StackDir()
	if err != nil {
		return "", err
	}
	dataDir, err := config.DataDir()
	if err != nil {
		return "", err
	}
	// Create the bind-mount sources so `docker compose` resolves the mounts and
	// the host dirs are owned by the user (not root-created by the daemon, which
	// would later block uninstall's RemoveHome). The watcher regenerates
	// routes/certs into data/traefik on its first reconcile; the metrics hub
	// persists into data/beszel when the observability profile is active (an empty
	// dir is harmless when it is not).
	for _, sub := range []string{"traefik", "beszel"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
			return "", err
		}
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
		data = replaceSentinels(data, tld, config.DNSPort, dataDir)
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
	if err := writeEnv(dir, tld, image); err != nil {
		return "", err
	}
	if err := writeDevOverride(dir, image); err != nil {
		return "", err
	}
	return dir, nil
}

// replaceSentinels substitutes the materialization sentinels (__TLD__,
// __DNSPORT__, __DATADIR__ — the absolute host data-dir path, the observability
// user email, the observability hub port, the Inspection read-API port, the
// watcher's Incident read-API port, and the canonical stack image) in an
// embedded asset. It is deterministic
// so the substitution can be unit tested directly; the Materialize WalkDir
// closure calls it per file. Data with no sentinel is returned unchanged. The
// observability email is deterministic from the TLD (its canonical form is
// observability.Email); only the generated password is injected at runtime via
// the 0600 beszel-hub.env file. __IMAGE__ is only the compose-level fallback
// for a missing .env — the ref the services actually run comes from
// PROXIMO_IMAGE, which is what makes an override survive a boot-time restart.
func replaceSentinels(data []byte, tld string, dnsPort int, dataDir string) []byte {
	for _, s := range []struct{ sentinel, value string }{
		{tldSentinel, tld},
		{dnsPortSentinel, strconv.Itoa(dnsPort)},
		{dataDirSentinel, dataDir},
		{obsEmailSentinel, observability.Email(tld)},
		{obsHubPortSentinel, strconv.Itoa(config.ObsHubPort)},
		{inspectPortSentinel, strconv.Itoa(config.InspectAPIPort)},
		{watcherPortSentinel, strconv.Itoa(config.WatcherAPIPort)},
		{imageSentinel, CanonicalImage()},
	} {
		// Guard the substitution so an absent sentinel skips ReplaceAll's copy.
		if needle := []byte(s.sentinel); bytes.Contains(data, needle) {
			data = bytes.ReplaceAll(data, needle, []byte(s.value))
		}
	}
	return data
}

// dockerfile is the stack-image build file at the repo root. The published
// image and a PROXIMO_SRC local build come from the same file, so the two paths
// cannot drift.
const dockerfile = "Dockerfile"

// devImage is the tag a local-source build is written to. It is deliberately
// not a ghcr.io ref: nothing must ever push or pull it by accident.
const devImage = "proximo:src"

// writeDevOverride wires a docker-compose.override.yml that builds the stack
// image from the PROXIMO_SRC checkout instead of pulling it. It is written only
// when the resolved image IS the local one: an explicit --image wins over
// PROXIMO_SRC, and nothing may then rebuild over the ref the developer named.
// Otherwise any stale override is removed, so the published image is used
// again. The override is loaded automatically by `docker compose` alongside the
// base file; the services already point at ${PROXIMO_IMAGE}, which the .env has
// set to devImage, so it only has to add the build.
func writeDevOverride(stackDir, image string) error {
	overridePath := filepath.Join(stackDir, "docker-compose.override.yml")

	src := os.Getenv("PROXIMO_SRC")
	if src == "" || image != devImage {
		if err := os.Remove(overridePath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	// The three services share one build definition, so compose builds it once
	// and the other two reuse the tag.
	// The same build identity the CLI carries, so a locally built stack logs the
	// commit it was built from rather than "none". The values are quoted because
	// YAML would otherwise read the RFC 3339 date as a timestamp and hand
	// compose back a re-serialized one with spaces in it, which splits the -X
	// linker flag it ends up in.
	const svc = `  %s:
    build:
      context: %s
      dockerfile: %s
      args:
        VERSION: "%s"
        COMMIT: "%s"
        DATE: "%s"
`
	var b strings.Builder
	b.WriteString("services:\n")
	for _, name := range []string{"dns", "watcher", "inspector"} {
		fmt.Fprintf(&b, svc, name, abs, dockerfile, version.Version, version.Commit, version.Date)
	}
	return os.WriteFile(overridePath, []byte(b.String()), 0o644)
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

// imageEnvKey is the compose variable the stack image ref is passed through. It
// lives in the materialized .env, which is what makes an --image override
// sticky across container restarts at boot.
const imageEnvKey = "PROXIMO_IMAGE"

func writeEnv(stackDir, tld, image string) error {
	// PROXIMO_VERSION stamps the materialized services with the CLI version (a
	// proximo.version label) so the running stack's version can be read back for
	// skew detection; PROXIMO_IMAGE is the image the three Go services run, and
	// is stamped back as proximo.image so a stack can never run one thing and
	// declare another.
	content := fmt.Sprintf("PROXIMO_TLD=%s\n%s=%s\nPROXIMO_VERSION=%s\n",
		tld, imageEnvKey, image, version.Version)
	return os.WriteFile(filepath.Join(stackDir, ".env"), []byte(content), 0o644)
}

// EnvImage returns the stack image ref recorded in the materialized .env, or ""
// when nothing has been materialized yet. It is how `up` and `update` see the
// sticky --image override they are about to keep or clear.
func EnvImage() (string, error) {
	dir, err := StackDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if ref, ok := strings.CutPrefix(line, imageEnvKey+"="); ok {
			return strings.TrimSpace(ref), nil
		}
	}
	return "", nil
}

// imageRepo is the published multi-architecture image holding all three
// in-stack binaries. Old version tags are load-bearing: a binary installed at
// vX.Y.Z asks for that tag for as long as it stays installed.
const imageRepo = "ghcr.io/filippolmt/proximo"

// imageTag turns the build version into the published image tag. Released
// binaries carry a bare semver because GoReleaser's {{ .Version }} strips the
// leading "v"; restore it so the value is the tag the release pipeline pushed
// (vX.Y.Z). Local/dev builds ("dev" or empty) fall back to the branch tag the
// main pipeline pushes.
func imageTag(v string) string {
	switch {
	case v == "" || v == "dev":
		return "main"
	case strings.HasPrefix(v, "v"):
		return v
	default:
		return "v" + v
	}
}

// imageRef is the canonical stack image for a CLI version. proximo pins the
// exact version and never reads :latest — a floating tag would let a binary
// installed weeks ago pull services speaking a label contract it has never
// seen, in the one place skew detection cannot look.
func imageRef(v string) string {
	return imageRepo + ":" + imageTag(v)
}

// CanonicalImage is the stack image this CLI pins itself to. An --image
// override replaces it wholesale; `status` compares against this to tell the
// developer an override is in effect.
func CanonicalImage() string {
	return imageRef(version.Version)
}

func compose(stackDir string, args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = stackDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Composer runs a docker compose subcommand in a materialized stack directory.
// The production adapter (execComposer) shells out via compose(); tests pass a
// fake that records the commands so Converge's sequencing is verifiable without
// Docker. Only execution is bound — composeConvergeCmds stays the pure, tested
// source of the command sequence.
type Composer interface {
	Compose(stackDir string, args ...string) error
}

// execComposer is the production Composer: it runs `docker compose` with the
// working directory and inherited stdio that compose() configures.
type execComposer struct{}

func (execComposer) Compose(stackDir string, args ...string) error {
	return compose(stackDir, args...)
}

// defaultComposer is the Composer used by the exported bring-up/teardown
// entrypoints. Tests drive the unexported variants with a fake instead.
var defaultComposer Composer = execComposer{}

// ConvergeOpts tunes how Converge brings the stack up.
type ConvergeOpts struct {
	// Force re-pulls the stack image even when its tag is already cached. A
	// benign no-op on an immutable vX.Y.Z, and the only way to advance a mobile
	// ref that has not changed name.
	Force bool
	// Image overrides the stack image ref verbatim — a tag, a digest, or a
	// locally built image. Empty means CanonicalImage(). It replaces the whole
	// stack, never one component, and is written into the materialized .env so
	// containers restarting at boot keep it.
	Image string
}

// EffectiveImage resolves the ref a converge with these options runs. An
// explicit --image wins; then a PROXIMO_SRC checkout, which is built rather
// than pulled; otherwise the version-pinned published ref. Whatever it returns
// is what the .env records and the services stamp as proximo.image, so the
// stack can never run one image and declare another — and the CLI must resolve
// what it reports through here, not re-derive it, for the same reason.
func (o ConvergeOpts) EffectiveImage() string {
	switch {
	case o.Image != "":
		return o.Image
	case os.Getenv("PROXIMO_SRC") != "":
		return devImage
	default:
		return CanonicalImage()
	}
}

// Converge materializes the embedded stack to disk and brings it up with
// ref-aware pull freshness. It is the single bring-up path shared by `proximo
// up`, `install`, and `proximo update`, so "update now" and "update on next
// start" cannot drift.
func Converge(tld, certDir string, opts ConvergeOpts) error {
	return convergeWith(defaultComposer, tld, certDir, opts)
}

// convergeWith is Converge with the Composer injected, so a fake composer can
// assert the issued command sequence for mobile/immutable refs and Force
// without Docker.
func convergeWith(c Composer, tld, certDir string, opts ConvergeOpts) error {
	_, err := convergeCore(c, tld, certDir, opts)
	return err
}

// convergeCore materializes the stack and runs the core converge, returning the
// stack directory so a caller can stage further compose commands into the same
// project. It is the one place the effective image is resolved and the one
// place a failure becomes a Remedy — which is why the observability bring-up
// runs outside it: those services run their own upstream images, and the stack
// image has nothing to do with them failing.
func convergeCore(c Composer, tld, certDir string, opts ConvergeOpts) (string, error) {
	image := opts.EffectiveImage()
	dir, err := Materialize(tld, certDir, image)
	if err != nil {
		return "", err
	}
	for _, args := range composeConvergeCmds(image, opts.Force) {
		if err := c.Compose(dir, args...); err != nil {
			return "", remedyFor(image, err)
		}
	}
	return dir, nil
}

// imagePresent reports whether the image is already on the host. A var so the
// remedy path is testable without Docker.
var imagePresent = func(ref string) bool {
	return exec.Command("docker", "image", "inspect", ref).Run() == nil
}

// remedyFor adds an actionable next step to a failed converge when the stack
// image is not on the host — the state a failed pull leaves behind. It states
// only what it can check (the image is absent) and hands over a command whose
// own output names the cause: a tag that was never published, a package that is
// no longer public, or no route to the registry at all. Other services can fail
// the same converge, so it does not claim the pull is what broke.
//
// proximo prints the remedy and stops; it deliberately does NOT fall back to
// building the image on the host. That fallback fails on one network path and
// retries on a path that needs the same network, so it usually fails twice and
// ten times slower — and in the rare case it succeeds it leaves the developer
// running an image nobody else has, without knowing.
func remedyFor(image string, err error) error {
	if image == devImage || imagePresent(image) {
		return err
	}
	return fmt.Errorf("%w\n\nthe stack image %s is not on this host.\nRemedy: docker pull %s",
		err, image, image)
}

// ConvergeObservability brings the stack up with the opt-in observability
// profile active. It runs the normal core converge, brings up the log viewer and
// metrics hub, runs the supplied bootstrap (which must write the agent env file
// once the hub is reachable), then brings up the metrics agent. Sequencing the
// agent after the bootstrap guarantees it never starts without its hub
// credentials (design D4). bootstrap may be nil (e.g. in tests).
func ConvergeObservability(tld, certDir string, opts ConvergeOpts, bootstrap func(stackDir string) error) error {
	return convergeObservabilityWith(defaultComposer, tld, certDir, opts, bootstrap)
}

// convergeObservabilityWith is ConvergeObservability with the Composer injected
// so a fake composer can assert the staged command sequence without Docker. The
// default `up` carries no --profile flag (core only); only the hub and agent
// bring-ups activate the observability profile.
func convergeObservabilityWith(c Composer, tld, certDir string, opts ConvergeOpts, bootstrap func(stackDir string) error) error {
	dir, err := convergeCore(c, tld, certDir, opts)
	if err != nil {
		return err
	}
	for _, args := range composeObservabilityHubCmds() {
		if err := c.Compose(dir, args...); err != nil {
			return err
		}
	}
	if bootstrap != nil {
		if err := bootstrap(dir); err != nil {
			return err
		}
	}
	for _, args := range composeObservabilityAgentCmds() {
		if err := c.Compose(dir, args...); err != nil {
			return err
		}
	}
	return nil
}

// composeObservabilityHubCmds brings up the hub-side observability services (log
// viewer + metrics hub) under the observability profile. The agent is deferred
// to composeObservabilityAgentCmds, after the bootstrap. The bring-up re-pulls so
// the pinned images pick up rebuilds.
func composeObservabilityHubCmds() [][]string {
	return [][]string{
		{"--profile", observabilityProfile, "up", "-d", "--pull", "always", svcDozzle, svcBeszel},
	}
}

// composeObservabilityAgentCmds brings up the metrics agent under the
// observability profile, once the bootstrap has written its env file.
func composeObservabilityAgentCmds() [][]string {
	return [][]string{
		{"--profile", observabilityProfile, "up", "-d", svcBeszelAgent},
	}
}

// semverTag matches a canonical release tag (vX.Y.Z, optionally pre-released):
// the only tag family the release pipeline publishes and never moves.
var semverTag = regexp.MustCompile(`^v\d+\.\d+\.\d+`)

// isMobileRef reports whether an image ref can change under a fixed name, and
// so must be re-pulled on every converge. A digest pins bytes; a canonical
// release tag (vX.Y.Z) is published once and never moved. Everything else —
// main, sha-<short>, latest, an untagged ref, a locally built tag — is treated
// as mobile, because pulling too often only costs a manifest check while
// pulling too rarely runs stale code.
func isMobileRef(ref string) bool {
	if strings.Contains(ref, "@") {
		return false
	}
	i := strings.LastIndex(ref, ":")
	if i < 0 || strings.Contains(ref[i+1:], "/") {
		return true // untagged: docker resolves it to the mobile :latest
	}
	return !semverTag.MatchString(ref[i+1:])
}

// composeConvergeCmds returns the docker compose command(s) Converge runs, in
// order, for a given stack image ref and force flag.
//
// Traefik (and the observability images) are pinned to tags their upstreams
// move for patches, so they are refreshed on every converge however the stack
// image is pinned; failures are ignored so an offline host still converges from
// what it already has. The stack image itself follows the mobile/immutable
// asymmetry: a mobile ref (or --force) pulls always, a release tag pulls only
// when missing, since it can never have changed. --build is a no-op unless a
// PROXIMO_SRC override added a build section.
func composeConvergeCmds(image string, force bool) [][]string {
	pull := "missing"
	if force || isMobileRef(image) {
		pull = "always"
	}
	return [][]string{
		{"pull", "--ignore-pull-failures", "traefik"},
		{"up", "-d", "--build", "--pull", pull},
	}
}

// Down stops and removes the whole stack — core services plus the opt-in
// observability dashboards — without touching host configuration. A plain
// `docker compose down` leaves profile-gated services running, so the
// observability profile must be enabled (and --remove-orphans added) for the
// dashboards to be torn down together with the core stack.
func Down() error {
	return downWith(defaultComposer)
}

// Purge is Down plus proximo's own stack images — the pinned version in use and
// every superseded one `update` deliberately left cached. `uninstall` reverses
// everything proximo put on the host, and those images are one of those things;
// a plain `down` keeps them, because stopping the stack must not make the next
// `up` re-download it.
//
// Third-party images (Traefik, the observability dashboards) are left alone:
// proximo did not author them, and a developer may well be sharing them with
// another project. That is why this is not `compose down --rmi all`.
func Purge() error {
	return purgeWith(defaultComposer)
}

// purgeWith is Purge with the Composer injected for testing.
func purgeWith(c Composer) error {
	if err := downWith(c); err != nil {
		return err
	}
	purgeImages()
	return nil
}

// purgeImages best-effort removes every proximo stack image on the host. A var
// so uninstall's wiring stays testable, and errors are ignored throughout: an
// image still referenced by something else is a reason to leave it, not to fail
// an uninstall that has already reversed the host.
var purgeImages = func() {
	// By ref, never by image ID: one build publishes several tags, so an ID can
	// be referenced more than once and `docker image rm <id>` refuses it.
	out, err := exec.Command("docker", "image", "ls",
		"--format", "{{.Repository}}:{{.Tag}}", "--filter", "reference="+imageRepo).Output()
	if err != nil {
		return
	}
	refs := []string{devImage}
	for _, ref := range strings.Fields(string(out)) {
		if !strings.Contains(ref, "<none>") {
			refs = append(refs, ref)
		}
	}
	for _, ref := range refs {
		_ = exec.Command("docker", "image", "rm", ref).Run()
	}
}

// downWith is Down with the Composer injected so a fake can assert the issued
// teardown command without Docker.
func downWith(c Composer) error {
	dir, err := StackDir()
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(dir, "docker-compose.yml")); statErr != nil {
		// Nothing materialized; nothing to tear down.
		return nil
	}
	return c.Compose(dir, "--profile", observabilityProfile, "down", "--remove-orphans")
}

// DownObservability stops and removes only the opt-in observability services,
// leaving the core stack running. It is the scoped counterpart to Down behind
// `proximo down --observability`.
func DownObservability() error {
	return downObservabilityWith(defaultComposer)
}

// downObservabilityWith is DownObservability with the Composer injected for
// testing. `rm -s -f` stops then force-removes just the observability services;
// the profile flag is required to address the profile-gated services.
func downObservabilityWith(c Composer) error {
	dir, err := StackDir()
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(dir, "docker-compose.yml")); statErr != nil {
		// Nothing materialized; nothing to tear down.
		return nil
	}
	return c.Compose(dir, "--profile", observabilityProfile, "rm", "-s", "-f",
		svcDozzle, svcBeszel, svcBeszelAgent)
}
