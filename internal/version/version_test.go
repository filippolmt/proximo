package version

import "testing"

func TestResolve(t *testing.T) {
	for _, tc := range []struct{ ldflags, module, want string }{
		// A release build: the linker flag wins.
		{"0.18.0", "(devel)", "0.18.0"},
		{"0.18.0", "v0.17.0", "0.18.0"},
		// `go install …@vX.Y.Z` sets no linker flag; the module version names it.
		{"dev", "v0.18.0", "v0.18.0"},
		{"dev", "v0.18.0-rc.1", "v0.18.0-rc.1"},
		// A build from a checkout is not a release: no published image has its tag.
		{"dev", "(devel)", "dev"},
		{"dev", "", "dev"},
		{"dev", "v0.17.1-0.20261108093000-ff11ce8a1b2c", "dev"},
		{"dev", "v0.17.1-0.20261108093000-ff11ce8a1b2c+dirty", "dev"},
		{"dev", "v0.0.0-20261108093000-ff11ce8a1b2c", "dev"},
		{"dev", "v0.18.0+dirty", "dev"},
	} {
		if got := resolve(tc.ldflags, tc.module); got != tc.want {
			t.Errorf("resolve(%q, %q) = %q, want %q", tc.ldflags, tc.module, got, tc.want)
		}
	}
}
