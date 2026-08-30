package inspect

import "embed"

//go:embed assets
var assets embed.FS

// Agent returns the script the hop injects. It is proximo's own — there is no
// vendored bundle to build, pin or regenerate — so this is a plain read.
func Agent() ([]byte, error) {
	return assets.ReadFile("assets/agent.js")
}
