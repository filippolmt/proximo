package docker

import "testing"

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
