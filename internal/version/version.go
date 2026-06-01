// Package version holds build metadata injected at release time via -ldflags.
package version

// These variables are overridden at build time with linker flags, e.g.
//
//	-X github.com/filippolmt/proximo/internal/version.Version=v1.2.3
var (
	// Version is the released semantic version (or "dev" for local builds).
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
	// Date is the build timestamp.
	Date = "unknown"
)
