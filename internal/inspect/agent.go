package inspect

import (
	"embed"
	"fmt"
)

//go:embed assets
var assets embed.FS

// sdkPath is the vendored @sentry/browser bundle. It is committed rather than
// fetched at build time so that building proximo — and building the stack images
// from a published module — needs nothing but the module itself. `make
// vendor-agent` refreshes it.
const sdkPath = "assets/sentry.min.js"

// Agent returns the script the hop injects: the vendored Sentry browser SDK
// followed by proximo's own init snippet.
func Agent() ([]byte, error) {
	sdk, err := assets.ReadFile(sdkPath)
	if err != nil {
		return nil, fmt.Errorf("vendored browser SDK missing (%s): run `make vendor-agent`", sdkPath)
	}
	snippet, err := assets.ReadFile("assets/agent.js")
	if err != nil {
		return nil, err
	}
	return append(append(sdk, '\n'), snippet...), nil
}
