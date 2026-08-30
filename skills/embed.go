// Package skills holds what proximo publishes to coding agents. It exists so
// the skill source has a Go package to be embedded from: `go:embed` cannot
// reach out of its own directory, and the single source of the skill must stay
// `skills/proximo/` — a public, stable path, because the marketplace entry
// pins a `git-subdir` on it.
//
// Not to be confused with `.claude/skills/`, which holds the skills proximo
// *consumes* while it is being developed. See docs/development.md.
package skills

import "embed"

// FS is the skill tree as shipped in the binary: `proximo/SKILL.md` and its
// references, byte-identical to what `proximo skill install` writes.
//
//go:embed all:proximo
var FS embed.FS
