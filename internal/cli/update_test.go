package cli

import "testing"

func TestDecideUpdate(t *testing.T) {
	tests := []struct {
		name     string
		dockerUp bool
		force    bool
		stackVer string
		cliVer   string
		want     updateAction
	}{
		{"docker down defers", false, false, "", "v0.2.0", actionDockerDown},
		{"docker down wins over force", false, true, "", "v0.2.0", actionDockerDown},
		{"stack down nothing to converge", true, false, "", "v0.2.0", actionStackDown},
		{"aligned is up to date", true, false, "v0.2.0", "v0.2.0", actionUpToDate},
		{"force rebuilds even when aligned", true, true, "v0.2.0", "v0.2.0", actionConverge},
		{"skew converges", true, false, "v0.1.0", "v0.2.0", actionConverge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideUpdate(tt.dockerUp, tt.force, tt.stackVer, tt.cliVer)
			if got != tt.want {
				t.Errorf("decideUpdate(%v, %v, %q, %q) = %d, want %d",
					tt.dockerUp, tt.force, tt.stackVer, tt.cliVer, got, tt.want)
			}
		})
	}
}
