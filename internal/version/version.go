// Package version holds build metadata injected at release time via -ldflags.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// These variables are overridden at build time with linker flags, e.g.
//
//	-X github.com/filippolmt/proximo/internal/version.Version=1.2.3
var (
	// Version is the released semantic version (or "dev" for local builds).
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
	// Date is the build timestamp.
	Date = "unknown"
)

// `go install github.com/filippolmt/proximo@vX.Y.Z` sets no linker flag, so
// without this the binary would call itself dev and run the moving :main stack
// image instead of the one its release pins.
func init() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		Version = resolve(Version, bi.Main.Version)
	}
}

// releaseVersion is a module version a release tag produced: vX.Y.Z, or a
// release candidate such as vX.Y.Z-rc.1. Anchored, unlike semverTag in
// internal/docker, which only asks whether an image tag can still move.
var releaseVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+(-rc\.\d+)?$`)

// resolve picks the version a binary reports from the one the linker stamped
// and the module version in its build info. The stamped one always wins; a
// module version counts only when it is a release tag. A build from a checkout
// is stamped with a pseudo-version (and +dirty) since Go 1.24, which no
// published image carries, so it stays dev. The leading v is dropped: Version
// is a bare semver, as GoReleaser stamps it, so a go install binary and a
// release archive of one release compare equal (docs/development.md).
func resolve(stamped, module string) string {
	if stamped != "dev" || !releaseVersion.MatchString(module) {
		return stamped
	}
	return strings.TrimPrefix(module, "v")
}
