package cli

import "testing"

// canonical is a stand-in for the version-pinned stack image: the ref a plain
// `update` asks for.
const canonical = "ghcr.io/filippolmt/proximo:v0.2.0"

func TestDecideUpdate(t *testing.T) {
	tests := []struct {
		name         string
		dockerUp     bool
		stackRunning bool
		force        bool
		stackVer     string
		cliVer       string
		stackImage   string
		wantImage    string
		want         updateAction
	}{
		{"docker down defers", false, false, false, "", "v0.2.0", "", canonical, actionDockerDown},
		{"docker down wins over force", false, false, true, "", "v0.2.0", "", canonical, actionDockerDown},
		{"stack down nothing to converge", true, false, false, "", "v0.2.0", "", canonical, actionStackDown},
		{"aligned is up to date", true, true, false, "v0.2.0", "v0.2.0", canonical, canonical, actionUpToDate},
		{"force converges even when aligned", true, true, true, "v0.2.0", "v0.2.0", canonical, canonical, actionConverge},
		{"skew converges", true, true, false, "v0.1.0", "v0.2.0", canonical, canonical, actionConverge},
		// A pre-0.4.0 stack carries no version label: it is running but
		// unlabeled, and must converge — not be mistaken for "no stack".
		{"legacy unlabeled stack converges", true, true, false, "", "v0.2.0", "", canonical, actionConverge},
		// A sticky --image override this run is clearing: same version, different
		// image. Reporting "up to date" would leave the stack running one thing
		// while declaring another.
		{"clearing an override converges", true, true, false, "v0.2.0", "v0.2.0",
			"ghcr.io/filippolmt/proximo:sha-abc1234", canonical, actionConverge},
		// Adopting an override on an otherwise aligned stack.
		{"adopting an override converges", true, true, false, "v0.2.0", "v0.2.0",
			canonical, "proximo:src", actionConverge},
		// A stack that predates image stamping carries no proximo.image label.
		{"unlabeled image converges", true, true, false, "v0.2.0", "v0.2.0", "", canonical, actionConverge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideUpdate(tt.dockerUp, tt.stackRunning, tt.force, tt.stackVer, tt.cliVer, tt.stackImage, tt.wantImage)
			if got != tt.want {
				t.Errorf("decideUpdate(%v, %v, %v, %q, %q, %q, %q) = %d, want %d",
					tt.dockerUp, tt.stackRunning, tt.force, tt.stackVer, tt.cliVer,
					tt.stackImage, tt.wantImage, got, tt.want)
			}
		})
	}
}
