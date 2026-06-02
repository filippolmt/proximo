package docker

import (
	"slices"
	"testing"
)

// TestReplaceSentinels covers the pure substitution lifted out of Materialize:
// __TLD__ and __DNSPORT__ are replaced, and content with no sentinel passes
// through unchanged.
func TestReplaceSentinels(t *testing.T) {
	got := string(replaceSentinels([]byte("host: app.__TLD__\nport: __DNSPORT__\n"), "test", 5354))
	want := "host: app.test\nport: 5354\n"
	if got != want {
		t.Errorf("replaceSentinels = %q, want %q", got, want)
	}

	// Both sentinels may appear more than once.
	multi := string(replaceSentinels([]byte("__TLD__ __TLD__ __DNSPORT__"), "dev", 99))
	if multi != "dev dev 99" {
		t.Errorf("repeated sentinels = %q, want %q", multi, "dev dev 99")
	}

	// No sentinel: passthrough, byte-identical.
	plain := []byte("nothing to replace here")
	if out := replaceSentinels(plain, "test", 5354); string(out) != string(plain) {
		t.Errorf("passthrough = %q, want %q", out, plain)
	}
}

// recordComposer is a fake Composer that records the compose commands it is
// asked to run, so Converge's sequencing is verifiable without Docker.
type recordComposer struct{ cmds [][]string }

func (r *recordComposer) Compose(_ string, args ...string) error {
	r.cmds = append(r.cmds, append([]string(nil), args...))
	return nil
}

// TestConvergeRunsCommandSequence asserts convergeWith issues exactly the
// command sequence composeConvergeCmds produces — for an immutable tag (cache
// reused), a mobile ref (cache busted), and Force on a tag — so execution
// faithfully follows the pure, tested decision.
func TestConvergeRunsCommandSequence(t *testing.T) {
	equalCmds := func(a, b [][]string) bool {
		return slices.EqualFunc(a, b, slices.Equal[[]string])
	}
	cases := []struct {
		name  string
		ref   string
		force bool
	}{
		{"immutable tag reuses cache", "v0.1.0", false},
		{"mobile ref busts cache", "main", false},
		{"force on a tag busts cache", "v0.1.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			c := &recordComposer{}
			if err := convergeWith(c, tc.ref, "test", "", ConvergeOpts{Force: tc.force}); err != nil {
				t.Fatalf("convergeWith: %v", err)
			}
			want := composeConvergeCmds(tc.ref, tc.force)
			if !equalCmds(c.cmds, want) {
				t.Errorf("convergeWith ran %v, want %v", c.cmds, want)
			}
		})
	}
}

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
