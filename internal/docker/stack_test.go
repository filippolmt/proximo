package docker

import (
	"slices"
	"testing"
)

func TestModuleRef(t *testing.T) {
	cases := map[string]string{
		// Released binaries carry a bare semver (GoReleaser drops the "v").
		"0.1.0":     "v0.1.0",
		"1.2.3":     "v1.2.3",
		"0.1.0-rc1": "v0.1.0-rc1",
		// Already-canonical refs are left untouched.
		"v0.1.0": "v0.1.0",
		// Local/dev builds fall back to the default branch.
		"dev": "main",
		"":    "main",
	}
	for in, want := range cases {
		if got := moduleRef(in); got != want {
			t.Errorf("moduleRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsMobileRef(t *testing.T) {
	mobile := []string{"", "main", "dev"}
	for _, ref := range mobile {
		if !isMobileRef(ref) {
			t.Errorf("isMobileRef(%q) = false, want true", ref)
		}
	}
	immutable := []string{"v0.1.0", "v1.2.3", "v0.1.0-rc1"}
	for _, ref := range immutable {
		if isMobileRef(ref) {
			t.Errorf("isMobileRef(%q) = true, want false", ref)
		}
	}
}

func TestComposeConvergeCmds(t *testing.T) {
	hasNoCacheBuild := func(cmds [][]string) bool {
		for _, c := range cmds {
			if len(c) > 0 && c[0] == "build" && slices.Contains(c, "--no-cache") {
				return true
			}
		}
		return false
	}
	upPulls := func(cmds [][]string) bool {
		up := cmds[len(cmds)-1]
		return up[0] == "up" && slices.Contains(up, "--build") &&
			slices.Contains(up, "--pull") && slices.Contains(up, "always")
	}

	tests := []struct {
		name        string
		ref         string
		force       bool
		wantNoCache bool
	}{
		{"immutable tag reuses cache", "v0.1.0", false, false},
		{"mobile ref busts cache", "main", false, true},
		{"empty ref busts cache", "", false, true},
		{"force on a tag busts cache", "v0.1.0", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds := composeConvergeCmds(tt.ref, tt.force)
			if got := hasNoCacheBuild(cmds); got != tt.wantNoCache {
				t.Errorf("composeConvergeCmds(%q, %v) no-cache build = %v, want %v",
					tt.ref, tt.force, got, tt.wantNoCache)
			}
			// The bring-up always re-pulls so the pinned Traefik tag is refreshed.
			if !upPulls(cmds) {
				t.Errorf("composeConvergeCmds(%q, %v) up cmd does not --pull always: %v",
					tt.ref, tt.force, cmds)
			}
		})
	}
}

func TestVersionSkew(t *testing.T) {
	if w := VersionSkew("v0.1.0", "v0.1.0"); w != "" {
		t.Errorf("aligned versions = %q, want empty", w)
	}
	if w := VersionSkew("", "v0.1.0"); w != "" {
		t.Errorf("stack down = %q, want empty", w)
	}
	if w := VersionSkew("v0.1.0", "v0.2.0"); w == "" {
		t.Error("mismatch = empty, want a warning")
	}
}
