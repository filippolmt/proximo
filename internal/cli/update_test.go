package cli

import (
	"testing"

	"github.com/filippolmt/proximo/internal/docker"
)

// canonical is a stand-in for the version-pinned stack image: the ref a plain
// `update` asks for.
const canonical = "ghcr.io/filippolmt/proximo:v0.2.0"

func TestDecideUpdate(t *testing.T) {
	tests := []struct {
		name         string
		dockerUp     bool
		mustConverge bool
		stack        docker.StackInfo
		cliVer       string
		wantImage    string
		want         updateAction
	}{
		{"docker down defers", false, false,
			docker.StackInfo{}, "v0.2.0", canonical, actionDockerDown},
		{"docker down wins over force", false, true,
			docker.StackInfo{}, "v0.2.0", canonical, actionDockerDown},
		{"stack down nothing to converge", true, false,
			docker.StackInfo{}, "v0.2.0", canonical, actionStackDown},
		{"aligned is up to date", true, false,
			docker.StackInfo{Running: true, Version: "v0.2.0", Image: canonical},
			"v0.2.0", canonical, actionUpToDate},
		{"force converges even when aligned", true, true,
			docker.StackInfo{Running: true, Version: "v0.2.0", Image: canonical},
			"v0.2.0", canonical, actionConverge},
		{"skew converges", true, false,
			docker.StackInfo{Running: true, Version: "v0.1.0", Image: canonical},
			"v0.2.0", canonical, actionConverge},
		// A pre-0.4.0 stack carries no version label: it is running but
		// unlabeled, and must converge — not be mistaken for "no stack".
		{"legacy unlabeled stack converges", true, false,
			docker.StackInfo{Running: true, Image: canonical}, "v0.2.0", canonical, actionConverge},
		// A sticky --image override this run is clearing: same version, different
		// image. Reporting "up to date" would leave the stack running one thing
		// while declaring another.
		{"clearing an override converges", true, false,
			docker.StackInfo{Running: true, Version: "v0.2.0", Image: "ghcr.io/filippolmt/proximo:sha-abc1234"},
			"v0.2.0", canonical, actionConverge},
		// Adopting an override on an otherwise aligned stack.
		{"adopting an override converges", true, false,
			docker.StackInfo{Running: true, Version: "v0.2.0", Image: canonical},
			"v0.2.0", "proximo:src", actionConverge},
		// A stack that predates image stamping carries no proximo.image label.
		{"unlabeled image converges", true, false,
			docker.StackInfo{Running: true, Version: "v0.2.0"}, "v0.2.0", canonical, actionConverge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideUpdate(tt.dockerUp, tt.mustConverge, tt.stack, tt.cliVer, tt.wantImage)
			if got != tt.want {
				t.Errorf("decideUpdate(%v, %v, %+v, %q, %q) = %d, want %d",
					tt.dockerUp, tt.mustConverge, tt.stack, tt.cliVer, tt.wantImage, got, tt.want)
			}
		})
	}
}
