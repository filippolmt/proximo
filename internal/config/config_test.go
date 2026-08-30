package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeTLD(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "plain label", raw: "test", want: "test"},
		{name: "strips leading dot", raw: ".dev", want: "dev"},
		{name: "lowercases", raw: "DEV", want: "dev"},
		{name: "trims whitespace and dot", raw: "  .Local-Dev  ", want: "local-dev"},
		{name: "rejects empty", raw: "", wantErr: true},
		{name: "rejects multi-label", raw: "foo.bar", wantErr: true},
		{name: "rejects leading hyphen", raw: "-dev", wantErr: true},
		{name: "rejects trailing hyphen", raw: "dev-", wantErr: true},
		{name: "rejects illegal chars", raw: "dev_box", wantErr: true},
		{name: "rejects reserved .local", raw: ".local", wantErr: true},
		{name: "rejects reserved local", raw: "LOCAL", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeTLD(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeTLD(%q) = %q, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeTLD(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeTLD(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDefault(t *testing.T) {
	if got := Default().TLD; got != DefaultTLD {
		t.Fatalf("Default().TLD = %q, want %q", got, DefaultTLD)
	}
}

// withTempConfigDir points proximo's state home (Dir → $HOME/.proximo) at a
// fresh temp directory for the duration of the test.
func withTempConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	withTempConfigDir(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with no config file: %v", err)
	}
	if cfg.TLD != DefaultTLD {
		t.Fatalf("missing config TLD = %q, want default %q", cfg.TLD, DefaultTLD)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withTempConfigDir(t)
	if err := (Config{TLD: "dev"}).Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TLD != "dev" {
		t.Fatalf("round-trip TLD = %q, want %q", cfg.TLD, "dev")
	}
}

func TestLoadBlankTLDFallsBackToDefault(t *testing.T) {
	withTempConfigDir(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tld":""}`), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TLD != DefaultTLD {
		t.Fatalf("blank TLD = %q, want default %q", cfg.TLD, DefaultTLD)
	}
}

// TestDirResolvesUnderHome pins the state home to $HOME/.proximo on both
// platforms, created on resolution.
func TestDirResolvesUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(home, ".proximo")
	if dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("Dir() did not create %q: %v", dir, err)
	}
}

// TestRemoveHome verifies the uninstall seam deletes the whole state home,
// without Docker or sudo.
func TestRemoveHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveHome(); err != nil {
		t.Fatalf("RemoveHome: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("home still present after RemoveHome: %v", err)
	}
}

// TestTLDWarning: a TLD with no reservation behind it warns (it is still
// accepted — NormalizeTLD covers rejection); a reserved one stays quiet.
func TestTLDWarning(t *testing.T) {
	for _, tld := range []string{"test", "internal", "example", "invalid", "localhost"} {
		if w := TLDWarning(tld); w != "" {
			t.Errorf("TLDWarning(%q) = %q, want no warning", tld, w)
		}
	}
	for _, tld := range []string{"dev", "app", "zip", "loc", "lan"} {
		if TLDWarning(tld) == "" {
			t.Errorf("TLDWarning(%q) = \"\", want a warning", tld)
		}
		if _, err := NormalizeTLD(tld); err != nil {
			t.Errorf("NormalizeTLD(%q) must warn, never reject: %v", tld, err)
		}
	}
}
